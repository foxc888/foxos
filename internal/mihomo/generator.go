package mihomo

import (
	"errors"
	"fmt"
	"sort"

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
	proxies := make([]map[string]any, 0, len(input.Nodes))
	for _, node := range input.Nodes {
		if err := node.Validate(); err != nil {
			return nil, err
		}
		if _, exists := nodeNames[node.ID]; exists {
			return nil, fmt.Errorf("%w: duplicate node id", ErrInvalidConfig)
		}
		nodeNames[node.ID] = node.Name
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
	}
	groups := make([]map[string]any, 0, len(input.Groups))
	for _, group := range input.Groups {
		rendered, err := renderGroup(group, nodeNames, groupNames)
		if err != nil {
			return nil, err
		}
		groups = append(groups, rendered)
	}
	sort.SliceStable(proxies, func(i, j int) bool { return proxies[i]["name"].(string) < proxies[j]["name"].(string) })
	document := Document{
		Mode: input.Mode, MixedPort: input.MixedPort, AllowLAN: input.AllowLAN,
		Proxies: proxies, ProxyGroups: groups, Rules: append([]string(nil), input.Rules...),
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

func renderNode(node domain.Node) map[string]any {
	out := make(map[string]any, len(node.Extra)+12)
	for key, value := range node.Extra {
		out[key] = value
	}
	out["name"], out["type"], out["server"], out["port"] = node.Name, node.Type, node.Server, node.Port
	if node.Username != "" { out["username"] = node.Username }
	if node.Password != "" {
		if node.Type == "hysteria2" { out["password"] = node.Password } else { out["password"] = node.Password }
	}
	if node.UUID != "" { out["uuid"] = node.UUID }
	if node.Cipher != "" { out["cipher"] = node.Cipher }
	if node.Network != "" { out["network"] = node.Network }
	if node.SNI != "" { out["servername"] = node.SNI }
	if node.UDP { out["udp"] = true }
	if node.TLS { out["tls"] = true }
	if node.SkipCertVerify { out["skip-cert-verify"] = true }
	if node.Path != "" || node.Host != "" {
		opts := map[string]any{}
		if node.Path != "" { opts["path"] = node.Path }
		if node.Host != "" { opts["headers"] = map[string]any{"Host": node.Host} }
		out["ws-opts"] = opts
	}
	return out
}

func renderGroup(group domain.Group, nodes, groups map[string]string) (map[string]any, error) {
	members := make([]string, 0, len(group.NodeIDs)+len(group.GroupIDs))
	for _, id := range group.NodeIDs {
		name, ok := nodes[id]
		if !ok { return nil, fmt.Errorf("%w: unknown node %q", ErrInvalidConfig, id) }
		members = append(members, name)
	}
	for _, id := range group.GroupIDs {
		name, ok := groups[id]
		if !ok || id == group.ID { return nil, fmt.Errorf("%w: invalid group reference %q", ErrInvalidConfig, id) }
		members = append(members, name)
	}
	out := map[string]any{"name": group.Name, "type": group.Type, "proxies": members}
	if group.URL != "" { out["url"] = group.URL }
	if group.Interval > 0 { out["interval"] = group.Interval }
	if group.Tolerance > 0 { out["tolerance"] = group.Tolerance }
	if group.Strategy != "" { out["strategy"] = group.Strategy }
	return out, nil
}
