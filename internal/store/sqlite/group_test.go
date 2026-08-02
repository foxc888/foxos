package sqlite

import (
	"context"
	"testing"

	"github.com/foxc888/foxos/internal/domain"
)

func TestGroupLifecycle(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SaveNode(ctx, domain.Node{ID: "node-1", Name: "Node 1", Type: "http", Server: "node.example", Port: 8080}); err != nil {
		t.Fatal(err)
	}
	group := domain.Group{ID: "group-1", Name: "Proxy", Type: "select", NodeIDs: []string{"node-1"}}
	if err := store.SaveGroup(ctx, group); err != nil {
		t.Fatal(err)
	}
	got, err := store.Group(ctx, group.ID)
	if err != nil || got.Name != "Proxy" {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	got.Name = "Main Proxy"
	if err := store.SaveGroup(ctx, got); err != nil {
		t.Fatal(err)
	}
	groups, err := store.Groups(ctx)
	if err != nil || len(groups) != 1 || groups[0].Name != "Main Proxy" {
		t.Fatalf("groups=%+v err=%v", groups, err)
	}
	if err := store.DeleteGroup(ctx, group.ID); err != nil {
		t.Fatal(err)
	}
}

func TestSaveGroupValidatesReferencesAndCycles(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		setup func(*testing.T, *Store)
		group domain.Group
	}{
		{
			name:  "missing node",
			group: domain.Group{ID: "missing", Name: "Missing", Type: "select", NodeIDs: []string{"does-not-exist"}},
		},
		{
			name: "group cycle",
			setup: func(t *testing.T, store *Store) {
				t.Helper()
				ctx := context.Background()
				if err := store.SaveNode(ctx, domain.Node{ID: "node-1", Name: "Node 1", Type: "http", Server: "node.example", Port: 8080}); err != nil {
					t.Fatal(err)
				}
				if err := store.SaveGroup(ctx, domain.Group{ID: "group-a", Name: "A", Type: "select", NodeIDs: []string{"node-1"}}); err != nil {
					t.Fatal(err)
				}
				if err := store.SaveGroup(ctx, domain.Group{ID: "group-b", Name: "B", Type: "select", GroupIDs: []string{"group-a"}}); err != nil {
					t.Fatal(err)
				}
			},
			group: domain.Group{ID: "group-a", Name: "A", Type: "select", GroupIDs: []string{"group-b"}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			store, err := Open(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if test.setup != nil {
				test.setup(t, store)
			}
			if err := store.SaveGroup(context.Background(), test.group); err == nil {
				t.Fatal("expected reference validation error")
			}
		})
	}
}

func TestGroupReferencesIncludeGroupsAndDevicePolicies(t *testing.T) {
	t.Parallel()
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	for _, node := range []domain.Node{
		{ID: "entry", Name: "Entry", Type: "http", Server: "entry.example", Port: 8080},
		{ID: "relay", Name: "Relay", Type: "socks5", Server: "relay.example", Port: 1080},
	} {
		if err := store.SaveNode(ctx, node); err != nil {
			t.Fatal(err)
		}
	}
	chain := domain.Group{ID: "chain-a", Name: "Chain A", Type: "chain", NodeIDs: []string{"entry", "relay"}}
	if err := store.SaveGroup(ctx, chain); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveGroup(ctx, domain.Group{ID: "selector", Name: "Selector", Type: "select", GroupIDs: []string{chain.ID}}); err != nil {
		t.Fatal(err)
	}
	policy := domain.DevicePolicy{ID: "phone", Name: "Phone", MACAddress: "AA:BB:CC:DD:EE:FF", StaticIP: "192.168.1.20", DHCPServer: "dhcp-lan", Egress: domain.EgressProxyChain, TargetID: chain.ID}
	if err := store.SaveDevicePolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	references, err := store.GroupReferences(ctx, chain.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"proxy-group:selector": false, "device-policy:phone": false}
	for _, reference := range references {
		if _, exists := want[reference]; exists {
			want[reference] = true
		}
	}
	for reference, found := range want {
		if !found {
			t.Fatalf("missing %s in %v", reference, references)
		}
	}
}
