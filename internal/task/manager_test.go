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
