import { describe, expect, it } from "vitest";
import type { DevicePolicy } from "../api";
import type { Device } from "../data";
import { egressLabel, policyForDevice, proposedDevicePolicy } from "../policy-state";

const device: Device = {
  id: 31,
  name: "Laptop",
  kind: "computer",
  ip: "192.168.1.20",
  mac: "AA-BB-CC-DD-EE-FF",
  iface: "bridge-lan",
  dhcpServer: "dhcp-lan",
  tags: [],
  profileTracked: false,
  egress: "未设置",
  latency: null,
  online: true,
  fixed: true,
  lastSeen: "now",
};

describe("device policy state", () => {
  it("matches RouterOS devices to persisted policies using normalized MAC addresses", () => {
    const policy: DevicePolicy = { id: "laptop", name: "Laptop", macAddress: "aa:bb:cc:dd:ee:ff", staticIp: device.ip, dhcpServer: "dhcp-lan", egress: "blocked" };
    expect(policyForDevice([policy], device)).toBe(policy);
    expect(egressLabel(policy.egress)).toBe("阻断");
  });

  it("builds a proposed target policy without leaking stale target IDs into direct mode", () => {
    const stored: DevicePolicy = { id: "laptop", name: "Old", macAddress: device.mac, staticIp: device.ip, dhcpServer: "dhcp-main", egress: "mihomo-node", targetId: "node-old" };
    expect(proposedDevicePolicy(device, stored, "direct", "node-old")).toEqual({
      id: "laptop",
      name: "Laptop",
      macAddress: device.mac,
      staticIp: device.ip,
      dhcpServer: "dhcp-main",
      egress: "direct",
    });
  });
});
