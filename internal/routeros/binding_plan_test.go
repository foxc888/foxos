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

func TestPlanRejectsUnownedDynamicLease(t *testing.T) {
	_, err := PlanDeviceBinding(policy(), []Lease{{ID: "*1", Address: "10.0.0.19", MACAddress: "AA:BB:CC:DD:EE:FF", Dynamic: "true"}})
	if !errors.Is(err, ErrPlanConflict) {
		t.Fatalf("err=%v", err)
	}
}
func TestPlanUpdatesOwnedLease(t *testing.T) {
	plan, err := PlanDeviceBinding(policy(), []Lease{{ID: "*1", Address: "10.0.0.19", MACAddress: "AA:BB:CC:DD:EE:FF", Dynamic: "false", Server: "dhcp-lan", Comment: "foxos:device:phone"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Operations) != 1 || plan.Operations[0].Method != http.MethodPatch || !plan.RequiresConfirmation || plan.Operations[0].Rollback == nil {
		t.Fatalf("plan=%+v", plan)
	}
}
func TestPlanRejectsIPConflict(t *testing.T) {
	_, err := PlanDeviceBinding(policy(), []Lease{{ID: "*2", Address: "10.0.0.20", MACAddress: "11:22:33:44:55:66"}})
	if !errors.Is(err, ErrPlanConflict) {
		t.Fatalf("err=%v", err)
	}
}
func TestPlanIsNoOpWhenAlreadyStatic(t *testing.T) {
	plan, err := PlanDeviceBinding(policy(), []Lease{{ID: "*1", Address: "10.0.0.20", MACAddress: "AA:BB:CC:DD:EE:FF", Dynamic: "false", Comment: "foxos:device:phone"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Operations) != 0 || plan.RequiresConfirmation {
		t.Fatalf("plan=%+v", plan)
	}
}
func TestPlanRejectsManagementAddress(t *testing.T) {
	p := policy()
	p.StaticIP = "10.0.0.4"
	_, err := PlanDeviceBinding(p, nil)
	if !errors.Is(err, ErrPlanConflict) {
		t.Fatalf("err=%v", err)
	}
}
