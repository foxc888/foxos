package mihomo

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/foxc888/foxos/internal/domain"
)

const testDigestKey = "0123456789abcdef0123456789abcdef"

type configMemoryStore struct {
	nodes                         []domain.Node
	groups                        []domain.Group
	policies                      []domain.DevicePolicy
	draft                         domain.MihomoDraft
	snapshots                     map[string]domain.MihomoSnapshot
	saveSnapshotErr               error
	saveErrorAfterWrite           bool
	readSnapshotErr               error
	rejectCanceledSnapshotContext bool
	beforeSave                    func(domain.MihomoSnapshot)
	afterSave                     func(*configMemoryStore, domain.MihomoSnapshot)
}

func (s *configMemoryStore) Nodes(context.Context) ([]domain.Node, error)   { return s.nodes, nil }
func (s *configMemoryStore) Groups(context.Context) ([]domain.Group, error) { return s.groups, nil }
func (s *configMemoryStore) DevicePolicies(context.Context) ([]domain.DevicePolicy, error) {
	return s.policies, nil
}
func (s *configMemoryStore) MihomoDraft(context.Context) (domain.MihomoDraft, error) {
	if s.draft.ID == "" {
		return domain.MihomoDraft{}, domain.ErrNotFound
	}
	return s.draft, nil
}
func (s *configMemoryStore) SaveMihomoDraft(_ context.Context, draft domain.MihomoDraft) error {
	draft.Revision++
	s.draft = draft
	return nil
}
func (s *configMemoryStore) SaveMihomoSnapshot(ctx context.Context, snapshot domain.MihomoSnapshot) error {
	if s.rejectCanceledSnapshotContext && ctx.Err() != nil {
		return ctx.Err()
	}
	if s.beforeSave != nil {
		s.beforeSave(snapshot)
	}
	if s.rejectCanceledSnapshotContext && ctx.Err() != nil {
		return ctx.Err()
	}
	if s.saveSnapshotErr != nil && !s.saveErrorAfterWrite {
		return s.saveSnapshotErr
	}
	if s.snapshots == nil {
		s.snapshots = map[string]domain.MihomoSnapshot{}
	}
	s.snapshots[snapshot.ID] = snapshot
	if s.afterSave != nil {
		s.afterSave(s, snapshot)
	}
	if s.saveSnapshotErr != nil {
		return s.saveSnapshotErr
	}
	return nil
}
func (s *configMemoryStore) MihomoSnapshot(ctx context.Context, id string) (domain.MihomoSnapshot, error) {
	if s.rejectCanceledSnapshotContext && ctx.Err() != nil {
		return domain.MihomoSnapshot{}, ctx.Err()
	}
	if s.readSnapshotErr != nil {
		return domain.MihomoSnapshot{}, s.readSnapshotErr
	}
	item, ok := s.snapshots[id]
	if !ok {
		return domain.MihomoSnapshot{}, domain.ErrNotFound
	}
	return item, nil
}
func (s *configMemoryStore) MihomoSnapshots(context.Context, int) ([]domain.MihomoSnapshot, error) {
	items := make([]domain.MihomoSnapshot, 0, len(s.snapshots))
	for _, item := range s.snapshots {
		copy := item
		copy.Body = nil
		items = append(items, copy)
	}
	return items, nil
}

func TestServicePreviewRedactsSecrets(t *testing.T) {
	t.Parallel()
	const credential = "00000000-0000-0000-0000-000000000001"
	store := &configMemoryStore{nodes: []domain.Node{{ID: "n1", Name: "Node", Type: "vless", Server: "example.com", Port: 443, UUID: credential}}}
	service := &Service{Store: store, DigestKey: []byte(testDigestKey)}
	preview, err := service.Preview(context.Background(), domain.MihomoDraft{Mode: "rule", Rules: []string{"MATCH,DIRECT"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(preview.YAML, credential) || strings.Contains(preview.Diff, credential) || !preview.HasSecret || len(preview.Digest) != 64 {
		t.Fatalf("preview=%+v", preview)
	}
}

func TestServicePreviewRedactsWireGuardPreSharedKeyFromYAMLAndDiff(t *testing.T) {
	t.Parallel()
	const preSharedKey = "fixture-wireguard-pre-shared-key"
	store := &configMemoryStore{nodes: []domain.Node{{
		ID: "wg-1", Name: "WireGuard", Type: "wireguard", Server: "198.51.100.10", Port: 51820,
		Extra: map[string]any{
			"private-key":    "fixture-wireguard-private-key",
			"public-key":     "fixture-wireguard-public-key",
			"pre-shared-key": preSharedKey,
		},
	}}}
	service := &Service{Store: store, DigestKey: []byte(testDigestKey)}
	preview, err := service.Preview(context.Background(), domain.MihomoDraft{Mode: "rule", Rules: []string{"MATCH,DIRECT"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(preview.YAML, preSharedKey) || strings.Contains(preview.Diff, preSharedKey) {
		t.Fatalf("WireGuard pre-shared-key leaked from preview: %+v", preview)
	}
	for _, output := range []string{preview.YAML, preview.Diff} {
		if !strings.Contains(output, "pre-shared-key: '***'") {
			t.Fatalf("redaction marker missing from %q", output)
		}
	}
	if !preview.HasSecret {
		t.Fatal("preview did not report redacted secret material")
	}
}

func TestServicePreviewRedactsHysteriaObfsPasswordFromYAMLAndDiff(t *testing.T) {
	t.Parallel()
	const obfsPassword = "fixture-hysteria-obfs-password"
	store := &configMemoryStore{nodes: []domain.Node{{
		ID: "hy2-1", Name: "Hysteria", Type: "hysteria2", Server: "198.51.100.20", Port: 443,
		Password: "fixture-hysteria-password",
		Extra: map[string]any{
			"obfs":          "salamander",
			"obfs-password": obfsPassword,
		},
	}}}
	service := &Service{Store: store, DigestKey: []byte(testDigestKey)}
	preview, err := service.Preview(context.Background(), domain.MihomoDraft{Mode: "rule", Rules: []string{"MATCH,DIRECT"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(preview.YAML, obfsPassword) || strings.Contains(preview.Diff, obfsPassword) {
		t.Fatalf("Hysteria obfs-password leaked from preview: %+v", preview)
	}
	for _, output := range []string{preview.YAML, preview.Diff} {
		if !strings.Contains(output, "obfs-password: '***'") {
			t.Fatalf("redaction marker missing from %q", output)
		}
	}
	if !preview.HasSecret {
		t.Fatal("preview did not report redacted obfs-password")
	}
}

func TestServicePreviewRejectsMissingDigestKey(t *testing.T) {
	t.Parallel()
	service := &Service{Store: &configMemoryStore{}}
	if _, err := service.Preview(context.Background(), domain.MihomoDraft{Mode: "rule"}); !errors.Is(err, ErrMihomoDigestKey) {
		t.Fatalf("Preview() error = %v", err)
	}
}

func TestServiceDraftUsesReachableMixedPortOnFirstRun(t *testing.T) {
	t.Parallel()
	service := &Service{Store: &configMemoryStore{}}
	draft, err := service.Draft(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if draft.MixedPort != 7890 {
		t.Fatalf("mixedPort=%d, want 7890", draft.MixedPort)
	}
}

func TestReadCurrentConfigRejectsSymlink(t *testing.T) {
	t.Parallel()
	target := filepath.Join(t.TempDir(), "target.yaml")
	if err := os.WriteFile(target, []byte("mode: rule\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := readCurrentConfig(link); err == nil {
		t.Fatal("expected symlinked Mihomo configuration rejection")
	}
}

func TestRedactYAML(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		body       string
		wantSecret bool
		contains   string
		excludes   string
	}{
		{name: "nested UUID", body: "proxies:\n  - name: node\n    uuid: credential\n", wantSecret: true, contains: "uuid: '***'", excludes: "credential"},
		{name: "nested password", body: "auth:\n  password: credential\n", wantSecret: true, contains: "password: '***'", excludes: "credential"},
		{name: "normalized pre shared key", body: "proxy:\n  pre_shared_key: credential\n", wantSecret: true, contains: "pre_shared_key: '***'", excludes: "credential"},
		{name: "normalized obfs password", body: "proxy:\n  obfs-password: credential\n", wantSecret: true, contains: "obfs-password: '***'", excludes: "credential"},
		{name: "non-sensitive substring", body: "secretary: visible\n", contains: "secretary: visible"},
		{name: "ordinary value", body: "mode: rule\n", contains: "mode: rule"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			redacted, hasSecret := redactYAML([]byte(test.body))
			if hasSecret != test.wantSecret || !strings.Contains(string(redacted), test.contains) || (test.excludes != "" && strings.Contains(string(redacted), test.excludes)) {
				t.Fatalf("redacted=%q hasSecret=%t", redacted, hasSecret)
			}
		})
	}
}

func TestServiceApplyPersistsDeterministicSnapshot(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store := &configMemoryStore{nodes: []domain.Node{{ID: "n1", Name: "Node", Type: "vless", Server: "example.com", Port: 443, UUID: "credential"}}}
	runtime := &fakeRuntime{}
	service := &Service{Store: store, Applier: &Applier{ConfigPath: filepath.Join(dir, "config.yaml"), BackupDir: filepath.Join(dir, "backups"), Runtime: runtime}, DigestKey: []byte(testDigestKey), Now: func() time.Time { return time.Unix(123, 0) }}
	draft := domain.MihomoDraft{Mode: "rule", Rules: []string{"MATCH,DIRECT"}}
	preview, err := service.Preview(context.Background(), draft)
	if err != nil {
		t.Fatal(err)
	}
	_, first, err := service.ApplyPreview(context.Background(), draft, preview.Digest, "first")
	if err != nil {
		t.Fatal(err)
	}
	_, second, err := service.ApplyPreview(context.Background(), draft, preview.Digest, "second")
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || len(store.snapshots) != 1 {
		t.Fatalf("first=%s second=%s snapshots=%d", first.ID, second.ID, len(store.snapshots))
	}
	_, restored, err := service.Restore(context.Background(), first.ID, "restored")
	if err != nil || restored.Digest != first.Digest || restored.Label != "restored" {
		t.Fatalf("restored=%+v err=%v", restored, err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "config.yaml"))
	if err != nil || !strings.Contains(string(body), "credential") {
		t.Fatalf("body=%q err=%v", body, err)
	}
}

func TestServiceApplyRejectsChangedDigest(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store := &configMemoryStore{nodes: []domain.Node{{ID: "n1", Name: "Node", Type: "vless", Server: "example.com", Port: 443, UUID: "credential"}}}
	service := &Service{Store: store, Applier: &Applier{ConfigPath: filepath.Join(dir, "config.yaml"), BackupDir: filepath.Join(dir, "backups"), Runtime: &fakeRuntime{}}, DigestKey: []byte(testDigestKey)}
	_, _, err := service.ApplyPreview(context.Background(), domain.MihomoDraft{Mode: "rule"}, "wrong", "")
	if err == nil || errors.Is(err, ErrApplyFailed) {
		t.Fatalf("err=%v", err)
	}
}

func TestServicePersistsSnapshotBeforeCompletingJournal(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	config := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(config, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	applier := &Applier{ConfigPath: config, BackupDir: filepath.Join(root, "backups"), Runtime: &fakeRuntime{}}
	store := &configMemoryStore{nodes: []domain.Node{{ID: "n1", Name: "Node", Type: "vless", Server: "example.com", Port: 443, UUID: "credential"}}}
	service := &Service{Store: store, Applier: applier, DigestKey: []byte(testDigestKey)}
	draft := domain.MihomoDraft{Mode: "rule", Rules: []string{"MATCH,DIRECT"}}
	preview, err := service.Preview(context.Background(), draft)
	if err != nil {
		t.Fatal(err)
	}
	sawPending := false
	store.beforeSave = func(snapshot domain.MihomoSnapshot) {
		intent, found, pendingErr := applier.Pending()
		if pendingErr != nil || !found || intent.Phase != "snapshot_save_started" || intent.TargetDigest != snapshot.Digest {
			t.Errorf("snapshot persisted without matching pending journal: intent=%+v found=%t err=%v", intent, found, pendingErr)
			return
		}
		sawPending = true
	}
	ctx := WithApplyOperation(context.Background(), "mihomo.apply", "job-persist-order")
	if _, _, err := service.ApplyPreview(ctx, draft, preview.Digest, "ordered"); err != nil {
		t.Fatal(err)
	}
	if !sawPending {
		t.Fatal("snapshot save did not observe pending journal")
	}
	if _, found, err := applier.Pending(); err != nil || found {
		t.Fatalf("journal not completed after snapshot save: found=%t err=%v", found, err)
	}
}

func TestServiceSnapshotFailureRollsBackWithIndependentContext(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	config := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(config, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime := &contextRecordingRuntime{}
	applier := &Applier{ConfigPath: config, BackupDir: filepath.Join(root, "backups"), Runtime: runtime}
	ctx, cancel := context.WithCancel(context.Background())
	store := &configMemoryStore{
		nodes:           []domain.Node{{ID: "n1", Name: "Node", Type: "vless", Server: "example.com", Port: 443, UUID: "credential"}},
		saveSnapshotErr: errors.New("database unavailable"),
		beforeSave:      func(domain.MihomoSnapshot) { cancel() },
	}
	service := &Service{Store: store, Applier: applier, DigestKey: []byte(testDigestKey)}
	draft := domain.MihomoDraft{Mode: "rule", Rules: []string{"MATCH,DIRECT"}}
	preview, err := service.Preview(ctx, draft)
	if err != nil {
		t.Fatal(err)
	}
	ctx = WithApplyOperation(ctx, "mihomo.apply", "job-cancelled")
	result, _, err := service.ApplyPreview(ctx, draft, preview.Digest, "failure")
	if !errors.Is(err, ErrApplyFailed) || !result.RolledBack {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	body, readErr := os.ReadFile(config)
	if readErr != nil || string(body) != "old" {
		t.Fatalf("config=%q err=%v", body, readErr)
	}
	if len(runtime.reloadContextErrors) != 2 || runtime.reloadContextErrors[1] != nil {
		t.Fatalf("rollback reused canceled request context: %v", runtime.reloadContextErrors)
	}
	if _, found, err := applier.Pending(); err != nil || found {
		t.Fatalf("successful compensation left journal: found=%t err=%v", found, err)
	}
}

func TestServiceSnapshotPersistenceIgnoresCanceledRequestContext(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	config := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(config, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	store := &configMemoryStore{
		nodes:                         []domain.Node{{ID: "n1", Name: "Node", Type: "vless", Server: "example.com", Port: 443, UUID: "credential"}},
		beforeSave:                    func(domain.MihomoSnapshot) { cancel() },
		rejectCanceledSnapshotContext: true,
	}
	service := &Service{Store: store, Applier: &Applier{ConfigPath: config, BackupDir: filepath.Join(root, "backups"), Runtime: &fakeRuntime{}}, DigestKey: []byte(testDigestKey)}
	draft := domain.MihomoDraft{Mode: "rule", Rules: []string{"MATCH,DIRECT"}}
	preview, err := service.Preview(ctx, draft)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.ApplyPreview(WithApplyOperation(ctx, "mihomo.apply", "job-detached-save"), draft, preview.Digest, "detached"); err != nil {
		t.Fatal(err)
	}
	if ctx.Err() != context.Canceled {
		t.Fatalf("request context was not canceled: %v", ctx.Err())
	}
}

func TestServiceSaveErrorUsesAuthoritativeSnapshotReadback(t *testing.T) {
	tests := []struct {
		name             string
		readErr          error
		wantErr          error
		wantJournal      bool
		writeBeforeError bool
	}{
		{name: "exact snapshot was committed", wantJournal: false, writeBeforeError: true},
		{name: "readback error remains unconfirmed", readErr: errors.New("readback unavailable"), wantErr: ErrPendingApply, wantJournal: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			config := filepath.Join(root, "config.yaml")
			if err := os.WriteFile(config, []byte("old"), 0o600); err != nil {
				t.Fatal(err)
			}
			runtime := &fakeRuntime{}
			applier := &Applier{ConfigPath: config, BackupDir: filepath.Join(root, "backups"), Runtime: runtime}
			store := &configMemoryStore{
				nodes:               []domain.Node{{ID: "n1", Name: "Node", Type: "vless", Server: "example.com", Port: 443, UUID: "credential"}},
				saveSnapshotErr:     errors.New("commit result unavailable"),
				saveErrorAfterWrite: test.writeBeforeError,
				readSnapshotErr:     test.readErr,
			}
			service := &Service{Store: store, Applier: applier, DigestKey: []byte(testDigestKey)}
			draft := domain.MihomoDraft{Mode: "rule", Rules: []string{"MATCH,DIRECT"}}
			preview, err := service.Preview(context.Background(), draft)
			if err != nil {
				t.Fatal(err)
			}
			result, _, err := service.ApplyPreview(WithApplyOperation(context.Background(), "mihomo.apply", "job-save-error"), draft, preview.Digest, "uncertain")
			if test.wantErr == nil && err != nil {
				t.Fatal(err)
			}
			if test.wantErr != nil && !errors.Is(err, test.wantErr) {
				t.Fatalf("ApplyPreview() error=%v", err)
			}
			if result.RolledBack || runtime.reloads != 1 {
				t.Fatalf("ambiguous save was rolled back: result=%+v reloads=%d", result, runtime.reloads)
			}
			intent, found, pendingErr := applier.Pending()
			if pendingErr != nil || found != test.wantJournal {
				t.Fatalf("journal found=%t want=%t intent=%+v err=%v", found, test.wantJournal, intent, pendingErr)
			}
			if found && intent.Phase != "snapshot_unconfirmed" {
				t.Fatalf("intent=%+v", intent)
			}
		})
	}
}

func TestServiceSaveErrorRejectsStaleSnapshotLabelReadback(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	config := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(config, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime := &fakeRuntime{}
	applier := &Applier{ConfigPath: config, BackupDir: filepath.Join(root, "backups"), Runtime: runtime}
	store := &configMemoryStore{
		nodes:           []domain.Node{{ID: "n1", Name: "Node", Type: "vless", Server: "example.com", Port: 443, UUID: "credential"}},
		saveSnapshotErr: errors.New("database unavailable before write"),
	}
	service := &Service{Store: store, Applier: applier, DigestKey: []byte(testDigestKey)}
	draft := domain.MihomoDraft{Mode: "rule", Rules: []string{"MATCH,DIRECT"}}
	body, err := Generate(Input{Mode: draft.Mode, Rules: draft.Rules, Nodes: store.nodes})
	if err != nil {
		t.Fatal(err)
	}
	digest, err := service.digest(body)
	if err != nil {
		t.Fatal(err)
	}
	id, err := snapshotID(digest)
	if err != nil {
		t.Fatal(err)
	}
	store.snapshots = map[string]domain.MihomoSnapshot{
		id: {ID: id, Digest: digest, Label: "old label", Body: body, CreatedAt: time.Unix(1, 0).UTC()},
	}

	result, snapshot, err := service.ApplyPreview(WithApplyOperation(context.Background(), "mihomo.apply", "job-stale-label"), draft, digest, "new label")
	if !errors.Is(err, ErrPendingApply) {
		t.Fatalf("ApplyPreview() error=%v, want %v", err, ErrPendingApply)
	}
	if result.RolledBack || runtime.reloads != 1 || snapshot.Label != "new label" {
		t.Fatalf("result=%+v reloads=%d snapshot=%+v", result, runtime.reloads, snapshot)
	}
	if stored := store.snapshots[id]; stored.Label != "old label" {
		t.Fatalf("stale snapshot was unexpectedly replaced: %+v", stored)
	}
	intent, found, pendingErr := applier.Pending()
	if pendingErr != nil || !found || intent.Phase != "snapshot_unconfirmed" {
		t.Fatalf("journal intent=%+v found=%t err=%v", intent, found, pendingErr)
	}
}

func TestServiceRollbackFailureIsRecoveredOnRestart(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	config := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(config, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime := &fakeRuntime{rollbackReloadErr: errors.New("rollback reload unavailable")}
	applier := &Applier{ConfigPath: config, BackupDir: filepath.Join(root, "backups"), Runtime: runtime}
	store := &configMemoryStore{
		nodes:           []domain.Node{{ID: "n1", Name: "Node", Type: "vless", Server: "example.com", Port: 443, UUID: "credential"}},
		saveSnapshotErr: errors.New("database unavailable"),
	}
	service := &Service{Store: store, Applier: applier, DigestKey: []byte(testDigestKey)}
	draft := domain.MihomoDraft{Mode: "rule", Rules: []string{"MATCH,DIRECT"}}
	preview, err := service.Preview(context.Background(), draft)
	if err != nil {
		t.Fatal(err)
	}
	result, _, err := service.ApplyPreview(WithApplyOperation(context.Background(), "mihomo.apply", "job-recover"), draft, preview.Digest, "failure")
	if !errors.Is(err, ErrRollbackFailed) || result.RolledBack {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	intent, found, err := applier.Pending()
	if err != nil || !found || intent.Phase != "rollback_required" {
		t.Fatalf("recoverable journal missing: intent=%+v found=%t err=%v", intent, found, err)
	}
	runtime.rollbackReloadErr = nil
	if err := service.RecoverPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	body, readErr := os.ReadFile(config)
	if readErr != nil || string(body) != "old" {
		t.Fatalf("config=%q err=%v", body, readErr)
	}
	if _, found, err := applier.Pending(); err != nil || found {
		t.Fatalf("restart recovery left journal: found=%t err=%v", found, err)
	}
}

func TestServiceCommittedSnapshotOnlyCompletesJournalAfterRestart(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	runtime := &fakeRuntime{}
	applier := &Applier{ConfigPath: filepath.Join(root, "config.yaml"), BackupDir: filepath.Join(root, "backups"), Runtime: runtime}
	clearCalls := 0
	applier.clearPendingHook = func(string) error {
		clearCalls++
		if clearCalls == 1 {
			return errors.New("journal directory temporarily unavailable")
		}
		return nil
	}
	store := &configMemoryStore{nodes: []domain.Node{{ID: "n1", Name: "Node", Type: "vless", Server: "example.com", Port: 443, UUID: "credential"}}}
	service := &Service{Store: store, Applier: applier, DigestKey: []byte(testDigestKey)}
	draft := domain.MihomoDraft{Mode: "rule", Rules: []string{"MATCH,DIRECT"}}
	preview, err := service.Preview(context.Background(), draft)
	if err != nil {
		t.Fatal(err)
	}
	_, snapshot, err := service.ApplyPreview(WithApplyOperation(context.Background(), "mihomo.apply", "job-cleanup"), draft, preview.Digest, "committed")
	if !errors.Is(err, ErrPendingApply) {
		t.Fatalf("ApplyPreview() error=%v", err)
	}
	if _, ok := store.snapshots[snapshot.ID]; !ok {
		t.Fatal("snapshot was not committed before cleanup failure")
	}
	if err := service.RecoverPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	if runtime.reloads != 1 {
		t.Fatalf("committed state was rolled back or reloaded: reloads=%d", runtime.reloads)
	}
	if _, found, err := applier.Pending(); err != nil || found {
		t.Fatalf("committed journal not finalized: found=%t err=%v", found, err)
	}
}

func TestServiceSnapshotReadbackUncertaintyNeverRollsBack(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*configMemoryStore)
		repair    func(*configMemoryStore, domain.MihomoSnapshot)
	}{
		{
			name: "read error",
			configure: func(store *configMemoryStore) {
				store.readSnapshotErr = errors.New("readback unavailable")
			},
			repair: func(store *configMemoryStore, _ domain.MihomoSnapshot) { store.readSnapshotErr = nil },
		},
		{
			name: "id mismatch",
			configure: func(store *configMemoryStore) {
				store.afterSave = func(current *configMemoryStore, snapshot domain.MihomoSnapshot) {
					changed := snapshot
					changed.ID = "snapshot-other"
					current.snapshots[snapshot.ID] = changed
				}
			},
			repair: func(store *configMemoryStore, snapshot domain.MihomoSnapshot) {
				store.snapshots[snapshot.ID] = snapshot
			},
		},
		{
			name: "digest mismatch",
			configure: func(store *configMemoryStore) {
				store.afterSave = func(current *configMemoryStore, snapshot domain.MihomoSnapshot) {
					changed := snapshot
					changed.Digest = strings.Repeat("f", 64)
					current.snapshots[snapshot.ID] = changed
				}
			},
			repair: func(store *configMemoryStore, snapshot domain.MihomoSnapshot) {
				store.snapshots[snapshot.ID] = snapshot
			},
		},
		{
			name: "body mismatch",
			configure: func(store *configMemoryStore) {
				store.afterSave = func(current *configMemoryStore, snapshot domain.MihomoSnapshot) {
					changed := snapshot
					changed.Body = []byte("mode: global\n")
					current.snapshots[snapshot.ID] = changed
				}
			},
			repair: func(store *configMemoryStore, snapshot domain.MihomoSnapshot) {
				store.snapshots[snapshot.ID] = snapshot
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			config := filepath.Join(root, "config.yaml")
			if err := os.WriteFile(config, []byte("old"), 0o600); err != nil {
				t.Fatal(err)
			}
			runtime := &fakeRuntime{}
			applier := &Applier{ConfigPath: config, BackupDir: filepath.Join(root, "backups"), Runtime: runtime}
			store := &configMemoryStore{nodes: []domain.Node{{ID: "n1", Name: "Node", Type: "vless", Server: "example.com", Port: 443, UUID: "credential"}}}
			test.configure(store)
			service := &Service{Store: store, Applier: applier, DigestKey: []byte(testDigestKey)}
			draft := domain.MihomoDraft{Mode: "rule", Rules: []string{"MATCH,DIRECT"}}
			preview, err := service.Preview(context.Background(), draft)
			if err != nil {
				t.Fatal(err)
			}
			_, snapshot, err := service.ApplyPreview(WithApplyOperation(context.Background(), "mihomo.apply", "job-readback"), draft, preview.Digest, "uncertain")
			if !errors.Is(err, ErrPendingApply) {
				t.Fatalf("ApplyPreview() error=%v", err)
			}
			body, readErr := os.ReadFile(config)
			if readErr != nil || string(body) == "old" || runtime.reloads != 1 {
				t.Fatalf("uncertain commit was rolled back: body=%q reloads=%d err=%v", body, runtime.reloads, readErr)
			}
			intent, found, pendingErr := applier.Pending()
			if pendingErr != nil || !found || intent.Phase != "snapshot_unconfirmed" {
				t.Fatalf("unconfirmed journal missing: intent=%+v found=%t err=%v", intent, found, pendingErr)
			}
			test.repair(store, snapshot)
			if err := service.RecoverPending(context.Background()); err != nil {
				t.Fatal(err)
			}
			if runtime.reloads != 1 {
				t.Fatalf("restart reconciliation reloaded committed config: %d", runtime.reloads)
			}
			if _, found, err := applier.Pending(); err != nil || found {
				t.Fatalf("reconciled journal remains: found=%t err=%v", found, err)
			}
		})
	}
}

func TestServiceMissingSnapshotReadbackRollsBack(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	config := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(config, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime := &fakeRuntime{}
	applier := &Applier{ConfigPath: config, BackupDir: filepath.Join(root, "backups"), Runtime: runtime}
	store := &configMemoryStore{nodes: []domain.Node{{ID: "n1", Name: "Node", Type: "vless", Server: "example.com", Port: 443, UUID: "credential"}}}
	store.afterSave = func(current *configMemoryStore, snapshot domain.MihomoSnapshot) {
		delete(current.snapshots, snapshot.ID)
	}
	service := &Service{Store: store, Applier: applier, DigestKey: []byte(testDigestKey)}
	draft := domain.MihomoDraft{Mode: "rule", Rules: []string{"MATCH,DIRECT"}}
	preview, err := service.Preview(context.Background(), draft)
	if err != nil {
		t.Fatal(err)
	}
	result, _, err := service.ApplyPreview(WithApplyOperation(context.Background(), "mihomo.apply", "job-missing-readback"), draft, preview.Digest, "missing")
	if !errors.Is(err, ErrApplyFailed) || !result.RolledBack {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	body, readErr := os.ReadFile(config)
	if readErr != nil || string(body) != "old" || runtime.reloads != 2 {
		t.Fatalf("body=%q reloads=%d err=%v", body, runtime.reloads, readErr)
	}
}

func TestServiceRestartRollsBackSnapshotNotFound(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	config := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(config, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime := &fakeRuntime{}
	applier := &Applier{ConfigPath: config, BackupDir: filepath.Join(root, "backups"), Runtime: runtime}
	store := &configMemoryStore{nodes: []domain.Node{{ID: "n1", Name: "Node", Type: "vless", Server: "example.com", Port: 443, UUID: "credential"}}, readSnapshotErr: errors.New("readback unavailable")}
	store.afterSave = func(current *configMemoryStore, snapshot domain.MihomoSnapshot) {
		delete(current.snapshots, snapshot.ID)
	}
	service := &Service{Store: store, Applier: applier, DigestKey: []byte(testDigestKey)}
	draft := domain.MihomoDraft{Mode: "rule", Rules: []string{"MATCH,DIRECT"}}
	preview, err := service.Preview(context.Background(), draft)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.ApplyPreview(WithApplyOperation(context.Background(), "mihomo.apply", "job-restart-not-found"), draft, preview.Digest, "restart"); !errors.Is(err, ErrPendingApply) {
		t.Fatalf("ApplyPreview() error=%v", err)
	}
	store.readSnapshotErr = nil
	if err := service.RecoverPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	body, readErr := os.ReadFile(config)
	if readErr != nil || string(body) != "old" || runtime.reloads != 2 {
		t.Fatalf("body=%q reloads=%d err=%v", body, runtime.reloads, readErr)
	}
	if _, found, err := applier.Pending(); err != nil || found {
		t.Fatalf("journal remains: found=%t err=%v", found, err)
	}
}

func TestServiceRestoreRequiresSnapshotReadback(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	body := []byte("mode: rule\n")
	digest, err := domainDigest([]byte(testDigestKey), body)
	if err != nil {
		t.Fatal(err)
	}
	snapshotIDValue, err := snapshotID(digest)
	if err != nil {
		t.Fatal(err)
	}
	store := &configMemoryStore{snapshots: map[string]domain.MihomoSnapshot{snapshotIDValue: {ID: snapshotIDValue, Digest: digest, Body: body}}}
	store.afterSave = func(current *configMemoryStore, snapshot domain.MihomoSnapshot) {
		changed := snapshot
		changed.Body = []byte("mode: global\n")
		current.snapshots[snapshot.ID] = changed
	}
	applier := &Applier{ConfigPath: filepath.Join(root, "config.yaml"), BackupDir: filepath.Join(root, "backups"), Runtime: &fakeRuntime{}}
	service := &Service{Store: store, Applier: applier, DigestKey: []byte(testDigestKey)}
	_, _, err = service.Restore(WithApplyOperation(context.Background(), "mihomo.restore", "job-restore-readback"), snapshotIDValue, "restored")
	if !errors.Is(err, ErrPendingApply) {
		t.Fatalf("Restore() error=%v", err)
	}
	intent, found, pendingErr := applier.Pending()
	if pendingErr != nil || !found || intent.OperationKind != "mihomo.restore" || intent.Phase != "snapshot_unconfirmed" {
		t.Fatalf("restore journal=%+v found=%t err=%v", intent, found, pendingErr)
	}
}

type contextRecordingRuntime struct {
	reloadContextErrors []error
}

func (*contextRecordingRuntime) Validate(context.Context, []byte) error { return nil }
func (r *contextRecordingRuntime) Reload(ctx context.Context) error {
	r.reloadContextErrors = append(r.reloadContextErrors, ctx.Err())
	return nil
}
func (*contextRecordingRuntime) Healthy(context.Context) error { return nil }

func TestServiceRestoreRejectsCorruptSnapshotBeforeApply(t *testing.T) {
	t.Parallel()
	body := []byte("mode: rule\n")
	validDigest, err := domainDigest([]byte(testDigestKey), body)
	if err != nil {
		t.Fatal(err)
	}
	otherKeyDigest, err := domainDigest([]byte("abcdef0123456789abcdef0123456789"), body)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		digest string
		body   []byte
	}{
		{name: "body changed", digest: validDigest, body: []byte("mode: global\n")},
		{name: "key changed", digest: otherKeyDigest, body: body},
		{name: "invalid digest", digest: "not-a-sha256", body: body},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			runtime := &fakeRuntime{}
			store := &configMemoryStore{snapshots: map[string]domain.MihomoSnapshot{
				"snapshot": {ID: "snapshot", Digest: test.digest, Body: test.body},
			}}
			service := &Service{Store: store, Applier: &Applier{ConfigPath: filepath.Join(root, "config.yaml"), BackupDir: filepath.Join(root, "backups"), Runtime: runtime}, DigestKey: []byte(testDigestKey)}
			if _, _, err := service.Restore(context.Background(), "snapshot", ""); !errors.Is(err, ErrMihomoSnapshotCorrupt) {
				t.Fatalf("Restore() error = %v", err)
			}
			if runtime.reloads != 0 {
				t.Fatalf("runtime reloads = %d, want 0", runtime.reloads)
			}
			if _, err := os.Stat(filepath.Join(root, "config.yaml")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("config write occurred before integrity rejection: %v", err)
			}
		})
	}
}

func TestDomainDigestRequiresAKeyAndSeparatesKeyDomains(t *testing.T) {
	t.Parallel()
	body := []byte("password: low-entropy-credential\n")
	first, err := domainDigest([]byte(testDigestKey), body)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := domainDigest([]byte(testDigestKey), body)
	if err != nil {
		t.Fatal(err)
	}
	second, err := domainDigest([]byte("abcdef0123456789abcdef0123456789"), body)
	if err != nil {
		t.Fatal(err)
	}
	if first != repeated || first == second || len(first) != sha256.Size*2 {
		t.Fatalf("unexpected keyed digest behavior: first=%q repeated=%q second=%q", first, repeated, second)
	}
	if _, err := domainDigest([]byte("short"), body); !errors.Is(err, ErrMihomoDigestKey) {
		t.Fatalf("short key error = %v", err)
	}
}
