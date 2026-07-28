package subscription

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/foxc888/foxos/internal/domain"
	"github.com/foxc888/foxos/internal/mihomo"
	"gopkg.in/yaml.v3"
)

const maxParsedSubscriptionBytes = 8 << 20

type ParseIssue struct {
	Index    int    `json:"index"`
	Protocol string `json:"protocol,omitempty"`
	Reason   string `json:"reason"`
}

type ParseResult struct {
	Format  string
	Nodes   []domain.Node
	Skipped int
	Errors  []ParseIssue
}

func ParseNodes(body []byte) (ParseResult, error) {
	return parseNodeDocument(body, true)
}

func parseNodeDocument(body []byte, allowBase64 bool) (ParseResult, error) {
	if len(body) == 0 {
		return ParseResult{}, errors.New("subscription is empty")
	}
	if len(body) > maxParsedSubscriptionBytes {
		return ParseResult{}, errors.New("subscription content exceeds 8 MiB")
	}
	text := strings.TrimSpace(strings.TrimPrefix(string(body), "\ufeff"))
	if text == "" {
		return ParseResult{}, errors.New("subscription is empty")
	}
	if result, found, err := parseYAMLNodes([]byte(text)); found || err != nil {
		return result, err
	}
	if allowBase64 && !strings.Contains(text, "://") {
		if decoded, err := decodeSubscriptionBase64(text); err == nil {
			result, parseErr := parseNodeDocument(decoded, false)
			if parseErr == nil {
				result.Format = "base64-" + result.Format
				return result, nil
			}
		}
	}
	return parseLinkLines(text)
}

func parseYAMLNodes(body []byte) (ParseResult, bool, error) {
	var document map[string]any
	if err := yaml.Unmarshal(body, &document); err != nil {
		return ParseResult{}, false, nil
	}
	value, found := document["proxies"]
	if !found {
		return ParseResult{}, false, nil
	}
	items, ok := value.([]any)
	if !ok {
		return ParseResult{}, true, errors.New("subscription YAML proxies must be a sequence")
	}
	result := ParseResult{Format: "yaml", Nodes: make([]domain.Node, 0, len(items))}
	for index, item := range items {
		mapping, ok := item.(map[string]any)
		if !ok {
			result.addIssue(index+1, "", errors.New("proxy entry must be a mapping"))
			continue
		}
		node, err := nodeFromMihomoMap(mapping)
		if err != nil {
			result.addIssue(index+1, scalarString(mapping["type"]), err)
			continue
		}
		result.Nodes = append(result.Nodes, node)
	}
	if len(result.Nodes) == 0 {
		return result, true, errors.New("subscription has no valid proxy nodes")
	}
	return result, true, nil
}

func nodeFromMihomoMap(value map[string]any) (domain.Node, error) {
	if len(value) == 0 || len(value) > 256 {
		return domain.Node{}, errors.New("proxy entry field count is invalid")
	}
	extra := make(map[string]any, len(value))
	for key, item := range value {
		if strings.TrimSpace(key) == "" || len(key) > 128 {
			return domain.Node{}, errors.New("proxy entry contains an invalid field name")
		}
		extra[key] = item
	}
	delete(extra, "name")
	node := domain.Node{
		ID:             "parsed-node",
		Name:           scalarString(value["name"]),
		Type:           normalizedNodeType(scalarString(value["type"])),
		Server:         scalarString(value["server"]),
		Username:       scalarString(value["username"]),
		Password:       scalarString(value["password"]),
		UUID:           scalarString(value["uuid"]),
		Cipher:         scalarString(value["cipher"]),
		Network:        scalarString(value["network"]),
		SNI:            firstNonEmpty(scalarString(value["servername"]), scalarString(value["sni"])),
		UDP:            scalarBool(value["udp"]),
		TLS:            scalarBool(value["tls"]),
		SkipCertVerify: scalarBool(value["skip-cert-verify"]),
		Extra:          extra,
	}
	port, err := scalarInt(value["port"])
	if err != nil {
		return domain.Node{}, errors.New("proxy entry port is invalid")
	}
	node.Port = port
	if ws, ok := value["ws-opts"].(map[string]any); ok {
		node.Path = scalarString(ws["path"])
		if headers, ok := ws["headers"].(map[string]any); ok {
			node.Host = firstNonEmpty(scalarString(headers["Host"]), scalarString(headers["host"]))
		}
	}
	if node.Name == "" {
		node.Name = node.Server
	}
	if err := node.Validate(); err != nil {
		return domain.Node{}, err
	}
	return node, nil
}

func parseLinkLines(text string) (ParseResult, error) {
	lines := strings.FieldsFunc(text, func(character rune) bool { return character == '\n' || character == '\r' })
	result := ParseResult{Format: "links", Nodes: make([]domain.Node, 0, len(lines))}
	entry := 0
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		for _, value := range strings.Fields(line) {
			entry++
			node, err := mihomo.ParseShareLink(value)
			if err != nil {
				result.addIssue(entry, shareLinkProtocol(value), err)
				continue
			}
			result.Nodes = append(result.Nodes, node)
		}
	}
	if len(result.Nodes) == 0 {
		return result, errors.New("subscription has no valid share links")
	}
	return result, nil
}

func (r *ParseResult) addIssue(index int, protocol string, err error) {
	r.Skipped++
	if len(r.Errors) >= 100 {
		return
	}
	reason := "invalid proxy entry"
	if err != nil {
		reason = err.Error()
	}
	r.Errors = append(r.Errors, ParseIssue{Index: index, Protocol: strings.ToLower(protocol), Reason: reason})
}

func decodeSubscriptionBase64(value string) ([]byte, error) {
	compact := strings.NewReplacer("\r", "", "\n", "", "\t", "", " ", "").Replace(value)
	for _, encoding := range []*base64.Encoding{base64.RawStdEncoding, base64.StdEncoding, base64.RawURLEncoding, base64.URLEncoding} {
		if decoded, err := encoding.DecodeString(compact); err == nil && len(decoded) > 0 {
			return decoded, nil
		}
	}
	return nil, errors.New("invalid subscription base64")
}

func normalizedNodeType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "shadowsocks":
		return "ss"
	case "socks", "socks5":
		return "socks5"
	case "hy2":
		return "hysteria2"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func scalarString(value any) string {
	switch item := value.(type) {
	case string:
		return strings.TrimSpace(item)
	case int:
		return strconv.Itoa(item)
	case int64:
		return strconv.FormatInt(item, 10)
	case uint64:
		return strconv.FormatUint(item, 10)
	case float64:
		return strconv.FormatFloat(item, 'f', -1, 64)
	default:
		return ""
	}
}

func scalarInt(value any) (int, error) {
	switch item := value.(type) {
	case int:
		return item, nil
	case int64:
		return int(item), nil
	case uint64:
		if item > uint64(^uint(0)>>1) {
			return 0, errors.New("integer overflow")
		}
		return int(item), nil
	case float64:
		if item != float64(int(item)) {
			return 0, errors.New("port is not an integer")
		}
		return int(item), nil
	case string:
		return strconv.Atoi(strings.TrimSpace(item))
	default:
		return 0, errors.New("port is missing")
	}
}

func scalarBool(value any) bool {
	switch item := value.(type) {
	case bool:
		return item
	case string:
		parsed, _ := strconv.ParseBool(item)
		return parsed
	default:
		return false
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func shareLinkProtocol(value string) string {
	if index := strings.Index(value, "://"); index > 0 && index <= 20 {
		return strings.ToLower(value[:index])
	}
	return "unknown"
}

func (r ParseResult) String() string {
	return fmt.Sprintf("%s: %d valid, %d skipped", r.Format, len(r.Nodes), r.Skipped)
}
