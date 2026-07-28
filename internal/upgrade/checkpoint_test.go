package upgrade

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/foxc888/foxos/internal/domain"
	storepkg "github.com/foxc888/foxos/internal/store/sqlite"
)

type testQuiescer struct {
	store  *storepkg.Store
	active bool
}

func (q *testQuiescer) Quiesce(ctx context.Context) (bool, error) {
	if q.active {
		return false, nil
	}
	if err := q.store.FreezeWrites(ctx); err != nil {
		return false, err
	}
	q.active = true
	return true, nil
}

func (q *testQuiescer) Resume() {
	if q.active {
		q.store.ResumeWrites()
		q.active = false
	}
}

func TestCheckpointIsIdempotentAndMarksPromoted(t *testing.T) {
	directory := t.TempDir()
	databasePath := filepath.Join(directory, "foxos.db")
	store, err := storepkg.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service := Service{
		Store:        store,
		DatabasePath: databasePath,
		StatePath:    filepath.Join(directory, "upgrade.json"),
		BackupDir:    filepath.Join(directory, "backups"),
		Version:      "v1",
		Now:          func() time.Time { return time.Date(2026, 7, 27, 1, 2, 3, 0, time.UTC) },
		Quiescer:     &testQuiescer{store: store},
	}
	first, err := service.Create(context.Background(), "*A")
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Create(context.Background(), "*A")
	if err != nil {
		t.Fatal(err)
	}
	if first.SnapshotPath != second.SnapshotPath || first.SnapshotSHA256 != second.SnapshotSHA256 {
		t.Fatalf("idempotent checkpoint changed: first=%+v second=%+v", first, second)
	}
	promoted, err := service.MarkPromoted(context.Background(), "*A")
	if err != nil {
		t.Fatal(err)
	}
	if promoted.Status != "promoted" || promoted.SchemaVersion != storepkg.CurrentSchemaVersion() {
		t.Fatalf("unexpected promoted checkpoint: %+v", promoted)
	}
	if _, err := service.MarkPromoted(context.Background(), "*B"); err == nil {
		t.Fatal("expected operation mismatch")
	}
}

func TestRecoverDatabaseRestoresCheckpointForOlderBinary(t *testing.T) {
	directory := t.TempDir()
	databasePath := filepath.Join(directory, "foxos.db")
	statePath := filepath.Join(directory, "upgrade.json")
	backupDir := filepath.Join(directory, "backups")
	store, err := storepkg.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	service := Service{Store: store, DatabasePath: databasePath, StatePath: statePath, BackupDir: backupDir, Version: "old", Quiescer: &testQuiescer{store: store}}
	checkpoint, err := service.Create(context.Background(), "upgrade-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`CREATE TABLE future_only(id INTEGER); INSERT INTO schema_migrations(version, applied_at) VALUES(6, CURRENT_TIMESTAMP);`); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	result, err := RecoverDatabase(databasePath, statePath, backupDir, "old", storepkg.CurrentSchemaVersion())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Restored || result.Checkpoint.OperationID != checkpoint.OperationID || result.Checkpoint.Status != "restored" {
		t.Fatalf("unexpected recovery result: %+v", result)
	}
	database, err = sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var version int
	if err := database.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != storepkg.CurrentSchemaVersion() {
		t.Fatalf("schema version=%d", version)
	}
	if _, err := database.Exec(`SELECT * FROM future_only`); err == nil {
		t.Fatal("future-only table survived rollback")
	}
}

func TestCheckpointRearmsRestoredOperationFromCurrentDatabase(t *testing.T) {
	directory := t.TempDir()
	databasePath := filepath.Join(directory, "foxos.db")
	statePath := filepath.Join(directory, "upgrade.json")
	backupDir := filepath.Join(directory, "backups")
	store, err := storepkg.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	firstTime := time.Date(2026, 7, 27, 1, 2, 3, 0, time.UTC)
	service := Service{
		Store:        store,
		DatabasePath: databasePath,
		StatePath:    statePath,
		BackupDir:    backupDir,
		Version:      "old",
		Now:          func() time.Time { return firstTime },
		Quiescer:     &testQuiescer{store: store},
	}
	first, err := service.Create(context.Background(), "upgrade-retry")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`CREATE TABLE future_retry_only(id INTEGER); INSERT INTO schema_migrations(version, applied_at) VALUES(6, CURRENT_TIMESTAMP);`); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	result, err := RecoverDatabase(databasePath, statePath, backupDir, "old", storepkg.CurrentSchemaVersion())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Restored || result.Checkpoint.Status != "restored" {
		t.Fatalf("unexpected recovery result: %+v", result)
	}

	recoveredStore, err := storepkg.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer recoveredStore.Close()
	const recoveredAuditID = "after-automatic-restore"
	if err := recoveredStore.SaveAudit(context.Background(), domain.AuditEvent{
		ID:       recoveredAuditID,
		Action:   "upgrade.database_restored",
		TargetID: "upgrade-retry",
		Outcome:  domain.AuditSucceeded,
	}); err != nil {
		t.Fatal(err)
	}
	secondTime := firstTime.Add(time.Minute)
	retryService := Service{
		Store:        recoveredStore,
		DatabasePath: databasePath,
		StatePath:    statePath,
		BackupDir:    backupDir,
		Version:      "old",
		Now:          func() time.Time { return secondTime },
		Quiescer:     &testQuiescer{store: recoveredStore},
	}
	rearmed, err := retryService.Create(context.Background(), "upgrade-retry")
	if err != nil {
		t.Fatal(err)
	}
	if rearmed.Status != "checkpoint_ready" || rearmed.RestoredByVersion != "" || !rearmed.CreatedAt.Equal(secondTime) {
		t.Fatalf("restored checkpoint was not rearmed: %+v", rearmed)
	}
	if rearmed.SnapshotPath == first.SnapshotPath {
		t.Fatal("rearmed checkpoint reused the pre-restore snapshot")
	}

	snapshot, err := sql.Open("sqlite", rearmed.SnapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	var auditCount int
	if err := snapshot.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE id = ?`, recoveredAuditID).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 1 {
		t.Fatalf("rearmed snapshot contains %d recovery audit rows, want 1", auditCount)
	}

	idempotent, err := retryService.Create(context.Background(), "upgrade-retry")
	if err != nil {
		t.Fatal(err)
	}
	if idempotent.SnapshotPath != rearmed.SnapshotPath || idempotent.SnapshotSHA256 != rearmed.SnapshotSHA256 {
		t.Fatalf("rearmed checkpoint is not idempotent: first=%+v second=%+v", rearmed, idempotent)
	}
}

func TestRecoverDatabaseFailsClosedWhenCheckpointWasTampered(t *testing.T) {
	directory := t.TempDir()
	databasePath := filepath.Join(directory, "foxos.db")
	statePath := filepath.Join(directory, "upgrade.json")
	backupDir := filepath.Join(directory, "backups")
	store, err := storepkg.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	service := Service{Store: store, DatabasePath: databasePath, StatePath: statePath, BackupDir: backupDir, Version: "old", Quiescer: &testQuiescer{store: store}}
	checkpoint, err := service.Create(context.Background(), "upgrade-2")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO schema_migrations(version, applied_at) VALUES(6, CURRENT_TIMESTAMP)`); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(checkpoint.SnapshotPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("tampered"); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := RecoverDatabase(databasePath, statePath, backupDir, "old", storepkg.CurrentSchemaVersion()); err == nil {
		t.Fatal("expected tampered checkpoint failure")
	}
}

func TestRecoverDatabaseLeavesCompatibleDatabaseUntouched(t *testing.T) {
	directory := t.TempDir()
	databasePath := filepath.Join(directory, "foxos.db")
	store, err := storepkg.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	result, err := RecoverDatabase(databasePath, filepath.Join(directory, "missing.json"), filepath.Join(directory, "backups"), "same", storepkg.CurrentSchemaVersion())
	if err != nil {
		t.Fatal(err)
	}
	if result.Restored {
		t.Fatal("compatible database was unexpectedly restored")
	}
}

func TestReadCheckpointRejectsSymlink(t *testing.T) {
	t.Parallel()
	target := filepath.Join(t.TempDir(), "checkpoint.json")
	if err := os.WriteFile(target, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "upgrade.json")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := readCheckpoint(link); err == nil {
		t.Fatal("expected symlinked upgrade checkpoint rejection")
	}
}

func TestRecoveryFinalizesReplaceAfterPowerLossWithoutRepeatingIt(t *testing.T) {
	directory := t.TempDir()
	databasePath := filepath.Join(directory, "foxos.db")
	statePath := filepath.Join(directory, "upgrade.json")
	backupDir := filepath.Join(directory, "backups")
	store, err := storepkg.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	service := Service{Store: store, DatabasePath: databasePath, StatePath: statePath, BackupDir: backupDir, Version: "old", Quiescer: &testQuiescer{store: store}}
	if _, err := service.Create(context.Background(), "power-loss"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`CREATE TABLE future_power_loss(id INTEGER); INSERT INTO schema_migrations(version, applied_at) VALUES(6, CURRENT_TIMESTAMP);`); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	injected := errors.New("power lost after database replace")
	if _, err := recoverDatabase(databasePath, statePath, backupDir, "old", storepkg.CurrentSchemaVersion(), recoveryHooks{afterReplace: func() error { return injected }}); !errors.Is(err, injected) {
		t.Fatalf("recovery error=%v, want injected failure", err)
	}
	interrupted, err := readCheckpoint(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if interrupted.Status != statusRestoreProgress {
		t.Fatalf("status=%s, want %s", interrupted.Status, statusRestoreProgress)
	}
	replacedAgain := false
	result, err := recoverDatabase(databasePath, statePath, backupDir, "old", storepkg.CurrentSchemaVersion(), recoveryHooks{afterReplace: func() error {
		replacedAgain = true
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if replacedAgain || !result.Restored || result.Checkpoint.Status != statusRestored {
		t.Fatalf("replacedAgain=%t result=%+v", replacedAgain, result)
	}

	recoveredStore, err := storepkg.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer recoveredStore.Close()
	for range 2 {
		if err := ReconcileAudits(context.Background(), recoveredStore, result.Checkpoint); err != nil {
			t.Fatal(err)
		}
	}
	events, err := recoveredStore.AuditEvents(context.Background(), 20)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, event := range events {
		counts[event.ID]++
	}
	for _, id := range []string{"upgrade-checkpoint-power-loss", "upgrade-restored-power-loss"} {
		if counts[id] != 1 {
			t.Fatalf("audit %s count=%d, want 1", id, counts[id])
		}
	}
}

func TestAbortRequiresSourceSchemaAndRearmsCheckpoint(t *testing.T) {
	directory := t.TempDir()
	databasePath := filepath.Join(directory, "foxos.db")
	store, err := storepkg.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	quiescer := &testQuiescer{store: store}
	service := Service{
		Store:        store,
		DatabasePath: databasePath,
		StatePath:    filepath.Join(directory, "upgrade.json"),
		BackupDir:    filepath.Join(directory, "backups"),
		Version:      "source-v1",
		Quiescer:     quiescer,
	}
	first, err := service.Create(context.Background(), "abort-release")
	if err != nil {
		t.Fatal(err)
	}
	node := domain.Node{ID: "after-abort", Name: "After abort", Type: "socks5", Server: "127.0.0.1", Port: 1080}
	if err := store.CreateNode(context.Background(), node); !errors.Is(err, storepkg.ErrWritesFrozen) {
		t.Fatalf("checkpoint did not freeze writes: %v", err)
	}
	aborted, err := service.Abort(context.Background(), "abort-release")
	if err != nil {
		t.Fatal(err)
	}
	if aborted.Status != statusAborted || quiescer.active {
		t.Fatalf("aborted=%+v quiesced=%t", aborted, quiescer.active)
	}
	if err := store.CreateNode(context.Background(), node); err != nil {
		t.Fatalf("abort did not resume writes: %v", err)
	}
	second, err := service.Create(context.Background(), "abort-release")
	if err != nil {
		t.Fatal(err)
	}
	if second.Status != statusCheckpointReady || second.SnapshotPath == first.SnapshotPath {
		t.Fatalf("abort retry did not create a fresh checkpoint: first=%+v second=%+v", first, second)
	}
	if _, err := service.Abort(context.Background(), "abort-release"); err != nil {
		t.Fatal(err)
	}
}
