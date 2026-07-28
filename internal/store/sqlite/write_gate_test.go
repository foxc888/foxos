package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/foxc888/foxos/internal/domain"
)

func TestWriteGateDrainsTransactionsAndRejectsOrdinaryWrites(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "foxos.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	tx, err := store.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	frozen := make(chan error, 1)
	go func() { frozen <- store.FreezeWrites(context.Background()) }()
	select {
	case err := <-frozen:
		t.Fatalf("freeze returned before active transaction drained: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-frozen:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("freeze did not finish after transaction commit")
	}

	node := domain.Node{ID: "node-frozen", Name: "Frozen", Type: "socks5", Server: "127.0.0.1", Port: 1080}
	if err := store.CreateNode(context.Background(), node); !errors.Is(err, ErrWritesFrozen) {
		t.Fatalf("ordinary write error=%v, want ErrWritesFrozen", err)
	}
	if _, err := store.Nodes(context.Background()); err != nil {
		t.Fatalf("read failed while writes were frozen: %v", err)
	}
	if err := store.BackupDatabase(context.Background(), filepath.Join(t.TempDir(), "ordinary.sqlite")); !errors.Is(err, ErrWritesFrozen) {
		t.Fatalf("ordinary backup error=%v, want ErrWritesFrozen", err)
	}

	upgradeSnapshot := filepath.Join(t.TempDir(), "upgrade.sqlite")
	if err := store.BackupUpgradeDatabase(context.Background(), upgradeSnapshot); err != nil {
		t.Fatalf("upgrade snapshot was blocked by its own gate: %v", err)
	}
	event := domain.AuditEvent{ID: "upgrade-promoted-test", Action: "upgrade.promoted", TargetID: "test", Outcome: domain.AuditSucceeded}
	if err := store.SaveUpgradeAudit(context.Background(), event); err != nil {
		t.Fatalf("upgrade audit was blocked by its own gate: %v", err)
	}

	store.ResumeWrites()
	if err := store.CreateNode(context.Background(), node); err != nil {
		t.Fatalf("write did not resume: %v", err)
	}
}

func TestWriteGateCancellationLeavesWritesFrozenUntilExplicitResume(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "foxos.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	tx, err := store.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := store.FreezeWrites(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("freeze error=%v, want context cancellation", err)
	}
	if !store.WritesFrozen() {
		t.Fatal("canceled freeze reopened the write gate")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	store.ResumeWrites()
}

func TestWriteGateRejectsAlertSignalUpsert(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	observedAt := time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC)
	if count, err := store.TrackAlertSignal(ctx, "node.failure.frozen", true, observedAt); err != nil || count != 1 {
		t.Fatalf("seed alert signal count=%d err=%v", count, err)
	}
	if err := store.FreezeWrites(ctx); err != nil {
		t.Fatal(err)
	}
	if count, err := store.TrackAlertSignal(ctx, "node.failure.frozen", true, observedAt.Add(time.Minute)); !errors.Is(err, ErrWritesFrozen) {
		t.Errorf("frozen alert signal count=%d err=%v, want ErrWritesFrozen", count, err)
	}

	store.ResumeWrites()
	if count, err := store.TrackAlertSignal(ctx, "node.failure.frozen", true, observedAt.Add(2*time.Minute)); err != nil || count != 2 {
		t.Fatalf("alert signal changed while frozen: count=%d want=2 err=%v", count, err)
	}
}
