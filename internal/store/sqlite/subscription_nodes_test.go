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
	if err := store.DeleteSubscriptionWithNodes(ctx, source.ID, []string{first.ID, second.ID}); !errors.Is(err, ErrSubscriptionNodesReferenced) {
		t.Fatalf("delete err=%v", err)
	}
	if err := store.DeleteGroup(ctx, group.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteSubscriptionWithNodes(ctx, source.ID, []string{first.ID, second.ID}); err != nil {
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

func TestApplySubscriptionUpdateRollsBackNodesWithSourceMetadata(t *testing.T) {
	t.Parallel()
	store, err := Open(filepath.Join(t.TempDir(), "foxos.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	source := domain.Subscription{ID: "source-atomic", Name: "Atomic", URL: "https://example.com/source", Enabled: true, Interval: 3600}
	oldNode := domain.Node{ID: "sub-old", Name: "Atomic / Old", Type: "vless", Server: "old.example", Port: 443, UUID: "fixture-old", SubscriptionID: source.ID}
	newNode := domain.Node{ID: "sub-new", Name: "Atomic / New", Type: "vless", Server: "new.example", Port: 443, UUID: "fixture-new", SubscriptionID: source.ID}
	if err := store.SaveSubscription(ctx, source); err != nil {
		t.Fatal(err)
	}
	storedSource, err := store.Subscription(ctx, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceSubscriptionNodes(ctx, source.ID, []domain.Node{oldNode}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `CREATE TRIGGER fail_subscription_result BEFORE UPDATE OF last_digest ON subscriptions BEGIN SELECT RAISE(ABORT, 'injected source failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := store.ApplySubscriptionUpdate(ctx, source.ID, []domain.Node{newNode}, "digest-new", storedSource.SettingsRevision); err == nil {
		t.Fatal("expected injected transaction failure")
	}
	items, err := store.SubscriptionNodes(ctx, source.ID)
	if err != nil || len(items) != 1 || items[0].ID != oldNode.ID {
		t.Fatalf("nodes changed after rollback: %+v err=%v", items, err)
	}
	stored, err := store.Subscription(ctx, source.ID)
	if err != nil || stored.LastDigest != "" || !stored.LastSuccessAt.IsZero() {
		t.Fatalf("source metadata changed after rollback: %+v err=%v", stored, err)
	}
}

func TestReplaceSubscriptionNodesCannotTakeOverAnotherOwner(t *testing.T) {
	t.Parallel()
	store, err := Open(filepath.Join(t.TempDir(), "foxos.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	manual := domain.Node{ID: "shared-id", Name: "Manual", Type: "vless", Server: "manual.example", Port: 443, UUID: "manual-fixture"}
	if err := store.SaveNode(ctx, manual); err != nil {
		t.Fatal(err)
	}
	owned := domain.Node{ID: manual.ID, Name: "Source / Node", Type: "vless", Server: "source.example", Port: 443, UUID: "source-fixture", SubscriptionID: "source-a"}
	if err := store.ReplaceSubscriptionNodes(ctx, "source-a", []domain.Node{owned}); err == nil {
		t.Fatal("expected ownership collision")
	}
	stored, err := store.Node(ctx, manual.ID)
	if err != nil || stored.SubscriptionID != "" || stored.Name != manual.Name {
		t.Fatalf("manual node was changed: %+v err=%v", stored, err)
	}
}

func TestDeleteSubscriptionPreservingNodesIsAtomicAndKeepsReferences(t *testing.T) {
	t.Parallel()
	store, err := Open(filepath.Join(t.TempDir(), "foxos.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	source := domain.Subscription{ID: "source-detach", Name: "Detach", URL: "https://example.com/source", Enabled: true, Interval: 3600}
	node := domain.Node{ID: "sub-detach", Name: "Detach / Node", Type: "vless", Server: "node.example", Port: 443, UUID: "fixture", SubscriptionID: source.ID}
	if err := store.SaveSubscription(ctx, source); err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceSubscriptionNodes(ctx, source.ID, []domain.Node{node}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveGroup(ctx, domain.Group{ID: "group-detach", Name: "Keep reference", Type: "select", NodeIDs: []string{node.ID}}); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteSubscriptionPreservingNodes(ctx, source.ID, []string{node.ID}); err != nil {
		t.Fatal(err)
	}
	stored, err := store.Node(ctx, node.ID)
	if err != nil || stored.SubscriptionID != "" {
		t.Fatalf("detached node=%+v err=%v", stored, err)
	}
	if _, err := store.Subscription(ctx, source.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("subscription err=%v", err)
	}

	rollbackSource := domain.Subscription{ID: "source-rollback", Name: "Rollback", URL: "https://example.com/rollback", Enabled: true, Interval: 3600}
	rollbackNode := domain.Node{ID: "sub-rollback", Name: "Rollback / Node", Type: "vless", Server: "rollback.example", Port: 443, UUID: "fixture", SubscriptionID: rollbackSource.ID}
	if err := store.SaveSubscription(ctx, rollbackSource); err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceSubscriptionNodes(ctx, rollbackSource.ID, []domain.Node{rollbackNode}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `CREATE TRIGGER fail_subscription_delete BEFORE DELETE ON subscriptions WHEN OLD.id='source-rollback' BEGIN SELECT RAISE(ABORT, 'injected delete failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteSubscriptionPreservingNodes(ctx, rollbackSource.ID, []string{rollbackNode.ID}); err == nil {
		t.Fatal("expected injected detach failure")
	}
	stored, err = store.Node(ctx, rollbackNode.ID)
	if err != nil || stored.SubscriptionID != rollbackSource.ID {
		t.Fatalf("detach rollback node=%+v err=%v", stored, err)
	}
	if _, err := store.Subscription(ctx, rollbackSource.ID); err != nil {
		t.Fatalf("detach rollback source missing: %v", err)
	}
}

func TestDeleteSubscriptionRejectsNodeSetChangedAfterConfirmation(t *testing.T) {
	t.Parallel()
	for _, strategy := range []struct {
		name   string
		delete func(context.Context, *Store, string, []string) error
	}{
		{name: "cascade", delete: func(ctx context.Context, store *Store, id string, expected []string) error {
			return store.DeleteSubscriptionWithNodes(ctx, id, expected)
		}},
		{name: "detach", delete: func(ctx context.Context, store *Store, id string, expected []string) error {
			return store.DeleteSubscriptionPreservingNodes(ctx, id, expected)
		}},
	} {
		t.Run(strategy.name, func(t *testing.T) {
			t.Parallel()
			store, err := Open(filepath.Join(t.TempDir(), "foxos.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			ctx := context.Background()
			source := domain.Subscription{ID: "source-stale", Name: "Stale", URL: "https://example.com/source", Enabled: true, Interval: 3600}
			confirmed := domain.Node{ID: "confirmed", Name: "Stale / Confirmed", Type: "http", Server: "confirmed.example", Port: 8080, SubscriptionID: source.ID}
			addedLater := domain.Node{ID: "added-later", Name: "Stale / Added Later", Type: "http", Server: "later.example", Port: 8080, SubscriptionID: source.ID}
			if err := store.SaveSubscription(ctx, source); err != nil {
				t.Fatal(err)
			}
			if err := store.ReplaceSubscriptionNodes(ctx, source.ID, []domain.Node{confirmed}); err != nil {
				t.Fatal(err)
			}
			if err := store.ReplaceSubscriptionNodes(ctx, source.ID, []domain.Node{confirmed, addedLater}); err != nil {
				t.Fatal(err)
			}
			if err := strategy.delete(ctx, store, source.ID, []string{confirmed.ID}); !errors.Is(err, ErrSubscriptionDeletePlanStale) {
				t.Fatalf("delete error=%v, want stale plan", err)
			}
			items, err := store.SubscriptionNodes(ctx, source.ID)
			if err != nil || len(items) != 2 {
				t.Fatalf("nodes changed despite stale plan: %+v err=%v", items, err)
			}
			if _, err := store.Subscription(ctx, source.ID); err != nil {
				t.Fatalf("subscription removed despite stale plan: %v", err)
			}
		})
	}
}
