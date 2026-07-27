package mihomo

import (
	"fmt"
	"strings"
	"testing"

	"github.com/foxc888/foxos/internal/domain"
	"gopkg.in/yaml.v3"
)

func TestGenerate(t *testing.T) {
	body, err := Generate(Input{
		Mode: "rule", MixedPort: 7890, AllowLAN: true,
		Nodes:  []domain.Node{{ID: "n1", Name: "HK-01", Type: "vless", Server: "example.com", Port: 443, UUID: "00000000-0000-0000-0000-000000000001", TLS: true}},
		Groups: []domain.Group{{ID: "g1", Name: "Proxy", Type: "select", NodeIDs: []string{"n1"}}},
		Rules:  []string{"MATCH,Proxy"},
	})
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, want := range []string{"name: HK-01", "name: Proxy", "MATCH,Proxy"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in:\n%s", want, text)
		}
	}
}

func TestGenerateStructurallyMergesManagedFieldsIntoTrustedBase(t *testing.T) {
	t.Parallel()
	base := []byte(`external-controller: 0.0.0.0:9090
secret: controller-secret
bind-address: "*"
external-ui: ui
log-level: warning
dns:
  enable: true
  listen: 0.0.0.0:1053
tun:
  enable: false
proxies:
  - name: stale
    type: direct
proxy-groups:
  - name: stale
    type: select
    proxies: [DIRECT]
rules: [MATCH,REJECT]
`)
	body, err := Generate(Input{
		Base: base, Mode: "global", MixedPort: 7891, AllowLAN: true,
		Nodes: []domain.Node{{ID: "node", Name: "Managed", Type: "http", Server: "proxy.example", Port: 8080}},
		Rules: []string{"MATCH,DIRECT"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(body, &document); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]any{
		"external-controller": "0.0.0.0:9090",
		"secret":              "controller-secret",
		"bind-address":        "*",
		"external-ui":         "ui",
		"log-level":           "warning",
		"mode":                "global",
		"mixed-port":          7891,
		"allow-lan":           true,
	} {
		if got := document[key]; got != want {
			t.Fatalf("%s=%#v, want %#v\n%s", key, got, want, body)
		}
	}
	if document["dns"] == nil || document["tun"] == nil {
		t.Fatalf("runtime sections were discarded:\n%s", body)
	}
	text := string(body)
	if strings.Contains(text, "name: stale") || !strings.Contains(text, "name: Managed") {
		t.Fatalf("managed collections were not replaced:\n%s", body)
	}
}

func TestGenerateDefaultsMixedPort(t *testing.T) {
	t.Parallel()
	body, err := Generate(Input{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "mixed-port: 7890") {
		t.Fatalf("missing safe mixed port default:\n%s", body)
	}
}

func TestGenerateRejectsMissingReference(t *testing.T) {
	_, err := Generate(Input{
		Nodes:  []domain.Node{{ID: "n1", Name: "HK-01", Type: "http", Server: "example.com", Port: 8080}},
		Groups: []domain.Group{{ID: "g1", Name: "Proxy", Type: "select", NodeIDs: []string{"missing"}}},
	})
	if err == nil {
		t.Fatal("expected reference error")
	}
}

func TestGenerateRendersDevicePolicyRulesDeterministically(t *testing.T) {
	t.Parallel()
	node := domain.Node{ID: "n1", Name: "HK-01", Type: "http", Server: "example.com", Port: 8080}
	group := domain.Group{ID: "g1", Name: "Proxy", Type: "select", NodeIDs: []string{"n1"}}
	policies := []domain.DevicePolicy{
		{ID: "z-policy", Name: "Z", MACAddress: "AA:BB:CC:DD:EE:02", StaticIP: "192.168.1.22", DHCPServer: "dhcp", Egress: domain.EgressProxyChain, TargetID: "g1"},
		{ID: "a-policy", Name: "A", MACAddress: "AA:BB:CC:DD:EE:01", StaticIP: "192.168.1.21", DHCPServer: "dhcp", Egress: domain.EgressMihomoNode, TargetID: "n1"},
	}
	body, err := Generate(Input{Nodes: []domain.Node{node}, Groups: []domain.Group{group}, Policies: policies})
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	first := strings.Index(text, "SRC-IP-CIDR,192.168.1.21/32,HK-01")
	second := strings.Index(text, "SRC-IP-CIDR,192.168.1.22/32,Proxy")
	if first < 0 || second < 0 || first > second {
		t.Fatalf("policy rules are missing or not sorted:\n%s", text)
	}
}

func TestGenerateRejectsProtectedPolicyAddress(t *testing.T) {
	t.Parallel()
	_, err := Generate(Input{Policies: []domain.DevicePolicy{{ID: "admin", Name: "Admin", MACAddress: "AA:BB:CC:DD:EE:01", StaticIP: "10.0.0.1", DHCPServer: "dhcp", Egress: domain.EgressDirect}}})
	if err == nil {
		t.Fatal("expected protected address error")
	}
}

func TestGenerateRendersProxyChainInUIOrder(t *testing.T) {
	t.Parallel()
	nodes := []domain.Node{
		{ID: "first", Name: "First", Type: "http", Server: "first.example", Port: 8080, Extra: map[string]any{"dialer-proxy": "untrusted"}},
		{ID: "second", Name: "Second", Type: "socks5", Server: "second.example", Port: 1080},
		{ID: "third", Name: "Exit", Type: "http", Server: "exit.example", Port: 8080},
	}
	for _, test := range []struct {
		name    string
		nodeIDs []string
	}{
		{name: "two hops", nodeIDs: []string{"first", "second"}},
		{name: "three hops", nodeIDs: []string{"first", "second", "third"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			chain := domain.Group{ID: "chain-a", Name: "Work Chain", Type: "chain", NodeIDs: test.nodeIDs}
			body, err := Generate(Input{Nodes: nodes, Groups: []domain.Group{chain}})
			if err != nil {
				t.Fatal(err)
			}
			var document Document
			if err := yaml.Unmarshal(body, &document); err != nil {
				t.Fatal(err)
			}
			byName := make(map[string]map[string]any, len(document.Proxies))
			for _, proxy := range document.Proxies {
				name, _ := proxy["name"].(string)
				byName[name] = proxy
				if proxy["dialer-proxy"] == "untrusted" {
					t.Fatalf("unmanaged dialer-proxy leaked into proxy: %+v", proxy)
				}
			}
			for index := 1; index < len(test.nodeIDs); index++ {
				current := fmt.Sprintf("Work Chain [hop %d] %s", index+1, nodes[index].Name)
				previous := fmt.Sprintf("Work Chain [hop %d] %s", index, nodes[index-1].Name)
				if got := byName[current]["dialer-proxy"]; got != previous {
					t.Fatalf("%s dialer-proxy=%v, want %s; UI order must be RouterOS -> %v -> Internet\n%s", current, got, previous, test.nodeIDs, body)
				}
			}
			last := len(test.nodeIDs)
			wantExit := fmt.Sprintf("Work Chain [hop %d] %s", last, nodes[last-1].Name)
			members, _ := document.ProxyGroups[0]["proxies"].([]any)
			if len(members) != 1 || members[0] != wantExit {
				t.Fatalf("selector=%v, want final exit %s\n%s", members, wantExit, body)
			}
		})
	}
}

func TestGenerateRejectsGroupCyclesAndNameCollisions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input Input
	}{
		{
			name: "group cycle",
			input: Input{Groups: []domain.Group{
				{ID: "a", Name: "A", Type: "select", GroupIDs: []string{"b"}},
				{ID: "b", Name: "B", Type: "select", GroupIDs: []string{"a"}},
			}},
		},
		{
			name: "node and group name collision",
			input: Input{
				Nodes:  []domain.Node{{ID: "node", Name: "Shared", Type: "http", Server: "node.example", Port: 8080}},
				Groups: []domain.Group{{ID: "group", Name: "Shared", Type: "select", NodeIDs: []string{"node"}}},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := Generate(test.input); err == nil {
				t.Fatal("expected invalid configuration error")
			}
		})
	}
}
