package task

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/foxc888/foxos/internal/domain"
	"github.com/foxc888/foxos/internal/store/sqlite"
)

type failedEntropy struct{}

func (failedEntropy) Read([]byte) (int, error) { return 0, errors.New("entropy unavailable") }

type transientTaskStore struct {
	Store
	failJob              atomic.Bool
	alwaysFailJob        atomic.Bool
	failUpdate           atomic.Bool
	persistUpdateOnError bool
	jobCalls             atomic.Int32
}

func (s *transientTaskStore) Job(ctx context.Context, id string) (domain.Job, error) {
	s.jobCalls.Add(1)
	if s.alwaysFailJob.Load() || s.failJob.Swap(false) {
		return domain.Job{}, errors.New("transient job read failure")
	}
	return s.Store.Job(ctx, id)
}

func (s *transientTaskStore) UpdateJob(ctx context.Context, job domain.Job) error {
	if !s.failUpdate.Swap(false) {
		return s.Store.UpdateJob(ctx, job)
	}
	if s.persistUpdateOnError {
		if err := s.Store.UpdateJob(ctx, job); err != nil {
			return err
		}
	}
	return errors.New("transient job update failure")
}

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

func TestManagerRetriesTransientPreExecutionStoreFailuresExactlyOnce(t *testing.T) {
	tests := []struct {
		name                 string
		failJob              bool
		failUpdate           bool
		persistUpdateOnError bool
	}{
		{name: "job read fails before claim", failJob: true},
		{name: "running update fails before persistence", failUpdate: true},
		{name: "running update persists before returning error", failUpdate: true, persistUpdateOnError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			base, err := sqlite.Open(filepath.Join(t.TempDir(), "transient.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer base.Close()
			store := &transientTaskStore{Store: base, persistUpdateOnError: test.persistUpdateOnError}
			manager, err := New(context.Background(), store)
			if err != nil {
				t.Fatal(err)
			}
			defer manager.Close()
			var calls atomic.Int32
			if err := manager.Register("transient", func(context.Context, domain.Job, Progress) (map[string]any, domain.JobStatus, error) {
				calls.Add(1)
				return map[string]any{"ok": true}, domain.JobSucceeded, nil
			}); err != nil {
				t.Fatal(err)
			}
			store.failJob.Store(test.failJob)
			store.failUpdate.Store(test.failUpdate)
			job, err := manager.Submit(context.Background(), "transient", "", nil)
			if err != nil {
				t.Fatal(err)
			}
			completed := waitForJob(t, manager, job.ID, domain.JobSucceeded)
			if calls.Load() != 1 || completed.Attempts != 1 {
				t.Fatalf("calls=%d job=%+v", calls.Load(), completed)
			}
		})
	}
}

func TestManagerRetryDoesNotQueueUnregisteredKind(t *testing.T) {
	t.Parallel()
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "unregistered.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	job := domain.Job{ID: "job-unregistered", Kind: "optional.component", Status: domain.JobFailed, Progress: 100, Request: map[string]any{}, Result: map[string]any{}}
	if _, _, err := store.CreateJob(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	manager, err := New(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if _, err := manager.Retry(context.Background(), job.ID); err == nil {
		t.Fatal("retry accepted a job whose kind is not registered")
	}
	stored, err := store.Job(context.Background(), job.ID)
	if err != nil || stored.Status != domain.JobFailed {
		t.Fatalf("stored=%+v err=%v", stored, err)
	}
}

func TestManagerCloseCancelsPersistentPreExecutionRetry(t *testing.T) {
	t.Parallel()
	base, err := sqlite.Open(filepath.Join(t.TempDir(), "persistent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	store := &transientTaskStore{Store: base}
	manager, err := New(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Register("persistent", func(context.Context, domain.Job, Progress) (map[string]any, domain.JobStatus, error) {
		t.Fatal("handler ran while every pre-execution read failed")
		return nil, domain.JobFailed, nil
	}); err != nil {
		t.Fatal(err)
	}
	manager.retryDelay = func(uint) time.Duration { return time.Hour }
	store.alwaysFailJob.Store(true)
	job, err := manager.Submit(context.Background(), "persistent", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for store.jobCalls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if stored, err := base.Job(context.Background(), job.ID); err != nil || stored.Status != domain.JobQueued {
		t.Fatalf("stored=%+v err=%v", stored, err)
	}
	done := make(chan error, 1)
	go func() { done <- manager.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("manager close did not cancel a scheduled retry")
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
	}); err == nil {
		t.Fatal("startup registration accepted an interrupted job without recovery")
	}
	failed := waitForJob(t, manager, job.ID, domain.JobFailed)
	if failed.ErrorClass != "recovery_protocol_missing" {
		t.Fatalf("job=%+v", failed)
	}
}

func TestManagerRecoversEveryActiveJobWithoutLatest500Limit(t *testing.T) {
	t.Parallel()
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "all-active.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	const total = 520
	for index := 0; index < total; index++ {
		job := domain.Job{ID: fmt.Sprintf("active-%04d", index), Kind: "bulk-recovery", Status: domain.JobRunning, Request: map[string]any{}, Result: map[string]any{}}
		if _, _, err := store.CreateJob(context.Background(), job); err != nil {
			t.Fatal(err)
		}
	}
	manager, err := NewPaused(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	var recovered atomic.Int32
	if err := manager.RegisterWithRecovery("bulk-recovery", func(context.Context, domain.Job, Progress) (map[string]any, domain.JobStatus, error) {
		t.Fatal("terminal recovery must not execute the handler")
		return nil, domain.JobFailed, nil
	}, func(context.Context, domain.Job) (RecoveryDecision, error) {
		recovered.Add(1)
		return RecoveryDecision{Status: domain.JobSucceeded, Result: map[string]any{"phase": "readback_succeeded"}}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if recovered.Load() != total {
		t.Fatalf("recovered=%d want=%d", recovered.Load(), total)
	}
}

func TestManagerQueuesMoreThanLegacyChannelCapacity(t *testing.T) {
	t.Parallel()
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "queued.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	const total = 160
	for index := 0; index < total; index++ {
		job := domain.Job{ID: fmt.Sprintf("queued-%04d", index), Kind: "bulk-queue", Status: domain.JobQueued, Request: map[string]any{}, Result: map[string]any{}}
		if _, _, err := store.CreateJob(context.Background(), job); err != nil {
			t.Fatal(err)
		}
	}
	manager, err := NewPaused(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	var calls atomic.Int32
	if err := manager.Register("bulk-queue", func(context.Context, domain.Job, Progress) (map[string]any, domain.JobStatus, error) {
		calls.Add(1)
		return nil, domain.JobSucceeded, nil
	}); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("paused manager executed a recovered job before startup completed")
	}
	if err := manager.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for calls.Load() != total && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if calls.Load() != total {
		t.Fatalf("executed=%d want=%d", calls.Load(), total)
	}
}

func TestPausedManagerRestoresGlobalCreationOrderAcrossKinds(t *testing.T) {
	t.Parallel()
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "global-order.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	created := time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC)
	for _, job := range []domain.Job{
		{ID: "older", Kind: "subscription.update", Status: domain.JobQueued, Request: map[string]any{}, Result: map[string]any{}, CreatedAt: created},
		{ID: "newer", Kind: "mihomo.apply", Status: domain.JobQueued, Request: map[string]any{}, Result: map[string]any{}, CreatedAt: created.Add(time.Second)},
	} {
		if _, _, err := store.CreateJob(context.Background(), job); err != nil {
			t.Fatal(err)
		}
	}
	manager, err := NewPaused(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	executed := make(chan string, 2)
	handler := func(_ context.Context, job domain.Job, _ Progress) (map[string]any, domain.JobStatus, error) {
		executed <- job.ID
		return nil, domain.JobSucceeded, nil
	}
	if err := manager.Register("mihomo.apply", handler); err != nil {
		t.Fatal(err)
	}
	if err := manager.Register("subscription.update", handler); err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"older", "newer"} {
		select {
		case got := <-executed:
			if got != want {
				t.Fatalf("execution order got %q, want %q", got, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for %q", want)
		}
	}
}

func TestManagerDoesNotPublishHandlerWhenRecoveryScanOrUpdateFails(t *testing.T) {
	tests := []struct {
		name   string
		mutate string
	}{
		{name: "bad job JSON", mutate: `UPDATE jobs SET request_json='{' WHERE id='broken'`},
		{name: "update failure", mutate: `CREATE TRIGGER fail_job_update BEFORE UPDATE ON jobs BEGIN SELECT RAISE(FAIL, 'forced update failure'); END`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "recovery-failure.db")
			store, err := sqlite.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			job := domain.Job{ID: "broken", Kind: "unsafe", Status: domain.JobRunning, Request: map[string]any{}, Result: map[string]any{}}
			if _, _, err := store.CreateJob(context.Background(), job); err != nil {
				t.Fatal(err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			mutateSQLite(t, path, test.mutate)
			store, err = sqlite.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			manager, err := NewPaused(context.Background(), store)
			if err != nil {
				t.Fatal(err)
			}
			defer manager.Close()
			err = manager.RegisterWithRecovery("unsafe", func(context.Context, domain.Job, Progress) (map[string]any, domain.JobStatus, error) {
				t.Fatal("handler was published despite recovery failure")
				return nil, domain.JobFailed, nil
			}, func(context.Context, domain.Job) (RecoveryDecision, error) {
				return RecoveryDecision{Status: domain.JobSucceeded}, nil
			})
			if err == nil {
				t.Fatal("expected recovery registration failure")
			}
			manager.mu.RLock()
			_, published := manager.handlers["unsafe"]
			manager.mu.RUnlock()
			if published {
				t.Fatal("handler became available before successful recovery")
			}
		})
	}
}

func TestPausedManagerDoesNotRunEarlierKindWhenLaterKindFails(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "multi-kind.db")
	store, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, job := range []domain.Job{
		{ID: "first", Kind: "first", Status: domain.JobQueued, Request: map[string]any{}, Result: map[string]any{}},
		{ID: "second", Kind: "second", Status: domain.JobRunning, Request: map[string]any{}, Result: map[string]any{}},
	} {
		if _, _, err := store.CreateJob(context.Background(), job); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	mutateSQLite(t, path, `UPDATE jobs SET result_json='{' WHERE id='second'`)
	store, err = sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	manager, err := NewPaused(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	var calls atomic.Int32
	if err := manager.Register("first", func(context.Context, domain.Job, Progress) (map[string]any, domain.JobStatus, error) {
		calls.Add(1)
		return nil, domain.JobSucceeded, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := manager.RegisterWithRecovery("second", func(context.Context, domain.Job, Progress) (map[string]any, domain.JobStatus, error) {
		return nil, domain.JobSucceeded, nil
	}, func(context.Context, domain.Job) (RecoveryDecision, error) {
		return RecoveryDecision{Status: domain.JobSucceeded}, nil
	}); err == nil {
		t.Fatal("later kind registration unexpectedly succeeded")
	}
	time.Sleep(50 * time.Millisecond)
	if calls.Load() != 0 {
		t.Fatal("earlier recovered task executed before all kinds registered")
	}
}

func TestUpgradePausedManagerDoesNotInvokeInterruptedJobRecovery(t *testing.T) {
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "upgrade-paused-recovery.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	job := domain.Job{ID: "interrupted", Kind: "external", Status: domain.JobRunning, Request: map[string]any{}, Result: map[string]any{}}
	if _, _, err := store.CreateJob(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	manager, err := NewPaused(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if err := manager.Pause(context.Background()); err != nil {
		t.Fatal(err)
	}
	var recoveryCalls atomic.Int32
	err = manager.RegisterWithRecovery("external", func(context.Context, domain.Job, Progress) (map[string]any, domain.JobStatus, error) {
		return nil, domain.JobSucceeded, nil
	}, func(context.Context, domain.Job) (RecoveryDecision, error) {
		recoveryCalls.Add(1)
		return RecoveryDecision{Status: domain.JobSucceeded}, nil
	})
	if err == nil || recoveryCalls.Load() != 0 {
		t.Fatalf("registration error=%v recoveryCalls=%d", err, recoveryCalls.Load())
	}
}

func mutateSQLite(t *testing.T, path, statement string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(statement); err != nil {
		t.Fatal(err)
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

func TestManagerPauseDrainsCompleteHandlerAndRejectsNewWork(t *testing.T) {
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "pause.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	manager, err := New(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	started := make(chan struct{})
	release := make(chan struct{})
	if err := manager.Register("blocking", func(context.Context, domain.Job, Progress) (map[string]any, domain.JobStatus, error) {
		close(started)
		<-release
		return nil, domain.JobSucceeded, nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Submit(context.Background(), "blocking", "", nil); err != nil {
		t.Fatal(err)
	}
	<-started
	paused := make(chan error, 1)
	go func() { paused <- manager.Pause(context.Background()) }()
	select {
	case err := <-paused:
		t.Fatalf("pause returned while handler was active: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-paused:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("pause did not finish after handler returned")
	}
	if _, err := manager.Submit(context.Background(), "blocking", "", nil); !errors.Is(err, ErrPaused) {
		t.Fatalf("submit error=%v, want ErrPaused", err)
	}
	if _, err := manager.Retry(context.Background(), "missing"); !errors.Is(err, ErrPaused) {
		t.Fatalf("retry error=%v, want ErrPaused", err)
	}
	manager.Resume()
	if manager.Paused() {
		t.Fatal("manager remained paused after Resume")
	}
}

func TestPausedManagerClaimsQueuedJobExactlyOnceAfterResume(t *testing.T) {
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "resume.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	manager, err := NewPaused(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	var calls atomic.Int32
	if err := manager.Register("queued", func(context.Context, domain.Job, Progress) (map[string]any, domain.JobStatus, error) {
		calls.Add(1)
		return nil, domain.JobSucceeded, nil
	}); err != nil {
		t.Fatal(err)
	}
	job, err := manager.Submit(context.Background(), "queued", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Pause(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if calls.Load() != 0 {
		t.Fatal("worker claimed a job while paused")
	}
	manager.Resume()
	completed := waitForJob(t, manager, job.ID, domain.JobSucceeded)
	if calls.Load() != 1 || completed.Attempts != 1 {
		t.Fatalf("calls=%d job=%+v", calls.Load(), completed)
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
