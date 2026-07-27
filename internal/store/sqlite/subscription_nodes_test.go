package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/foxc888/foxos/internal/domain"
)

func TestReplaceAndDeleteSubscriptionNodesProtectReferences(t *testing.T) {
	t.Parallel()
	store, err := Open(filepath.Join(t.TempDir(), "foxos.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	source := domain.Subscription{ID: "source-a", Name: "Primary", URL: "https://example.com/source", Enabled: true, Interval: 3600}
	if err := store.SaveSubscription(ctx, source); err != nil {
		t.Fatal(err)
	}
	first := domain.Node{ID: "sub-first", Name: "Primary / First", Type: "vless", Server: "first.example", Port: 443, UUID: "fixture-first", SubscriptionID: source.ID}
	second := domain.Node{ID: "sub-second", Name: "Primary / Second", Type: "vless", Server: "second.example", Port: 443, UUID: "fixture-second", SubscriptionID: source.ID}
	if err := store.ReplaceSubscriptionNodes(ctx, source.ID, []domain.Node{first, second}); err != nil {
		t.Fatal(err)
	}
	group := domain.Group{ID: "group-a", Name: "Group", Type: "select", NodeIDs: []string{first.ID}}
	if err := store.SaveGroup(ctx, group); err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceSubscriptionNodes(ctx, source.ID, []domain.Node{second}); !errors.Is(err, ErrSubscriptionNodesReferenced) {
		t.Fatalf("err=%v", err)
	}
	items, err := store.SubscriptionNodes(ctx, source.ID)
	if err != nil || len(items) != 2 {
		t.Fatalf("items=%+v err=%v", items, err)
	}
	if err := store.DeleteSubscriptionWithNodes(ctx, source.ID); !errors.Is(err, ErrSubscriptionNodesReferenced) {
		t.Fatalf("delete err=%v", err)
	}
	if err := store.DeleteGroup(ctx, group.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteSubscriptionWithNodes(ctx, source.ID); err != nil {
		t.Fatal(err)
	}
	items, err = store.SubscriptionNodes(ctx, source.ID)
	if err != nil || len(items) != 0 {
		t.Fatalf("items=%+v err=%v", items, err)
	}
	if _, err := store.Subscription(ctx, source.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("subscription err=%v", err)
	}
}
