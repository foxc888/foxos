package domain

import "testing"

func TestDevicePolicyValidationIsSiteAgnostic(t *testing.T) {
	t.Parallel()
	policy := DevicePolicy{ID: "device", MACAddress: "AA:BB:CC:DD:EE:FF", StaticIP: "192.168.50.1", DHCPServer: "dhcp-lan", Egress: EgressDirect}
	if err := policy.Validate(); err != nil {
		t.Fatalf("site-specific management protection belongs to the execution planner: %v", err)
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
