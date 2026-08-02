package backup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeDatabase struct {
	current      []byte
	restorePaths []string
	failRollback bool
}

type failedEntropy struct{}

func (failedEntropy) Read([]byte) (int, error) { return 0, errors.New("entropy unavailable") }

func (f *fakeDatabase) BackupDatabase(_ context.Context, destination string) error {
	return os.WriteFile(destination, f.current, 0o600)
}

func (f *fakeDatabase) RestoreDatabase(_ context.Context, source string) error {
	if f.failRollback && strings.Contains(filepath.Base(source), ".rollback.sqlite") {
		return errors.New("rollback unavailable")
	}
	body, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	f.restorePaths = append(f.restorePaths, source)
	f.current = append([]byte(nil), body...)
	return nil
}

func TestServicePrepareStorageCreatesWritableDirectory(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	directory := filepath.Join(root, "foxos")
	service := Service{Directory: directory}
	if err := service.PrepareStorage(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("backup directory info=%v err=%v", info, err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 0 {
		t.Fatalf("storage probe residue entries=%v err=%v", entries, err)
	}
}

func TestServicePrepareStorageRejectsSymlink(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	directory := filepath.Join(root, "foxos")
	if err := os.Symlink(t.TempDir(), directory); err != nil {
		t.Fatal(err)
	}
	if err := (Service{Directory: directory}).PrepareStorage(); err == nil {
		t.Fatal("symlink backup directory was accepted")
	}
}

func TestServiceCreateOperationPublishesRecoverableManifest(t *testing.T) {
	t.Parallel()
	service := Service{Database: &fakeDatabase{current: []byte("sqlite")}, Directory: t.TempDir()}
	manifest, err := service.CreateOperation(context.Background(), "release", "job-create-1")
	if err != nil {
		t.Fatal(err)
	}
	if manifest.OperationID != "job-create-1" {
		t.Fatalf("manifest=%+v", manifest)
	}
	staging := filepath.Join(service.Directory, "."+manifest.ID+".staging")
	if err := os.Rename(service.Path(manifest.ID), staging); err != nil {
		t.Fatal(err)
	}
	recovered, found, err := service.ReconcileCreate("job-create-1")
	if err != nil || !found || recovered.ID != manifest.ID {
		t.Fatalf("recovered=%+v found=%t err=%v", recovered, found, err)
	}
	if _, err := service.Inspect(manifest.ID); err != nil {
		t.Fatalf("published backup is not inspectable: %v", err)
	}
}

func (f *fakeDatabase) DatabaseMatchesBackup(_ context.Context, source string) (bool, error) {
	body, err := os.ReadFile(source)
	if err != nil {
		return false, err
	}
	return string(body) == string(f.current), nil
}

func TestServiceCreateAndInspectVerifiesFiles(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte("mode: rule\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	service := Service{Database: &fakeDatabase{current: []byte("sqlite-backup")}, MihomoPath: configPath, Directory: directory, Retention: 5}
	manifest, err := service.Create(context.Background(), "release point")
	if err != nil {
		t.Fatal(err)
	}
	if manifest.FileCount != 2 || len(manifest.Checksums) != 2 {
		t.Fatalf("manifest=%+v", manifest)
	}
	preview, err := service.Inspect(manifest.ID)
	if err != nil || len(preview.Digest) != 64 || preview.Manifest.ID != manifest.ID {
		t.Fatalf("preview=%+v err=%v", preview, err)
	}
	if err := os.WriteFile(filepath.Join(service.Path(manifest.ID), manifest.Database), []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Inspect(manifest.ID); !errors.Is(err, ErrInvalidBackup) {
		t.Fatalf("err=%v", err)
	}
}

func TestServiceCreateRejectsUnsafeLabels(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		label string
	}{
		{name: "too long", label: strings.Repeat("a", 121)},
		{name: "control character", label: "release\npoint"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			service := Service{Database: &fakeDatabase{current: []byte("sqlite")}, Directory: t.TempDir()}
			if _, err := service.Create(context.Background(), test.label); err == nil {
				t.Fatal("expected label validation error")
			}
		})
	}
}

func TestServiceCreateFailsClosedWhenSecureRandomIsUnavailable(t *testing.T) {
	t.Parallel()
	service := Service{Database: &fakeDatabase{current: []byte("sqlite")}, Directory: t.TempDir(), entropy: failedEntropy{}}
	if _, err := service.Create(context.Background(), "point"); err == nil || !strings.Contains(err.Error(), "generate backup id") {
		t.Fatalf("Create() error = %v", err)
	}
}

func TestServiceRestoreRollsDatabaseBackWhenMihomoFails(t *testing.T) {
	database := &fakeDatabase{current: []byte("before")}
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte("mode: rule\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	service := Service{
		Database:   database,
		MihomoPath: configPath,
		Directory:  t.TempDir(),
		MihomoRestore: func(context.Context, []byte) error {
			return errors.New("reload failed")
		},
	}
	manifest, err := service.Create(context.Background(), "before change")
	if err != nil {
		t.Fatal(err)
	}
	preview, err := service.Inspect(manifest.ID)
	if err != nil {
		t.Fatal(err)
	}
	database.current = []byte("after")
	if err := os.WriteFile(configPath, []byte("mode: direct\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err = service.Restore(context.Background(), manifest.ID, preview.Digest)
	if !errors.Is(err, ErrRestoreRolledBack) || string(database.current) != "after" || len(database.restorePaths) != 2 {
		t.Fatalf("current=%q restores=%v err=%v", database.current, database.restorePaths, err)
	}
}

func TestServiceRestoreRejectsChangedManifest(t *testing.T) {
	database := &fakeDatabase{current: []byte("before")}
	service := Service{Database: database, Directory: t.TempDir()}
	manifest, err := service.Create(context.Background(), "point")
	if err != nil {
		t.Fatal(err)
	}
	preview, err := service.Inspect(manifest.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Restore(context.Background(), manifest.ID, strings.Repeat("0", len(preview.Digest))); !errors.Is(err, ErrBackupChanged) {
		t.Fatalf("err=%v", err)
	}
	if len(database.restorePaths) != 0 {
		t.Fatalf("restore paths=%v", database.restorePaths)
	}
}

func TestServiceRestoreRecoveryResumesAfterDatabaseCommit(t *testing.T) {
	database := &fakeDatabase{current: []byte("target")}
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte("mode: rule\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	service := Service{Database: database, MihomoPath: configPath, Directory: t.TempDir()}
	manifest, err := service.CreateOperation(context.Background(), "target", "job-create-target")
	if err != nil {
		t.Fatal(err)
	}
	preview, err := service.Inspect(manifest.ID)
	if err != nil {
		t.Fatal(err)
	}
	database.current = []byte("before")
	if err := os.WriteFile(configPath, []byte("mode: direct\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	base, err := safeBase(service.Directory)
	if err != nil {
		t.Fatal(err)
	}
	operationID := "job-restore-resume"
	rollbackFile := ".restore-" + operationID + ".rollback.sqlite"
	if err := os.WriteFile(filepath.Join(base, rollbackFile), []byte("before"), 0o600); err != nil {
		t.Fatal(err)
	}
	beforeDigest, err := service.liveMihomoDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := restoreJournal{Version: 1, OperationID: operationID, BackupID: manifest.ID, Digest: preview.Digest, RollbackFile: rollbackFile, MihomoBeforeDigest: beforeDigest, Phase: restoreDatabaseRestored}
	if err := saveRestoreJournal(base, &journal); err != nil {
		t.Fatal(err)
	}
	database.current = []byte("target")
	state, err := service.ReconcileRestore(context.Background(), manifest.ID, preview.Digest, operationID)
	if err != nil || state != RestoreRecoveryPending {
		t.Fatalf("state=%s err=%v", state, err)
	}
	service.MihomoRestore = func(_ context.Context, body []byte) error {
		return os.WriteFile(configPath, body, 0o600)
	}
	if err := service.RestoreOperation(context.Background(), manifest.ID, preview.Digest, operationID); err != nil {
		t.Fatal(err)
	}
	state, err = service.ReconcileRestore(context.Background(), manifest.ID, preview.Digest, operationID)
	if err != nil || state != RestoreRecoveryCompleted {
		t.Fatalf("state=%s err=%v", state, err)
	}
}

func TestServiceRestoreRecoveryRejectsUnrelatedDatabaseState(t *testing.T) {
	database := &fakeDatabase{current: []byte("target")}
	service := Service{Database: database, Directory: t.TempDir()}
	manifest, err := service.CreateOperation(context.Background(), "target", "job-create-target")
	if err != nil {
		t.Fatal(err)
	}
	preview, err := service.Inspect(manifest.ID)
	if err != nil {
		t.Fatal(err)
	}
	base, _ := safeBase(service.Directory)
	operationID := "job-restore-partial"
	rollbackFile := ".restore-" + operationID + ".rollback.sqlite"
	if err := os.WriteFile(filepath.Join(base, rollbackFile), []byte("before"), 0o600); err != nil {
		t.Fatal(err)
	}
	journal := restoreJournal{Version: 1, OperationID: operationID, BackupID: manifest.ID, Digest: preview.Digest, RollbackFile: rollbackFile, Phase: restoreDatabaseRestored}
	if err := saveRestoreJournal(base, &journal); err != nil {
		t.Fatal(err)
	}
	database.current = []byte("external-change")
	state, err := service.ReconcileRestore(context.Background(), manifest.ID, preview.Digest, operationID)
	if err != nil || state != RestoreRecoveryPartial {
		t.Fatalf("state=%s err=%v", state, err)
	}
}

func TestServiceRestoreReportsRollbackFailure(t *testing.T) {
	database := &fakeDatabase{current: []byte("target")}
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte("mode: rule\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	service := Service{Database: database, MihomoPath: configPath, Directory: t.TempDir(), MihomoRestore: func(context.Context, []byte) error {
		return errors.New("reload failed")
	}}
	manifest, err := service.CreateOperation(context.Background(), "target", "job-create-target")
	if err != nil {
		t.Fatal(err)
	}
	preview, err := service.Inspect(manifest.ID)
	if err != nil {
		t.Fatal(err)
	}
	database.current = []byte("before")
	database.failRollback = true
	if err := os.WriteFile(configPath, []byte("mode: direct\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err = service.RestoreOperation(context.Background(), manifest.ID, preview.Digest, "job-restore-failed")
	if !errors.Is(err, ErrRestoreRollbackFailed) || string(database.current) != "target" {
		t.Fatalf("current=%q err=%v", database.current, err)
	}
	state, reconcileErr := service.ReconcileRestore(context.Background(), manifest.ID, preview.Digest, "job-restore-failed")
	if reconcileErr != nil || state != RestoreRecoveryPartial {
		t.Fatalf("state=%s err=%v", state, reconcileErr)
	}
}
