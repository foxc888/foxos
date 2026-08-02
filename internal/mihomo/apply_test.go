package mihomo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testApplyIntegrityKey = "fedcba9876543210fedcba9876543210"

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
	result, err := (&Applier{ConfigPath: filepath.Join(root, "config.yaml"), BackupDir: filepath.Join(root, "backups"), Runtime: runtime, IntegrityKey: []byte(testApplyIntegrityKey)}).Apply(context.Background(), []byte("new"))
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
	result, err := (&Applier{ConfigPath: config, BackupDir: filepath.Join(root, "backups"), Runtime: runtime, IntegrityKey: []byte(testApplyIntegrityKey)}).Apply(context.Background(), []byte("new"))
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
	result, err := (&Applier{ConfigPath: config, BackupDir: filepath.Join(root, "backups"), Runtime: runtime, IntegrityKey: []byte(testApplyIntegrityKey)}).Apply(context.Background(), []byte("new"))
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

func TestNewApplyIntegrityKeyRejectsShortRoot(t *testing.T) {
	t.Parallel()
	if _, err := NewApplyIntegrityKey([]byte("too-short")); err == nil {
		t.Fatal("short Mihomo apply integrity root key was accepted")
	}
}

func TestApplierPrepareStorageCreatesWritableDirectory(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	backupDir := filepath.Join(root, "backups")
	applier := &Applier{ConfigPath: filepath.Join(root, "config.yaml"), BackupDir: backupDir}
	if err := applier.PrepareStorage(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(backupDir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("backup directory info=%v err=%v", info, err)
	}
	entries, err := os.ReadDir(backupDir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("storage probe residue entries=%v err=%v", entries, err)
	}
}

func TestApplierPrepareStorageRejectsNonDirectories(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		setup func(*testing.T, string)
	}{
		{name: "regular file", setup: func(t *testing.T, path string) {
			t.Helper()
			if err := os.WriteFile(path, []byte("not a directory"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "symlink", setup: func(t *testing.T, path string) {
			t.Helper()
			target := t.TempDir()
			if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			backupDir := filepath.Join(root, "backups")
			test.setup(t, backupDir)
			applier := &Applier{ConfigPath: filepath.Join(root, "config.yaml"), BackupDir: backupDir}
			if err := applier.PrepareStorage(); err == nil {
				t.Fatal("invalid backup directory was accepted")
			}
		})
	}
}

func TestApplyJournalMACIsKeyedAndDomainSeparated(t *testing.T) {
	t.Parallel()
	intent := PendingApply{
		Version:          applyIntentVersion,
		ID:               "apply-fixed",
		OperationID:      "job-mac",
		OperationKind:    "mihomo.apply",
		TargetMAC:        strings.Repeat("a", sha256.Size*2),
		PreviousMAC:      strings.Repeat("b", sha256.Size*2),
		TargetDigest:     strings.Repeat("c", sha256.Size*2),
		SnapshotLabel:    "expected snapshot",
		RequiresSnapshot: true,
		BackupPath:       "/backups/config-fixed.yaml",
		Phase:            "snapshot_unconfirmed",
		LastError:        "persistence uncertain",
		CreatedAt:        time.Date(2026, 7, 29, 1, 2, 3, 4, time.UTC),
	}
	journalMAC := func(t *testing.T, rootKey string) string {
		t.Helper()
		integrityKey, err := NewApplyIntegrityKey([]byte(rootKey))
		if err != nil {
			t.Fatal(err)
		}
		applier := &Applier{IntegrityKey: integrityKey}
		value, err := applier.pendingMAC(intent)
		if err != nil {
			t.Fatal(err)
		}
		return hex.EncodeToString(value)
	}

	first := journalMAC(t, "0123456789abcdef0123456789abcdef")
	repeated := journalMAC(t, "0123456789abcdef0123456789abcdef")
	otherKey := journalMAC(t, "abcdef0123456789abcdef0123456789")
	raw, err := json.Marshal(pendingApplyMACPayload{
		Version:          intent.Version,
		ID:               intent.ID,
		OperationID:      intent.OperationID,
		OperationKind:    intent.OperationKind,
		TargetMAC:        intent.TargetMAC,
		PreviousMAC:      intent.PreviousMAC,
		TargetDigest:     intent.TargetDigest,
		SnapshotLabel:    intent.SnapshotLabel,
		RequiresSnapshot: intent.RequiresSnapshot,
		BackupPath:       intent.BackupPath,
		Phase:            intent.Phase,
		LastError:        intent.LastError,
		CreatedAt:        intent.CreatedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	rawDigest := sha256.Sum256(raw)
	integrityKey, err := NewApplyIntegrityKey([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	configDomainMAC := (&Applier{IntegrityKey: integrityKey}).configMAC(raw)
	if first != repeated || first == otherKey || first == hex.EncodeToString(rawDigest[:]) || first == configDomainMAC {
		t.Fatalf("unexpected journal MAC behavior: first=%q repeated=%q other=%q", first, repeated, otherKey)
	}
}

func TestPendingRejectsJournalWithDifferentIntegrityKey(t *testing.T) {
	for _, withPrevious := range []bool{false, true} {
		name := "without previous config"
		if withPrevious {
			name = "with previous config"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			config := filepath.Join(root, "config.yaml")
			if withPrevious {
				if err := os.WriteFile(config, []byte("mode: rule\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			first := &Applier{ConfigPath: config, BackupDir: filepath.Join(root, "backups"), Runtime: &fakeRuntime{}, IntegrityKey: []byte(testApplyIntegrityKey)}
			if _, err := first.ApplyPending(context.Background(), []byte("mode: global\n"), ApplyOperation{ID: "job-key-a", Kind: "mihomo.apply"}); err != nil {
				t.Fatal(err)
			}
			second := &Applier{ConfigPath: config, BackupDir: first.BackupDir, Runtime: &fakeRuntime{}, IntegrityKey: []byte("0123456789abcdef0123456789abcdef")}
			if _, found, err := second.Pending(); err == nil || found {
				t.Fatalf("journal authenticated with a different key: found=%t err=%v", found, err)
			}
		})
	}
}

func TestPendingRejectsTamperedAuthenticatedFields(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*PendingApply, string)
	}{
		{name: "id", mutate: func(intent *PendingApply, _ string) { intent.ID = "apply-tampered" }},
		{name: "operation id", mutate: func(intent *PendingApply, _ string) { intent.OperationID = "job-tampered" }},
		{name: "operation kind", mutate: func(intent *PendingApply, _ string) { intent.OperationKind = "mihomo.restore" }},
		{name: "target mac", mutate: func(intent *PendingApply, _ string) { intent.TargetMAC = strings.Repeat("d", sha256.Size*2) }},
		{name: "previous mac", mutate: func(intent *PendingApply, _ string) { intent.PreviousMAC = strings.Repeat("e", sha256.Size*2) }},
		{name: "target digest", mutate: func(intent *PendingApply, _ string) { intent.TargetDigest = strings.Repeat("f", sha256.Size*2) }},
		{name: "snapshot label", mutate: func(intent *PendingApply, _ string) { intent.SnapshotLabel = "tampered label" }},
		{name: "snapshot flag", mutate: func(intent *PendingApply, _ string) { intent.RequiresSnapshot = false }},
		{name: "backup path", mutate: func(intent *PendingApply, root string) { intent.BackupPath = filepath.Join(root, "other.yaml") }},
		{name: "phase", mutate: func(intent *PendingApply, _ string) { intent.Phase = "rollback_required" }},
		{name: "last error", mutate: func(intent *PendingApply, _ string) { intent.LastError = "tampered" }},
		{name: "created at", mutate: func(intent *PendingApply, _ string) { intent.CreatedAt = intent.CreatedAt.Add(time.Second) }},
		{name: "journal mac", mutate: func(intent *PendingApply, _ string) { intent.JournalMAC = strings.Repeat("0", sha256.Size*2) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			config := filepath.Join(root, "config.yaml")
			if err := os.WriteFile(config, []byte("mode: rule\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			applier := &Applier{ConfigPath: config, BackupDir: filepath.Join(root, "backups"), Runtime: &fakeRuntime{}, IntegrityKey: []byte(testApplyIntegrityKey)}
			if _, err := applier.ApplyPending(context.Background(), []byte("mode: global\n"), ApplyOperation{ID: "job-auth", Kind: "mihomo.apply", TargetDigest: strings.Repeat("a", sha256.Size*2), SnapshotLabel: "expected label"}); err != nil {
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
			test.mutate(&intent, root)
			body, err = json.Marshal(intent)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(journalPath, body, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, found, err := applier.Pending(); err == nil || found || !strings.Contains(err.Error(), "authentication failed") {
				t.Fatalf("Pending() accepted tampered %s: found=%t err=%v", test.name, found, err)
			}
		})
	}
}

func TestPendingTransitionsResignJournal(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	applier := &Applier{ConfigPath: filepath.Join(root, "config.yaml"), BackupDir: filepath.Join(root, "backups"), Runtime: &fakeRuntime{}, IntegrityKey: []byte(testApplyIntegrityKey)}
	result, err := applier.ApplyPending(context.Background(), []byte("mode: global\n"), ApplyOperation{ID: "job-transition", Kind: "mihomo.apply", TargetDigest: strings.Repeat("a", sha256.Size*2), SnapshotLabel: "transition label"})
	if err != nil {
		t.Fatal(err)
	}
	previousMAC := ""
	for _, transition := range []struct {
		phase string
		apply func() error
	}{
		{phase: "external_applied", apply: func() error { return nil }},
		{phase: "snapshot_save_started", apply: func() error { return applier.beginSnapshotSave(result.IntentID) }},
		{phase: "snapshot_unconfirmed", apply: func() error { return applier.markSnapshotUnconfirmed(result.IntentID) }},
		{phase: "snapshot_confirmed", apply: func() error { return applier.markSnapshotConfirmed(result.IntentID) }},
	} {
		if err := transition.apply(); err != nil {
			t.Fatal(err)
		}
		intent, found, err := applier.Pending()
		if err != nil || !found || intent.Phase != transition.phase || !validConfigMAC(intent.JournalMAC) {
			t.Fatalf("phase=%s intent=%+v found=%t err=%v", transition.phase, intent, found, err)
		}
		if previousMAC != "" && intent.JournalMAC == previousMAC {
			t.Fatalf("phase %s reused journal MAC %q", transition.phase, intent.JournalMAC)
		}
		previousMAC = intent.JournalMAC
	}
}

func TestPendingRejectsLegacyJournalWithActionableVersionError(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	backupDir := filepath.Join(root, "backups")
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := `{"version":1,"id":"legacy","targetSha256":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}`
	if err := os.WriteFile(filepath.Join(backupDir, applyIntentName), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	applier := &Applier{ConfigPath: filepath.Join(root, "config.yaml"), BackupDir: backupDir, Runtime: &fakeRuntime{}, IntegrityKey: []byte(testApplyIntegrityKey)}
	_, found, err := applier.Pending()
	if found || !errors.Is(err, ErrUnsupportedApplyJournal) || !strings.Contains(err.Error(), "previous FoxOS version") {
		t.Fatalf("Pending() found=%t err=%v", found, err)
	}
}

func TestApplySuccess(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(config, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime := &fakeRuntime{}
	applier := Applier{ConfigPath: config, BackupDir: filepath.Join(root, "backups"), Runtime: runtime, IntegrityKey: []byte(testApplyIntegrityKey), Now: func() time.Time { return time.Date(2026, 7, 26, 1, 2, 3, 0, time.UTC) }}
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
	result, err := (&Applier{ConfigPath: config, BackupDir: filepath.Join(root, "backups"), Runtime: runtime, IntegrityKey: []byte(testApplyIntegrityKey)}).Apply(context.Background(), []byte("new"))
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
	_, err := (&Applier{ConfigPath: config, BackupDir: filepath.Join(root, "backups"), Runtime: &fakeRuntime{validateErr: errors.New("bad")}, IntegrityKey: []byte(testApplyIntegrityKey)}).Apply(context.Background(), []byte("new"))
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
	applier := &Applier{ConfigPath: config, BackupDir: filepath.Join(root, "backups"), Runtime: &fakeRuntime{}, IntegrityKey: []byte(testApplyIntegrityKey)}
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
	journalMAC, err := applier.pendingMAC(intent)
	if err != nil {
		t.Fatal(err)
	}
	intent.JournalMAC = hex.EncodeToString(journalMAC)
	body, err = json.Marshal(intent)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(journalPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, found, err := applier.Pending(); err == nil || found || !strings.Contains(err.Error(), "escapes the backup directory") {
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
			applier := &Applier{ConfigPath: config, BackupDir: filepath.Join(root, "backups"), Runtime: &fakeRuntime{}, IntegrityKey: []byte(testApplyIntegrityKey)}
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
	applier := &Applier{ConfigPath: filepath.Join(root, "config.yaml"), BackupDir: filepath.Join(root, "backups"), Runtime: &fakeRuntime{}, IntegrityKey: []byte(testApplyIntegrityKey)}
	digest := strings.Repeat("b", 64)
	result, err := applier.ApplyPending(context.Background(), []byte("new"), ApplyOperation{ID: "job-42", Kind: "mihomo.restore", TargetDigest: digest, SnapshotLabel: "restored snapshot"})
	if err != nil {
		t.Fatal(err)
	}
	intent, found, err := applier.Pending()
	if err != nil || !found {
		t.Fatalf("Pending() found=%t err=%v", found, err)
	}
	if intent.ID != result.IntentID || intent.OperationID != "job-42" || intent.OperationKind != "mihomo.restore" || intent.TargetDigest != digest || intent.SnapshotLabel != "restored snapshot" || !intent.RequiresSnapshot {
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
