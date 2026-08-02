package subscription

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestParseNodesAcceptsYAMLWithArbitraryLeadingFieldsAndPreservesAdvancedOptions(t *testing.T) {
	t.Parallel()
	body := []byte(`# provider metadata may precede proxies
mixed-port: 7890
profile:
  store-selected: true
proxies:
  - name: Reality
    type: vless
    server: reality.example
    port: 443
    uuid: fixture-reality
    tls: true
    network: ws
    client-fingerprint: chrome
    alpn: [h2, http/1.1]
    reality-opts:
      public-key: fixture-public
      short-id: fixture-short
    ws-opts:
      path: /transport
      headers:
        Host: edge.example
      max-early-data: 2048
    smux:
      enabled: true
  - name: TUIC
    type: tuic
    server: tuic.example
    port: 443
    uuid: fixture-tuic
    password: fixture-password
    congestion-controller: bbr
    alpn: [h3]
  - name: WireGuard
    type: wireguard
    server: wg.example
    port: 51820
    ip: [10.10.0.2/32]
    private-key: fixture-private
    public-key: fixture-public
    reserved: [1, 2, 3]
  - name: Invalid
    type: vless
    server: invalid.example
    uuid: missing-port
`)
	result, err := ParseNodes(body)
	if err != nil {
		t.Fatal(err)
	}
	if result.Format != "yaml" || len(result.Nodes) != 3 || result.Skipped != 1 || len(result.Errors) != 1 || result.Errors[0].Index != 4 {
		t.Fatalf("result=%+v", result)
	}
	reality := result.Nodes[0]
	if reality.Path != "/transport" || reality.Host != "edge.example" || reality.Extra["client-fingerprint"] != "chrome" || reality.Extra["reality-opts"] == nil || reality.Extra["smux"] == nil {
		t.Fatalf("Reality options were not preserved: %+v", reality)
	}
	if result.Nodes[1].Type != "tuic" || result.Nodes[2].Type != "wireguard" {
		t.Fatalf("advanced node types were not normalized: %+v", result.Nodes)
	}
}

func TestParseNodesAcceptsBase64LinkListAndReportsItemsWithoutEchoingSecrets(t *testing.T) {
	t.Parallel()
	links := "# comment\n" +
		"vless://fixture-uuid@example.com:443?security=reality&pbk=fixture-public&sid=short&fp=chrome&type=grpc&serviceName=svc#East\n" +
		"not-a-supported-link\n"
	body := base64.StdEncoding.EncodeToString([]byte(links))
	result, err := ParseNodes([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if result.Format != "base64-links" || len(result.Nodes) != 1 || result.Skipped != 1 || len(result.Errors) != 1 {
		t.Fatalf("result=%+v", result)
	}
	if strings.Contains(result.Errors[0].Reason, "fixture-uuid") || strings.Contains(result.Errors[0].Reason, "not-a-supported-link") {
		t.Fatalf("parse issue exposed source material: %+v", result.Errors[0])
	}
	node := result.Nodes[0]
	if node.Network != "grpc" || node.Extra["reality-opts"] == nil || node.Extra["grpc-opts"] == nil || node.Extra["client-fingerprint"] != "chrome" {
		t.Fatalf("share link options were not normalized: %+v", node)
	}
}

func TestParseNodesRejectsEmptyAndAllInvalidDocuments(t *testing.T) {
	t.Parallel()
	for _, body := range []string{"", "proxies: []", "invalid-without-scheme"} {
		if _, err := ParseNodes([]byte(body)); err == nil {
			t.Fatalf("body %q should fail", body)
		}
	}
}
