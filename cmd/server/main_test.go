package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/foxc888/foxos/internal/api"
	"github.com/foxc888/foxos/internal/domain"
	"github.com/foxc888/foxos/internal/mihomo"
	"github.com/foxc888/foxos/internal/routeros"
	"github.com/foxc888/foxos/internal/store/sqlite"
	"github.com/foxc888/foxos/internal/task"
)

func TestSecurityHeaders(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name             string
		path             string
		wantCacheControl string
	}{
		{name: "API response is not cached", path: "/api/v1/health/live", wantCacheControl: "no-store"},
		{name: "static response keeps normal cache policy", path: "/assets/app.js"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			handler := securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			}))
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))

			wantHeaders := map[string]string{
				"Content-Security-Policy": "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'",
				"X-Content-Type-Options":  "nosniff",
				"X-Frame-Options":         "DENY",
				"Referrer-Policy":         "no-referrer",
				"Permissions-Policy":      "camera=(), microphone=(), geolocation=()",
				"Cache-Control":           test.wantCacheControl,
			}
			for name, want := range wantHeaders {
				if got := response.Header().Get(name); got != want {
					t.Errorf("%s=%q, want %q", name, got, want)
				}
			}
		})
	}
}

func TestMihomoFailureClassification(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		result     mihomo.ApplyResult
		err        error
		wantStatus domain.JobStatus
		wantClass  string
	}{
		{name: "apply failed", err: mihomo.ErrApplyFailed, wantStatus: domain.JobFailed, wantClass: "mihomo_apply_failed"},
		{name: "rolled back", result: mihomo.ApplyResult{RolledBack: true}, err: mihomo.ErrApplyFailed, wantStatus: domain.JobRolledBack, wantClass: "mihomo_apply_rolled_back"},
		{name: "rollback failed", result: mihomo.ApplyResult{RolledBack: false}, err: errors.Join(mihomo.ErrRollbackFailed, errors.New("reload")), wantStatus: domain.JobFailed, wantClass: "mihomo_apply_rollback_failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			status, class := mihomoFailure("mihomo_apply", test.result, test.err)
			if status != test.wantStatus || class != test.wantClass {
				t.Fatalf("status=%s class=%s", status, class)
			}
		})
	}
}

type mihomoJobService struct {
	preview       mihomo.Preview
	previewErr    error
	applyResult   mihomo.ApplyResult
	applySnapshot domain.MihomoSnapshot
	applyErr      error
	restoreResult mihomo.ApplyResult
	restoreItem   domain.MihomoSnapshot
	restoreErr    error
	previewCalls  atomic.Int32
	applyCalls    atomic.Int32
}

type blockingMihomoAudit struct {
	store        *sqlite.Store
	finalStarted chan struct{}
	releaseFinal chan struct{}
	startOnce    sync.Once
	releaseOnce  sync.Once
}

func (a *blockingMihomoAudit) SaveAudit(ctx context.Context, event domain.AuditEvent) error {
	if event.Outcome == domain.AuditSucceeded {
		a.startOnce.Do(func() { close(a.finalStarted) })
		select {
		case <-a.releaseFinal:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return a.store.SaveAudit(ctx, event)
}

func (a *blockingMihomoAudit) release() {
	a.releaseOnce.Do(func() { close(a.releaseFinal) })
}

func (s *mihomoJobService) Draft(context.Context) (domain.MihomoDraft, error) {
	return domain.MihomoDraft{}, nil
}

func (s *mihomoJobService) SaveDraft(_ context.Context, draft domain.MihomoDraft) (domain.MihomoDraft, error) {
	return draft, nil
}

func (s *mihomoJobService) Preview(context.Context, domain.MihomoDraft) (mihomo.Preview, error) {
	s.previewCalls.Add(1)
	return s.preview, s.previewErr
}

func (s *mihomoJobService) ApplyPreview(context.Context, domain.MihomoDraft, string, string) (mihomo.ApplyResult, domain.MihomoSnapshot, error) {
	s.applyCalls.Add(1)
	return s.applyResult, s.applySnapshot, s.applyErr
}

func (s *mihomoJobService) Restore(context.Context, string, string) (mihomo.ApplyResult, domain.MihomoSnapshot, error) {
	return s.restoreResult, s.restoreItem, s.restoreErr
}

func TestMihomoApplyJobAuditLifecycle(t *testing.T) {
	t.Parallel()
	const digest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	tests := []struct {
		name             string
		request          map[string]any
		service          *mihomoJobService
		wantStatus       domain.JobStatus
		wantOutcome      domain.AuditOutcome
		wantErrorClass   string
		wantPreviewCalls int32
		wantApplyCalls   int32
		wantDiff         string
	}{
		{
			name:       "successful apply records server preview diff",
			request:    mihomoApplyJobRequest(map[string]any{"mode": "rule"}, digest),
			service:    &mihomoJobService{preview: mihomo.Preview{Digest: digest, Diff: "+password: '***'\n", HasSecret: true}, applySnapshot: domain.MihomoSnapshot{ID: "snapshot-ok"}},
			wantStatus: domain.JobSucceeded, wantOutcome: domain.AuditSucceeded, wantPreviewCalls: 1, wantApplyCalls: 1, wantDiff: "+password: '***'\n",
		},
		{
			name:       "invalid persisted request is terminally audited",
			request:    mihomoApplyJobRequest("not-an-object", digest),
			service:    &mihomoJobService{},
			wantStatus: domain.JobFailed, wantOutcome: domain.AuditFailed, wantErrorClass: "mihomo_request_invalid",
		},
		{
			name:       "preview failure never leaves started audit",
			request:    mihomoApplyJobRequest(map[string]any{"mode": "rule"}, digest),
			service:    &mihomoJobService{previewErr: errors.New("preview unavailable")},
			wantStatus: domain.JobFailed, wantOutcome: domain.AuditFailed, wantErrorClass: "mihomo_preview_failed", wantPreviewCalls: 1,
		},
		{
			name:       "changed database state is rejected before apply",
			request:    mihomoApplyJobRequest(map[string]any{"mode": "rule"}, digest),
			service:    &mihomoJobService{preview: mihomo.Preview{Digest: strings.Repeat("a", 64), Diff: "+mode: rule\n"}},
			wantStatus: domain.JobFailed, wantOutcome: domain.AuditFailed, wantErrorClass: "mihomo_apply_stale", wantPreviewCalls: 1,
		},
		{
			name:       "verified rollback is classified and audited",
			request:    mihomoApplyJobRequest(map[string]any{"mode": "rule"}, digest),
			service:    &mihomoJobService{preview: mihomo.Preview{Digest: digest, Diff: "+mode: rule\n"}, applyResult: mihomo.ApplyResult{RolledBack: true}, applyErr: mihomo.ErrApplyFailed},
			wantStatus: domain.JobRolledBack, wantOutcome: domain.AuditFailed, wantErrorClass: "mihomo_apply_rolled_back", wantPreviewCalls: 1, wantApplyCalls: 1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			store, manager := newMihomoJobManager(t, test.service)
			job, err := manager.Submit(context.Background(), "mihomo.apply", test.name, test.request)
			if err != nil {
				t.Fatal(err)
			}
			completed := waitForStoredJob(t, manager, job.ID, test.wantStatus)
			if completed.ErrorClass != test.wantErrorClass {
				t.Fatalf("errorClass=%q, want %q", completed.ErrorClass, test.wantErrorClass)
			}
			if got := test.service.previewCalls.Load(); got != test.wantPreviewCalls {
				t.Fatalf("preview calls=%d, want %d", got, test.wantPreviewCalls)
			}
			if got := test.service.applyCalls.Load(); got != test.wantApplyCalls {
				t.Fatalf("apply calls=%d, want %d", got, test.wantApplyCalls)
			}
			events, err := store.AuditEvents(context.Background(), 10)
			if err != nil {
				t.Fatal(err)
			}
			if len(events) != 1 || events[0].Outcome != test.wantOutcome {
				t.Fatalf("audit=%+v, want outcome %s", events, test.wantOutcome)
			}
			if got, _ := events[0].Details["errorClass"].(string); got != test.wantErrorClass {
				t.Fatalf("audit errorClass=%q, want %q", got, test.wantErrorClass)
			}
			if test.wantDiff != "" {
				if got := events[0].Details["diff"]; got != test.wantDiff {
					t.Fatalf("audit diff=%q, want %q", got, test.wantDiff)
				}
				if events[0].Details["containsRedactedSecrets"] != true {
					t.Fatalf("audit did not preserve redaction marker: %+v", events[0].Details)
				}
			}
		})
	}
}

func TestMihomoApplyJobDoesNotSucceedBeforeFinalAudit(t *testing.T) {
	t.Parallel()
	const digest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	manager, err := task.New(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	audit := &blockingMihomoAudit{
		store:        store,
		finalStarted: make(chan struct{}),
		releaseFinal: make(chan struct{}),
	}
	t.Cleanup(audit.release)
	service := &mihomoJobService{
		preview:       mihomo.Preview{Digest: digest, Diff: "+mode: rule\n"},
		applySnapshot: domain.MihomoSnapshot{ID: "snapshot-ok"},
	}
	registerMihomoJobs(manager, service, audit)
	job, err := manager.Submit(context.Background(), "mihomo.apply", t.Name(), mihomoApplyJobRequest(map[string]any{"mode": "rule"}, digest))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-audit.finalStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("final Mihomo audit did not start")
	}
	pending, err := manager.Job(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if pending.Status != domain.JobVerifying || pending.Progress != 95 {
		t.Fatalf("job exposed premature terminal state: status=%s progress=%d", pending.Status, pending.Progress)
	}
	audit.release()
	completed := waitForStoredJob(t, manager, job.ID, domain.JobSucceeded)
	if completed.Progress != 100 {
		t.Fatalf("completed progress=%d, want 100", completed.Progress)
	}
}

func TestMihomoApplyJobRecoversAfterRestart(t *testing.T) {
	t.Parallel()
	const digest = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	seed := domain.Job{ID: "job-recovered", Kind: "mihomo.apply", Status: domain.JobRunning, Progress: 35, Request: mihomoApplyJobRequest(map[string]any{"mode": "rule"}, digest), Result: map[string]any{}}
	if _, _, err := store.CreateJob(context.Background(), seed); err != nil {
		t.Fatal(err)
	}
	manager, err := task.New(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	service := &mihomoJobService{preview: mihomo.Preview{Digest: digest, Diff: "+mode: rule\n"}, applySnapshot: domain.MihomoSnapshot{ID: "snapshot-recovered"}}
	registerMihomoJobs(manager, service, store)
	completed := waitForStoredJob(t, manager, seed.ID, domain.JobSucceeded)
	if completed.Attempts != 1 || completed.Result["snapshotId"] != "snapshot-recovered" {
		t.Fatalf("recovered job=%+v", completed)
	}
	events, err := store.AuditEvents(context.Background(), 10)
	if err != nil || len(events) != 1 || events[0].Outcome != domain.AuditSucceeded {
		t.Fatalf("audit=%+v err=%v", events, err)
	}
}

func TestBoundedAuditDiff(t *testing.T) {
	t.Parallel()
	value := strings.Repeat("a", (64<<10)-1) + "界" + strings.Repeat("b", 100)
	got, truncated := boundedAuditDiff(value)
	if !truncated || !strings.HasSuffix(got, "# diff truncated by FoxOS\n") || strings.ToValidUTF8(got, "") != got {
		t.Fatalf("invalid bounded diff: truncated=%t len=%d", truncated, len(got))
	}
}

type egressJobPolicies struct {
	mu        sync.Mutex
	current   *domain.DevicePolicy
	saveErr   error
	saveCalls int
}

func (s *egressJobPolicies) SaveDevicePolicy(_ context.Context, policy domain.DevicePolicy) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saveCalls++
	if s.saveErr != nil {
		return s.saveErr
	}
	copy := policy
	s.current = &copy
	return nil
}

func (s *egressJobPolicies) DevicePolicy(context.Context, string) (domain.DevicePolicy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current == nil {
		return domain.DevicePolicy{}, domain.ErrNotFound
	}
	return *s.current, nil
}

func (s *egressJobPolicies) DevicePolicies(context.Context) ([]domain.DevicePolicy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current == nil {
		return nil, nil
	}
	return []domain.DevicePolicy{*s.current}, nil
}

func (s *egressJobPolicies) DeleteDevicePolicy(context.Context, string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.current = nil
	return nil
}

func (s *egressJobPolicies) snapshot() (*domain.DevicePolicy, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current == nil {
		return nil, s.saveCalls
	}
	copy := *s.current
	return &copy, s.saveCalls
}

type egressJobPlanner struct {
	state routeros.EgressState
}

func (p egressJobPlanner) PlanDeviceEgress(_ context.Context, policy domain.DevicePolicy) (routeros.EgressPlan, error) {
	return routeros.PlanDeviceEgress(policy, p.state)
}

type egressJobExecutor struct {
	executeCalls    atomic.Int32
	compensateCalls atomic.Int32
	executeErr      error
	compensateErr   error
}

func (e *egressJobExecutor) Execute(context.Context, routeros.EgressPlan) error {
	e.executeCalls.Add(1)
	return e.executeErr
}

func (e *egressJobExecutor) Compensate(context.Context, routeros.EgressPlan) error {
	e.compensateCalls.Add(1)
	return e.compensateErr
}

func TestEgressJobPersistsOnlyAfterExecutionAndCompensatesPersistFailure(t *testing.T) {
	t.Parallel()
	direct := domain.DevicePolicy{ID: "phone", Name: "Phone", MACAddress: "AA:BB:CC:DD:EE:FF", StaticIP: "192.168.1.20", DHCPServer: "dhcp-lan", Egress: domain.EgressDirect}
	blocked := direct
	blocked.Egress = domain.EgressBlocked
	ready := routeros.EgressState{FilterRules: []map[string]string{{".id": "*f1", "chain": "forward", "action": "jump", "jump-target": "foxos-forward", "comment": "foxos:anchor:forward", "disabled": "false"}}}
	tests := []struct {
		name                string
		previous            *domain.DevicePolicy
		current             *domain.DevicePolicy
		desired             domain.DevicePolicy
		state               routeros.EgressState
		saveErr             error
		compensateErr       error
		wantStatus          domain.JobStatus
		wantErrorClass      string
		wantExecuteCalls    int32
		wantCompensations   int32
		wantPersistedEgress domain.EgressType
	}{
		{name: "new direct policy is persisted without RouterOS writes", desired: direct, wantStatus: domain.JobSucceeded, wantExecuteCalls: 0, wantPersistedEgress: domain.EgressDirect},
		{name: "SQLite failure rolls RouterOS changes back", previous: &direct, current: &direct, desired: blocked, state: ready, saveErr: errors.New("database unavailable"), wantStatus: domain.JobRolledBack, wantErrorClass: "egress_policy_persist_rolled_back", wantExecuteCalls: 1, wantCompensations: 1, wantPersistedEgress: domain.EgressDirect},
		{name: "failed compensation remains a failed task", previous: &direct, current: &direct, desired: blocked, state: ready, saveErr: errors.New("database unavailable"), compensateErr: errors.New("rollback unavailable"), wantStatus: domain.JobFailed, wantErrorClass: "egress_policy_persist_rollback_failed", wantExecuteCalls: 1, wantCompensations: 1, wantPersistedEgress: domain.EgressDirect},
		{name: "changed previous policy rejects stale task", previous: &direct, current: func() *domain.DevicePolicy { changed := direct; changed.Name = "Changed"; return &changed }(), desired: blocked, state: ready, wantStatus: domain.JobFailed, wantErrorClass: "egress_plan_stale", wantPersistedEgress: domain.EgressDirect},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			raw, err := routeros.PlanDeviceEgress(test.desired, test.state)
			if err != nil {
				t.Fatal(err)
			}
			plan := api.FinalizeEgressPlan(raw, test.previous)
			store, err := sqlite.Open(filepath.Join(t.TempDir(), "jobs.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			policies := &egressJobPolicies{current: clonePolicy(test.current), saveErr: test.saveErr}
			executor := &egressJobExecutor{compensateErr: test.compensateErr}
			manager, err := task.New(context.Background(), store)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = manager.Close() })
			registerEgressJobs(manager, policies, egressJobPlanner{state: test.state}, executor, store)
			job, err := manager.Submit(context.Background(), "routeros.egress", test.name, map[string]any{"plan": plan, "actor": "test", "source": "127.0.0.1"})
			if err != nil {
				t.Fatal(err)
			}
			completed := waitForStoredJob(t, manager, job.ID, test.wantStatus)
			if completed.ErrorClass != test.wantErrorClass {
				t.Fatalf("errorClass=%q, want %q", completed.ErrorClass, test.wantErrorClass)
			}
			if executor.executeCalls.Load() != test.wantExecuteCalls || executor.compensateCalls.Load() != test.wantCompensations {
				t.Fatalf("execute=%d compensate=%d", executor.executeCalls.Load(), executor.compensateCalls.Load())
			}
			persisted, _ := policies.snapshot()
			if persisted == nil || persisted.Egress != test.wantPersistedEgress {
				t.Fatalf("persisted=%+v, want egress %s", persisted, test.wantPersistedEgress)
			}
			events, err := store.AuditEvents(context.Background(), 10)
			if err != nil || len(events) != 1 || events[0].Outcome == domain.AuditStarted {
				t.Fatalf("audit=%+v err=%v", events, err)
			}
		})
	}
}

func clonePolicy(policy *domain.DevicePolicy) *domain.DevicePolicy {
	if policy == nil {
		return nil
	}
	copy := *policy
	return &copy
}

func mihomoApplyJobRequest(draft any, digest string) map[string]any {
	return map[string]any{"draft": draft, "digest": digest, "label": "test", "actor": "test", "source": "127.0.0.1"}
}

func newMihomoJobManager(t *testing.T, service *mihomoJobService) (*sqlite.Store, *task.Manager) {
	t.Helper()
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	manager, err := task.New(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	registerMihomoJobs(manager, service, store)
	return store, manager
}

func waitForStoredJob(t *testing.T, manager *task.Manager, id string, status domain.JobStatus) domain.Job {
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
