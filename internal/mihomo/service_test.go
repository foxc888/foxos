package mihomo

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/foxc888/foxos/internal/domain"
)

type configMemoryStore struct {
	nodes     []domain.Node
	groups    []domain.Group
	policies  []domain.DevicePolicy
	draft     domain.MihomoDraft
	snapshots map[string]domain.MihomoSnapshot
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
func (s *configMemoryStore) SaveMihomoSnapshot(_ context.Context, snapshot domain.MihomoSnapshot) error {
	if s.snapshots == nil {
		s.snapshots = map[string]domain.MihomoSnapshot{}
	}
	s.snapshots[snapshot.ID] = snapshot
	return nil
}
func (s *configMemoryStore) MihomoSnapshot(_ context.Context, id string) (domain.MihomoSnapshot, error) {
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
	service := &Service{Store: store}
	preview, err := service.Preview(context.Background(), domain.MihomoDraft{Mode: "rule", Rules: []string{"MATCH,DIRECT"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(preview.YAML, credential) || strings.Contains(preview.Diff, credential) || !preview.HasSecret || len(preview.Digest) != 64 {
		t.Fatalf("preview=%+v", preview)
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
	service := &Service{Store: store, Applier: &Applier{ConfigPath: filepath.Join(dir, "config.yaml"), BackupDir: filepath.Join(dir, "backups"), Runtime: runtime}, Now: func() time.Time { return time.Unix(123, 0) }}
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
	body, err := os.ReadFile(filepath.Join(dir, "config.yaml"))
	if err != nil || !strings.Contains(string(body), "credential") {
		t.Fatalf("body=%q err=%v", body, err)
	}
}

func TestServiceApplyRejectsChangedDigest(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store := &configMemoryStore{nodes: []domain.Node{{ID: "n1", Name: "Node", Type: "vless", Server: "example.com", Port: 443, UUID: "credential"}}}
	service := &Service{Store: store, Applier: &Applier{ConfigPath: filepath.Join(dir, "config.yaml"), BackupDir: filepath.Join(dir, "backups"), Runtime: &fakeRuntime{}}}
	_, _, err := service.ApplyPreview(context.Background(), domain.MihomoDraft{Mode: "rule"}, "wrong", "")
	if err == nil || errors.Is(err, ErrApplyFailed) {
		t.Fatalf("err=%v", err)
	}
}

func TestServiceRestoreRejectsCorruptSnapshotBeforeApply(t *testing.T) {
	t.Parallel()
	body := []byte("mode: rule\n")
	validDigest, err := digest(body)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		digest string
		body   []byte
	}{
		{name: "body changed", digest: validDigest, body: []byte("mode: global\n")},
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
			service := &Service{Store: store, Applier: &Applier{ConfigPath: filepath.Join(root, "config.yaml"), BackupDir: filepath.Join(root, "backups"), Runtime: runtime}}
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
