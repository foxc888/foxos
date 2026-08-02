package mihomo

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
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
	case "vless", "trojan", "hysteria2", "hy2", "tuic", "wireguard", "socks5", "socks", "http", "https":
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
	node := domain.Node{Name: name, Server: parsed.Hostname(), Port: port, UDP: query.Get("udp") == "true", SNI: first(query.Get("sni"), query.Get("peer")), Network: first(query.Get("type"), query.Get("network")), Path: query.Get("path"), Host: query.Get("host"), Extra: shareLinkOptions(query)}
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
	case "tuic":
		node.Type = "tuic"
		node.UDP = true
		node.TLS = true
		if parsed.User != nil {
			node.UUID = parsed.User.Username()
			node.Password, _ = parsed.User.Password()
		}
	case "wireguard":
		node.Type = "wireguard"
		node.UDP = true
		if parsed.User != nil {
			node.Extra["private-key"] = parsed.User.Username()
		}
		for queryName, optionName := range map[string]string{"publickey": "public-key", "public-key": "public-key", "presharedkey": "pre-shared-key", "pre-shared-key": "pre-shared-key", "ip": "ip", "ipv6": "ipv6", "reserved": "reserved", "mtu": "mtu"} {
			if value := query.Get(queryName); value != "" {
				node.Extra[optionName] = value
			}
		}
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

func shareLinkOptions(query url.Values) map[string]any {
	options := make(map[string]any)
	if value := query.Get("flow"); value != "" {
		options["flow"] = value
	}
	if value := first(query.Get("fp"), query.Get("client-fingerprint")); value != "" {
		options["client-fingerprint"] = value
	}
	if value := query.Get("alpn"); value != "" {
		options["alpn"] = splitNonEmpty(value)
	}
	publicKey := query.Get("pbk")
	shortID := first(query.Get("sid"), query.Get("short-id"))
	if publicKey != "" || shortID != "" {
		reality := make(map[string]any)
		if publicKey != "" {
			reality["public-key"] = publicKey
		}
		if shortID != "" {
			reality["short-id"] = shortID
		}
		options["reality-opts"] = reality
	}
	if value := first(query.Get("serviceName"), query.Get("service-name")); value != "" {
		options["grpc-opts"] = map[string]any{"grpc-service-name": value}
	}
	if value := query.Get("packetEncoding"); value != "" {
		options["packet-encoding"] = value
	}
	if query.Get("mux") == "1" || query.Get("mux") == "true" {
		options["smux"] = map[string]any{"enabled": true}
	}
	if value := query.Get("obfs"); value != "" {
		options["obfs"] = value
	}
	if value := first(query.Get("obfs-password"), query.Get("obfsParam")); value != "" {
		options["obfs-password"] = value
	}
	if value := query.Get("congestion_control"); value != "" {
		options["congestion-controller"] = value
	}
	if value := query.Get("udp_relay_mode"); value != "" {
		options["udp-relay-mode"] = value
	}
	if value := query.Get("ed"); value != "" {
		if amount, err := strconv.Atoi(value); err == nil && amount > 0 {
			options["ws-opts"] = map[string]any{"max-early-data": amount, "early-data-header-name": first(query.Get("eh"), "Sec-WebSocket-Protocol")}
		}
	}
	return options
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
	ALPN string `json:"alpn"`
	FP   string `json:"fp"`
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
	extra := map[string]any{}
	if alterID, err := anyPort(value.Aid); err == nil && alterID >= 0 {
		extra["alterId"] = alterID
	}
	if value.FP != "" {
		extra["client-fingerprint"] = value.FP
	}
	if value.ALPN != "" {
		extra["alpn"] = splitNonEmpty(value.ALPN)
	}
	node := domain.Node{Name: name, Type: "vmess", Server: value.Add, Port: port, UUID: value.ID, Cipher: "auto", Network: value.Net, Host: value.Host, Path: value.Path, SNI: value.SNI, TLS: value.TLS != "" && value.TLS != "none", Extra: extra}
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
	extra := map[string]any{}
	if plugin := hostPort.Query().Get("plugin"); plugin != "" {
		extra["plugin"] = plugin
	}
	node := domain.Node{Name: name, Type: "ss", Server: hostPort.Hostname(), Port: port, Cipher: methodPassword[0], Password: methodPassword[1], UDP: true, Extra: extra}
	return finalizeImportedNode(node)
}
func finalizeImportedNode(node domain.Node) (domain.Node, error) {
	// Parser results can be returned to callers, so IDs must not disclose or
	// verify credential material. The import endpoint replaces this provisional
	// random ID with its own random persistence ID.
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return domain.Node{}, fmt.Errorf("generate imported node id: %w", err)
	}
	node.ID = "node-" + hex.EncodeToString(id[:])
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

func splitNonEmpty(value string) []string {
	items := strings.FieldsFunc(value, func(character rune) bool { return character == ',' || character == '|' })
	result := make([]string, 0, len(items))
	for _, item := range items {
		if item = strings.TrimSpace(item); item != "" {
			result = append(result, item)
		}
	}
	return result
}
