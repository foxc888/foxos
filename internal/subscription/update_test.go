package subscription

import (
	"context"
	"errors"
	"testing"

	"github.com/foxc888/foxos/internal/domain"
)

type updateSourceStore struct {
	item       domain.Subscription
	lastDigest string
	lastError  string
	success    bool
}

func (s *updateSourceStore) Subscription(context.Context, string) (domain.Subscription, error) {
	return s.item, nil
}
func (s *updateSourceStore) UpdateSubscriptionResult(_ context.Context, _ string, digest, message string, success bool) error {
	s.lastDigest = digest
	s.lastError = message
	s.success = success
	return nil
}

type updateNodeStore struct {
	existing []domain.Node
	replaced []domain.Node
	err      error
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

func TestUpdaterPreviewDeduplicatesAndUsesStableIDs(t *testing.T) {
	t.Parallel()
	sources := &updateSourceStore{item: domain.Subscription{ID: "source-a", Name: "Primary", URL: "https://example.com/source"}}
	nodes := &updateNodeStore{}
	fetcher := &updateFetcher{result: Result{Digest: "digest-a", Body: []byte("fixture")}}
	updater := Updater{Sources: sources, Nodes: nodes, Fetcher: fetcher, Parser: func([]byte) ([]domain.Node, error) {
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
	updater := Updater{Sources: sources, Nodes: nodes, Fetcher: fetcher, Parser: func([]byte) ([]domain.Node, error) {
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

func TestUpdaterApplyRetainsOldNodesOnAtomicStoreFailure(t *testing.T) {
	t.Parallel()
	old := domain.Node{ID: "old", Name: "Old", Type: "vless", Server: "old.example", Port: 443, UUID: "old-fixture", SubscriptionID: "source-a"}
	sources := &updateSourceStore{item: domain.Subscription{ID: "source-a", Name: "Primary", URL: "https://example.com/source"}}
	nodes := &updateNodeStore{existing: []domain.Node{old}, err: errors.New("referenced")}
	updater := Updater{Sources: sources, Nodes: nodes, Fetcher: &updateFetcher{result: Result{Digest: "digest", Body: []byte("fixture")}}, Parser: func([]byte) ([]domain.Node, error) {
		return []domain.Node{{Name: "New", Type: "vless", Server: "new.example", Port: 443, UUID: "new-fixture"}}, nil
	}}
	if _, err := updater.Apply(context.Background(), "source-a", nil); err == nil {
		t.Fatal("expected replacement error")
	}
	if len(nodes.existing) != 1 || nodes.existing[0].ID != old.ID || sources.lastError == "" {
		t.Fatalf("nodes=%+v source=%+v", nodes.existing, sources)
	}
}
