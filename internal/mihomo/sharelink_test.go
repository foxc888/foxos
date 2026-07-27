package mihomo

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func TestParseVLESS(t *testing.T) {
	node, err := ParseShareLink("vless://00000000-0000-0000-0000-000000000001@example.com:443?security=tls&type=ws&path=%2Fws#HK-01")
	if err != nil {
		t.Fatal(err)
	}
	if node.Type != "vless" || node.Name != "HK-01" || node.Port != 443 || !node.TLS || node.Path != "/ws" {
		t.Fatalf("node=%+v", node)
	}
}
func TestParseTrojan(t *testing.T) {
	node, err := ParseShareLink("trojan://secret@example.com:443?sni=example.com#US-01")
	if err != nil {
		t.Fatal(err)
	}
	if node.Type != "trojan" || node.Password != "secret" || node.SNI != "example.com" {
		t.Fatalf("node=%+v", node)
	}
}
func TestParseVMess(t *testing.T) {
	raw := `{"v":"2","ps":"JP-01","add":"example.com","port":"443","id":"00000000-0000-0000-0000-000000000001","net":"ws","path":"/ws","tls":"tls"}`
	node, err := ParseShareLink("vmess://" + base64.RawStdEncoding.EncodeToString([]byte(raw)))
	if err != nil {
		t.Fatal(err)
	}
	if node.Type != "vmess" || node.Name != "JP-01" || !node.TLS {
		t.Fatalf("node=%+v", node)
	}
}
func TestUnsupportedShareLink(t *testing.T) {
	_, err := ParseShareLink("ftp://example.com/file")
	if !errors.Is(err, ErrUnsupportedShareLink) {
		t.Fatalf("err=%v", err)
	}
}
func TestBatchRejectsPartialFailure(t *testing.T) {
	_, err := ParseShareLinks("trojan://secret@example.com:443#one\ninvalid://value")
	if err == nil {
		t.Fatal("expected atomic batch failure")
	}
}

func TestProvisionalNodeIDIsRandomAndDoesNotExposeCredential(t *testing.T) {
	t.Parallel()
	first, err := ParseShareLink("vless://00000000-0000-0000-0000-000000000001@example.com:443?security=tls#node")
	if err != nil {
		t.Fatal(err)
	}
	second, err := ParseShareLink("vless://00000000-0000-0000-0000-000000000002@example.com:443?security=tls#node")
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID || !strings.HasPrefix(first.ID, "node-") || !strings.HasPrefix(second.ID, "node-") {
		t.Fatalf("provisional IDs are not independently random: %q %q", first.ID, second.ID)
	}
	for _, value := range []string{first.ID, second.ID} {
		if strings.Contains(value, "00000000") {
			t.Fatalf("credential material leaked into externally visible ID: %q", value)
		}
	}
}
