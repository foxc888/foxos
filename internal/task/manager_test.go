package task

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/foxc888/foxos/internal/domain"
	"github.com/foxc888/foxos/internal/store/sqlite"
)

type failedEntropy struct{}

func (failedEntropy) Read([]byte) (int, error) { return 0, errors.New("entropy unavailable") }

func TestManagerRunsPersistedJobAndDeduplicates(t *testing.T) {
	t.Parallel()
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	manager, err := New(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	var calls atomic.Int32
	if err := manager.Register("test", func(_ context.Context, job domain.Job, progress Progress) (map[string]any, domain.JobStatus, error) {
		calls.Add(1)
		progress(domain.JobVerifying, 75)
		return map[string]any{"request": job.Request["value"]}, domain.JobSucceeded, nil
	}); err != nil {
		t.Fatal(err)
	}
	first, err := manager.Submit(context.Background(), "test", "dedupe", map[string]any{"value": "one"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Submit(context.Background(), "test", "dedupe", map[string]any{"value": "two"})
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Fatalf("first=%s second=%s", first.ID, second.ID)
	}
	completed := waitForJob(t, manager, first.ID, domain.JobSucceeded)
	if completed.Progress != 100 || completed.Attempts != 1 || calls.Load() != 1 {
		t.Fatalf("job=%+v calls=%d", completed, calls.Load())
	}
}

func TestManagerRetryFailedJob(t *testing.T) {
	t.Parallel()
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	manager, err := New(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	var calls atomic.Int32
	if err := manager.Register("retry", func(context.Context, domain.Job, Progress) (map[string]any, domain.JobStatus, error) {
		if calls.Add(1) == 1 {
			return map[string]any{"errorClass": "upstream_unavailable"}, domain.JobFailed, errors.New("sensitive upstream failure")
		}
		return map[string]any{"ok": true}, domain.JobSucceeded, nil
	}); err != nil {
		t.Fatal(err)
	}
	job, err := manager.Submit(context.Background(), "retry", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	failed := waitForJob(t, manager, job.ID, domain.JobFailed)
	if failed.ErrorMessage != "operation failed" || failed.ErrorClass != "upstream_unavailable" {
		t.Fatalf("error message leaked: %q", failed.ErrorMessage)
	}
	if _, err := manager.Retry(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	completed := waitForJob(t, manager, job.ID, domain.JobSucceeded)
	if completed.Attempts != 2 {
		t.Fatalf("attempts=%d", completed.Attempts)
	}
}

func TestManagerSubmitFailsClosedWhenSecureRandomIsUnavailable(t *testing.T) {
	t.Parallel()
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	manager, err := New(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	manager.entropy = failedEntropy{}
	if _, err := manager.Submit(context.Background(), "test", "", nil); err == nil {
		t.Fatal("expected secure random failure")
	}
}

func TestManagerUsesKindSpecificRecoveryForInterruptedJob(t *testing.T) {
	t.Parallel()
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "recovery.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	job := domain.Job{ID: "job-recovery", Kind: "routeros.egress", Status: domain.JobVerifying, Progress: 70, Request: map[string]any{"value": "original"}, Result: map[string]any{"phase": "external_applied"}}
	if _, _, err := store.CreateJob(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	manager, err := New(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	var calls atomic.Int32
	err = manager.RegisterWithRecovery("routeros.egress", func(context.Context, domain.Job, Progress) (map[string]any, domain.JobStatus, error) {
		calls.Add(1)
		return map[string]any{"applied": true}, domain.JobSucceeded, nil
	}, func(_ context.Context, interrupted domain.Job) (RecoveryDecision, error) {
		if interrupted.Result["phase"] != "external_applied" {
			t.Fatalf("interrupted=%+v", interrupted)
		}
		return RecoveryDecision{Status: domain.JobQueued, Result: map[string]any{"phase": "readback_pending"}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	completed := waitForJob(t, manager, job.ID, domain.JobSucceeded)
	if calls.Load() != 1 || completed.Attempts != 1 || completed.Result["recovered"] != true {
		t.Fatalf("job=%+v calls=%d", completed, calls.Load())
	}
}

func TestManagerFailsInterruptedJobWithoutRecoveryProtocol(t *testing.T) {
	t.Parallel()
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "missing-recovery.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	job := domain.Job{ID: "job-no-recovery", Kind: "unsafe", Status: domain.JobRunning}
	if _, _, err := store.CreateJob(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	manager, err := New(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if err := manager.Register("unsafe", func(context.Context, domain.Job, Progress) (map[string]any, domain.JobStatus, error) {
		t.Fatal("interrupted job must not run without recovery")
		return nil, domain.JobFailed, nil
	}); err != nil {
		t.Fatal(err)
	}
	failed := waitForJob(t, manager, job.ID, domain.JobFailed)
	if failed.ErrorClass != "recovery_protocol_missing" {
		t.Fatalf("job=%+v", failed)
	}
}

func TestCheckpointIsDurableBeforeHandlerReturns(t *testing.T) {
	t.Parallel()
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "checkpoint.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	manager, err := New(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	checkpointed := make(chan struct{})
	release := make(chan struct{})
	if err := manager.Register("checkpoint", func(ctx context.Context, _ domain.Job, _ Progress) (map[string]any, domain.JobStatus, error) {
		if err := Checkpoint(ctx, "external_applied", map[string]any{"resourceDigest": "abc"}); err != nil {
			return nil, domain.JobFailed, err
		}
		close(checkpointed)
		<-release
		return map[string]any{"ok": true}, domain.JobSucceeded, nil
	}); err != nil {
		t.Fatal(err)
	}
	job, err := manager.Submit(context.Background(), "checkpoint", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	<-checkpointed
	persisted, err := store.Job(context.Background(), job.ID)
	if err != nil || persisted.Result["phase"] != "external_applied" || persisted.Result["resourceDigest"] != "abc" {
		t.Fatalf("persisted=%+v err=%v", persisted, err)
	}
	close(release)
	waitForJob(t, manager, job.ID, domain.JobSucceeded)
}

func waitForJob(t *testing.T, manager *Manager, id string, status domain.JobStatus) domain.Job {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		job, err := manager.Job(context.Background(), id)
		if err == nil && job.Status == status {
			return job
		}
		time.Sleep(5 * time.Millisecond)
	}
	job, err := manager.Job(context.Background(), id)
	t.Fatalf("job=%+v err=%v, want status %s", job, err, status)
	return domain.Job{}
}
