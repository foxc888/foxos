package upgrade

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	storepkg "github.com/foxc888/foxos/internal/store/sqlite"
)

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
	promoted, err := service.MarkPromoted("*A")
	if err != nil {
		t.Fatal(err)
	}
	if promoted.Status != "promoted" || promoted.SchemaVersion != storepkg.CurrentSchemaVersion() {
		t.Fatalf("unexpected promoted checkpoint: %+v", promoted)
	}
	if _, err := service.MarkPromoted("*B"); err == nil {
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
	service := Service{Store: store, DatabasePath: databasePath, StatePath: statePath, BackupDir: backupDir, Version: "old"}
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
	if _, err := database.Exec(`CREATE TABLE future_only(id INTEGER); INSERT INTO schema_migrations(version, applied_at) VALUES(5, CURRENT_TIMESTAMP);`); err != nil {
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

func TestRecoverDatabaseFailsClosedWhenCheckpointWasTampered(t *testing.T) {
	directory := t.TempDir()
	databasePath := filepath.Join(directory, "foxos.db")
	statePath := filepath.Join(directory, "upgrade.json")
	backupDir := filepath.Join(directory, "backups")
	store, err := storepkg.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	service := Service{Store: store, DatabasePath: databasePath, StatePath: statePath, BackupDir: backupDir, Version: "old"}
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
	if _, err := database.Exec(`INSERT INTO schema_migrations(version, applied_at) VALUES(5, CURRENT_TIMESTAMP)`); err != nil {
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
