package routeros

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/foxc888/foxos/internal/domain"
)

func TestPlanDeviceEgressModes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		egress   domain.EgressType
		target   string
		wantPath string
		wantOps  int
	}{
		{name: "direct", egress: domain.EgressDirect, wantOps: 0},
		{name: "Mihomo node", egress: domain.EgressMihomoNode, target: "node-a", wantPath: "/rest/ip/firewall/mangle", wantOps: 5},
		{name: "proxy chain", egress: domain.EgressProxyChain, target: "group-a", wantPath: "/rest/ip/firewall/mangle", wantOps: 5},
		{name: "L2TP", egress: domain.EgressL2TP, target: "l2tp-out", wantPath: "/rest/ip/route", wantOps: 6},
		{name: "blocked", egress: domain.EgressBlocked, wantPath: "/rest/ip/firewall/filter", wantOps: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			policy := domain.DevicePolicy{ID: "phone", Name: "Phone", MACAddress: "AA:BB:CC:DD:EE:FF", StaticIP: "10.0.0.20", DHCPServer: "dhcp-lan", Egress: test.egress, TargetID: test.target}
			plan, err := PlanDeviceEgress(policy, readyEgressState(test.egress, test.target))
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.Operations) != test.wantOps || (test.wantOps > 0 && plan.Operations[len(plan.Operations)-1].Path != test.wantPath) {
				t.Fatalf("plan=%+v", plan)
			}
			if plan.RequiresConfirmation != (test.wantOps > 0) {
				t.Fatalf("requires confirmation=%t", plan.RequiresConfirmation)
			}
			for _, operation := range plan.Operations {
				if err := ValidateEgressOperation(operation); err != nil {
					t.Fatalf("operation=%+v err=%v", operation, err)
				}
			}
		})
	}
}

func readyEgressState(egress domain.EgressType, target string) EgressState {
	state := EgressState{
		MangleRules:   []map[string]string{{".id": "*m1", "chain": "prerouting", "action": "jump", "jump-target": egressMangleChain, "comment": "foxos:anchor:mangle", "disabled": "false"}},
		FilterRules:   []map[string]string{{".id": "*f1", "chain": "forward", "action": "jump", "jump-target": egressFilterChain, "comment": "foxos:anchor:forward", "disabled": "false"}},
		RoutingTables: []map[string]string{{"name": mihomoTable, "fib": "yes", "disabled": "false"}},
		Routes:        []map[string]string{{".id": "*r0", "dst-address": "0.0.0.0/0", "gateway": "10.0.0.2", "routing-table": mihomoTable, "comment": "foxos:prerequisite:mihomo-transparent", "disabled": "false"}},
	}
	if egress == domain.EgressL2TP {
		state.RoutingTables = append(state.RoutingTables, map[string]string{"name": l2tpTable("phone"), "fib": "yes", "disabled": "false"})
		state.L2TPClients = []map[string]string{{"name": target, "running": "true", "disabled": "false"}}
	}
	return state
}

func TestPlanDeviceEgressProtectsManagementAddresses(t *testing.T) {
	t.Parallel()
	for _, ip := range []string{"10.0.0.1", "10.0.0.2", "10.0.0.3", "10.0.0.4"} {
		t.Run(ip, func(t *testing.T) {
			t.Parallel()
			policy := domain.DevicePolicy{ID: "phone", MACAddress: "AA:BB:CC:DD:EE:FF", StaticIP: ip, DHCPServer: "dhcp-lan", Egress: domain.EgressBlocked}
			if _, err := PlanDeviceEgress(policy, EgressState{}); !errors.Is(err, ErrEgressPlan) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

type fakeEgressWriter struct {
	mu                    sync.Mutex
	state                 EgressState
	applied               int
	applyErrAt            int
	verifyErr             error
	compensated           int
	compensatedOperations int
	mutate                bool
}

func (w *fakeEgressWriter) EgressState(context.Context) (EgressState, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.state, nil
}
func (w *fakeEgressWriter) ApplyEgress(_ context.Context, operation EgressOperation) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.applied++
	if w.applyErrAt == w.applied {
		return errors.New("write failed")
	}
	if w.mutate {
		w.state.FilterRules = append(w.state.FilterRules, map[string]string{".id": "*new", "chain": operation.Body["chain"], "action": operation.Body["action"], "comment": operation.OwnedComment})
	}
	return nil
}
func (w *fakeEgressWriter) VerifyEgress(context.Context, EgressPlan) error { return w.verifyErr }

func (w *fakeEgressWriter) CompensateEgress(_ context.Context, operations []EgressOperation) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.compensated++
	w.compensatedOperations = len(operations)
	return nil
}

func TestEgressExecutorCompensatesFailedVerification(t *testing.T) {
	t.Parallel()
	policy := domain.DevicePolicy{ID: "phone", MACAddress: "AA:BB:CC:DD:EE:FF", StaticIP: "10.0.0.20", DHCPServer: "dhcp-lan", Egress: domain.EgressBlocked}
	plan, err := PlanDeviceEgress(policy, readyEgressState(domain.EgressBlocked, ""))
	if err != nil {
		t.Fatal(err)
	}
	writer := &fakeEgressWriter{state: readyEgressState(domain.EgressBlocked, ""), verifyErr: errors.New("mismatch")}
	if err := (EgressExecutor{Writer: writer}).Execute(context.Background(), plan); !errors.Is(err, ErrEgressRolledBack) {
		t.Fatalf("err=%v", err)
	}
	if writer.applied != len(plan.Operations) || writer.compensated != 1 {
		t.Fatalf("writer=%+v", writer)
	}
}

func TestEgressExecutorCompensatesOnlyAppliedPrefix(t *testing.T) {
	t.Parallel()
	policy := domain.DevicePolicy{ID: "phone", MACAddress: "AA:BB:CC:DD:EE:FF", StaticIP: "10.0.0.20", DHCPServer: "dhcp-lan", Egress: domain.EgressMihomoNode, TargetID: "node-a"}
	state := readyEgressState(domain.EgressMihomoNode, policy.TargetID)
	plan, err := PlanDeviceEgress(policy, state)
	if err != nil {
		t.Fatal(err)
	}
	writer := &fakeEgressWriter{state: state, applyErrAt: 2}
	err = (EgressExecutor{Writer: writer}).Execute(context.Background(), plan)
	if !errors.Is(err, ErrEgressRolledBack) {
		t.Fatalf("err=%v", err)
	}
	if writer.applied != 2 || writer.compensated != 1 || writer.compensatedOperations != 1 {
		t.Fatalf("applied=%d compensated=%d operations=%d", writer.applied, writer.compensated, writer.compensatedOperations)
	}
}

func TestEgressExecutorDoesNotCompensateWhenFirstWriteFails(t *testing.T) {
	t.Parallel()
	policy := domain.DevicePolicy{ID: "phone", MACAddress: "AA:BB:CC:DD:EE:FF", StaticIP: "10.0.0.20", DHCPServer: "dhcp-lan", Egress: domain.EgressBlocked}
	state := readyEgressState(domain.EgressBlocked, "")
	plan, err := PlanDeviceEgress(policy, state)
	if err != nil {
		t.Fatal(err)
	}
	writer := &fakeEgressWriter{state: state, applyErrAt: 1}
	err = (EgressExecutor{Writer: writer}).Execute(context.Background(), plan)
	if err == nil || errors.Is(err, ErrEgressRolledBack) {
		t.Fatalf("err=%v", err)
	}
	if writer.compensated != 0 {
		t.Fatalf("compensated=%d", writer.compensated)
	}
}

func TestEgressStateDigestIsStableAcrossMapInsertionOrder(t *testing.T) {
	t.Parallel()
	left := EgressState{Routes: []map[string]string{{"gateway": "10.0.0.2", "comment": "foxos:route"}}}
	right := EgressState{Routes: []map[string]string{{"comment": "foxos:route", "gateway": "10.0.0.2"}}}
	if EgressStateDigest(left) != EgressStateDigest(right) {
		t.Fatal("equivalent RouterOS state produced different digests")
	}
}

func TestEgressExecutorRejectsStalePlanBeforeWrite(t *testing.T) {
	t.Parallel()
	state := readyEgressState(domain.EgressBlocked, "")
	policy := domain.DevicePolicy{ID: "phone", MACAddress: "AA:BB:CC:DD:EE:FF", StaticIP: "10.0.0.20", DHCPServer: "dhcp-lan", Egress: domain.EgressBlocked}
	plan, err := PlanDeviceEgress(policy, state)
	if err != nil {
		t.Fatal(err)
	}
	state.FilterRules = append(state.FilterRules, map[string]string{".id": "*user", "chain": "forward", "action": "accept", "comment": "user-rule"})
	writer := &fakeEgressWriter{state: state}
	err = (EgressExecutor{Writer: writer}).Execute(context.Background(), plan)
	if !errors.Is(err, ErrEgressPlanStale) {
		t.Fatalf("err=%v", err)
	}
	if writer.applied != 0 {
		t.Fatalf("applied=%d, want 0", writer.applied)
	}
}

func TestEgressExecutorSerializesConcurrentPlans(t *testing.T) {
	state := readyEgressState(domain.EgressBlocked, "")
	policy := domain.DevicePolicy{ID: "phone", MACAddress: "AA:BB:CC:DD:EE:FF", StaticIP: "10.0.0.20", DHCPServer: "dhcp-lan", Egress: domain.EgressBlocked}
	plan, err := PlanDeviceEgress(policy, state)
	if err != nil {
		t.Fatal(err)
	}
	writer := &fakeEgressWriter{state: state, mutate: true}
	executor := EgressExecutor{Writer: writer}
	errorsSeen := make(chan error, 2)
	var started sync.WaitGroup
	started.Add(2)
	for range 2 {
		go func() {
			started.Done()
			started.Wait()
			errorsSeen <- executor.Execute(context.Background(), plan)
		}()
	}
	first := <-errorsSeen
	second := <-errorsSeen
	close(errorsSeen)
	succeeded := 0
	stale := 0
	for _, executeErr := range []error{first, second} {
		switch {
		case executeErr == nil:
			succeeded++
		case errors.Is(executeErr, ErrEgressPlanStale):
			stale++
		default:
			t.Fatalf("unexpected error: %v", executeErr)
		}
	}
	if succeeded != 1 || stale != 1 || writer.applied != 1 {
		t.Fatalf("succeeded=%d stale=%d applied=%d", succeeded, stale, writer.applied)
	}
}
