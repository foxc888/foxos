package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/foxc888/foxos/internal/confirmation"
	"github.com/foxc888/foxos/internal/domain"
	"github.com/foxc888/foxos/internal/routeros"
)

type fakeLeases struct{ leases []routeros.Lease }

func (f fakeLeases) Leases(context.Context) ([]routeros.Lease, error) { return f.leases, nil }

type fakeBindingExecutor struct{ calls int }
type fakeAudit struct{ events []domain.AuditEvent }

func (a *fakeAudit) SaveAudit(_ context.Context, event domain.AuditEvent) error {
	a.events = append(a.events, event)
	return nil
}
func (f *fakeBindingExecutor) Execute(context.Context, routeros.Plan, string) error {
	f.calls++
	return nil
}

func TestBindingPreviewReturnsConfirmationAndExecuteConsumesOnce(t *testing.T) {
	const token = "01234567890123456789012345678901"
	app, err := New(&memoryNodes{nodes: map[string]domain.Node{}}, token)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := confirmation.New([]byte("abcdefghijklmnopqrstuvwxyz012345"))
	if err != nil {
		t.Fatal(err)
	}
	executor := &fakeBindingExecutor{}
	mux := http.NewServeMux()
	audit := &fakeAudit{}
	app.RegisterBindingPlan(mux, fakeLeases{leases: []routeros.Lease{{ID: "*1", Address: "10.0.0.19", MACAddress: "AA:BB:CC:DD:EE:FF", Server: "dhcp-lan", Dynamic: "false", Comment: "foxos:device:phone"}}}, signer, executor, confirmation.NewReplayGuard(), audit)
	body := `{"id":"phone","name":"iPhone","macAddress":"AA:BB:CC:DD:EE:FF","staticIp":"10.0.0.20","dhcpServer":"dhcp-lan","egress":"direct"}`
	request := httptest.NewRequest("POST", "/api/v1/routeros/plans/device-binding", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var preview struct {
		Plan              routeros.Plan `json:"plan"`
		ConfirmationToken string        `json:"confirmationToken"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if preview.ConfirmationToken == "" || !preview.Plan.RequiresConfirmation {
		t.Fatalf("preview=%+v", preview)
	}
	executionBody, _ := json.Marshal(map[string]any{"plan": preview.Plan, "confirmationToken": preview.ConfirmationToken})
	for index, want := range []int{http.StatusOK, http.StatusConflict} {
		execute := httptest.NewRequest("POST", "/api/v1/routeros/plans/device-binding/execute", strings.NewReader(string(executionBody)))
		execute.Header.Set("Authorization", "Bearer "+token)
		result := httptest.NewRecorder()
		mux.ServeHTTP(result, execute)
		if result.Code != want {
			t.Fatalf("attempt=%d status=%d body=%s", index, result.Code, result.Body.String())
		}
	}
	if executor.calls != 1 {
		t.Fatalf("calls=%d", executor.calls)
	}
	if len(audit.events) != 2 || audit.events[0].Outcome != domain.AuditStarted || audit.events[1].Outcome != domain.AuditSucceeded {
		t.Fatalf("audit=%+v", audit.events)
	}
}
