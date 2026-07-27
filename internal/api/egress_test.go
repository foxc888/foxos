package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/foxc888/foxos/internal/confirmation"
	"github.com/foxc888/foxos/internal/domain"
	"github.com/foxc888/foxos/internal/routeros"
)

type fakeDevicePolicies struct {
	policy    domain.DevicePolicy
	saveCalls int
	saveErr   error
}

func (f *fakeDevicePolicies) SaveDevicePolicy(_ context.Context, policy domain.DevicePolicy) error {
	f.saveCalls++
	if f.saveErr != nil {
		return f.saveErr
	}
	f.policy = policy
	return nil
}
func (f *fakeDevicePolicies) DevicePolicy(context.Context, string) (domain.DevicePolicy, error) {
	if f.policy.ID == "" {
		return domain.DevicePolicy{}, domain.ErrNotFound
	}
	return f.policy, nil
}
func (f *fakeDevicePolicies) DevicePolicies(context.Context) ([]domain.DevicePolicy, error) {
	return []domain.DevicePolicy{f.policy}, nil
}
func (f *fakeDevicePolicies) DeleteDevicePolicy(context.Context, string) error { return nil }

type fakeEgressPlanner struct {
	plan routeros.EgressPlan
}

func (f *fakeEgressPlanner) PlanDeviceEgress(context.Context, domain.DevicePolicy) (routeros.EgressPlan, error) {
	return f.plan, nil
}

type fakeEgressAPIExecutor struct {
	calls       int
	compensates int
}

func (f *fakeEgressAPIExecutor) Execute(context.Context, routeros.EgressPlan) error {
	f.calls++
	return nil
}

func (f *fakeEgressAPIExecutor) Compensate(context.Context, routeros.EgressPlan) error {
	f.compensates++
	return nil
}

type fakeEgressJobs struct {
	calls   int
	request map[string]any
}

type acceptingReplayStore struct{}

func (acceptingReplayStore) ConsumeReplay(context.Context, string, time.Time) error { return nil }

func (f *fakeEgressJobs) Submit(_ context.Context, kind, key string, request map[string]any) (domain.Job, error) {
	f.calls++
	f.request = request
	return domain.Job{ID: "job-egress", Kind: kind, Status: domain.JobQueued, IdempotencyKey: key, Request: request, CreatedAt: time.Now().UTC()}, nil
}

func TestEgressExecuteRejectsChangedPlanBeforeSubmission(t *testing.T) {
	fixture := newEgressAPIFixture(t)
	preview := fixture.preview(t)
	fixture.planner.plan.StaticIP = "10.0.0.21"

	response := fixture.execute(t, preview)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "egress_plan_stale") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if fixture.executor.calls != 0 || fixture.jobs.calls != 0 {
		t.Fatalf("executor=%d jobs=%d", fixture.executor.calls, fixture.jobs.calls)
	}
}

func TestEgressExecuteQueuesDurableJob(t *testing.T) {
	fixture := newEgressAPIFixture(t)
	preview := fixture.preview(t)

	response := fixture.execute(t, preview)
	if response.Code != http.StatusAccepted || !strings.Contains(response.Body.String(), "job-egress") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if fixture.executor.calls != 0 || fixture.jobs.calls != 1 || fixture.jobs.request["auditId"] == "" {
		t.Fatalf("executor=%d jobs=%d request=%+v", fixture.executor.calls, fixture.jobs.calls, fixture.jobs.request)
	}
	if len(fixture.audit.events) != 2 || fixture.audit.events[0].Outcome != domain.AuditStarted || fixture.audit.events[1].Details["jobId"] != "job-egress" {
		t.Fatalf("audit=%+v", fixture.audit.events)
	}
}

func TestEgressPlanAcceptsProposedPolicyWithoutPersistingIt(t *testing.T) {
	t.Parallel()
	fixture := newEgressAPIFixture(t)
	fixture.policy.policy = domain.DevicePolicy{}
	proposed := domain.DevicePolicy{ID: "phone", Name: "Phone", MACAddress: "AA:BB:CC:DD:EE:FF", StaticIP: "10.0.0.20", DHCPServer: "dhcp-lan", Egress: domain.EgressDirect}
	plan, err := routeros.PlanDeviceEgress(proposed, routeros.EgressState{})
	if err != nil {
		t.Fatal(err)
	}
	fixture.planner.plan = plan

	preview := fixture.previewPolicy(t, proposed)
	if preview.Plan.PreviousPolicy != nil || !preview.Plan.RequiresConfirmation || preview.ConfirmationToken == "" || len(preview.Plan.Operations) != 0 {
		t.Fatalf("preview=%+v", preview)
	}
	if fixture.policy.saveCalls != 0 {
		t.Fatalf("preview persisted policy %d times", fixture.policy.saveCalls)
	}
	response := fixture.execute(t, preview)
	if response.Code != http.StatusAccepted || fixture.jobs.calls != 1 || fixture.policy.saveCalls != 0 {
		t.Fatalf("status=%d jobs=%d saves=%d body=%s", response.Code, fixture.jobs.calls, fixture.policy.saveCalls, response.Body.String())
	}
}

func TestEgressPlanWithoutStateOrPolicyChangesNeedsNoExecution(t *testing.T) {
	t.Parallel()
	fixture := newEgressAPIFixture(t)
	policy := fixture.policy.policy
	policy.Egress = domain.EgressDirect
	fixture.policy.policy = policy
	plan, err := routeros.PlanDeviceEgress(policy, routeros.EgressState{})
	if err != nil {
		t.Fatal(err)
	}
	fixture.planner.plan = plan
	preview := fixture.preview(t)
	if preview.Plan.RequiresConfirmation || preview.ConfirmationToken != "" || len(preview.Plan.Operations) != 0 {
		t.Fatalf("preview=%+v", preview)
	}
}

func TestEgressExecuteRejectsChangedPolicyBeforeSubmission(t *testing.T) {
	t.Parallel()
	fixture := newEgressAPIFixture(t)
	preview := fixture.preview(t)
	fixture.policy.policy.Name = "Changed after preview"
	response := fixture.execute(t, preview)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "egress_plan_stale") || fixture.jobs.calls != 0 {
		t.Fatalf("status=%d jobs=%d body=%s", response.Code, fixture.jobs.calls, response.Body.String())
	}
}

type egressAPIFixture struct {
	mux      *http.ServeMux
	policy   *fakeDevicePolicies
	planner  *fakeEgressPlanner
	executor *fakeEgressAPIExecutor
	jobs     *fakeEgressJobs
	audit    *fakeAudit
}

type egressPreviewResponse struct {
	Plan              routeros.EgressPlan `json:"plan"`
	ConfirmationToken string              `json:"confirmationToken"`
}

func newEgressAPIFixture(t *testing.T) *egressAPIFixture {
	t.Helper()
	const apiToken = "01234567890123456789012345678901"
	app, err := New(&memoryNodes{nodes: map[string]domain.Node{}}, apiToken)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := confirmation.New([]byte("abcdefghijklmnopqrstuvwxyz012345"))
	if err != nil {
		t.Fatal(err)
	}
	policy := domain.DevicePolicy{ID: "phone", Name: "Phone", MACAddress: "AA:BB:CC:DD:EE:FF", StaticIP: "10.0.0.20", DHCPServer: "dhcp-lan", Egress: domain.EgressBlocked}
	state := routeros.EgressState{FilterRules: []map[string]string{{".id": "*f1", "chain": "forward", "action": "jump", "jump-target": "foxos-forward", "comment": "foxos:anchor:forward", "disabled": "false"}}}
	plan, err := routeros.PlanDeviceEgress(policy, state)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &egressAPIFixture{policy: &fakeDevicePolicies{policy: policy}, planner: &fakeEgressPlanner{plan: plan}, executor: &fakeEgressAPIExecutor{}, jobs: &fakeEgressJobs{}, audit: &fakeAudit{}, mux: http.NewServeMux()}
	app.RegisterEgress(fixture.mux, fixture.policy, fixture.planner, signer, acceptingReplayStore{}, fixture.executor, fixture.jobs, fixture.audit)
	return fixture
}

func (f *egressAPIFixture) preview(t *testing.T) egressPreviewResponse {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/routeros/plans/egress/phone", nil)
	request.Header.Set("Authorization", "Bearer 01234567890123456789012345678901")
	response := httptest.NewRecorder()
	f.mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var preview egressPreviewResponse
	if err := json.Unmarshal(response.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	return preview
}

func (f *egressAPIFixture) previewPolicy(t *testing.T, policy domain.DevicePolicy) egressPreviewResponse {
	t.Helper()
	body, err := json.Marshal(policyPayload(policy))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/routeros/plans/egress/"+policy.ID, strings.NewReader(string(body)))
	request.Header.Set("Authorization", "Bearer 01234567890123456789012345678901")
	response := httptest.NewRecorder()
	f.mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var preview egressPreviewResponse
	if err := json.Unmarshal(response.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	return preview
}

func (f *egressAPIFixture) execute(t *testing.T, preview egressPreviewResponse) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]any{"plan": preview.Plan, "confirmationToken": preview.ConfirmationToken})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/routeros/plans/egress/phone/execute", strings.NewReader(string(body)))
	request.Header.Set("Authorization", "Bearer 01234567890123456789012345678901")
	response := httptest.NewRecorder()
	f.mux.ServeHTTP(response, request)
	return response
}
