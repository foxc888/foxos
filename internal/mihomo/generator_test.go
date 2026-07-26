package mihomo

import (
	"strings"
	"testing"

	"github.com/foxc888/foxos/internal/domain"
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
