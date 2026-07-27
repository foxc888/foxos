package mihomo

import (
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

func TestGenerateRendersOrderedProxyChainWithDialerProxy(t *testing.T) {
	t.Parallel()
	entry := domain.Node{ID: "entry", Name: "Exit", Type: "http", Server: "exit.example", Port: 8080, Extra: map[string]any{"dialer-proxy": "untrusted"}}
	relay := domain.Node{ID: "relay", Name: "Relay", Type: "socks5", Server: "relay.example", Port: 1080}
	chain := domain.Group{ID: "chain-a", Name: "Work Chain", Type: "chain", NodeIDs: []string{entry.ID, relay.ID}}
	policy := domain.DevicePolicy{ID: "phone", Name: "Phone", MACAddress: "AA:BB:CC:DD:EE:FF", StaticIP: "192.168.1.20", DHCPServer: "dhcp", Egress: domain.EgressProxyChain, TargetID: chain.ID}
	body, err := Generate(Input{Nodes: []domain.Node{entry, relay}, Groups: []domain.Group{chain}, Policies: []domain.DevicePolicy{policy}})
	if err != nil {
		t.Fatal(err)
	}
	var document Document
	if err := yaml.Unmarshal(body, &document); err != nil {
		t.Fatal(err)
	}
	chainEntry := "Work Chain [hop 1] Exit"
	chainRelay := "Work Chain [hop 2] Relay"
	foundEntry := false
	for _, proxy := range document.Proxies {
		if proxy["name"] == chainEntry {
			foundEntry = proxy["dialer-proxy"] == chainRelay
		}
		if proxy["dialer-proxy"] == "untrusted" {
			t.Fatalf("unmanaged dialer-proxy leaked into proxy: %+v", proxy)
		}
	}
	if !foundEntry {
		t.Fatalf("chain entry or dialer target missing: %+v", document.Proxies)
	}
	foundGroup := false
	for _, group := range document.ProxyGroups {
		members, _ := group["proxies"].([]any)
		if group["name"] == chain.Name && group["type"] == "select" && len(members) == 1 && members[0] == chainEntry {
			foundGroup = true
		}
	}
	if !foundGroup {
		t.Fatalf("chain selector missing: %+v", document.ProxyGroups)
	}
	if len(document.Rules) != 1 || document.Rules[0] != "SRC-IP-CIDR,192.168.1.20/32,Work Chain" {
		t.Fatalf("rules=%v", document.Rules)
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
