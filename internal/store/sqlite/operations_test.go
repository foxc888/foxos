package sqlite

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/foxc888/foxos/internal/domain"
)

func TestMihomoDraftAndSnapshotLifecycle(t *testing.T) {
	t.Parallel()
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SaveMihomoDraft(ctx, domain.MihomoDraft{Mode: "rule", MixedPort: 7890, AllowLAN: true, Rules: []string{"MATCH,DIRECT"}}); err != nil {
		t.Fatal(err)
	}
	draft, err := store.MihomoDraft(ctx)
	if err != nil || draft.Revision != 1 || draft.MixedPort != 7890 {
		t.Fatalf("draft=%+v err=%v", draft, err)
	}
	draft.MixedPort = 7891
	if err := store.SaveMihomoDraft(ctx, draft); err != nil {
		t.Fatal(err)
	}
	draft, err = store.MihomoDraft(ctx)
	if err != nil || draft.Revision != 2 || draft.MixedPort != 7891 {
		t.Fatalf("draft=%+v err=%v", draft, err)
	}
	snapshot := domain.MihomoSnapshot{ID: "snapshot-a", Digest: "digest-a", Label: "initial", Body: []byte("mode: rule\n")}
	if err := store.SaveMihomoSnapshot(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	items, err := store.MihomoSnapshots(ctx, 10)
	if err != nil || len(items) != 1 || len(items[0].Body) != 0 {
		t.Fatalf("items=%+v err=%v", items, err)
	}
	stored, err := store.MihomoSnapshot(ctx, snapshot.ID)
	if err != nil || string(stored.Body) != string(snapshot.Body) {
		t.Fatalf("stored=%+v err=%v", stored, err)
	}
}

func TestCreateJobIsIdempotentAndDoesNotApplyGenericRecovery(t *testing.T) {
	t.Parallel()
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	job := domain.Job{ID: "job-a", Kind: "mihomo.apply", Status: domain.JobRunning, IdempotencyKey: "same", Request: map[string]any{"digest": "a"}}
	first, existed, err := store.CreateJob(ctx, job)
	if err != nil || existed {
		t.Fatalf("first=%+v existed=%v err=%v", first, existed, err)
	}
	second, existed, err := store.CreateJob(ctx, domain.Job{ID: "job-b", Kind: job.Kind, IdempotencyKey: job.IdempotencyKey})
	if err != nil || !existed || second.ID != first.ID {
		t.Fatalf("second=%+v existed=%v err=%v", second, existed, err)
	}
	recovered, err := store.Job(ctx, first.ID)
	if err != nil || recovered.Status != domain.JobRunning {
		t.Fatalf("recovered=%+v err=%v", recovered, err)
	}
}

func TestConsumeReplayIsAtomicUnderConcurrency(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	const workers = 12
	var wait sync.WaitGroup
	results := make(chan error, workers)
	for index := 0; index < workers; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			results <- store.ConsumeReplay(context.Background(), "digest", time.Now().Add(time.Minute))
		}()
	}
	wait.Wait()
	close(results)
	succeeded := 0
	replayed := 0
	for err := range results {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrReplayToken):
			replayed++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if succeeded != 1 || replayed != workers-1 {
		t.Fatalf("succeeded=%d replayed=%d", succeeded, replayed)
	}
}

func TestAlertSignalsAndAcknowledgementLifecycle(t *testing.T) {
	t.Parallel()
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	seen := time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC)
	for index, want := range []int{1, 2, 3} {
		count, err := store.TrackAlertSignal(ctx, "node.failure.a", true, seen.Add(time.Duration(index)*time.Minute))
		if err != nil || count != want {
			t.Fatalf("count=%d want=%d err=%v", count, want, err)
		}
	}
	alert := domain.Alert{ID: "alert-a", Key: "node.failure.a", Severity: domain.AlertWarning, Title: "Node failed", FirstSeen: seen, LastSeen: seen}
	if err := store.SaveAlert(ctx, alert); err != nil {
		t.Fatal(err)
	}
	if err := store.AcknowledgeAlert(ctx, alert.ID); err != nil {
		t.Fatal(err)
	}
	alert.LastSeen = seen.Add(time.Minute)
	if err := store.SaveAlert(ctx, alert); err != nil {
		t.Fatal(err)
	}
	items, err := store.Alerts(ctx, false)
	if err != nil || len(items) != 1 || !items[0].Acknowledged {
		t.Fatalf("items=%+v err=%v", items, err)
	}
	if err := store.ResolveAlert(ctx, alert.Key, seen.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	alert.FirstSeen = seen.Add(3 * time.Minute)
	alert.LastSeen = alert.FirstSeen
	if err := store.SaveAlert(ctx, alert); err != nil {
		t.Fatal(err)
	}
	items, err = store.Alerts(ctx, false)
	if err != nil || len(items) != 1 || items[0].Acknowledged || !items[0].FirstSeen.Equal(alert.FirstSeen) {
		t.Fatalf("reopened=%+v err=%v", items, err)
	}
	count, err := store.TrackAlertSignal(ctx, "node.failure.a", false, seen.Add(4*time.Minute))
	if err != nil || count != 0 {
		t.Fatalf("reset count=%d err=%v", count, err)
	}
}
