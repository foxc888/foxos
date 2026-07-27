package mihomo

import (
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"

	"github.com/foxc888/foxos/internal/domain"
	"gopkg.in/yaml.v3"
)

var ErrInvalidConfig = errors.New("invalid mihomo config")

type Document struct {
	Mode        string           `yaml:"mode"`
	MixedPort   int              `yaml:"mixed-port,omitempty"`
	AllowLAN    bool             `yaml:"allow-lan"`
	Proxies     []map[string]any `yaml:"proxies"`
	ProxyGroups []map[string]any `yaml:"proxy-groups"`
	Rules       []string         `yaml:"rules"`
}

type Input struct {
	Mode      string
	MixedPort int
	AllowLAN  bool
	Nodes     []domain.Node
	Groups    []domain.Group
	Policies  []domain.DevicePolicy
	Rules     []string
}

func Generate(input Input) ([]byte, error) {
	if input.Mode == "" {
		input.Mode = "rule"
	}
	if input.Mode != "rule" && input.Mode != "global" && input.Mode != "direct" {
		return nil, fmt.Errorf("%w: unsupported mode", ErrInvalidConfig)
	}
	nodeNames := make(map[string]string, len(input.Nodes))
	nodesByID := make(map[string]domain.Node, len(input.Nodes))
	allNames := make(map[string]string, len(input.Nodes)+len(input.Groups))
	proxies := make([]map[string]any, 0, len(input.Nodes))
	for _, node := range input.Nodes {
		if err := node.Validate(); err != nil {
			return nil, err
		}
		if _, exists := nodeNames[node.ID]; exists {
			return nil, fmt.Errorf("%w: duplicate node id", ErrInvalidConfig)
		}
		nodeNames[node.ID] = node.Name
		nodesByID[node.ID] = node
		if previous, exists := allNames[node.Name]; exists {
			return nil, fmt.Errorf("%w: duplicate Mihomo name %q (%s and node %s)", ErrInvalidConfig, node.Name, previous, node.ID)
		}
		allNames[node.Name] = "node " + node.ID
		proxies = append(proxies, renderNode(node))
	}
	groupNames := make(map[string]string, len(input.Groups))
	for _, group := range input.Groups {
		if err := group.Validate(); err != nil {
			return nil, err
		}
		if _, exists := groupNames[group.ID]; exists {
			return nil, fmt.Errorf("%w: duplicate group id", ErrInvalidConfig)
		}
		groupNames[group.ID] = group.Name
		if previous, exists := allNames[group.Name]; exists {
			return nil, fmt.Errorf("%w: duplicate Mihomo name %q (%s and group %s)", ErrInvalidConfig, group.Name, previous, group.ID)
		}
		allNames[group.Name] = "group " + group.ID
	}
	if err := validateGroupGraph(input.Groups); err != nil {
		return nil, err
	}
	groups := make([]map[string]any, 0, len(input.Groups))
	for _, group := range input.Groups {
		if group.Type == "chain" {
			chainProxies, rendered, err := renderChain(group, nodesByID)
			if err != nil {
				return nil, err
			}
			for _, proxy := range chainProxies {
				name, _ := proxy["name"].(string)
				if previous, exists := allNames[name]; exists {
					return nil, fmt.Errorf("%w: generated chain name %q conflicts with %s", ErrInvalidConfig, name, previous)
				}
				allNames[name] = "chain " + group.ID
			}
			proxies = append(proxies, chainProxies...)
			groups = append(groups, rendered)
			continue
		}
		rendered, err := renderGroup(group, nodeNames, groupNames)
		if err != nil {
			return nil, err
		}
		groups = append(groups, rendered)
	}
	policyRules, err := renderPolicyRules(input.Policies, nodeNames, groupNames)
	if err != nil {
		return nil, err
	}
	rules := append([]string(nil), policyRules...)
	rules = append(rules, input.Rules...)
	sort.SliceStable(proxies, func(i, j int) bool { return proxies[i]["name"].(string) < proxies[j]["name"].(string) })
	document := Document{
		Mode: input.Mode, MixedPort: input.MixedPort, AllowLAN: input.AllowLAN,
		Proxies: proxies, ProxyGroups: groups, Rules: rules,
	}
	if len(document.Rules) == 0 {
		document.Rules = []string{"MATCH,DIRECT"}
	}
	body, err := yaml.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	return body, nil
}

func renderPolicyRules(policies []domain.DevicePolicy, nodes, groups map[string]string) ([]string, error) {
	ordered := append([]domain.DevicePolicy(nil), policies...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })
	rules := make([]string, 0, len(ordered))
	seen := make(map[string]struct{}, len(ordered))
	for _, policy := range ordered {
		if err := policy.Validate(); err != nil {
			return nil, err
		}
		ip := net.ParseIP(policy.StaticIP)
		if ip == nil || ip.To4() == nil || isManagementAddress(ip.To4().String()) {
			return nil, fmt.Errorf("%w: invalid or protected device policy address", ErrInvalidConfig)
		}
		address := ip.To4().String()
		if _, exists := seen[address]; exists {
			return nil, fmt.Errorf("%w: duplicate device policy address %s", ErrInvalidConfig, address)
		}
		seen[address] = struct{}{}
		target := "DIRECT"
		switch policy.Egress {
		case domain.EgressMihomoNode:
			var ok bool
			target, ok = nodes[policy.TargetID]
			if !ok {
				return nil, fmt.Errorf("%w: unknown node policy target %q", ErrInvalidConfig, policy.TargetID)
			}
		case domain.EgressProxyChain:
			var ok bool
			target, ok = groups[policy.TargetID]
			if !ok {
				return nil, fmt.Errorf("%w: unknown group policy target %q", ErrInvalidConfig, policy.TargetID)
			}
		case domain.EgressBlocked:
			target = "REJECT"
		case domain.EgressDirect, domain.EgressL2TP:
			target = "DIRECT"
		}
		if strings.ContainsAny(target, ",\r\n") {
			return nil, fmt.Errorf("%w: policy target contains a rule delimiter", ErrInvalidConfig)
		}
		rules = append(rules, fmt.Sprintf("SRC-IP-CIDR,%s/32,%s", address, target))
	}
	return rules, nil
}

func isManagementAddress(value string) bool {
	switch value {
	case "10.0.0.1", "10.0.0.2", "10.0.0.3", "10.0.0.4":
		return true
	default:
		return false
	}
}

func renderNode(node domain.Node) map[string]any {
	out := make(map[string]any, len(node.Extra)+12)
	for key, value := range node.Extra {
		out[key] = value
	}
	delete(out, "dialer-proxy")
	out["name"], out["type"], out["server"], out["port"] = node.Name, node.Type, node.Server, node.Port
	if node.Username != "" {
		out["username"] = node.Username
	}
	if node.Password != "" {
		if node.Type == "hysteria2" {
			out["password"] = node.Password
		} else {
			out["password"] = node.Password
		}
	}
	if node.UUID != "" {
		out["uuid"] = node.UUID
	}
	if node.Cipher != "" {
		out["cipher"] = node.Cipher
	}
	if node.Network != "" {
		out["network"] = node.Network
	}
	if node.SNI != "" {
		out["servername"] = node.SNI
	}
	if node.UDP {
		out["udp"] = true
	}
	if node.TLS {
		out["tls"] = true
	}
	if node.SkipCertVerify {
		out["skip-cert-verify"] = true
	}
	if node.Path != "" || node.Host != "" {
		opts := map[string]any{}
		if node.Path != "" {
			opts["path"] = node.Path
		}
		if node.Host != "" {
			opts["headers"] = map[string]any{"Host": node.Host}
		}
		out["ws-opts"] = opts
	}
	return out
}

func renderChain(group domain.Group, nodes map[string]domain.Node) ([]map[string]any, map[string]any, error) {
	if group.Type != "chain" || len(group.NodeIDs) < 2 || len(group.GroupIDs) != 0 {
		return nil, nil, fmt.Errorf("%w: invalid chain group %q", ErrInvalidConfig, group.ID)
	}
	proxies := make([]map[string]any, 0, len(group.NodeIDs))
	names := make([]string, len(group.NodeIDs))
	for index, id := range group.NodeIDs {
		node, exists := nodes[id]
		if !exists {
			return nil, nil, fmt.Errorf("%w: unknown chain node %q", ErrInvalidConfig, id)
		}
		names[index] = group.Name + " [hop " + strconv.Itoa(index+1) + "] " + node.Name
		proxy := renderNode(node)
		proxy["name"] = names[index]
		proxies = append(proxies, proxy)
	}
	for index := 0; index+1 < len(proxies); index++ {
		proxies[index]["dialer-proxy"] = names[index+1]
	}
	return proxies, map[string]any{"name": group.Name, "type": "select", "proxies": []string{names[0]}}, nil
}

func validateGroupGraph(groups []domain.Group) error {
	byID := make(map[string]domain.Group, len(groups))
	for _, group := range groups {
		byID[group.ID] = group
	}
	state := make(map[string]uint8, len(groups))
	var visit func(string) error
	visit = func(id string) error {
		group, exists := byID[id]
		if !exists {
			return fmt.Errorf("%w: unknown group %q", ErrInvalidConfig, id)
		}
		if state[id] == 1 {
			return fmt.Errorf("%w: proxy group cycle at %q", ErrInvalidConfig, id)
		}
		if state[id] == 2 {
			return nil
		}
		state[id] = 1
		for _, child := range group.GroupIDs {
			if err := visit(child); err != nil {
				return err
			}
		}
		state[id] = 2
		return nil
	}
	for id := range byID {
		if err := visit(id); err != nil {
			return err
		}
	}
	return nil
}

func renderGroup(group domain.Group, nodes, groups map[string]string) (map[string]any, error) {
	members := make([]string, 0, len(group.NodeIDs)+len(group.GroupIDs))
	for _, id := range group.NodeIDs {
		name, ok := nodes[id]
		if !ok {
			return nil, fmt.Errorf("%w: unknown node %q", ErrInvalidConfig, id)
		}
		members = append(members, name)
	}
	for _, id := range group.GroupIDs {
		name, ok := groups[id]
		if !ok || id == group.ID {
			return nil, fmt.Errorf("%w: invalid group reference %q", ErrInvalidConfig, id)
		}
		members = append(members, name)
	}
	out := map[string]any{"name": group.Name, "type": group.Type, "proxies": members}
	if group.URL != "" {
		out["url"] = group.URL
	}
	if group.Interval > 0 {
		out["interval"] = group.Interval
	}
	if group.Tolerance > 0 {
		out["tolerance"] = group.Tolerance
	}
	if group.Strategy != "" {
		out["strategy"] = group.Strategy
	}
	return out, nil
}
