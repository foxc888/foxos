package task

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/foxc888/foxos/internal/domain"
)

type Store interface {
	CreateJob(context.Context, domain.Job) (domain.Job, bool, error)
	Job(context.Context, string) (domain.Job, error)
	Jobs(context.Context, int) ([]domain.Job, error)
	RecoverableJobs(context.Context, string) ([]domain.Job, error)
	UpdateJob(context.Context, domain.Job) error
}

type Progress func(domain.JobStatus, int)
type Handler func(context.Context, domain.Job, Progress) (map[string]any, domain.JobStatus, error)
type Recoverer func(context.Context, domain.Job) (RecoveryDecision, error)

type RecoveryDecision struct {
	Status       domain.JobStatus
	Progress     int
	Result       map[string]any
	ErrorClass   string
	ErrorMessage string
}

type checkpointKey struct{}
type checkpointWriter func(string, map[string]any) error

var ErrPaused = errors.New("task execution is paused for an upgrade checkpoint")

type Manager struct {
	store      Store
	ctx        context.Context
	cancel     context.CancelFunc
	wake       chan struct{}
	handlers   map[string]Handler
	mu         sync.RWMutex
	queueMu    sync.Mutex
	queue      []string
	queued     map[string]struct{}
	startMu    sync.Mutex
	started    bool
	wg         sync.WaitGroup
	entropy    io.Reader
	retryDelay func(uint) time.Duration
	stateMu    sync.Mutex
	paused     bool
	inFlight   int
	changed    chan struct{}
}

func New(parent context.Context, store Store) (*Manager, error) {
	manager, err := newManager(parent, store)
	if err != nil {
		return nil, err
	}
	if err := manager.Start(); err != nil {
		return nil, err
	}
	return manager, nil
}

// NewPaused is used during process startup so no recovered or queued task can
// execute until every kind has been registered successfully.
func NewPaused(parent context.Context, store Store) (*Manager, error) {
	return newManager(parent, store)
}

func newManager(parent context.Context, store Store) (*Manager, error) {
	if parent == nil {
		parent = context.Background()
	}
	if store == nil {
		return nil, errors.New("task store is required")
	}
	ctx, cancel := context.WithCancel(parent)
	return &Manager{store: store, ctx: ctx, cancel: cancel, wake: make(chan struct{}, 1), handlers: make(map[string]Handler), queued: make(map[string]struct{}), entropy: rand.Reader, retryDelay: dispatchRetryDelay, changed: make(chan struct{})}, nil
}

func (m *Manager) Start() error {
	if m == nil {
		return errors.New("job manager is unavailable")
	}
	m.startMu.Lock()
	defer m.startMu.Unlock()
	if m.started {
		return nil
	}
	if err := m.ctx.Err(); err != nil {
		return err
	}
	if err := m.sortRecoveredQueue(); err != nil {
		return err
	}
	m.started = true
	m.wg.Add(1)
	go m.worker()
	m.signal()
	return nil
}

func (m *Manager) Register(kind string, handler Handler) error {
	return m.RegisterWithRecovery(kind, handler, nil)
}

func (m *Manager) RequireNoRecoverableJobs(kinds ...string) error {
	if m == nil {
		return errors.New("job manager is unavailable")
	}
	for _, kind := range kinds {
		jobs, err := m.store.RecoverableJobs(m.ctx, kind)
		if err != nil {
			return fmt.Errorf("scan disabled %s jobs: %w", kind, err)
		}
		if len(jobs) != 0 {
			return fmt.Errorf("%s has %d recoverable jobs but its service is unavailable", kind, len(jobs))
		}
	}
	return nil
}

func (m *Manager) RegisterWithRecovery(kind string, handler Handler, recoverer Recoverer) error {
	if m == nil || handler == nil || kind == "" {
		return errors.New("job kind and handler are required")
	}
	m.mu.RLock()
	_, alreadyRegistered := m.handlers[kind]
	m.mu.RUnlock()
	if alreadyRegistered {
		return fmt.Errorf("job kind %q is already registered", kind)
	}
	jobs, err := m.store.RecoverableJobs(m.ctx, kind)
	if err != nil {
		return fmt.Errorf("scan recoverable %s jobs: %w", kind, err)
	}
	queued := make([]string, 0, len(jobs))
	for _, job := range jobs {
		if job.Kind != kind {
			return fmt.Errorf("recoverable job kind mismatch: got %q want %q", job.Kind, kind)
		}
		switch job.Status {
		case domain.JobQueued:
			queued = append(queued, job.ID)
		case domain.JobRunning, domain.JobVerifying:
			if m.Paused() {
				return fmt.Errorf("%s has an interrupted job while upgrade maintenance is active", kind)
			}
			requeue, err := m.recoverInterrupted(job, recoverer)
			if err != nil {
				return err
			}
			if requeue {
				queued = append(queued, job.ID)
			}
		default:
			return fmt.Errorf("store returned non-recoverable job status %q", job.Status)
		}
	}
	if err := m.ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	if _, exists := m.handlers[kind]; exists {
		m.mu.Unlock()
		return fmt.Errorf("job kind %q is already registered", kind)
	}
	m.handlers[kind] = handler
	m.mu.Unlock()
	for _, id := range queued {
		if err := m.enqueue(id); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) recoverInterrupted(job domain.Job, recoverer Recoverer) (bool, error) {
	now := time.Now().UTC()
	decision := RecoveryDecision{}
	var recoveryErr error
	if recoverer == nil {
		recoveryErr = errors.New("job kind has no recovery protocol")
	} else {
		decision, recoveryErr = recoverer(m.ctx, job)
	}
	if recoveryErr != nil {
		job.Status = domain.JobFailed
		job.Progress = 100
		job.ErrorClass = "recovery_protocol_missing"
		if recoverer != nil {
			job.ErrorClass = recoveryErrorClass(job.Kind)
		}
		job.ErrorMessage = "operation recovery failed"
		job.FinishedAt = now
		job.Result = mergeResult(job.Result, map[string]any{"phase": "recovery_failed", "recovered": false})
		if err := m.store.UpdateJob(m.ctx, job); err != nil {
			return false, errors.Join(recoveryErr, err)
		}
		return false, recoveryErr
	}
	if decision.Status != domain.JobQueued && decision.Status != domain.JobSucceeded && decision.Status != domain.JobFailed && decision.Status != domain.JobRolledBack {
		return m.recoverInterrupted(job, func(context.Context, domain.Job) (RecoveryDecision, error) {
			return RecoveryDecision{}, errors.New("recovery returned an invalid status")
		})
	}
	job.Status = decision.Status
	job.Result = mergeResult(job.Result, decision.Result)
	job.Result["recovered"] = true
	job.ErrorClass = decision.ErrorClass
	job.ErrorMessage = decision.ErrorMessage
	if decision.Progress >= 0 && decision.Progress <= 100 {
		job.Progress = decision.Progress
	}
	if job.Status == domain.JobQueued {
		job.Progress = 0
		job.StartedAt = time.Time{}
		job.FinishedAt = time.Time{}
		job.ErrorClass = ""
		job.ErrorMessage = ""
	} else {
		job.Progress = 100
		job.FinishedAt = now
	}
	if err := m.store.UpdateJob(m.ctx, job); err != nil {
		return false, err
	}
	return job.Status == domain.JobQueued, nil
}

func recoveryErrorClass(kind string) string {
	candidate := strings.ReplaceAll(strings.TrimSpace(kind), ".", "_") + "_recovery_failed"
	if safeErrorClass(candidate) {
		return candidate
	}
	return "operation_recovery_failed"
}

// Checkpoint durably records a bounded, non-secret phase from inside a task
// handler. It returns an error when called outside a managed task context.
func Checkpoint(ctx context.Context, phase string, values map[string]any) error {
	if ctx == nil || !safeErrorClass(phase) || len(values) > 32 {
		return errors.New("invalid task checkpoint")
	}
	writer, ok := ctx.Value(checkpointKey{}).(checkpointWriter)
	if !ok || writer == nil {
		return errors.New("task checkpoint is unavailable")
	}
	return writer(phase, values)
}

func (m *Manager) Submit(ctx context.Context, kind, idempotencyKey string, request map[string]any) (domain.Job, error) {
	if m == nil || kind == "" {
		return domain.Job{}, errors.New("job manager is unavailable")
	}
	if err := m.beginOperation(); err != nil {
		return domain.Job{}, err
	}
	defer m.endOperation()
	m.mu.RLock()
	handler := m.handlers[kind]
	m.mu.RUnlock()
	if handler == nil {
		return domain.Job{}, fmt.Errorf("job kind %q is not registered", kind)
	}
	id, err := newID("job", m.entropy)
	if err != nil {
		return domain.Job{}, fmt.Errorf("generate job id: %w", err)
	}
	job := domain.Job{ID: id, Kind: kind, Status: domain.JobQueued, IdempotencyKey: idempotencyKey, Request: request, Result: map[string]any{}, CreatedAt: time.Now().UTC()}
	created, existing, err := m.store.CreateJob(ctx, job)
	if err != nil {
		return domain.Job{}, err
	}
	if !existing || created.Status == domain.JobQueued {
		if err := m.enqueue(created.ID); err != nil {
			return domain.Job{}, err
		}
	}
	return created, nil
}

func (m *Manager) Job(ctx context.Context, id string) (domain.Job, error) {
	if m == nil {
		return domain.Job{}, errors.New("job manager is unavailable")
	}
	return m.store.Job(ctx, id)
}

func (m *Manager) Jobs(ctx context.Context, limit int) ([]domain.Job, error) {
	if m == nil {
		return nil, errors.New("job manager is unavailable")
	}
	return m.store.Jobs(ctx, limit)
}

func (m *Manager) Retry(ctx context.Context, id string) (domain.Job, error) {
	if m == nil {
		return domain.Job{}, errors.New("job manager is unavailable")
	}
	if err := m.beginOperation(); err != nil {
		return domain.Job{}, err
	}
	defer m.endOperation()
	job, err := m.store.Job(ctx, id)
	if err != nil {
		return domain.Job{}, err
	}
	if job.Status != domain.JobFailed && job.Status != domain.JobRolledBack {
		return domain.Job{}, fmt.Errorf("job %s is not retryable", id)
	}
	m.mu.RLock()
	handler := m.handlers[job.Kind]
	m.mu.RUnlock()
	if handler == nil {
		return domain.Job{}, fmt.Errorf("job kind %q is not registered", job.Kind)
	}
	job.Status = domain.JobQueued
	job.Progress = 0
	job.ErrorClass = ""
	job.ErrorMessage = ""
	job.FinishedAt = time.Time{}
	if err := m.store.UpdateJob(ctx, job); err != nil {
		return domain.Job{}, err
	}
	if err := m.enqueue(id); err != nil {
		return domain.Job{}, err
	}
	return job, nil
}

func (m *Manager) Close() error {
	if m == nil {
		return nil
	}
	m.cancel()
	m.wg.Wait()
	return nil
}

// Pause rejects new submissions and waits for the worker and any submission
// already updating durable job state to finish.
func (m *Manager) Pause(ctx context.Context) error {
	if m == nil {
		return errors.New("job manager is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	m.stateMu.Lock()
	if !m.paused {
		m.paused = true
		m.notifyStateLocked()
	}
	for m.inFlight > 0 {
		changed := m.changed
		m.stateMu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
		m.stateMu.Lock()
	}
	m.stateMu.Unlock()
	return nil
}

func (m *Manager) Resume() {
	if m == nil {
		return
	}
	m.stateMu.Lock()
	if m.paused {
		m.paused = false
		m.notifyStateLocked()
	}
	m.stateMu.Unlock()
	m.signal()
}

func (m *Manager) Paused() bool {
	if m == nil {
		return false
	}
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	return m.paused
}

func (m *Manager) beginOperation() error {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	if m.paused {
		return ErrPaused
	}
	if err := m.ctx.Err(); err != nil {
		return err
	}
	m.inFlight++
	return nil
}

func (m *Manager) endOperation() {
	m.stateMu.Lock()
	m.inFlight--
	m.notifyStateLocked()
	m.stateMu.Unlock()
}

func (m *Manager) notifyStateLocked() {
	close(m.changed)
	m.changed = make(chan struct{})
}

func (m *Manager) worker() {
	defer m.wg.Done()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-m.wake:
			for {
				if m.ctx.Err() != nil {
					return
				}
				if err := m.beginOperation(); err != nil {
					if errors.Is(err, ErrPaused) {
						break
					}
					return
				}
				id, ok := m.dequeue()
				if !ok {
					m.endOperation()
					break
				}
				m.run(id)
				m.endOperation()
			}
		}
	}
}

func (m *Manager) run(id string) {
	var uncertainClaim *domain.Job
	for failures := uint(0); ; failures++ {
		job, err := m.store.Job(m.ctx, id)
		if errors.Is(err, domain.ErrNotFound) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return
		}
		if err != nil {
			if !m.waitForDispatchRetry(failures) {
				return
			}
			continue
		}
		if job.Terminal() {
			return
		}
		m.mu.RLock()
		handler := m.handlers[job.Kind]
		m.mu.RUnlock()
		if handler == nil {
			return
		}
		if job.Status == domain.JobRunning && uncertainClaim != nil && sameExecutionClaim(job, *uncertainClaim) {
			m.execute(job, handler)
			return
		}
		if job.Status != domain.JobQueued {
			return
		}
		job.Status = domain.JobRunning
		job.Attempts++
		job.StartedAt = time.Now().UTC()
		job.Progress = 1
		if err := m.store.UpdateJob(m.ctx, job); err != nil {
			claimed := job
			uncertainClaim = &claimed
			if !m.waitForDispatchRetry(failures) {
				return
			}
			continue
		}
		m.execute(job, handler)
		return
	}
}

func (m *Manager) execute(job domain.Job, handler Handler) {
	progress := func(status domain.JobStatus, value int) {
		job.Status = status
		if value >= 0 && value <= 100 {
			job.Progress = value
		}
		_ = m.store.UpdateJob(m.ctx, job)
	}
	checkpoint := checkpointWriter(func(phase string, values map[string]any) error {
		job.Result = mergeResult(job.Result, values)
		job.Result["phase"] = phase
		job.Result["checkpointedAt"] = time.Now().UTC().Format(time.RFC3339Nano)
		return m.store.UpdateJob(m.ctx, job)
	})
	runCtx := context.WithValue(m.ctx, checkpointKey{}, checkpoint)
	result, finalStatus, handlerErr := handler(runCtx, job, progress)
	job.Result = mergeResult(job.Result, result)
	if handlerErr != nil {
		job.Status = finalStatus
		if job.Status == "" || job.Status == domain.JobRunning || job.Status == domain.JobVerifying {
			job.Status = domain.JobFailed
		}
		job.ErrorClass = jobErrorClass(job.Kind, result)
		job.ErrorMessage = safeError(handlerErr)
		job.Progress = 100
	} else {
		if finalStatus == "" {
			finalStatus = domain.JobSucceeded
		}
		job.Status = finalStatus
		job.Progress = 100
		job.Result["phase"] = "completed"
	}
	job.FinishedAt = time.Now().UTC()
	_ = m.store.UpdateJob(m.ctx, job)
}

func sameExecutionClaim(stored, claimed domain.Job) bool {
	return stored.ID == claimed.ID && stored.Kind == claimed.Kind && stored.Status == domain.JobRunning &&
		stored.Attempts == claimed.Attempts && stored.StartedAt.Equal(claimed.StartedAt)
}

func dispatchRetryDelay(failures uint) time.Duration {
	delay := 100 * time.Millisecond
	for step := uint(0); step < failures && delay < 5*time.Second; step++ {
		delay *= 2
	}
	if delay > 5*time.Second {
		return 5 * time.Second
	}
	return delay
}

func (m *Manager) waitForDispatchRetry(failures uint) bool {
	delay := dispatchRetryDelay(failures)
	if m.retryDelay != nil {
		delay = m.retryDelay(failures)
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-m.ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func mergeResult(current, update map[string]any) map[string]any {
	output := make(map[string]any, len(current)+len(update)+1)
	for key, value := range current {
		output[key] = value
	}
	for key, value := range update {
		output[key] = value
	}
	return output
}

func jobErrorClass(kind string, result map[string]any) string {
	if value, ok := result["errorClass"].(string); ok && safeErrorClass(value) {
		return value
	}
	fallback := strings.ReplaceAll(strings.TrimSpace(kind), ".", "_") + "_failed"
	if safeErrorClass(fallback) {
		return fallback
	}
	return "operation_failed"
}

func safeErrorClass(value string) bool {
	if value == "" || len(value) > 80 {
		return false
	}
	for _, char := range value {
		if !(char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '_') {
			return false
		}
	}
	return true
}

func (m *Manager) enqueue(id string) error {
	if id == "" {
		return errors.New("job id is required")
	}
	if err := m.ctx.Err(); err != nil {
		return err
	}
	m.queueMu.Lock()
	if _, exists := m.queued[id]; !exists {
		m.queue = append(m.queue, id)
		m.queued[id] = struct{}{}
	}
	m.queueMu.Unlock()
	m.signal()
	return nil
}

func (m *Manager) sortRecoveredQueue() error {
	m.queueMu.Lock()
	defer m.queueMu.Unlock()
	if len(m.queue) < 2 {
		return nil
	}
	jobs := make(map[string]domain.Job, len(m.queue))
	for _, id := range m.queue {
		job, err := m.store.Job(m.ctx, id)
		if err != nil {
			return fmt.Errorf("read recovered job %s: %w", id, err)
		}
		jobs[id] = job
	}
	sort.SliceStable(m.queue, func(i, j int) bool {
		left, right := jobs[m.queue[i]], jobs[m.queue[j]]
		if left.CreatedAt.Equal(right.CreatedAt) {
			return left.ID < right.ID
		}
		return left.CreatedAt.Before(right.CreatedAt)
	})
	return nil
}

func (m *Manager) dequeue() (string, bool) {
	m.queueMu.Lock()
	defer m.queueMu.Unlock()
	if len(m.queue) == 0 {
		return "", false
	}
	id := m.queue[0]
	m.queue[0] = ""
	m.queue = m.queue[1:]
	delete(m.queued, id)
	return id, true
}

func (m *Manager) signal() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

func newID(prefix string, reader io.Reader) (string, error) {
	if reader == nil {
		reader = rand.Reader
	}
	var body [12]byte
	if _, err := io.ReadFull(reader, body[:]); err != nil {
		return "", err
	}
	return prefix + "-" + hex.EncodeToString(body[:]), nil
}

func safeError(err error) string {
	if err == nil {
		return ""
	}
	return "operation failed"
}
