package mihomo

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/foxc888/foxos/internal/domain"
)

var ErrUnsupportedShareLink = errors.New("unsupported share link")

func ParseShareLinks(body string) ([]domain.Node, error) {
	lines := strings.FieldsFunc(body, func(r rune) bool { return r == '\n' || r == '\r' || r == ' ' || r == '\t' })
	nodes := make([]domain.Node, 0, len(lines))
	for index, line := range lines {
		node, err := ParseShareLink(strings.TrimSpace(line))
		if err != nil {
			return nil, fmt.Errorf("link %d: %w", index+1, err)
		}
		nodes = append(nodes, node)
	}
	if len(nodes) == 0 {
		return nil, errors.New("no share links found")
	}
	return nodes, nil
}

func ParseShareLink(raw string) (domain.Node, error) {
	if strings.HasPrefix(raw, "vmess://") {
		return parseVMess(strings.TrimPrefix(raw, "vmess://"))
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return domain.Node{}, err
	}
	switch strings.ToLower(parsed.Scheme) {
	case "vless", "trojan", "hysteria2", "hy2", "socks5", "socks", "http", "https":
		return parseURLNode(parsed)
	case "ss":
		return parseSS(raw)
	default:
		return domain.Node{}, fmt.Errorf("%w: %s", ErrUnsupportedShareLink, parsed.Scheme)
	}
}

func parseURLNode(parsed *url.URL) (domain.Node, error) {
	port, err := strconv.Atoi(parsed.Port())
	if err != nil {
		return domain.Node{}, errors.New("invalid port")
	}
	name, _ := url.QueryUnescape(strings.TrimPrefix(parsed.Fragment, "#"))
	if name == "" {
		name = parsed.Hostname()
	}
	query := parsed.Query()
	node := domain.Node{Name: name, Server: parsed.Hostname(), Port: port, UDP: query.Get("udp") == "true", SNI: first(query.Get("sni"), query.Get("peer")), Network: first(query.Get("type"), query.Get("network")), Path: query.Get("path"), Host: query.Get("host")}
	switch parsed.Scheme {
	case "vless":
		node.Type = "vless"
		if parsed.User != nil {
			node.UUID = parsed.User.Username()
		}
		node.TLS = query.Get("security") == "tls" || query.Get("security") == "reality"
	case "trojan":
		node.Type = "trojan"
		if parsed.User != nil {
			node.Password = parsed.User.Username()
		}
		node.TLS = true
	case "hysteria2", "hy2":
		node.Type = "hysteria2"
		if parsed.User != nil {
			node.Password = parsed.User.Username()
		}
		node.TLS = true
	case "socks5", "socks":
		node.Type = "socks5"
		if parsed.User != nil {
			node.Username = parsed.User.Username()
			node.Password, _ = parsed.User.Password()
		}
	case "http", "https":
		node.Type = "http"
		node.TLS = parsed.Scheme == "https"
		if parsed.User != nil {
			node.Username = parsed.User.Username()
			node.Password, _ = parsed.User.Password()
		}
	}
	if query.Get("allowInsecure") == "1" || query.Get("insecure") == "1" || query.Get("skip-cert-verify") == "true" {
		node.SkipCertVerify = true
	}
	return finalizeImportedNode(node)
}

type vmessJSON struct {
	V    string `json:"v"`
	PS   string `json:"ps"`
	Add  string `json:"add"`
	Port any    `json:"port"`
	ID   string `json:"id"`
	Aid  any    `json:"aid"`
	Net  string `json:"net"`
	Type string `json:"type"`
	Host string `json:"host"`
	Path string `json:"path"`
	TLS  string `json:"tls"`
	SNI  string `json:"sni"`
}

func parseVMess(encoded string) (domain.Node, error) {
	body, err := decodeBase64(encoded)
	if err != nil {
		return domain.Node{}, err
	}
	var value vmessJSON
	if err := json.Unmarshal(body, &value); err != nil {
		return domain.Node{}, err
	}
	port, err := anyPort(value.Port)
	if err != nil {
		return domain.Node{}, err
	}
	name := value.PS
	if name == "" {
		name = value.Add
	}
	node := domain.Node{Name: name, Type: "vmess", Server: value.Add, Port: port, UUID: value.ID, Network: value.Net, Host: value.Host, Path: value.Path, SNI: value.SNI, TLS: value.TLS != "" && value.TLS != "none"}
	return finalizeImportedNode(node)
}

func parseSS(raw string) (domain.Node, error) {
	value := strings.TrimPrefix(raw, "ss://")
	fragment := ""
	if index := strings.Index(value, "#"); index >= 0 {
		fragment = value[index+1:]
		value = value[:index]
	}
	if strings.Contains(value, "@") {
		parts := strings.SplitN(value, "@", 2)
		credential, err := decodeBase64(parts[0])
		if err != nil {
			credential = []byte(parts[0])
		}
		return ssNode(string(credential), parts[1], fragment)
	}
	decoded, err := decodeBase64(value)
	if err != nil {
		return domain.Node{}, err
	}
	full := string(decoded)
	at := strings.LastIndex(full, "@")
	if at < 0 {
		return domain.Node{}, errors.New("invalid ss link")
	}
	return ssNode(full[:at], full[at+1:], fragment)
}
func ssNode(credential, address, fragment string) (domain.Node, error) {
	methodPassword := strings.SplitN(credential, ":", 2)
	if len(methodPassword) != 2 {
		return domain.Node{}, errors.New("invalid ss credential")
	}
	hostPort, err := url.Parse("ss://" + address)
	if err != nil {
		return domain.Node{}, err
	}
	port, err := strconv.Atoi(hostPort.Port())
	if err != nil {
		return domain.Node{}, err
	}
	name, _ := url.QueryUnescape(fragment)
	if name == "" {
		name = hostPort.Hostname()
	}
	node := domain.Node{Name: name, Type: "ss", Server: hostPort.Hostname(), Port: port, Cipher: methodPassword[0], Password: methodPassword[1], UDP: true}
	return finalizeImportedNode(node)
}
func finalizeImportedNode(node domain.Node) (domain.Node, error) {
	material := fmt.Sprintf("%s|%s|%s|%d|%s|%s|%s", node.Type, node.Name, node.Server, node.Port, node.Username, node.UUID, node.Password)
	sum := sha256.Sum256([]byte(material))
	node.ID = fmt.Sprintf("node-%x", sum[:8])
	if err := node.Validate(); err != nil {
		return domain.Node{}, err
	}
	return node, nil
}

func decodeBase64(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	for _, encoding := range []*base64.Encoding{base64.RawURLEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.StdEncoding} {
		if body, err := encoding.DecodeString(value); err == nil {
			return body, nil
		}
	}
	return nil, errors.New("invalid base64")
}
func anyPort(value any) (int, error) {
	switch v := value.(type) {
	case string:
		return strconv.Atoi(v)
	case float64:
		return int(v), nil
	default:
		return 0, errors.New("invalid port")
	}
}
func first(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
