package task

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/foxc888/foxos/internal/domain"
)

type Store interface {
	CreateJob(context.Context, domain.Job) (domain.Job, bool, error)
	Job(context.Context, string) (domain.Job, error)
	Jobs(context.Context, int) ([]domain.Job, error)
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

type Manager struct {
	store    Store
	ctx      context.Context
	cancel   context.CancelFunc
	queue    chan string
	handlers map[string]Handler
	mu       sync.RWMutex
	wg       sync.WaitGroup
	entropy  io.Reader
}

func New(parent context.Context, store Store) (*Manager, error) {
	if parent == nil {
		parent = context.Background()
	}
	if store == nil {
		return nil, errors.New("task store is required")
	}
	ctx, cancel := context.WithCancel(parent)
	manager := &Manager{store: store, ctx: ctx, cancel: cancel, queue: make(chan string, 128), handlers: make(map[string]Handler), entropy: rand.Reader}
	manager.wg.Add(1)
	go manager.worker()
	return manager, nil
}

func (m *Manager) Register(kind string, handler Handler) error {
	return m.RegisterWithRecovery(kind, handler, nil)
}

func (m *Manager) RegisterWithRecovery(kind string, handler Handler, recoverer Recoverer) error {
	if m == nil || handler == nil || kind == "" {
		return errors.New("job kind and handler are required")
	}
	m.mu.Lock()
	m.handlers[kind] = handler
	m.mu.Unlock()
	jobs, err := m.store.Jobs(m.ctx, 500)
	if err != nil {
		return err
	}
	for _, job := range jobs {
		if job.Kind != kind {
			continue
		}
		switch job.Status {
		case domain.JobQueued:
			m.enqueue(job.ID)
		case domain.JobRunning, domain.JobVerifying:
			if err := m.recoverInterrupted(job, recoverer); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *Manager) recoverInterrupted(job domain.Job, recoverer Recoverer) error {
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
		return m.store.UpdateJob(m.ctx, job)
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
		return err
	}
	if job.Status == domain.JobQueued {
		m.enqueue(job.ID)
	}
	return nil
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
	id, err := newID("job", m.entropy)
	if err != nil {
		return domain.Job{}, fmt.Errorf("generate job id: %w", err)
	}
	job := domain.Job{ID: id, Kind: kind, Status: domain.JobQueued, IdempotencyKey: idempotencyKey, Request: request, Result: map[string]any{}, CreatedAt: time.Now().UTC()}
	created, existing, err := m.store.CreateJob(ctx, job)
	if err != nil {
		return domain.Job{}, err
	}
	if !existing {
		m.enqueue(created.ID)
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
	job, err := m.store.Job(ctx, id)
	if err != nil {
		return domain.Job{}, err
	}
	if job.Status != domain.JobFailed && job.Status != domain.JobRolledBack {
		return domain.Job{}, fmt.Errorf("job %s is not retryable", id)
	}
	job.Status = domain.JobQueued
	job.Progress = 0
	job.ErrorClass = ""
	job.ErrorMessage = ""
	job.FinishedAt = time.Time{}
	if err := m.store.UpdateJob(ctx, job); err != nil {
		return domain.Job{}, err
	}
	m.enqueue(id)
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

func (m *Manager) worker() {
	defer m.wg.Done()
	for {
		select {
		case <-m.ctx.Done():
			return
		case id := <-m.queue:
			m.run(id)
		}
	}
}

func (m *Manager) run(id string) {
	job, err := m.store.Job(m.ctx, id)
	if err != nil || job.Terminal() {
		return
	}
	m.mu.RLock()
	handler := m.handlers[job.Kind]
	m.mu.RUnlock()
	if handler == nil {
		return
	}
	job.Status = domain.JobRunning
	job.Attempts++
	job.StartedAt = time.Now().UTC()
	job.Progress = 1
	if err := m.store.UpdateJob(m.ctx, job); err != nil {
		return
	}
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

func (m *Manager) enqueue(id string) {
	select {
	case m.queue <- id:
	default:
		// The durable QUEUED state remains available for the next restart/retry.
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
