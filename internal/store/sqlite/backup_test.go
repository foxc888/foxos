package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/foxc888/foxos/internal/domain"
)

func TestBackupAndRestoreDatabase(t *testing.T) {
	t.Parallel()
	store, err := Open(filepath.Join(t.TempDir(), "live.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	node := domain.Node{ID: "node-a", Name: "Before", Type: "vless", Server: "example.com", Port: 443, UUID: "uuid"}
	if err := store.SaveNode(ctx, node); err != nil {
		t.Fatal(err)
	}
	backupPath := filepath.Join(t.TempDir(), "backup.db")
	if err := store.BackupDatabase(ctx, backupPath); err != nil {
		t.Fatal(err)
	}
	node.Name = "After"
	if err := store.SaveNode(ctx, node); err != nil {
		t.Fatal(err)
	}
	if err := store.RestoreDatabase(ctx, backupPath); err != nil {
		t.Fatal(err)
	}
	restored, err := store.Node(ctx, node.ID)
	if err != nil || restored.Name != "Before" {
		t.Fatalf("restored=%+v err=%v", restored, err)
	}
}

func TestRestoreDatabaseRejectsOldSchemaBeforeDeletingCurrentData(t *testing.T) {
	t.Parallel()
	store, err := Open(filepath.Join(t.TempDir(), "live.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	current := domain.Node{ID: "node-current", Name: "Current", Type: "vless", Server: "example.com", Port: 443, UUID: "uuid"}
	if err := store.SaveNode(ctx, current); err != nil {
		t.Fatal(err)
	}

	legacyPath := filepath.Join(t.TempDir(), "legacy.db")
	legacy, err := sql.Open("sqlite", legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(`
		CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL);
		INSERT INTO schema_migrations(version, applied_at) VALUES(1, CURRENT_TIMESTAMP);
		CREATE TABLE nodes (id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE, type TEXT NOT NULL, payload_json TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
	`); err != nil {
		_ = legacy.Close()
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	if err := store.RestoreDatabase(ctx, legacyPath); !errors.Is(err, ErrIncompatibleBackup) {
		t.Fatalf("RestoreDatabase() error = %v", err)
	}
	got, err := store.Node(ctx, current.ID)
	if err != nil || got.Name != current.Name {
		t.Fatalf("current data changed after rejected restore: node=%+v err=%v", got, err)
	}
}
