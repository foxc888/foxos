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
}

type failedEntropy struct{}

func (failedEntropy) Read([]byte) (int, error) { return 0, errors.New("entropy unavailable") }

func (f *fakeDatabase) BackupDatabase(_ context.Context, destination string) error {
	return os.WriteFile(destination, f.current, 0o600)
}

func (f *fakeDatabase) RestoreDatabase(_ context.Context, source string) error {
	body, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	f.restorePaths = append(f.restorePaths, source)
	f.current = append([]byte(nil), body...)
	return nil
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
	err = service.Restore(context.Background(), manifest.ID, preview.Digest)
	if err == nil || string(database.current) != "after" || len(database.restorePaths) != 2 {
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
