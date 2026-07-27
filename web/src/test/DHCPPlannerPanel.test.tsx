import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { http, HttpResponse } from "msw";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { saveApiToken, type DHCPExpansionPlan, type DHCPServerCapacity } from "../api";
import { DHCPPlannerPanel } from "../components/DHCPPlannerPanel";
import { server } from "./setup";

const capacity: DHCPServerCapacity = {
  serverName: "dhcp-lan",
  interface: "bridge-lan",
  running: true,
  poolNames: ["lan-pool"],
  network: "10.0.0.0/24",
  gateway: "10.0.0.1",
  ranges: [{ start: "10.0.0.100", end: "10.0.0.200", capacity: 101 }],
  configuredCapacity: 101,
  excludedWithinPool: 1,
  dynamicCapacity: 100,
  dynamicOccupied: 75,
  remaining: 25,
  utilizationPercent: 75,
  reservationSpaceCapacity: 153,
  reservationUsed: 2,
  reservationRemaining: 151,
  risk: "warning",
  conflicts: [{ address: "10.0.0.120", kind: "reserved_address_in_pool", detail: "静态地址位于动态池内" }],
  ready: true,
};

describe("DHCPPlannerPanel", () => {
  beforeEach(() => saveApiToken("t".repeat(40)));

  it("uses an exact confirmed plan and reads capacity back after execution", async () => {
    let reads = 0;
    let previewBody: unknown;
    let executionBody: unknown;
    const after = { ...capacity, configuredCapacity: 151, dynamicCapacity: 150, remaining: 75, ranges: [{ start: "10.0.0.50", end: "10.0.0.200", capacity: 151 }] };
    const plan: DHCPExpansionPlan = {
      serverName: "dhcp-lan",
      poolId: "*10",
      poolName: "lan-pool",
      network: "10.0.0.0/24",
      currentRanges: "10.0.0.100-10.0.0.200",
      proposedRanges: "10.0.0.50-10.0.0.200",
      requestedCapacity: 150,
      suggestedRanges: [{ start: "10.0.0.50", end: "10.0.0.99", capacity: 50 }],
      before: capacity,
      after,
      alternatives: [],
      stateDigest: "d".repeat(64),
      operation: { method: "PATCH", path: "/rest/ip/pool/*10", summary: "扩展 FoxOS 自有 DHCP 地址池" },
      warnings: ["只修改 FoxOS 自有地址池"],
      executable: true,
      requiresConfirmation: true,
    };
    server.use(
      http.get("/api/v1/routeros/dhcp/address-plan", () => {
        reads += 1;
        return HttpResponse.json({ configured: true, plan: { stateDigest: "d".repeat(64), ready: true, servers: [reads > 1 ? after : capacity] } });
      }),
      http.post("/api/v1/routeros/plans/dhcp-expansion", async ({ request }) => {
        previewBody = await request.json();
        return HttpResponse.json({ plan, confirmationToken: "confirm-once", expiresInSeconds: 300 });
      }),
      http.post("/api/v1/routeros/plans/dhcp-expansion/execute", async ({ request }) => {
        executionBody = await request.json();
        return HttpResponse.json({ status: "SUCCEEDED", serverName: "dhcp-lan", poolName: "lan-pool", ranges: plan.proposedRanges, auditId: "audit-1" });
      }),
    );
    const notify = vi.fn();
    render(<DHCPPlannerPanel notify={notify} />);

    expect(await screen.findByText("容量偏高")).toBeInTheDocument();
    expect(screen.getByText(/静态地址位于动态池内/)).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("目标安全动态容量"), { target: { value: "150" } });
    fireEvent.change(screen.getByLabelText("完整拟议范围"), { target: { value: "10.0.0.50-10.0.0.200" } });
    fireEvent.click(screen.getByRole("button", { name: "生成精确计划" }));

    await waitFor(() => expect(previewBody).toEqual({ serverName: "dhcp-lan", requestedCapacity: 150, proposedRanges: "10.0.0.50-10.0.0.200" }));
    const dialog = await screen.findByRole("dialog", { name: "确认 DHCP 地址池扩容" });
    expect(within(dialog).getByText(/10\.0\.0\.100-10\.0\.0\.200 → 10\.0\.0\.50-10\.0\.0\.200/)).toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole("checkbox"));
    fireEvent.click(within(dialog).getByRole("button", { name: "确认扩展地址池" }));

    await waitFor(() => expect(executionBody).toEqual({ plan, confirmationToken: "confirm-once" }));
    await waitFor(() => expect(reads).toBe(2));
    expect(notify).toHaveBeenCalledWith("DHCP 地址池已执行、回读并写入审计");
  });

  it("shows non-executable network alternatives without a confirmation dialog", async () => {
    server.use(
      http.get("/api/v1/routeros/dhcp/address-plan", () => HttpResponse.json({ configured: true, plan: { stateDigest: "d".repeat(64), ready: true, servers: [capacity] } })),
      http.post("/api/v1/routeros/plans/dhcp-expansion", () => HttpResponse.json({ plan: { serverName: "dhcp-lan", network: "10.0.0.0/24", requestedCapacity: 300, suggestedRanges: [], before: capacity, alternatives: [{ strategy: "expand-to-/23", executable: false, capacity: 510, impact: ["需要修改接口前缀"] }, { strategy: "split-vlan", executable: false, capacity: 300, impact: ["需要新增 VLAN"] }], stateDigest: "d".repeat(64), warnings: ["不会自动改掩码"], executable: false, requiresConfirmation: false }, confirmationToken: "", expiresInSeconds: 300 })),
    );
    render(<DHCPPlannerPanel notify={vi.fn()} />);
    await screen.findByText("容量偏高");
    fireEvent.change(screen.getByLabelText("目标安全动态容量"), { target: { value: "300" } });
    fireEvent.click(screen.getByRole("button", { name: "生成精确计划" }));
    expect(await screen.findByText("扩大至 /23 · 510")).toBeInTheDocument();
    expect(screen.getByText("VLAN 拆分 · 300")).toBeInTheDocument();
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });
});
