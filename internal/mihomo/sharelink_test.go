package mihomo

import "testing"

func TestParseShareLinkPreservesHysteriaObfsPassword(t *testing.T) {
	t.Parallel()

	node, err := ParseShareLink("hy2://credential@example.com:443?obfs=salamander&obfs-password=fixture-obfs-secret#Hysteria")
	if err != nil {
		t.Fatal(err)
	}
	if got := node.Extra["obfs-password"]; got != "fixture-obfs-secret" {
		t.Fatalf("obfs-password=%v", got)
	}
}
