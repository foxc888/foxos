package domain

import (
	"errors"
	"testing"
)

func TestDevicePolicyValidateProtectsManagementPlane(t *testing.T) {
	t.Parallel()
	for _, address := range []string{"10.0.0.1", "10.0.0.2", "10.0.0.3", "10.0.0.4"} {
		t.Run(address, func(t *testing.T) {
			t.Parallel()
			policy := DevicePolicy{ID: "device", MACAddress: "AA:BB:CC:DD:EE:FF", StaticIP: address, DHCPServer: "dhcp-lan", Egress: EgressDirect}
			if err := policy.Validate(); !errors.Is(err, ErrInvalidDevicePolicy) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestEqualDevicePoliciesNormalizesNetworkIdentifiers(t *testing.T) {
	t.Parallel()
	left := DevicePolicy{ID: "phone", Name: "Phone", MACAddress: "aa:bb:cc:dd:ee:ff", StaticIP: "192.168.1.20", DHCPServer: "dhcp-lan", Egress: EgressMihomoNode, TargetID: "node-a"}
	right := left
	right.MACAddress = "AA-BB-CC-DD-EE-FF"
	right.StaticIP = "192.168.001.020"
	if EqualDevicePolicies(left, right) {
		t.Fatal("invalid alternate IPv4 spelling must not compare equal")
	}
	right.StaticIP = left.StaticIP
	if !EqualDevicePolicies(left, right) {
		t.Fatalf("policies should be equal: left=%+v right=%+v", left, right)
	}
	right.TargetID = "node-b"
	if EqualDevicePolicies(left, right) {
		t.Fatal("changed target compared equal")
	}
}
