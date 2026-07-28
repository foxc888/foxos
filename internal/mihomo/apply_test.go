package mihomo

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeRuntime struct {
	validateErr, reloadErr, rollbackReloadErr, healthErr, rollbackHealthErr error
	reloads, healthChecks                                                   int
}

func (f *fakeRuntime) Validate(context.Context, []byte) error { return f.validateErr }
func (f *fakeRuntime) Reload(context.Context) error {
	f.reloads++
	if f.reloads == 1 {
		return f.reloadErr
	}
	return f.rollbackReloadErr
}

func TestApplyDoesNotClaimRollbackWhenNoPreviousConfigExists(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	runtime := &fakeRuntime{reloadErr: errors.New("reload failed")}
	result, err := (&Applier{ConfigPath: filepath.Join(root, "config.yaml"), BackupDir: filepath.Join(root, "backups"), Runtime: runtime}).Apply(context.Background(), []byte("new"))
	if !errors.Is(err, ErrRollbackFailed) || result.RolledBack {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestApplyDoesNotClaimRollbackWhenRestoredReloadFails(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	config := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(config, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime := &fakeRuntime{healthErr: errors.New("unhealthy"), rollbackReloadErr: errors.New("restored reload failed")}
	result, err := (&Applier{ConfigPath: config, BackupDir: filepath.Join(root, "backups"), Runtime: runtime}).Apply(context.Background(), []byte("new"))
	if !errors.Is(err, ErrRollbackFailed) || result.RolledBack {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestApplyDoesNotClaimRollbackWhenRestoredHealthFails(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	config := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(config, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime := &fakeRuntime{healthErr: errors.New("new config unhealthy"), rollbackHealthErr: errors.New("old config unhealthy")}
	result, err := (&Applier{ConfigPath: config, BackupDir: filepath.Join(root, "backups"), Runtime: runtime}).Apply(context.Background(), []byte("new"))
	if !errors.Is(err, ErrRollbackFailed) || result.RolledBack {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}
func (f *fakeRuntime) Healthy(context.Context) error {
	f.healthChecks++
	if f.healthChecks == 1 {
		return f.healthErr
	}
	return f.rollbackHealthErr
}

func TestApplySuccess(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(config, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime := &fakeRuntime{}
	applier := Applier{ConfigPath: config, BackupDir: filepath.Join(root, "backups"), Runtime: runtime, Now: func() time.Time { return time.Date(2026, 7, 26, 1, 2, 3, 0, time.UTC) }}
	result, err := applier.Apply(context.Background(), []byte("new"))
	if err != nil {
		t.Fatal(err)
	}
	if result.BackupPath == "" || result.RolledBack {
		t.Fatalf("result=%+v", result)
	}
	body, _ := os.ReadFile(config)
	if string(body) != "new" {
		t.Fatalf("config=%q", body)
	}
	backup, _ := os.ReadFile(result.BackupPath)
	if string(backup) != "old" {
		t.Fatalf("backup=%q", backup)
	}
}

func TestApplyRollsBackOnHealthFailure(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(root, "config.yaml")
	_ = os.WriteFile(config, []byte("old"), 0o600)
	runtime := &fakeRuntime{healthErr: errors.New("unhealthy")}
	result, err := (&Applier{ConfigPath: config, BackupDir: filepath.Join(root, "backups"), Runtime: runtime}).Apply(context.Background(), []byte("new"))
	if !errors.Is(err, ErrApplyFailed) || !result.RolledBack {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	body, _ := os.ReadFile(config)
	if string(body) != "old" {
		t.Fatalf("rollback config=%q", body)
	}
	if runtime.reloads != 2 {
		t.Fatalf("reloads=%d", runtime.reloads)
	}
}

func TestApplyStopsBeforeWriteOnValidationFailure(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(root, "config.yaml")
	_ = os.WriteFile(config, []byte("old"), 0o600)
	_, err := (&Applier{ConfigPath: config, BackupDir: filepath.Join(root, "backups"), Runtime: &fakeRuntime{validateErr: errors.New("bad")}}).Apply(context.Background(), []byte("new"))
	if !errors.Is(err, ErrApplyFailed) {
		t.Fatalf("err=%v", err)
	}
	body, _ := os.ReadFile(config)
	if string(body) != "old" {
		t.Fatalf("config changed=%q", body)
	}
}

func TestPendingRejectsBackupOutsideRestrictedDirectory(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	config := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(config, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	applier := &Applier{ConfigPath: config, BackupDir: filepath.Join(root, "backups"), Runtime: &fakeRuntime{}}
	result, err := applier.ApplyPending(context.Background(), []byte("new"), ApplyOperation{ID: "job-1", Kind: "mihomo.apply", TargetDigest: strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	journalPath := filepath.Join(applier.BackupDir, applyIntentName)
	body, err := os.ReadFile(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	var intent PendingApply
	if err := json.Unmarshal(body, &intent); err != nil {
		t.Fatal(err)
	}
	intent.BackupPath = filepath.Join(root, "outside.yaml")
	if err := os.WriteFile(intent.BackupPath, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	body, _ = json.Marshal(intent)
	if err := os.WriteFile(journalPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, found, err := applier.Pending(); err == nil || found {
		t.Fatalf("outside backup accepted: found=%t err=%v result=%+v", found, err, result)
	}
}

func TestPendingRejectsSymlinkedOrChangedBackup(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(t *testing.T, path string)
	}{
		{name: "symlink", mutate: func(t *testing.T, path string) {
			t.Helper()
			target := filepath.Join(t.TempDir(), "target.yaml")
			if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, path); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
		}},
		{name: "digest mismatch", mutate: func(t *testing.T, path string) {
			t.Helper()
			if err := os.WriteFile(path, []byte("changed"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			config := filepath.Join(root, "config.yaml")
			if err := os.WriteFile(config, []byte("old"), 0o600); err != nil {
				t.Fatal(err)
			}
			applier := &Applier{ConfigPath: config, BackupDir: filepath.Join(root, "backups"), Runtime: &fakeRuntime{}}
			result, err := applier.ApplyPending(context.Background(), []byte("new"), ApplyOperation{ID: "job-1", Kind: "mihomo.apply", TargetDigest: strings.Repeat("a", 64)})
			if err != nil {
				t.Fatal(err)
			}
			test.mutate(t, result.BackupPath)
			if _, found, err := applier.Pending(); err == nil || found {
				t.Fatalf("invalid backup accepted: found=%t err=%v", found, err)
			}
		})
	}
}

func TestApplyPendingBindsOperationIdentityAndDigest(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	applier := &Applier{ConfigPath: filepath.Join(root, "config.yaml"), BackupDir: filepath.Join(root, "backups"), Runtime: &fakeRuntime{}}
	digest := strings.Repeat("b", 64)
	result, err := applier.ApplyPending(context.Background(), []byte("new"), ApplyOperation{ID: "job-42", Kind: "mihomo.restore", TargetDigest: digest})
	if err != nil {
		t.Fatal(err)
	}
	intent, found, err := applier.Pending()
	if err != nil || !found {
		t.Fatalf("Pending() found=%t err=%v", found, err)
	}
	if intent.ID != result.IntentID || intent.OperationID != "job-42" || intent.OperationKind != "mihomo.restore" || intent.TargetDigest != digest || !intent.RequiresSnapshot {
		t.Fatalf("intent=%+v", intent)
	}
}

func TestSyncDirectoryConfinesVariablePath(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	directory := filepath.Join(root, "config")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := syncDirectory(directory); err != nil {
		t.Fatalf("sync real directory: %v", err)
	}

	filePath := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(filePath, []byte("mode: rule\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := syncDirectory(filePath); err == nil {
		t.Fatal("regular file accepted as directory sync target")
	}

	outside := t.TempDir()
	linkPath := filepath.Join(root, "outside")
	if err := os.Symlink(outside, linkPath); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := syncDirectory(linkPath); err == nil {
		t.Fatal("symlinked directory accepted as sync target")
	}
}
