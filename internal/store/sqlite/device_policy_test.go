package sqlite

import (
	"context"
	"testing"

	"github.com/foxc888/foxos/internal/domain"
)

func TestDevicePolicyLifecycle(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	policy := domain.DevicePolicy{ID: "phone", Name: "iPhone", MACAddress: "aa:bb:cc:dd:ee:ff", StaticIP: "10.0.0.20", DHCPServer: "dhcp-lan", Egress: domain.EgressDirect}
	if err := store.SaveDevicePolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	got, err := store.DevicePolicy(ctx, "phone")
	if err != nil || got.StaticIP != "10.0.0.20" {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	got.StaticIP = "10.0.0.21"
	if err := store.SaveDevicePolicy(ctx, got); err != nil {
		t.Fatal(err)
	}
	items, err := store.DevicePolicies(ctx)
	if err != nil || len(items) != 1 || items[0].StaticIP != "10.0.0.21" {
		t.Fatalf("items=%+v err=%v", items, err)
	}
	if err := store.DeleteDevicePolicy(ctx, "phone"); err != nil {
		t.Fatal(err)
	}
}
