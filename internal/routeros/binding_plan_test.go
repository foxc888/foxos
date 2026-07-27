package routeros

import (
	"errors"
	"net/http"
	"testing"

	"github.com/foxc888/foxos/internal/domain"
)

func policy() domain.DevicePolicy {
	return domain.DevicePolicy{ID: "phone", Name: "iPhone", MACAddress: "aa:bb:cc:dd:ee:ff", StaticIP: "10.0.0.20", DHCPServer: "dhcp-lan", Egress: domain.EgressDirect}
}

func bindingState(leases ...Lease) BindingState {
	return BindingState{
		Leases:      leases,
		Pools:       []IPPool{{ID: "*10", Name: "pool-lan", Ranges: "10.0.0.100-10.0.0.200"}},
		Networks:    []DHCPNetwork{{ID: "*11", Address: "10.0.0.0/24", Gateway: "10.0.0.1"}},
		Addresses:   []IPAddress{{ID: "*12", Address: "10.0.0.1/24", Network: "10.0.0.0", Interface: "bridge-lan", Disabled: "false"}},
		DHCPServers: []DHCPServer{{ID: "*13", Name: "dhcp-lan", Interface: "bridge-lan", AddressPool: "pool-lan", Running: "true", Disabled: "false"}},
	}
}

func TestPlanAdoptsOrdinaryDynamicLeaseWithMakeStatic(t *testing.T) {
	p := policy()
	p.StaticIP = "10.0.0.120"
	state := bindingState(Lease{ID: "*1", Address: "10.0.0.120", MACAddress: "AA:BB:CC:DD:EE:FF", Server: "dhcp-lan", Dynamic: "true", Disabled: "false"})
	plan, err := PlanDeviceBinding(p, state)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Operations) != 2 || plan.Operations[0].Method != http.MethodPost || plan.Operations[0].Path != "/rest/ip/dhcp-server/lease/make-static" || plan.Operations[1].Method != http.MethodPatch {
		t.Fatalf("plan=%+v", plan)
	}
	if plan.PreState.Digest != BindingStateDigest(state) || plan.PreState.Lease == nil || plan.PreState.Lease.Dynamic != "true" {
		t.Fatalf("preState=%+v", plan.PreState)
	}
	if plan.Operations[0].Rollback == nil || plan.Operations[0].Rollback.Method != http.MethodDelete || plan.Operations[1].Rollback == nil {
		t.Fatalf("rollback protocol missing: %+v", plan.Operations)
	}
}

func TestPlanRejectsUnknownStaticLeaseTakeover(t *testing.T) {
	_, err := PlanDeviceBinding(policy(), bindingState(Lease{ID: "*1", Address: "10.0.0.19", MACAddress: "AA:BB:CC:DD:EE:FF", Server: "dhcp-lan", Dynamic: "false", Comment: "manual"}))
	if !errors.Is(err, ErrPlanConflict) {
		t.Fatalf("err=%v", err)
	}
}

func TestPlanUpdatesOwnedLease(t *testing.T) {
	plan, err := PlanDeviceBinding(policy(), bindingState(Lease{ID: "*1", Address: "10.0.0.19", MACAddress: "AA:BB:CC:DD:EE:FF", Dynamic: "false", Server: "dhcp-lan", Comment: "foxos:device:phone"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Operations) != 1 || plan.Operations[0].Method != http.MethodPatch || !plan.RequiresConfirmation || plan.Operations[0].Rollback == nil {
		t.Fatalf("plan=%+v", plan)
	}
}

func TestPlanRejectsMovingOwnedStaticLeaseIntoDynamicPool(t *testing.T) {
	p := policy()
	p.StaticIP = "10.0.0.150"
	_, err := PlanDeviceBinding(p, bindingState(Lease{ID: "*1", Address: "10.0.0.19", MACAddress: "AA:BB:CC:DD:EE:FF", Dynamic: "false", Server: "dhcp-lan", Comment: "foxos:device:phone"}))
	if !errors.Is(err, ErrPlanConflict) {
		t.Fatalf("err=%v", err)
	}
}

func TestPlanRejectsLeaseAndARPConflicts(t *testing.T) {
	for _, test := range []struct {
		name  string
		state BindingState
	}{
		{name: "lease", state: bindingState(Lease{ID: "*2", Address: "10.0.0.20", MACAddress: "11:22:33:44:55:66"})},
		{name: "ARP", state: func() BindingState {
			state := bindingState()
			state.ARP = []ARP{{Address: "10.0.0.20", MACAddress: "11:22:33:44:55:66", Complete: "true"}}
			return state
		}()},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := PlanDeviceBinding(policy(), test.state); !errors.Is(err, ErrPlanConflict) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestPlanIsNoOpWhenAlreadyStatic(t *testing.T) {
	plan, err := PlanDeviceBinding(policy(), bindingState(Lease{ID: "*1", Address: "10.0.0.20", MACAddress: "AA:BB:CC:DD:EE:FF", Server: "dhcp-lan", Dynamic: "false", Comment: "foxos:device:phone"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Operations) != 0 || plan.RequiresConfirmation {
		t.Fatalf("plan=%+v", plan)
	}
}

func TestPlanValidatesNetworkSubnetAndReservedRange(t *testing.T) {
	for _, test := range []struct {
		name   string
		policy domain.DevicePolicy
		state  BindingState
	}{
		{name: "management address", policy: func() domain.DevicePolicy { value := policy(); value.StaticIP = "10.0.0.4"; return value }(), state: bindingState()},
		{name: "dynamic pool is not reservation space", policy: func() domain.DevicePolicy { value := policy(); value.StaticIP = "10.0.0.150"; return value }(), state: bindingState()},
		{name: "outside DHCP network", policy: func() domain.DevicePolicy { value := policy(); value.StaticIP = "10.0.1.20"; return value }(), state: bindingState()},
		{name: "RouterOS address", policy: func() domain.DevicePolicy { value := policy(); value.StaticIP = "10.0.0.1"; return value }(), state: bindingState()},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := PlanDeviceBinding(test.policy, test.state); !errors.Is(err, ErrPlanConflict) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestBindingStateDigestIgnoresLeaseObservationNoiseButDetectsManagedChange(t *testing.T) {
	state := bindingState(Lease{ID: "*1", Address: "10.0.0.20", MACAddress: "aa:bb:cc:dd:ee:ff", Server: "dhcp-lan", Dynamic: "true", LastSeen: "1s", Status: "bound"})
	changedNoise := state
	changedNoise.Leases = append([]Lease(nil), state.Leases...)
	changedNoise.Leases[0].LastSeen = "2s"
	changedNoise.Leases[0].Status = "waiting"
	if BindingStateDigest(state) != BindingStateDigest(changedNoise) {
		t.Fatal("observation-only fields made a confirmed plan stale")
	}
	changedAddress := state
	changedAddress.Leases = append([]Lease(nil), state.Leases...)
	changedAddress.Leases[0].Address = "10.0.0.21"
	if BindingStateDigest(state) == BindingStateDigest(changedAddress) {
		t.Fatal("managed lease change did not invalidate state digest")
	}
}
