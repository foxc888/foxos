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

type dhcpAPIExecutor struct {
	calls int
	plan  routeros.DHCPExpansionPlan
}

func (e *dhcpAPIExecutor) Execute(_ context.Context, plan routeros.DHCPExpansionPlan) error {
	e.calls++
	e.plan = plan
	return nil
}

type replayOnce struct {
	used map[string]struct{}
}

func (r *replayOnce) ConsumeReplay(_ context.Context, digest string, _ time.Time) error {
	if _, found := r.used[digest]; found {
		return confirmation.ErrReplayedToken
	}
	r.used[digest] = struct{}{}
	return nil
}

func dhcpAPIState() routeros.BindingState {
	return routeros.BindingState{
		Pools: []routeros.IPPool{{
			ID: "*10", Name: "pool-lan", Ranges: "10.0.0.100-10.0.0.200", Comment: "foxos:dhcp-pool:dhcp-lan",
		}},
		Networks: []routeros.DHCPNetwork{{ID: "*11", Address: "10.0.0.0/24", Gateway: "10.0.0.1"}},
		Addresses: []routeros.IPAddress{{
			ID: "*12", Address: "10.0.0.1/24", Interface: "bridge-lan", Disabled: "false",
		}},
		DHCPServers: []routeros.DHCPServer{{
			ID: "*13", Name: "dhcp-lan", Interface: "bridge-lan", AddressPool: "pool-lan", Running: "true", Disabled: "false",
		}},
	}
}

func TestDHCPAddressPlanAndExpansionWorkflow(t *testing.T) {
	t.Parallel()
	const apiToken = "01234567890123456789012345678901"
	app, err := New(&memoryNodes{nodes: map[string]domain.Node{}}, apiToken)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := confirmation.New([]byte("abcdefghijklmnopqrstuvwxyz012345"))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	executor := &dhcpAPIExecutor{}
	audit := &fakeAudit{}
	replay := &replayOnce{used: make(map[string]struct{})}
	app.RegisterDHCPPlanning(mux, fakeLeases{state: dhcpAPIState()}, signer, replay, executor, audit)

	read := httptest.NewRequest(http.MethodGet, "/api/v1/routeros/dhcp/address-plan", nil)
	read.Header.Set("Authorization", "Bearer "+apiToken)
	readResult := httptest.NewRecorder()
	mux.ServeHTTP(readResult, read)
	if readResult.Code != http.StatusOK || !strings.Contains(readResult.Body.String(), "\"configuredCapacity\":101") {
		t.Fatalf("status=%d body=%s", readResult.Code, readResult.Body.String())
	}

	previewRequest := httptest.NewRequest(http.MethodPost, "/api/v1/routeros/plans/dhcp-expansion", strings.NewReader(
		"{\"serverName\":\"dhcp-lan\",\"requestedCapacity\":150,\"proposedRanges\":\"10.0.0.50-10.0.0.200\"}",
	))
	previewRequest.Header.Set("Authorization", "Bearer "+apiToken)
	previewResult := httptest.NewRecorder()
	mux.ServeHTTP(previewResult, previewRequest)
	if previewResult.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", previewResult.Code, previewResult.Body.String())
	}
	var preview struct {
		Plan              routeros.DHCPExpansionPlan
		ConfirmationToken string
	}
	if err := json.Unmarshal(previewResult.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if !preview.Plan.Executable || preview.ConfirmationToken == "" {
		t.Fatalf("preview=%+v", preview)
	}
	body, _ := json.Marshal(map[string]any{"plan": preview.Plan, "confirmationToken": preview.ConfirmationToken})
	for attempt, want := range []int{http.StatusOK, http.StatusConflict} {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/routeros/plans/dhcp-expansion/execute", strings.NewReader(string(body)))
		request.Header.Set("Authorization", "Bearer "+apiToken)
		result := httptest.NewRecorder()
		mux.ServeHTTP(result, request)
		if result.Code != want {
			t.Fatalf("attempt=%d status=%d body=%s", attempt, result.Code, result.Body.String())
		}
	}
	if executor.calls != 1 || executor.plan.ProposedRanges != "10.0.0.50-10.0.0.200" {
		t.Fatalf("executor=%+v", executor)
	}
	if len(audit.events) != 2 || audit.events[1].Outcome != domain.AuditSucceeded {
		t.Fatalf("audit=%+v", audit.events)
	}
}

func TestDHCPExpansionLargeCapacityReturnsNonExecutableAlternatives(t *testing.T) {
	t.Parallel()
	const apiToken = "01234567890123456789012345678901"
	app, err := New(&memoryNodes{nodes: map[string]domain.Node{}}, apiToken)
	if err != nil {
		t.Fatal(err)
	}
	signer, _ := confirmation.New([]byte("abcdefghijklmnopqrstuvwxyz012345"))
	mux := http.NewServeMux()
	app.RegisterDHCPPlanning(mux, fakeLeases{state: dhcpAPIState()}, signer, &replayOnce{used: make(map[string]struct{})}, &dhcpAPIExecutor{}, &fakeAudit{})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/routeros/plans/dhcp-expansion", strings.NewReader(
		"{\"serverName\":\"dhcp-lan\",\"requestedCapacity\":300}",
	))
	request.Header.Set("Authorization", "Bearer "+apiToken)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "\"strategy\":\"expand-to-/23\"") ||
		!strings.Contains(response.Body.String(), "\"strategy\":\"split-vlan\"") ||
		!strings.Contains(response.Body.String(), "\"confirmationToken\":\"\"") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
