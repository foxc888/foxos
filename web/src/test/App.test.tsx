import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { http, HttpResponse } from "msw";
import { beforeEach, describe, expect, it } from "vitest";
import App from "../App";
import { saveApiToken, type DevicePolicy, type EgressPlan } from "../api";
import { server } from "./setup";

function appHandlers({ mihomoStatus = 200, dynamic = true }: { mihomoStatus?: number; dynamic?: boolean } = {}) {
  return [
    http.get("/api/v1/routeros/overview", () => HttpResponse.json({
      configured: true,
      online: true,
      resource: { "cpu-load": "12", version: "7.20" },
      interfaces: [{ ".id": "*1", name: "bridge-lan", type: "bridge", running: "true", disabled: "false" }],
      devices: [
        { macAddress: "AA:BB:CC:DD:EE:FF", address: "10.0.0.20", hostName: "test-laptop", interface: "bridge-lan", dhcpServer: "dhcp-lan", status: "bound", dynamic },
        { macAddress: "AA:BB:CC:DD:EE:00", address: "10.0.0.21", hostName: "test-phone", interface: "bridge-lan", dhcpServer: "dhcp-lan", status: "bound", dynamic },
      ],
    })),
    http.get("/api/v1/mihomo/overview", () => mihomoStatus === 200
      ? HttpResponse.json({ configured: true, online: true, version: "1.19.0", proxies: { "Node A": { name: "Node A", type: "VLESS", history: [{ time: "2026-07-27T00:00:00Z", delay: 42 }] }, GLOBAL: { name: "GLOBAL", type: "Selector", now: "Node A", all: ["Node A"] } }, connections: [{ id: "connection-a" }], traffic: { uploadTotal: 1024, downloadTotal: 2048, connectionCount: 1 } })
      : HttpResponse.json({ message: "controller down" }, { status: mihomoStatus })),
    http.get("/api/v1/mosdns/overview", () => HttpResponse.json({ configured: true, online: true, address: "10.0.0.3" })),
    http.get("/api/v1/nodes", () => HttpResponse.json([
      { id: "node-a", name: "Node A", type: "vless", server: "node-a.invalid", port: 443, hasCredential: true },
      { id: "node-b", name: "Node B", type: "vless", server: "node-b.invalid", port: 443, hasCredential: true },
    ])),
    http.get("/api/v1/routeros/l2tp", () => HttpResponse.json([{ id: "*1", name: "office-l2tp", connectTo: "vpn.example.invalid", running: true, disabled: false }])),
    http.get("/api/v1/routeros/routes", () => HttpResponse.json([{ ".id": "*r1", "dst-address": "0.0.0.0/0", gateway: "wan-gateway", distance: "1", active: "true", disabled: "false" }])),
    http.get("/api/v1/routeros/dhcp-servers", () => HttpResponse.json([{ ".id": "*d1", name: "dhcp-lan", interface: "bridge-lan", "address-pool": "lan-pool", running: "true", disabled: "false" }])),
    http.get("/api/v1/routeros/dhcp/address-plan", () => HttpResponse.json({ configured: true, plan: { stateDigest: "d".repeat(64), ready: true, servers: [{ serverName: "dhcp-lan", interface: "bridge-lan", running: true, poolNames: ["lan-pool"], network: "10.0.0.0/24", gateway: "10.0.0.1", ranges: [{ start: "10.0.0.100", end: "10.0.0.200", capacity: 101 }], configuredCapacity: 101, excludedWithinPool: 0, dynamicCapacity: 101, dynamicOccupied: 2, remaining: 99, utilizationPercent: 1.98, reservationSpaceCapacity: 153, reservationUsed: 0, reservationRemaining: 153, risk: "normal", conflicts: [], ready: true }] } })),
    http.get("/api/v1/routeros/containers", () => HttpResponse.json([{ ".id": "*c1", name: "foxos", comment: "foxos:active", status: "running", "root-dir": "disk1/foxos", interface: "veth-foxos", "start-on-boot": "true" }])),
    http.get("/api/v1/devices", () => HttpResponse.json({ sourceAvailable: true, observedAt: "2026-07-27T00:00:00Z", devices: [
      { macAddress: "AA:BB:CC:DD:EE:FF", alias: "", tags: [], vendor: "Framework", hostName: "test-laptop", ipAddress: "10.0.0.20", interface: "bridge-lan", dhcpServer: "dhcp-lan", online: true, lastKnownOnline: true, status: "online", firstSeen: "2026-07-26T00:00:00Z", lastSeen: "2026-07-27T00:00:00Z", updatedAt: "2026-07-27T00:00:00Z" },
      { macAddress: "AA:BB:CC:DD:EE:00", alias: "", tags: [], vendor: "", hostName: "test-phone", ipAddress: "10.0.0.21", interface: "bridge-lan", dhcpServer: "dhcp-lan", online: true, lastKnownOnline: true, status: "online", firstSeen: "2026-07-26T00:00:00Z", lastSeen: "2026-07-27T00:00:00Z", updatedAt: "2026-07-27T00:00:00Z" },
    ] })),
    http.get("/api/v1/devices/:mac/history", () => HttpResponse.json([{ id: 1, online: true, observedAt: "2026-07-27T00:00:00Z" }])),
    http.get("/api/v1/device-policies", () => HttpResponse.json([])),
    http.get("/api/v1/proxy-groups", () => HttpResponse.json([])),
    http.get("/api/v1/audit-events", () => HttpResponse.json([])),
    http.get("/api/v1/egress/capabilities", () => HttpResponse.json({ routerosConfigured: true, routerosOnline: true, modes: [{ mode: "direct", available: true, experimental: false, missing: [] }, { mode: "blocked", available: true, experimental: false, missing: [] }, { mode: "mihomo-node", available: false, experimental: true, missing: ["transparent_ingress_unverified"] }, { mode: "proxy-chain", available: false, experimental: true, missing: ["transparent_ingress_unverified"] }, { mode: "l2tp", available: false, experimental: true, missing: ["per_policy_l2tp_fib_table_missing"] }] })),
    http.get("/api/v1/jobs", () => HttpResponse.json([])),
    http.get("/api/v1/mihomo/draft", () => HttpResponse.json({ id: "active", mode: "rule", mixedPort: 7890, allowLan: false, rules: ["MATCH,DIRECT"], revision: 1 })),
    http.get("/api/v1/mihomo/snapshots", () => HttpResponse.json([])),
    http.get("/api/v1/subscriptions", () => HttpResponse.json([])),
    http.get("/api/v1/alerts", () => HttpResponse.json([])),
    http.get("/api/v1/backups", () => HttpResponse.json([])),
  ];
}

describe("App", () => {
  beforeEach(() => {
    window.history.replaceState(null, "", "#overview");
    saveApiToken("t".repeat(40));
  });

  it("keeps successful services live when Mihomo fails", async () => {
    server.use(...appHandlers({ mihomoStatus: 503 }));
    render(<App />);

    const routerCard = screen.getByText("RouterOS", { selector: ".service-summary strong" }).closest<HTMLElement>(".service-summary");
    const mihomoCard = screen.getByText("Mihomo", { selector: ".service-summary strong" }).closest<HTMLElement>(".service-summary");
    expect(routerCard).not.toBeNull();
    expect(mihomoCard).not.toBeNull();
    await waitFor(() => expect(within(routerCard!).getByText("在线")).toBeInTheDocument());
    expect(within(mihomoCard!).getByText("不可用")).toBeInTheDocument();
    expect(await screen.findByRole("heading", { level: 2, name: "地址容量" })).toBeInTheDocument();
    expect(screen.getByText("dhcp-lan")).toBeInTheDocument();
  });

  it("marks a successfully loaded non-service resource as live", async () => {
    server.use(...appHandlers());
    render(<App />);

    const heading = await screen.findByRole("heading", { level: 2, name: "地址容量" });
    const panel = heading.closest<HTMLElement>("section");
    expect(panel).not.toBeNull();
    await waitFor(() => expect(within(panel!).getByText("实时")).toBeInTheDocument());
  });

  it("initialises from a deep link and responds to history navigation", async () => {
    server.use(...appHandlers());
    window.history.replaceState(null, "", "#settings");
    render(<App />);
    expect(screen.getByRole("heading", { level: 1, name: "设置" })).toBeInTheDocument();

    window.history.pushState(null, "", "#routeros");
    fireEvent.popState(window);
    expect(await screen.findByRole("heading", { level: 1, name: "网络与地址" })).toBeInTheDocument();
    await waitFor(() => expect(screen.getByRole("main")).toHaveFocus());
  });

  it("waits for a container command and reads RouterOS state back", async () => {
    let status = "running";
    let commandCalls = 0;
    server.use(...appHandlers());
    server.use(
      http.get("/api/v1/routeros/containers", () => HttpResponse.json([{ ".id": "*c1", name: "foxos", comment: "foxos:active", status, "root-dir": "disk1/foxos", interface: "veth-foxos", "start-on-boot": "true" }])),
      http.post("/api/v1/routeros/containers/:id/commands/:command", async ({ params, request }) => {
        commandCalls += 1;
        expect(params).toEqual({ id: "*c1", command: "stop" });
        const body = await request.json() as { owner: string; idempotencyKey: string };
        expect(body.owner).toBe("foxos:active");
        expect(body.idempotencyKey).toMatch(/^container-stop-/);
        return HttpResponse.json({ status: "QUEUED", job: { id: "job-container-stop", kind: "routeros.container-command", status: "QUEUED", progress: 0, attempts: 0, createdAt: "2026-07-27T00:00:00Z", updatedAt: "2026-07-27T00:00:00Z" } }, { status: 202 });
      }),
      http.get("/api/v1/jobs/job-container-stop", () => {
        status = "stopped";
        return HttpResponse.json({ id: "job-container-stop", kind: "routeros.container-command", status: "SUCCEEDED", progress: 100, attempts: 1, result: { status: "stopped" }, createdAt: "2026-07-27T00:00:00Z", updatedAt: "2026-07-27T00:00:01Z" });
      }),
    );
    window.history.replaceState(null, "", "#network");
    render(<App />);

    const stop = await screen.findByRole("button", { name: "停止" });
    expect(stop).toBeEnabled();
    fireEvent.click(stop);
    fireEvent.click(stop);
    expect(await screen.findByText(/已停止，RouterOS 状态已回读/)).toBeInTheDocument();
    expect(commandCalls).toBe(1);
    await waitFor(() => expect(screen.getByRole("button", { name: "启动" })).toBeEnabled());
    expect(screen.getByText("已启用")).toBeInTheDocument();
  });

  it("does not present a running RouterOS L2TP session as a TCP probe", async () => {
    server.use(...appHandlers());
    window.history.replaceState(null, "", "#proxies");
    render(<App />);

    expect(await screen.findByText("L2TP 会话运行")).toBeInTheDocument();
    expect(screen.queryByText("TCP 可达")).not.toBeInTheDocument();
  });

  it("shows Mihomo runtime traffic, selector and delay separately from TCP probes", async () => {
    server.use(...appHandlers());
    window.history.replaceState(null, "", "#proxies");
    render(<App />);

    expect(await screen.findByText("2.0 KiB")).toBeInTheDocument();
    expect(screen.getAllByText("GLOBAL").length).toBeGreaterThan(0);
    expect(screen.getAllByText("Node A").length).toBeGreaterThan(1);
    expect(screen.getAllByText("42 ms").length).toBeGreaterThan(0);
    expect(screen.getAllByText("未检测", { selector: "td" }).length).toBeGreaterThan(0);
  });

  it("keeps node HTTP and current-policy exit results separate from TCP", async () => {
    server.use(
      ...appHandlers(),
      http.post("/api/v1/mihomo/probes/node-a", () => HttpResponse.json({
        nodeId: "node-a",
        nodeName: "Node A",
        nodeHttp: { available: true, success: true, latencyMs: 64 },
        exit: { available: true, success: true, latencyMs: 81, scope: "current-policy", ipAddress: "1.1.1.1" },
      })),
    );
    window.history.replaceState(null, "", "#proxies");
    render(<App />);

    fireEvent.click(await screen.findByRole("button", { name: "代理 HTTP / 出口" }));
    expect(await screen.findByText("64 ms")).toBeInTheDocument();
    expect(screen.getByText("1.1.1.1（当前策略）")).toBeInTheDocument();
    expect(screen.getAllByText("未单独检测", { selector: "dd" })).toHaveLength(2);
  });

  it("uses arrow keys to move and select device grid rows", async () => {
    server.use(...appHandlers());
    window.history.replaceState(null, "", "#devices");
    render(<App />);

    await screen.findByText("test-phone");
    const grid = screen.getByRole("grid", { name: "RouterOS 设备" });
    const rows = Array.from(grid.querySelectorAll<HTMLTableRowElement>("tbody tr"));
    expect(rows).toHaveLength(2);
    rows[0].focus();
    fireEvent.keyDown(rows[0], { key: "ArrowDown" });
    expect(rows[1]).toHaveFocus();
    expect(rows[1]).toHaveAttribute("aria-selected", "true");
  });

  it("persists device metadata through the audited API before reporting success", async () => {
    let savedBody: unknown;
    server.use(
      ...appHandlers(),
      http.put("/api/v1/devices/:mac", async ({ request }) => {
        savedBody = await request.json();
        return HttpResponse.json({ macAddress: "AA:BB:CC:DD:EE:FF", alias: "Office laptop", tags: ["work", "trusted"], vendor: "Framework", hostName: "test-laptop", online: false, lastKnownOnline: true, status: "unavailable", firstSeen: "2026-07-26T00:00:00Z", lastSeen: "2026-07-27T00:00:00Z", updatedAt: "2026-07-27T00:00:01Z" });
      }),
    );
    window.history.replaceState(null, "", "#devices");
    render(<App />);

    const alias = await screen.findByRole("textbox", { name: "别名" });
    fireEvent.change(alias, { target: { value: "Office laptop" } });
    fireEvent.change(screen.getByRole("textbox", { name: "标签" }), { target: { value: "work, trusted" } });
    fireEvent.click(screen.getByRole("button", { name: "保存画像" }));
    await waitFor(() => expect(savedBody).toEqual({ alias: "Office laptop", vendor: "Framework", tags: ["work", "trusted"] }));
    expect(await screen.findByText(/设备画像已保存到 SQLite 并写入审计；RouterOS 未修改/)).toBeInTheDocument();
  });

  it("confirms and waits for a database-only direct egress task", async () => {
    let executeCalls = 0;
    server.use(
      ...appHandlers({ dynamic: false }),
      http.post("/api/v1/routeros/plans/egress/:id", async ({ request }) => {
        const policy = await request.json() as DevicePolicy;
        const plan: EgressPlan = {
          policyId: policy.id,
          staticIp: policy.staticIp,
          egress: policy.egress,
          policy,
          stateDigest: "d".repeat(64),
          operations: [],
          warnings: ["成功回读 RouterOS 后才会持久化目标设备策略"],
          requiresConfirmation: true,
        };
        return HttpResponse.json({ plan, confirmationToken: "confirm-egress", expiresInSeconds: 300 });
      }),
      http.post("/api/v1/routeros/plans/egress/:id/execute", () => {
        executeCalls += 1;
        return HttpResponse.json({ status: "QUEUED", job: { id: "job-egress", kind: "routeros.egress", status: "QUEUED", progress: 0, attempts: 0, createdAt: "2026-07-27T00:00:00Z", updatedAt: "2026-07-27T00:00:00Z" } }, { status: 202 });
      }),
      http.get("/api/v1/jobs/job-egress", () => HttpResponse.json({ id: "job-egress", kind: "routeros.egress", status: "SUCCEEDED", progress: 100, attempts: 1, createdAt: "2026-07-27T00:00:00Z", updatedAt: "2026-07-27T00:00:01Z" })),
    );
    window.history.replaceState(null, "", "#devices");
    render(<App />);

    const prepare = await screen.findByRole("button", { name: "生成应用计划" });
    await waitFor(() => expect(prepare).toBeEnabled());
    fireEvent.click(prepare);
    const dialog = await screen.findByRole("dialog", { name: "确认设备出口变更" });
    expect(within(dialog).getByText("RouterOS：无需变更；只在任务验证后持久化策略")).toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole("checkbox"));
    fireEvent.click(within(dialog).getByRole("button", { name: "确认应用出口策略" }));
    await waitFor(() => expect(executeCalls).toBe(1));
    expect(await screen.findByText(/出口策略已执行、回读并持久化/)).toBeInTheDocument();
  });

  it("persists two ordered chain nodes without publishing Mihomo", async () => {
    let savedBody: unknown;
    let publishCalls = 0;
    server.use(
      ...appHandlers(),
      http.post("/api/v1/proxy-groups", async ({ request }) => {
        savedBody = await request.json();
        return HttpResponse.json({ id: "chain-a", ...(savedBody as object) }, { status: 201 });
      }),
      http.post("/api/v1/mihomo/config/apply", () => {
        publishCalls += 1;
        return HttpResponse.json({ status: "SUCCEEDED" });
      }),
    );
    window.history.replaceState(null, "", "#proxies");
    render(<App />);

    const nodePicker = await screen.findByRole("combobox", { name: "添加链路节点" });
    await waitFor(() => expect(nodePicker).toBeEnabled());
    fireEvent.change(nodePicker, { target: { value: "node-a" } });
    fireEvent.change(nodePicker, { target: { value: "node-b" } });
    const chainPanel = screen.getByRole("heading", { name: "链式代理组" }).closest<HTMLElement>("section");
    expect(chainPanel).not.toBeNull();
    fireEvent.change(within(chainPanel!).getByRole("textbox", { name: "名称" }), { target: { value: "工作出口链" } });
    fireEvent.click(screen.getByRole("button", { name: "保存组" }));
    await waitFor(() => expect(savedBody).toMatchObject({ name: "工作出口链", type: "chain", nodeIds: ["node-a", "node-b"] }));
    expect(publishCalls).toBe(0);
    expect(await screen.findByText(/已保存到 SQLite；Mihomo 运行配置尚未改变/)).toBeInTheDocument();
  });
});
