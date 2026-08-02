import type { DevicePolicy, EgressType } from "./api";
import type { Device } from "./data";

export function normalizeMac(value: string): string {
  return value.trim().replace(/-/g, ":").toLowerCase();
}

export function policyForDevice(policies: DevicePolicy[], device?: Device): DevicePolicy | undefined {
  if (!device) return undefined;
  const mac = normalizeMac(device.mac);
  return policies.find((policy) => normalizeMac(policy.macAddress) === mac);
}

export function egressLabel(egress?: EgressType): string {
  switch (egress) {
    case "direct": return "直连";
    case "mihomo-node": return "Mihomo 节点";
    case "proxy-chain": return "链式代理";
    case "l2tp": return "RouterOS L2TP";
    case "blocked": return "阻断";
    default: return "未设置";
  }
}

export function proposedDevicePolicy(device: Device, stored: DevicePolicy | undefined, egress: EgressType, targetId: string): DevicePolicy {
  return {
    id: stored?.id ?? `device-${device.id.toString(16)}`,
    name: device.name,
    macAddress: device.mac,
    staticIp: device.ip,
    dhcpServer: stored?.dhcpServer || device.dhcpServer || "",
    egress,
    ...(egress === "direct" || egress === "blocked" ? {} : { targetId: targetId.trim() }),
  };
}
