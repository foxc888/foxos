package sqlite

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/foxc888/foxos/internal/domain"
)

func TestOpenRestrictsDatabasePermissions(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "foxos.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("database mode = %o, want 600", got)
	}
}

func TestSubscriptionSettingsAndSyncStatusUseIndependentColumns(t *testing.T) {
	t.Parallel()
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	initial := domain.Subscription{ID: "source", Name: "Initial", URL: "https://example.com/initial", Enabled: true, Interval: 3600}
	if err := store.CreateSubscription(ctx, initial); err != nil {
		t.Fatal(err)
	}
	created, err := store.Subscription(ctx, initial.ID)
	if err != nil {
		t.Fatal(err)
	}

	settings := initial
	settings.Name = "Updated"
	settings.URL = "https://example.com/updated"
	settings.Enabled = false
	settings.Interval = 7200
	start := make(chan struct{})
	var wait sync.WaitGroup
	wait.Add(2)
	settingsResult := make(chan error, 1)
	syncResult := make(chan error, 1)
	go func() {
		defer wait.Done()
		<-start
		settingsResult <- store.UpdateSubscription(ctx, settings)
	}()
	go func() {
		defer wait.Done()
		<-start
		syncResult <- store.UpdateSubscriptionResult(ctx, initial.ID, created.SettingsRevision, "digest-1", "", true)
	}()
	close(start)
	wait.Wait()
	if err := <-settingsResult; err != nil {
		t.Fatal(err)
	}
	syncErr := <-syncResult
	if syncErr != nil && !errors.Is(syncErr, domain.ErrSubscriptionSettingsStale) {
		t.Fatal(syncErr)
	}

	stored, err := store.Subscription(ctx, initial.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Name != settings.Name || stored.URL != settings.URL || stored.Enabled || stored.Interval != settings.Interval {
		t.Fatalf("settings were overwritten: %+v", stored)
	}
	if syncErr == nil && (stored.LastDigest != "digest-1" || stored.LastSuccessAt.IsZero() || stored.LastAttemptAt.IsZero() || stored.LastError != "") {
		t.Fatalf("successful sync status was overwritten: %+v", stored)
	}
	if errors.Is(syncErr, domain.ErrSubscriptionSettingsStale) && (stored.LastDigest != "" || !stored.LastSuccessAt.IsZero() || !stored.LastAttemptAt.IsZero() || stored.LastError != "") {
		t.Fatalf("stale sync status was committed: %+v", stored)
	}

	staleSettings := initial
	staleSettings.Name = "Second update"
	if err := store.UpdateSubscription(ctx, staleSettings); err != nil {
		t.Fatal(err)
	}
	stored, err = store.Subscription(ctx, initial.ID)
	if err != nil || (syncErr == nil && (stored.LastDigest != "digest-1" || stored.LastSuccessAt.IsZero())) || (syncErr != nil && (stored.LastDigest != "" || !stored.LastSuccessAt.IsZero())) {
		t.Fatalf("stale settings update overwrote sync status: %+v error=%v", stored, err)
	}
}

func TestOpenEnablesSQLiteSafetyPragmas(t *testing.T) {
	t.Parallel()
	store, err := Open(filepath.Join(t.TempDir(), "foxos.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var journal string
	var foreignKeys, busyTimeout int
	if err := store.db.QueryRow(`PRAGMA journal_mode`).Scan(&journal); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`PRAGMA busy_timeout`).Scan(&busyTimeout); err != nil {
		t.Fatal(err)
	}
	if journal != "wal" || foreignKeys != 1 || busyTimeout != 5000 {
		t.Fatalf("journal=%q foreign_keys=%d busy_timeout=%d", journal, foreignKeys, busyTimeout)
	}
}

func TestNodeLifecycle(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	node := domain.Node{ID: "node-1", Name: "HK-01", Type: "vless", Server: "example.com", Port: 443, UUID: "00000000-0000-0000-0000-000000000001"}
	if err := store.SaveNode(ctx, node); err != nil {
		t.Fatal(err)
	}
	got, err := store.Node(ctx, node.ID)
	if err != nil || got.Name != node.Name {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	got.Name = "HK-02"
	if err := store.SaveNode(ctx, got); err != nil {
		t.Fatal(err)
	}
	list, err := store.Nodes(ctx)
	if err != nil || len(list) != 1 || list[0].Name != "HK-02" {
		t.Fatalf("list=%+v err=%v", list, err)
	}
	if err := store.DeleteNode(ctx, node.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Node(ctx, node.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v", err)
	}
}

func TestExplicitUpdatesNeverCreateMissingResources(t *testing.T) {
	t.Parallel()
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.CreateNode(ctx, domain.Node{ID: "existing-node", Name: "Existing", Type: "http", Server: "example.com", Port: 8080}); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		update func() error
		read   func() error
	}{
		{
			name: "node",
			update: func() error {
				return store.UpdateNode(ctx, domain.Node{ID: "missing-node", Name: "Missing", Type: "http", Server: "example.com", Port: 8080})
			},
			read: func() error { _, err := store.Node(ctx, "missing-node"); return err },
		},
		{
			name: "group",
			update: func() error {
				return store.UpdateGroup(ctx, domain.Group{ID: "missing-group", Name: "Missing", Type: "select", NodeIDs: []string{"existing-node"}})
			},
			read: func() error { _, err := store.Group(ctx, "missing-group"); return err },
		},
		{
			name: "subscription",
			update: func() error {
				return store.UpdateSubscription(ctx, domain.Subscription{ID: "missing-source", Name: "Missing", URL: "https://example.com/source", Interval: 3600})
			},
			read: func() error { _, err := store.Subscription(ctx, "missing-source"); return err },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.update(); !errors.Is(err, ErrNotFound) {
				t.Fatalf("update error=%v, want ErrNotFound", err)
			}
			if err := test.read(); !errors.Is(err, ErrNotFound) {
				t.Fatalf("read error=%v, want ErrNotFound", err)
			}
		})
	}
}
