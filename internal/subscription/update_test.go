package subscription

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/foxc888/foxos/internal/domain"
	storepkg "github.com/foxc888/foxos/internal/store/sqlite"
)

type updateSourceStore struct {
	item         domain.Subscription
	lastDigest   string
	lastError    string
	success      bool
	beforeResult func()
}

func (s *updateSourceStore) Subscription(context.Context, string) (domain.Subscription, error) {
	item := s.item
	if item.SettingsRevision < 1 {
		item.SettingsRevision = 1
	}
	return item, nil
}
func (s *updateSourceStore) UpdateSubscriptionResult(_ context.Context, _ string, settingsRevision int64, digest, message string, success bool) error {
	if s.beforeResult != nil {
		s.beforeResult()
	}
	currentRevision := s.item.SettingsRevision
	if currentRevision < 1 {
		currentRevision = 1
	}
	if settingsRevision != currentRevision {
		return domain.ErrSubscriptionSettingsStale
	}
	s.lastDigest = digest
	s.lastError = message
	s.success = success
	s.item.LastDigest = digest
	s.item.LastError = message
	return nil
}

type updateNodeStore struct {
	existing []domain.Node
	replaced []domain.Node
	err      error
}

type updateAtomicStore struct {
	nodes  *updateNodeStore
	source *updateSourceStore
	calls  int
	err    error
}

func (s *updateAtomicStore) ApplySubscriptionUpdate(_ context.Context, _ string, nodes []domain.Node, digest string, settingsRevision int64) error {
	if s.err != nil {
		return s.err
	}
	currentRevision := s.source.item.SettingsRevision
	if currentRevision < 1 {
		currentRevision = 1
	}
	if settingsRevision != currentRevision {
		return domain.ErrSubscriptionSettingsStale
	}
	s.calls++
	s.nodes.existing = append([]domain.Node(nil), nodes...)
	s.source.item.LastDigest = digest
	s.source.lastDigest = digest
	s.source.success = true
	return nil
}

func (s *updateNodeStore) SubscriptionNodes(context.Context, string) ([]domain.Node, error) {
	return append([]domain.Node(nil), s.existing...), nil
}
func (s *updateNodeStore) ReplaceSubscriptionNodes(_ context.Context, _ string, nodes []domain.Node) error {
	if s.err != nil {
		return s.err
	}
	s.replaced = append([]domain.Node(nil), nodes...)
	s.existing = append([]domain.Node(nil), nodes...)
	return nil
}

type updateFetcher struct {
	result Result
	err    error
}

func (f *updateFetcher) Fetch(context.Context, string) (Result, error) { return f.result, f.err }

type blockingUpdateFetcher struct {
	started chan struct{}
	release chan struct{}
	result  Result
	err     error
}

func (f *blockingUpdateFetcher) Fetch(context.Context, string) (Result, error) {
	close(f.started)
	<-f.release
	return f.result, f.err
}

func testIdentityHasher() IdentityHasher {
	hasher, err := NewIdentityHasher([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		panic(err)
	}
	return hasher
}

func TestUpdaterPreviewDeduplicatesAndUsesStableIDs(t *testing.T) {
	t.Parallel()
	sources := &updateSourceStore{item: domain.Subscription{ID: "source-a", Name: "Primary", URL: "https://example.com/source"}}
	nodes := &updateNodeStore{}
	fetcher := &updateFetcher{result: Result{Digest: "digest-a", Body: []byte("fixture")}}
	updater := Updater{Sources: sources, Nodes: nodes, Fetcher: fetcher, IdentityHasher: testIdentityHasher(), Parser: func([]byte) ([]domain.Node, error) {
		return []domain.Node{
			{ID: "remote-a", Name: "East", Type: "vless", Server: "example.com", Port: 443, UUID: "fixture-uuid"},
			{ID: "remote-b", Name: "Renamed", Type: "vless", Server: "example.com", Port: 443, UUID: "fixture-uuid"},
		}, nil
	}}
	preview, err := updater.Preview(context.Background(), "source-a")
	if err != nil {
		t.Fatal(err)
	}
	if preview.Plan.NodeCount != 1 || preview.Plan.AddCount != 1 || len(preview.Nodes) != 1 {
		t.Fatalf("preview=%+v", preview)
	}
	if preview.Nodes[0].SubscriptionID != "source-a" || len(preview.Nodes[0].ID) != len("sub-")+24 {
		t.Fatalf("node=%+v", preview.Nodes[0])
	}
	firstID := preview.Nodes[0].ID
	updater.Parser = func([]byte) ([]domain.Node, error) {
		return []domain.Node{{Name: "Different display name", Type: "vless", Server: "example.com", Port: 443, UUID: "fixture-uuid"}}, nil
	}
	next, err := updater.Preview(context.Background(), "source-a")
	if err != nil || next.Nodes[0].ID != firstID {
		t.Fatalf("next=%+v err=%v", next, err)
	}
}

func TestUpdaterApplyRejectsContentChangedAfterPreview(t *testing.T) {
	t.Parallel()
	sources := &updateSourceStore{item: domain.Subscription{ID: "source-a", Name: "Primary", URL: "https://example.com/source"}}
	nodes := &updateNodeStore{}
	fetcher := &updateFetcher{result: Result{Digest: "digest-a", Body: []byte("fixture")}}
	updater := Updater{Sources: sources, Nodes: nodes, Fetcher: fetcher, IdentityHasher: testIdentityHasher(), Parser: func([]byte) ([]domain.Node, error) {
		return []domain.Node{{Name: "East", Type: "vless", Server: "example.com", Port: 443, UUID: "fixture-uuid"}}, nil
	}}
	preview, err := updater.Preview(context.Background(), "source-a")
	if err != nil {
		t.Fatal(err)
	}
	fetcher.result.Digest = "digest-b"
	if _, err := updater.Apply(context.Background(), "source-a", &preview.Plan); !errors.Is(err, ErrContentChanged) {
		t.Fatalf("err=%v", err)
	}
	if len(nodes.replaced) != 0 || sources.success || sources.lastError == "" {
		t.Fatalf("nodes=%+v source=%+v", nodes.replaced, sources)
	}
}

func TestUpdaterRejectsOldResultsAfterEverySettingsChange(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		failure string
		change  func(*domain.Subscription)
		assert  func(domain.Subscription) bool
	}{
		{name: "url during fetch failure", failure: "fetch", change: func(item *domain.Subscription) { item.URL = "https://example.com/source-b" }, assert: func(item domain.Subscription) bool { return item.URL == "https://example.com/source-b" }},
		{name: "name during parse failure", failure: "parse", change: func(item *domain.Subscription) { item.Name = "Renamed" }, assert: func(item domain.Subscription) bool { return item.Name == "Renamed" }},
		{name: "enabled during successful fetch", change: func(item *domain.Subscription) { item.Enabled = false }, assert: func(item domain.Subscription) bool { return !item.Enabled }},
		{name: "interval during successful fetch", change: func(item *domain.Subscription) { item.Interval = 7200 }, assert: func(item domain.Subscription) bool { return item.Interval == 7200 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			store, err := storepkg.Open(filepath.Join(t.TempDir(), "foxos.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			ctx := context.Background()
			source := domain.Subscription{ID: "source-a", Name: "Primary", URL: "https://example.com/source-a", Enabled: true, Interval: 3600}
			if err := store.CreateSubscription(ctx, source); err != nil {
				t.Fatal(err)
			}
			oldNode := domain.Node{ID: "legacy-node", Name: "Primary / Existing", Type: "vless", Server: "old.example", Port: 443, UUID: "old-credential", SubscriptionID: source.ID}
			if err := store.ReplaceSubscriptionNodes(ctx, source.ID, []domain.Node{oldNode}); err != nil {
				t.Fatal(err)
			}
			fetcher := &blockingUpdateFetcher{started: make(chan struct{}), release: make(chan struct{}), result: Result{Digest: "new-digest", Body: []byte("fixture")}}
			if test.failure == "fetch" {
				fetcher.err = errors.New("old source fetch failed")
			}
			parser := func([]byte) ([]domain.Node, error) {
				if test.failure == "parse" {
					return nil, errors.New("old source parse failed")
				}
				return []domain.Node{{Name: "New", Type: "vless", Server: "new.example", Port: 443, UUID: "new-credential"}}, nil
			}
			updater := Updater{Sources: store, Nodes: store, Atomic: store, Fetcher: fetcher, IdentityHasher: testIdentityHasher(), Parser: parser}
			result := make(chan error, 1)
			go func() {
				_, applyErr := updater.Apply(ctx, source.ID, nil)
				result <- applyErr
			}()
			<-fetcher.started
			changed, err := store.Subscription(ctx, source.ID)
			if err != nil {
				t.Fatal(err)
			}
			test.change(&changed)
			if err := store.UpdateSubscription(ctx, changed); err != nil {
				t.Fatal(err)
			}
			close(fetcher.release)
			if err := <-result; !errors.Is(err, domain.ErrSubscriptionSettingsStale) {
				t.Fatalf("apply error=%v", err)
			}
			stored, err := store.Subscription(ctx, source.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !test.assert(stored) || stored.LastDigest != "" || stored.LastError != "" || !stored.LastAttemptAt.IsZero() || !stored.LastSuccessAt.IsZero() {
				t.Fatalf("stale result changed source state: %+v", stored)
			}
			nodes, err := store.SubscriptionNodes(ctx, source.ID)
			if err != nil || len(nodes) != 1 || nodes[0].ID != oldNode.ID {
				t.Fatalf("stale result changed nodes: %+v err=%v", nodes, err)
			}
		})
	}
}

func TestUpdaterExpectedPlanRevisionMismatchDoesNotRecordFailure(t *testing.T) {
	t.Parallel()
	sources := &updateSourceStore{item: domain.Subscription{ID: "source-a", Name: "Primary", URL: "https://example.com/source", Enabled: true, Interval: 3600, SettingsRevision: 1}}
	nodes := &updateNodeStore{}
	atomic := &updateAtomicStore{nodes: nodes, source: sources}
	updater := Updater{Sources: sources, Nodes: nodes, Atomic: atomic, Fetcher: &updateFetcher{result: Result{Digest: "digest", Body: []byte("fixture")}}, IdentityHasher: testIdentityHasher(), Parser: func([]byte) ([]domain.Node, error) {
		return []domain.Node{{Name: "Node", Type: "vless", Server: "example.com", Port: 443, UUID: "credential"}}, nil
	}}
	preview, err := updater.Preview(context.Background(), sources.item.ID)
	if err != nil {
		t.Fatal(err)
	}
	sources.item.SettingsRevision++
	sources.item.Name = "Renamed"
	if _, err := updater.Apply(context.Background(), sources.item.ID, &preview.Plan); !errors.Is(err, domain.ErrSubscriptionSettingsStale) {
		t.Fatalf("apply error=%v", err)
	}
	if sources.lastError != "" || sources.lastDigest != "" || sources.success || atomic.calls != 0 {
		t.Fatalf("stale expected plan wrote source state: source=%+v calls=%d", sources, atomic.calls)
	}
}

func TestUpdaterApplyRetainsOldNodesOnAtomicStoreFailure(t *testing.T) {
	t.Parallel()
	old := domain.Node{ID: "old", Name: "Old", Type: "vless", Server: "old.example", Port: 443, UUID: "old-fixture", SubscriptionID: "source-a"}
	sources := &updateSourceStore{item: domain.Subscription{ID: "source-a", Name: "Primary", URL: "https://example.com/source"}}
	nodes := &updateNodeStore{existing: []domain.Node{old}}
	atomic := &updateAtomicStore{nodes: nodes, source: sources, err: errors.New("referenced")}
	updater := Updater{Sources: sources, Nodes: nodes, Atomic: atomic, Fetcher: &updateFetcher{result: Result{Digest: "digest", Body: []byte("fixture")}}, IdentityHasher: testIdentityHasher(), Parser: func([]byte) ([]domain.Node, error) {
		return []domain.Node{{Name: "New", Type: "vless", Server: "new.example", Port: 443, UUID: "new-fixture"}}, nil
	}}
	if _, err := updater.Apply(context.Background(), "source-a", nil); err == nil {
		t.Fatal("expected replacement error")
	}
	if len(nodes.existing) != 1 || nodes.existing[0].ID != old.ID || sources.lastError == "" {
		t.Fatalf("nodes=%+v source=%+v", nodes.existing, sources)
	}
}

func TestUpdaterApplyCheckpointsDurablePhases(t *testing.T) {
	t.Parallel()
	sources := &updateSourceStore{item: domain.Subscription{ID: "source-a", Name: "Primary", URL: "https://example.com/source"}}
	nodes := &updateNodeStore{}
	atomic := &updateAtomicStore{nodes: nodes, source: sources}
	updater := Updater{Sources: sources, Nodes: nodes, Atomic: atomic, Fetcher: &updateFetcher{result: Result{Digest: "digest-a", Body: []byte("fixture")}}, IdentityHasher: testIdentityHasher(), Parser: func([]byte) ([]domain.Node, error) {
		return []domain.Node{{Name: "East", Type: "vless", Server: "example.com", Port: 443, UUID: "fixture-uuid"}}, nil
	}}
	var phases []string
	result, err := updater.ApplyWithCheckpoint(context.Background(), "source-a", nil, func(phase string, _ UpdatePreview) error {
		phases = append(phases, phase)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"preview_verified", "state_committed"}
	if !reflect.DeepEqual(phases, want) || result.Plan.Digest != "digest-a" {
		t.Fatalf("phases=%v result=%+v", phases, result)
	}
}

func TestUpdaterAtomicStoreHasNoIntermediateCheckpoint(t *testing.T) {
	t.Parallel()
	sources := &updateSourceStore{item: domain.Subscription{ID: "source-a", Name: "Primary", URL: "https://example.com/source"}}
	nodes := &updateNodeStore{}
	atomic := &updateAtomicStore{nodes: nodes, source: sources}
	updater := Updater{Sources: sources, Nodes: nodes, Atomic: atomic, Fetcher: &updateFetcher{result: Result{Digest: "digest-atomic", Body: []byte("fixture")}}, IdentityHasher: testIdentityHasher(), Parser: func([]byte) ([]domain.Node, error) {
		return []domain.Node{{Name: "East", Type: "vless", Server: "example.com", Port: 443, UUID: "fixture-uuid"}}, nil
	}}
	var phases []string
	result, err := updater.ApplyWithCheckpoint(context.Background(), "source-a", nil, func(phase string, _ UpdatePreview) error {
		phases = append(phases, phase)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"preview_verified", "state_committed"}; !reflect.DeepEqual(phases, want) || atomic.calls != 1 || sources.lastDigest != result.Plan.Digest {
		t.Fatalf("phases=%v calls=%d source=%+v result=%+v", phases, atomic.calls, sources, result)
	}
}

func TestUpdaterScheduledUpdateFailsClosedOnPartialParseOrLargeReduction(t *testing.T) {
	t.Parallel()
	makeOld := func(count int) []domain.Node {
		items := make([]domain.Node, 0, count)
		for index := 0; index < count; index++ {
			items = append(items, domain.Node{ID: "old-" + string(rune('a'+index)), Name: "Old", Type: "vless", Server: "old.example", Port: 443, UUID: "old-fixture", SubscriptionID: "source-a"})
		}
		return items
	}
	tests := []struct {
		name    string
		nodes   []domain.Node
		body    []byte
		parser  func([]byte) ([]domain.Node, error)
		wantErr error
	}{
		{
			name: "partial parse", nodes: makeOld(1),
			body:    []byte("vless://fixture@example.com:443#Valid\ninvalid-entry"),
			wantErr: ErrPartialParseRequiresConfirmation,
		},
		{
			name: "large reduction", nodes: makeOld(12), body: []byte("fixture"),
			parser: func([]byte) ([]domain.Node, error) {
				return []domain.Node{{Name: "Only", Type: "vless", Server: "new.example", Port: 443, UUID: "new-fixture"}}, nil
			},
			wantErr: ErrLargeReductionRequiresConfirmation,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sources := &updateSourceStore{item: domain.Subscription{ID: "source-a", Name: "Primary", URL: "https://example.com/source"}}
			nodes := &updateNodeStore{existing: append([]domain.Node(nil), test.nodes...)}
			updater := Updater{Sources: sources, Nodes: nodes, Fetcher: &updateFetcher{result: Result{Digest: "digest", Body: test.body}}, IdentityHasher: testIdentityHasher(), Parser: test.parser}
			if _, err := updater.Apply(context.Background(), "source-a", nil); !errors.Is(err, test.wantErr) {
				t.Fatalf("err=%v want=%v", err, test.wantErr)
			}
			if !reflect.DeepEqual(nodes.existing, test.nodes) {
				t.Fatalf("old nodes changed: %+v", nodes.existing)
			}
		})
	}
}

func TestUpdaterReconcileRepairsOnlyCompleteNodeSet(t *testing.T) {
	t.Parallel()
	old := domain.Node{ID: "old", Name: "Old", Type: "vless", Server: "old.example", Port: 443, UUID: "old-fixture", SubscriptionID: "source-a"}
	newRemote := domain.Node{Name: "New", Type: "vless", Server: "new.example", Port: 443, UUID: "new-fixture"}
	newRemote2 := domain.Node{Name: "Second", Type: "vless", Server: "second.example", Port: 443, UUID: "second-fixture"}
	tests := []struct {
		name           string
		current        func(UpdatePreview) []domain.Node
		lastDigest     string
		wantState      RecoveryState
		wantSourceSave bool
	}{
		{name: "complete nodes repair source result", current: func(preview UpdatePreview) []domain.Node { return preview.Nodes }, wantState: RecoveryCompleted, wantSourceSave: true},
		{name: "exact prestate can retry", current: func(UpdatePreview) []domain.Node { return []domain.Node{old} }, wantState: RecoveryPreState},
		{name: "partial nodes fail closed", current: func(preview UpdatePreview) []domain.Node { return []domain.Node{preview.Nodes[0]} }, wantState: RecoveryPartial},
		{name: "source ahead of nodes fails closed", current: func(UpdatePreview) []domain.Node { return []domain.Node{old} }, lastDigest: "digest-a", wantState: RecoveryPartial},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			sources := &updateSourceStore{item: domain.Subscription{ID: "source-a", Name: "Primary", URL: "https://example.com/source", LastDigest: test.lastDigest}}
			nodes := &updateNodeStore{existing: []domain.Node{old}}
			updater := Updater{Sources: sources, Nodes: nodes, Fetcher: &updateFetcher{result: Result{Digest: "digest-a", Body: []byte("fixture")}}, IdentityHasher: testIdentityHasher(), Parser: func([]byte) ([]domain.Node, error) {
				return []domain.Node{newRemote, newRemote2}, nil
			}}
			original, err := updater.Preview(context.Background(), "source-a")
			if err != nil {
				t.Fatal(err)
			}
			nodes.existing = append([]domain.Node(nil), test.current(original)...)
			state, _, err := updater.Reconcile(context.Background(), "source-a", original.Plan.Digest, UpdatePlanDigest(original.Plan), original.Plan.SettingsRevision)
			if err != nil {
				t.Fatal(err)
			}
			if state != test.wantState || sources.success != test.wantSourceSave {
				t.Fatalf("state=%s source=%+v", state, sources)
			}
		})
	}
}

func TestUpdaterReconcileUsesSettingsRevisionCASForSuccessMetadata(t *testing.T) {
	t.Parallel()
	sources := &updateSourceStore{item: domain.Subscription{ID: "source-a", Name: "Primary", URL: "https://example.com/source", SettingsRevision: 1}}
	nodes := &updateNodeStore{}
	updater := Updater{Sources: sources, Nodes: nodes, Fetcher: &updateFetcher{result: Result{Digest: "digest-a", Body: []byte("fixture")}}, IdentityHasher: testIdentityHasher(), Parser: func([]byte) ([]domain.Node, error) {
		return []domain.Node{{Name: "New", Type: "vless", Server: "new.example", Port: 443, UUID: "new-credential"}}, nil
	}}
	preview, err := updater.Preview(context.Background(), sources.item.ID)
	if err != nil {
		t.Fatal(err)
	}
	nodes.existing = append([]domain.Node(nil), preview.Nodes...)
	sources.beforeResult = func() {
		sources.beforeResult = nil
		sources.item.SettingsRevision++
		sources.item.Enabled = false
	}
	state, _, err := updater.Reconcile(context.Background(), sources.item.ID, preview.Plan.Digest, UpdatePlanDigest(preview.Plan), preview.Plan.SettingsRevision)
	if !errors.Is(err, domain.ErrSubscriptionSettingsStale) || state != RecoveryPartial {
		t.Fatalf("state=%s error=%v", state, err)
	}
	if sources.lastDigest != "" || sources.lastError != "" || sources.success {
		t.Fatalf("reconcile committed stale success metadata: %+v", sources)
	}
}

func TestUpdaterReadbackClassifiesCommittedPreStateAndPartialState(t *testing.T) {
	t.Parallel()
	old := domain.Node{ID: "old", Name: "Old", Type: "vless", Server: "old.example", Port: 443, UUID: "old-fixture", SubscriptionID: "source-a"}
	sources := &updateSourceStore{item: domain.Subscription{ID: "source-a", Name: "Primary", URL: "https://example.com/source"}}
	nodes := &updateNodeStore{existing: []domain.Node{old}}
	updater := Updater{Sources: sources, Nodes: nodes, Fetcher: &updateFetcher{result: Result{Digest: "digest-a", Body: []byte("fixture")}}, IdentityHasher: testIdentityHasher(), Parser: func([]byte) ([]domain.Node, error) {
		return []domain.Node{{Name: "New", Type: "vless", Server: "new.example", Port: 443, UUID: "new-fixture"}}, nil
	}}
	preview, err := updater.Preview(context.Background(), "source-a")
	if err != nil {
		t.Fatal(err)
	}
	state, err := updater.Readback(context.Background(), "source-a", preview)
	if err != nil || state != RecoveryPreState {
		t.Fatalf("pre-state=%s err=%v", state, err)
	}
	nodes.existing = append([]domain.Node(nil), preview.Nodes...)
	sources.item.LastDigest = preview.Plan.Digest
	state, err = updater.Readback(context.Background(), "source-a", preview)
	if err != nil || state != RecoveryCompleted {
		t.Fatalf("committed=%s err=%v", state, err)
	}
	nodes.existing = append(nodes.existing, old)
	state, err = updater.Readback(context.Background(), "source-a", preview)
	if err != nil || state != RecoveryPartial {
		t.Fatalf("partial=%s err=%v", state, err)
	}
}

func TestSubscriptionNodeIDUsesKeyedCredentialIdentity(t *testing.T) {
	t.Parallel()
	sources := &updateSourceStore{item: domain.Subscription{ID: "source-a", Name: "Primary", URL: "https://example.com/source"}}
	credential := "00000000-0000-0000-0000-000000000001"
	parserNode := domain.Node{Name: "Node", Type: "vless", Server: "example.com", Port: 443, UUID: credential}
	updater := Updater{Sources: sources, Nodes: &updateNodeStore{}, Fetcher: &updateFetcher{result: Result{Digest: "digest", Body: []byte("fixture")}}, IdentityHasher: testIdentityHasher(), Parser: func([]byte) ([]domain.Node, error) { return []domain.Node{parserNode}, nil }}
	first, err := updater.Preview(context.Background(), "source-a")
	if err != nil {
		t.Fatal(err)
	}
	parserNode.UUID = "00000000-0000-0000-0000-000000000002"
	second, err := updater.Preview(context.Background(), "source-a")
	if err != nil {
		t.Fatal(err)
	}
	if first.Nodes[0].ID == second.Nodes[0].ID {
		t.Fatalf("distinct credentials reused subscription node ID: %s", first.Nodes[0].ID)
	}
	rawDigest := sha256.Sum256([]byte(credential))
	if strings.Contains(first.Nodes[0].ID, credential) || strings.Contains(first.Nodes[0].ID, hex.EncodeToString(rawDigest[:])[:24]) {
		t.Fatalf("subscription node ID exposes raw credential material: %s", first.Nodes[0].ID)
	}
}

func TestSubscriptionNodeIDsRemainBoundToCredentialsWhenSourceOrderFlips(t *testing.T) {
	t.Parallel()
	sources := &updateSourceStore{item: domain.Subscription{ID: "source-a", Name: "Primary", URL: "https://example.com/source"}}
	nodes := &updateNodeStore{}
	firstCredential := "00000000-0000-0000-0000-000000000001"
	secondCredential := "00000000-0000-0000-0000-000000000002"
	parsed := []domain.Node{
		{Name: "First", Type: "vless", Server: "same.example", Port: 443, UUID: firstCredential},
		{Name: "Second", Type: "vless", Server: "same.example", Port: 443, UUID: secondCredential},
	}
	updater := Updater{Sources: sources, Nodes: nodes, Fetcher: &updateFetcher{result: Result{Digest: "digest", Body: []byte("fixture")}}, IdentityHasher: testIdentityHasher(), Parser: func([]byte) ([]domain.Node, error) {
		return append([]domain.Node(nil), parsed...), nil
	}}
	first, err := updater.Preview(context.Background(), "source-a")
	if err != nil {
		t.Fatal(err)
	}
	parsed[0], parsed[1] = parsed[1], parsed[0]
	second, err := updater.Preview(context.Background(), "source-a")
	if err != nil {
		t.Fatal(err)
	}
	idsByCredential := func(items []domain.Node) map[string]string {
		result := make(map[string]string, len(items))
		for _, item := range items {
			result[item.UUID] = item.ID
		}
		return result
	}
	if !reflect.DeepEqual(idsByCredential(first.Nodes), idsByCredential(second.Nodes)) {
		t.Fatalf("credential-to-ID mapping changed after reorder: first=%v second=%v", idsByCredential(first.Nodes), idsByCredential(second.Nodes))
	}
}

func TestSubscriptionNodeIDReusesEquivalentLegacyID(t *testing.T) {
	t.Parallel()
	legacy := domain.Node{ID: "sub-legacy-endpoint", Name: "Primary / Old", Type: "vless", Server: "same.example", Port: 443, UUID: "00000000-0000-0000-0000-000000000001", SubscriptionID: "source-a"}
	sources := &updateSourceStore{item: domain.Subscription{ID: "source-a", Name: "Primary", URL: "https://example.com/source"}}
	updater := Updater{Sources: sources, Nodes: &updateNodeStore{existing: []domain.Node{legacy}}, Fetcher: &updateFetcher{result: Result{Digest: "digest", Body: []byte("fixture")}}, IdentityHasher: testIdentityHasher(), Parser: func([]byte) ([]domain.Node, error) {
		return []domain.Node{{Name: "Renamed", Type: "vless", Server: "same.example", Port: 443, UUID: legacy.UUID}}, nil
	}}
	preview, err := updater.Preview(context.Background(), "source-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Nodes) != 1 || preview.Nodes[0].ID != legacy.ID {
		t.Fatalf("legacy ID was not preserved: %+v", preview.Nodes)
	}
}
