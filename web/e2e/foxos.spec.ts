import axe from "axe-core";
import { type Page, type Route } from "@playwright/test";
import { expect, test } from "./fixtures";

type MockOptions = {
  routerosFailure?: boolean;
  routesFailure?: boolean;
  mihomoProbeFailure?: boolean;
  mihomoRollback?: boolean;
};

type MockCalls = {
  containerCommands: unknown[];
  deviceMetadata: unknown[];
  egressExecutions: unknown[];
  mihomoApply: number;
  resourceReads: Record<string, number>;
};

const observedAt = "2026-07-27T00:00:00Z";

const node = {
  id: "node-test",
  name: "测试节点",
  type: "vless",
  server: "node.example.invalid",
  port: 443,
  hasCredential: true,
};

async function json(route: Route, body: unknown, status = 200) {
  await route.fulfill({
    status,
    contentType: "application/json; charset=utf-8",
    body: JSON.stringify(body),
  });
}

async function mockApi(page: Page, options: MockOptions = {}) {
  let jobReads = 0;
  let containerStatus = "running";
  let containerCommand = "";
  let storedPolicy: Record<string, unknown> | undefined;
  const calls: MockCalls = { containerCommands: [], deviceMetadata: [], egressExecutions: [], mihomoApply: 0, resourceReads: {} };
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname;
    if (request.method() === "GET") calls.resourceReads[path] = (calls.resourceReads[path] ?? 0) + 1;
    if (path === "/api/v1/site") {
      await json(route, {
        managementBridge: "bridge-lan",
        storageRoot: "disk1",
        network: "10.0.0.0/24",
        publicHostname: "foxos.home.arpa",
        publicUrl: "https://foxos.home.arpa",
        httpRedirectUrl: "http://10.0.0.4",
        protectedAddresses: ["10.0.0.1", "10.0.0.2", "10.0.0.3", "10.0.0.4"],
        services: {
          routeros: { address: "10.0.0.1", port: 80, url: "http://10.0.0.1:80" },
          mihomo: { address: "10.0.0.2", port: 9090, url: "http://10.0.0.2:9090" },
          mosdns: { address: "10.0.0.3", port: 53, url: "http://10.0.0.3:53" },
          foxos: { address: "10.0.0.4", port: 443, url: "https://foxos.home.arpa" },
        },
        https: { enabled: true, caSha256: "a".repeat(64), caDownloadPath: "/api/v1/site/ca", trustRequired: true },
      });
      return;
    }

    if (path === "/api/v1/routeros/overview" && options.routerosFailure) {
      await json(route, { configured: true, online: false, error: "routeros_unavailable" }, 503);
      return;
    }
    if (path === "/api/v1/routeros/overview") {
      await json(route, {
        configured: true,
        online: true,
        resource: { "cpu-load": "12", "free-hdd-space": "1000000000", "total-hdd-space": "2000000000" },
        interfaces: [{ name: "bridge-lan", running: "true" }],
        devices: [
          { macAddress: "AA:BB:CC:DD:EE:01", address: "10.0.0.20", hostName: "workstation", interface: "bridge-lan", dhcpServer: "dhcp-lan", status: "bound", dynamic: false },
          { macAddress: "AA:BB:CC:DD:EE:02", address: "10.0.0.21", hostName: "phone", interface: "bridge-lan", dhcpServer: "dhcp-lan", status: "bound", dynamic: true },
        ],
      });
      return;
    }
    if (path === "/api/v1/mihomo/overview") {
      await json(route, {
        configured: true,
        online: true,
        version: "test",
        proxies: {
          "测试节点": { name: "测试节点", type: "VLESS", alive: true, history: [{ time: observedAt, delay: 37 }] },
          GLOBAL: { name: "GLOBAL", type: "Selector", now: "测试节点", all: ["测试节点"] },
        },
        connections: [{ id: "connection-test" }],
        traffic: { uploadTotal: 1024, downloadTotal: 2048, connectionCount: 1 },
      });
      return;
    }
    if (path === "/api/v1/mosdns/overview") {
      await json(route, { configured: true, online: true, address: "10.0.0.3:53" });
      return;
    }
    if (path === "/api/v1/nodes" && request.method() === "GET") {
      await json(route, [node]);
      return;
    }
    if (path === "/api/v1/nodes" && request.method() === "POST") {
      await json(route, node, 201);
      return;
    }
    if (path === "/api/v1/routeros/l2tp") {
      await json(route, [{ id: "*1", name: "office-l2tp", connectTo: "vpn.example.invalid", running: true, disabled: false }]);
      return;
    }
    if (path === "/api/v1/routeros/routes" && options.routesFailure) {
      await json(route, { error: "routeros_routes_unavailable" }, 503);
      return;
    }
    if (path === "/api/v1/routeros/routes") {
      await json(route, [{ ".id": "*r1", "dst-address": "0.0.0.0/0", gateway: "wan-gateway", distance: "1", active: "true", disabled: "false" }]);
      return;
    }
    if (path === "/api/v1/routeros/dhcp-servers") {
      await json(route, [{ ".id": "*d1", name: "dhcp-lan", interface: "bridge-lan", "address-pool": "lan-pool", running: "true", disabled: "false" }]);
      return;
    }
    if (path === "/api/v1/routeros/dhcp/address-plan") {
      await json(route, { configured: true, plan: { stateDigest: "d".repeat(64), ready: true, servers: [{ serverName: "dhcp-lan", interface: "bridge-lan", running: true, poolNames: ["lan-pool"], network: "10.0.0.0/24", gateway: "10.0.0.1", ranges: [{ start: "10.0.0.100", end: "10.0.0.200", capacity: 101 }], configuredCapacity: 101, excludedWithinPool: 0, dynamicCapacity: 101, dynamicOccupied: 2, remaining: 99, utilizationPercent: 1.98, reservationSpaceCapacity: 153, reservationUsed: 0, reservationRemaining: 153, risk: "normal", conflicts: [], ready: true }] } });
      return;
    }
    if (path === "/api/v1/routeros/containers") {
      await json(route, [{ ".id": "*c1", name: "foxos", comment: "foxos:active", status: containerStatus, "root-dir": "disk1/foxos", interface: "veth-foxos", "start-on-boot": "true" }]);
      return;
    }
    if (path.startsWith("/api/v1/routeros/containers/") && path.includes("/commands/") && request.method() === "POST") {
      containerCommand = path.split("/").at(-1) ?? "";
      calls.containerCommands.push(request.postDataJSON());
      await json(route, { status: "QUEUED", job: { id: "job-container-command", kind: "routeros.container-command", status: "QUEUED", progress: 0, attempts: 0, createdAt: observedAt, updatedAt: observedAt } }, 202);
      return;
    }
    if (path === "/api/v1/jobs/job-container-command") {
      containerStatus = containerCommand === "stop" ? "stopped" : "running";
      await json(route, { id: "job-container-command", kind: "routeros.container-command", status: "SUCCEEDED", progress: 100, attempts: 1, result: { status: containerStatus }, createdAt: observedAt, updatedAt: "2026-07-27T00:00:01Z" });
      return;
    }
    if (path === "/api/v1/devices" && request.method() === "GET") {
      await json(route, {
        sourceAvailable: true,
        observedAt,
        devices: [
          { macAddress: "AA:BB:CC:DD:EE:01", alias: "", tags: ["trusted"], vendor: "Fox Labs", hostName: "workstation", ipAddress: "10.0.0.20", interface: "bridge-lan", dhcpServer: "dhcp-lan", online: true, lastKnownOnline: true, status: "online", firstSeen: "2026-07-26T00:00:00Z", lastSeen: observedAt, updatedAt: observedAt },
          { macAddress: "AA:BB:CC:DD:EE:02", alias: "", tags: [], vendor: "", hostName: "phone", ipAddress: "10.0.0.21", interface: "bridge-lan", dhcpServer: "dhcp-lan", online: true, lastKnownOnline: true, status: "online", firstSeen: "2026-07-26T00:00:00Z", lastSeen: observedAt, updatedAt: observedAt },
        ],
      });
      return;
    }
    if (path.startsWith("/api/v1/devices/") && path.endsWith("/history") && request.method() === "GET") {
      await json(route, [
        { id: 2, online: true, observedAt },
        { id: 1, online: false, observedAt: "2026-07-26T23:30:00Z" },
      ]);
      return;
    }
    if (path.startsWith("/api/v1/devices/") && request.method() === "PUT") {
      const body = request.postDataJSON() as { alias?: string; tags?: string[]; vendor?: string };
      calls.deviceMetadata.push(body);
      await json(route, { macAddress: "AA:BB:CC:DD:EE:01", alias: body.alias ?? "", tags: body.tags ?? [], vendor: body.vendor ?? "", hostName: "workstation", online: false, lastKnownOnline: true, status: "unavailable", firstSeen: "2026-07-26T00:00:00Z", lastSeen: observedAt, updatedAt: "2026-07-27T00:00:01Z" });
      return;
    }
    if (path === "/api/v1/device-policies" && request.method() === "GET") {
      await json(route, storedPolicy ? [storedPolicy] : []);
      return;
    }
    if (path === "/api/v1/proxy-groups" && request.method() === "GET") {
      await json(route, []);
      return;
    }
    if (path === "/api/v1/audit-events") {
      await json(route, []);
      return;
    }
    if (path === "/api/v1/egress/capabilities") {
      await json(route, { routerosConfigured: true, routerosOnline: true, modes: [{ mode: "direct", available: true, experimental: false, missing: [] }, { mode: "blocked", available: true, experimental: false, missing: [] }, { mode: "mihomo-node", available: false, experimental: true, missing: ["transparent_ingress_unverified"] }, { mode: "proxy-chain", available: false, experimental: true, missing: ["transparent_ingress_unverified"] }, { mode: "l2tp", available: false, experimental: true, missing: ["per_policy_l2tp_fib_table_missing"] }] });
      return;
    }
    if (path === "/api/v1/jobs" && request.method() === "GET") {
      await json(route, []);
      return;
    }
    if (path === "/api/v1/mihomo/draft" && request.method() === "GET") {
      await json(route, { id: "active", mode: "rule", mixedPort: 7890, allowLan: false, rules: ["MATCH,DIRECT"], revision: 1 });
      return;
    }
    if (path === "/api/v1/mihomo/draft" && request.method() === "PUT") {
      await json(route, { id: "active", mode: "rule", mixedPort: 7890, allowLan: false, rules: ["MATCH,DIRECT"], revision: 2 });
      return;
    }
    if (path === "/api/v1/mihomo/snapshots") {
      await json(route, [{ id: "snapshot-test", digest: "a".repeat(64), label: "发布前快照", createdAt: observedAt }]);
      return;
    }
    if (path === "/api/v1/mihomo/snapshots/snapshot-test/restore/plan") {
      await json(route, { plan: { action: "mihomo.restore", snapshotId: "snapshot-test" }, confirmationToken: "restore-token", expiresInSeconds: 300 });
      return;
    }
    if (path === "/api/v1/mihomo/snapshots/snapshot-test/restore") {
      await json(route, { status: "QUEUED", job: { id: "job-restore", kind: "mihomo.restore", status: "QUEUED", progress: 0, attempts: 0, createdAt: observedAt, updatedAt: observedAt } }, 202);
      return;
    }
    if (path === "/api/v1/mihomo/config/preview") {
      await json(route, {
        preview: {
          draft: { id: "active", mode: "rule", mixedPort: 7890, allowLan: false, rules: ["MATCH,DIRECT"], revision: 1 },
          digest: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
          yaml: "mode: rule\nrules:\n  - MATCH,DIRECT\n",
          diff: "+ rules: MATCH,DIRECT",
          hasSecret: false,
        },
        confirmationToken: "preview-token",
        expiresInSeconds: 300,
      });
      return;
    }
    if (path === "/api/v1/mihomo/config/apply") {
      calls.mihomoApply += 1;
      await json(route, {
        status: "QUEUED",
        job: {
          id: "job-mihomo",
          kind: "mihomo.apply",
          status: "QUEUED",
          progress: 0,
          attempts: 0,
          createdAt: new Date().toISOString(),
          updatedAt: new Date().toISOString(),
        },
      }, 202);
      return;
    }
    if (path === "/api/v1/jobs/job-mihomo") {
      jobReads += 1;
      await json(route, {
        id: "job-mihomo",
        kind: "mihomo.apply",
        status: options.mihomoRollback && jobReads > 0 ? "ROLLED_BACK" : "SUCCEEDED",
        progress: 100,
        result: { rolledBack: Boolean(options.mihomoRollback) },
        attempts: 1,
        createdAt: new Date().toISOString(),
        updatedAt: new Date().toISOString(),
      });
      return;
    }
    if (path === "/api/v1/jobs/job-restore") {
      await json(route, { id: "job-restore", kind: "mihomo.restore", status: "SUCCEEDED", progress: 100, attempts: 1, createdAt: observedAt, updatedAt: "2026-07-27T00:00:01Z" });
      return;
    }
    if (path.startsWith("/api/v1/routeros/plans/egress/") && path.endsWith("/execute")) {
      const body = request.postDataJSON() as { plan?: { policy?: Record<string, unknown> } };
      calls.egressExecutions.push(body);
      storedPolicy = body.plan?.policy;
      await json(route, { status: "QUEUED", job: { id: "job-egress", kind: "routeros.egress", status: "QUEUED", progress: 0, attempts: 0, createdAt: observedAt, updatedAt: observedAt } }, 202);
      return;
    }
    if (path.startsWith("/api/v1/routeros/plans/egress/") && request.method() === "POST") {
      const policy = request.postDataJSON() as Record<string, unknown>;
      const id = String(policy.id);
      await json(route, {
        plan: {
          policyId: id,
          staticIp: policy.staticIp,
          egress: policy.egress,
          targetId: policy.targetId,
          policy,
          stateDigest: "d".repeat(64),
          operations: [{ method: "POST", path: "/rest/ip/firewall/filter", summary: "拒绝设备转发流量", ownedComment: `foxos:egress:${id}` }],
          warnings: ["只修改带 foxos: 所有权标识的资源", "成功回读 RouterOS 后才会持久化目标设备策略"],
          requiresConfirmation: true,
        },
        confirmationToken: "egress-token",
        expiresInSeconds: 300,
      });
      return;
    }
    if (path === "/api/v1/jobs/job-egress") {
      await json(route, { id: "job-egress", kind: "routeros.egress", status: "SUCCEEDED", progress: 100, attempts: 1, createdAt: observedAt, updatedAt: "2026-07-27T00:00:01Z" });
      return;
    }
    if (path === "/api/v1/subscriptions") {
      await json(route, []);
      return;
    }
    if (path === "/api/v1/alerts") {
      await json(route, []);
      return;
    }
    if (path === "/api/v1/backups") {
      await json(route, []);
      return;
    }
    if (path.startsWith("/api/v1/nodes/") && path.endsWith("/probe")) {
      await json(route, { reachable: true, latencyMs: 42 });
      return;
    }
    if (path === "/api/v1/mihomo/probes/node-test") {
      if (options.mihomoProbeFailure) {
        await json(route, { error: "mihomo_probe_failed", message: "Mihomo Controller 探测失败" }, 503);
        return;
      }
      await json(route, { nodeId: "node-test", nodeName: "测试节点", nodeHttp: { available: true, success: true, latencyMs: 64 }, exit: { available: true, success: true, latencyMs: 81, scope: "current-policy", ipAddress: "198.18.0.10" } });
      return;
    }
    await json(route, { error: "unhandled_e2e_mock", message: `${request.method()} ${path}` }, 501);
  });
  return calls;
}

async function expectNoA11yViolations(page: Page) {
  await page.addScriptTag({ content: axe.source });
  const violations = await page.evaluate(async () => {
    const result = await (window as typeof window & { axe: typeof axe }).axe.run(document, {
      runOnly: { type: "tag", values: ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"] },
    });
    return result.violations.map((violation) => ({
      id: violation.id,
      impact: violation.impact,
      help: violation.help,
      targets: violation.nodes.map((node) => node.target.join(" ")),
    }));
  });
  expect(violations, JSON.stringify(violations, null, 2)).toEqual([]);
}

async function expectLayoutIntegrity(page: Page, view: string, mobile: boolean) {
  await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
  const violations = await page.evaluate(({ isMobile }) => {
    const selector = "main, .topbar, .page-content, h1, h2, .panel, .summary-card, .button, .container-command, .data-mode, input, select, textarea";
    const elements = Array.from(document.querySelectorAll<HTMLElement>(selector));
    const messages: string[] = [];
    for (const element of elements) {
      const style = getComputedStyle(element);
      if (style.display === "none" || style.visibility === "hidden" || !element.getClientRects().length || element.closest('[aria-hidden="true"]')) continue;
      const rect = element.getBoundingClientRect();
      const label = element.getAttribute("aria-label") || element.textContent?.trim().slice(0, 48) || element.tagName.toLowerCase();
      const intentionallyScrollable = Boolean(element.closest(".table-wrap, .chain-builder-row"));
      if (!intentionallyScrollable && (rect.left < -1 || rect.right > window.innerWidth + 1)) {
        messages.push(`${label}: horizontal bounds ${rect.left.toFixed(1)}..${rect.right.toFixed(1)} / ${window.innerWidth}`);
      }
      if (element.matches("h1, h2, .button, .container-command, .data-mode") && (element.scrollWidth > element.clientWidth + 1 || element.scrollHeight > element.clientHeight + 1)) {
        messages.push(`${label}: clipped ${element.clientWidth}x${element.clientHeight} < ${element.scrollWidth}x${element.scrollHeight}`);
      }
      if (isMobile && element.matches("button:not(.sidebar-scrim):not(.skip-link), input, select")) {
        const target = element.matches("input, select") ? element.closest("label") ?? element : element;
        const targetHeight = target.getBoundingClientRect().height;
        if (targetHeight < 43.5) messages.push(`${label}: touch target height ${targetHeight.toFixed(1)}`);
      }
    }
    return messages;
  }, { isMobile: mobile });
  expect(violations, `${view}:\n${violations.join("\n")}`).toEqual([]);
}

test.beforeEach(async ({ page }) => {
  await page.addInitScript(() => {
    window.sessionStorage.setItem("foxos.apiToken", "e2e-token-".padEnd(40, "x"));
  });
});

test("renders independently verifiable service state without horizontal overflow", async ({ page }) => {
  await mockApi(page);
  await page.goto("/#overview");
  await expect(page.getByRole("heading", { name: "总览" })).toBeVisible();
  await expect(page.getByText("RouterOS").first()).toBeVisible();
  const dimensions = await page.evaluate(() => ({ width: document.documentElement.clientWidth, scrollWidth: document.documentElement.scrollWidth }));
  expect(dimensions.scrollWidth).toBeLessThanOrEqual(dimensions.width + 1);
});

test("captures the documented 1440 by 900 overview", async ({ page }, testInfo) => {
  test.skip(!process.env.UPDATE_DOC_SCREENSHOTS || testInfo.project.name !== "desktop", "documentation screenshot generation is opt-in and desktop-only");
  await mockApi(page);
  await page.goto("/#overview");
  await expect(page.getByRole("heading", { level: 1, name: "总览" })).toBeVisible();
  await expect(page.locator(".data-mode")).toContainText("15 / 15");
  expect(page.viewportSize()).toEqual({ width: 1440, height: 900 });
  await page.screenshot({ path: "../docs/screenshots/overview-desktop.jpg", type: "jpeg", quality: 90, fullPage: false });
});

test("keeps all core views inside the viewport", async ({ page }, testInfo) => {
  await mockApi(page);
  for (const view of ["overview", "network", "devices", "proxies", "operations", "settings"]) {
    await page.goto(`/#${view}`);
    await expect(page.getByRole("main")).toBeVisible();
    const dimensions = await page.evaluate(() => ({ width: document.documentElement.clientWidth, scrollWidth: document.documentElement.scrollWidth }));
    if (testInfo.project.name === "mobile") expect(dimensions.width).toBe(390);
    expect(dimensions.scrollWidth, `${view} root overflow`).toBeLessThanOrEqual(dimensions.width + 1);
    await expectLayoutIntegrity(page, view, testInfo.project.name === "mobile");
  }
});

test("pauses resource polling while hidden and refreshes on visibility and command", async ({ page }, testInfo) => {
  test.skip(testInfo.project.name !== "desktop", "one browser project is sufficient for timer semantics");
  await page.clock.install({ time: new Date("2026-07-27T00:00:00Z") });
  const calls = await mockApi(page);
  await page.goto("/#overview");
  await expect(page.getByRole("heading", { level: 1, name: "总览" })).toBeVisible();
  await page.waitForLoadState("networkidle");
  const initialReads = calls.resourceReads["/api/v1/routeros/overview"] ?? 0;
  expect(initialReads).toBeGreaterThan(0);

  await page.evaluate(() => {
    Object.defineProperty(document, "hidden", { configurable: true, value: true });
    document.dispatchEvent(new Event("visibilitychange"));
  });
  await page.clock.runFor(45_000);
  expect(calls.resourceReads["/api/v1/routeros/overview"]).toBe(initialReads);

  await page.evaluate(() => {
    Object.defineProperty(document, "hidden", { configurable: true, value: false });
    document.dispatchEvent(new Event("visibilitychange"));
  });
  await page.clock.runFor(300);
  await expect.poll(() => calls.resourceReads["/api/v1/routeros/overview"] ?? 0).toBe(initialReads + 1);

  await page.getByRole("button", { name: "刷新全部状态" }).click();
  await expect.poll(() => calls.resourceReads["/api/v1/routeros/overview"] ?? 0).toBe(initialReads + 2);
});

test("backs off page polling after a resource failure", async ({ page }, testInfo) => {
  test.skip(testInfo.project.name !== "desktop", "one browser project is sufficient for timer semantics");
  await page.clock.install({ time: new Date("2026-07-27T00:00:00Z") });
  const calls = await mockApi(page, { routerosFailure: true });
  await page.goto("/#overview");
  await page.waitForLoadState("networkidle");
  const initialReads = calls.resourceReads["/api/v1/routeros/overview"] ?? 0;
  expect(initialReads).toBeGreaterThan(0);

  const refreshedPaths = [
    "/api/v1/routeros/overview",
    "/api/v1/mihomo/overview",
    "/api/v1/mosdns/overview",
    "/api/v1/routeros/dhcp/address-plan",
    "/api/v1/jobs",
    "/api/v1/audit-events",
  ];
  const refreshResponses = Promise.all(refreshedPaths.map((path) => page.waitForResponse((response) => {
    const url = new URL(response.url());
    return response.request().method() === "GET" && url.pathname === path;
  })));
  await page.clock.runFor(15_100);
  await Promise.all((await refreshResponses).map((response) => response.finished()));
  await page.evaluate(() => Promise.resolve());
  await expect.poll(() => calls.resourceReads["/api/v1/routeros/overview"] ?? 0).toBe(initialReads + 1);
  await page.clock.runFor(29_500);
  expect(calls.resourceReads["/api/v1/routeros/overview"]).toBe(initialReads + 1);
  await page.clock.runFor(600);
  await expect.poll(() => calls.resourceReads["/api/v1/routeros/overview"] ?? 0).toBe(initialReads + 2);
});

test("keeps a failed RouterOS resource unavailable while other data remains visible", async ({ page }) => {
  await mockApi(page, { routerosFailure: true });
  await page.goto("/#overview");
  const service = page.locator(".service-summary").filter({ hasText: "RouterOS" });
  await expect(service).toContainText("不可用");
  await expect(page.locator(".service-summary").filter({ hasText: "Mihomo" })).toContainText("在线");
  await expect(page.getByText("全部已验证")).toHaveCount(0);
});

test("degrades a failed route resource without hiding DHCP and container state", async ({ page }) => {
  await mockApi(page, { routesFailure: true });
  await page.goto("/#network");
  await expect(page.getByText("路由数据不可用")).toBeVisible();
  await expect(page.getByText("dhcp-lan", { exact: true }).first()).toBeVisible();
  await expect(page.getByText("foxos", { exact: true })).toBeVisible();
});

test("runs an owned container command once and confirms RouterOS readback", async ({ page }) => {
  const calls = await mockApi(page);
  await page.goto("/#network");
  const container = page.getByRole("region", { name: "foxos" });
  await expect(container).toContainText("当前运行状态运行中");
  await expect(container).toContainText("开机自动启动已启用");
  const stop = container.getByRole("button", { name: "停止" });
  await stop.click();
  await stop.click({ force: true });
  await expect.poll(() => calls.containerCommands.length).toBe(1);
  expect(calls.containerCommands[0]).toMatchObject({ owner: "foxos:active" });
  await expect(page.getByRole("status")).toContainText("已停止，RouterOS 状态已回读");
  await expect(container).toContainText("当前运行状态已停止");
  await expect(container.getByRole("button", { name: "启动" })).toBeEnabled();
});

test("supports hash deep links and browser back navigation", async ({ page }, testInfo) => {
  await mockApi(page);
  await page.goto("/#proxies");
  await expect(page.getByRole("heading", { level: 1, name: "代理与订阅" })).toBeVisible();
  if (testInfo.project.name === "mobile") await page.getByRole("button", { name: "打开导航" }).click();
  await page.getByRole("button", { name: "网络与地址" }).click();
  await expect(page).toHaveURL(/#network$/);
  await page.goBack();
  await expect(page).toHaveURL(/#proxies$/);
  await expect(page.getByRole("heading", { level: 1, name: "代理与订阅" })).toBeVisible();
  await expect(page.getByRole("main")).toBeFocused();
});

test("distinguishes a RouterOS L2TP session from TCP reachability", async ({ page }) => {
  await mockApi(page);
  await page.goto("/#proxies");
  await expect(page.getByText("L2TP 会话运行").first()).toBeVisible();
  await expect(page.getByText("TCP 可达", { exact: true })).toHaveCount(0);
});

test("selects grid rows with arrow keys", async ({ page }) => {
  await mockApi(page);
  await page.goto("/#devices");
  const grid = page.getByRole("grid", { name: "RouterOS 设备" });
  await expect(grid.getByRole("columnheader")).toHaveCount(7);
  const rows = grid.getByRole("row").filter({ has: page.getByRole("gridcell") });
  await expect(rows).toHaveCount(2);
  await rows.first().focus();
  await page.keyboard.press("ArrowDown");
  await expect(rows.nth(1)).toBeFocused();
  await expect(rows.nth(1)).toHaveAttribute("aria-selected", "true");
});

test("persists a device profile only after the audited API succeeds", async ({ page }) => {
  const calls = await mockApi(page);
  await page.goto("/#devices");
  await page.getByRole("textbox", { name: "别名" }).fill("办公电脑");
  await page.getByRole("textbox", { name: "标签" }).fill("trusted, work");
  const refreshed = page.waitForResponse((response) => new URL(response.url()).pathname === "/api/v1/devices");
  const historyReloaded = page.waitForResponse((response) => new URL(response.url()).pathname.endsWith("/history"));
  await page.getByRole("button", { name: "保存画像" }).click();
  await expect.poll(() => calls.deviceMetadata.length).toBe(1);
  expect(calls.deviceMetadata[0]).toEqual({ alias: "办公电脑", vendor: "Fox Labs", tags: ["trusted", "work"] });
  await expect(page.getByRole("status")).toContainText("设备画像已保存到 SQLite 并写入审计；RouterOS 未修改");
  await refreshed;
  await historyReloaded;
});

test("keeps node HTTP and current-policy exit checks separate from TCP", async ({ page }) => {
  await mockApi(page);
  await page.goto("/#proxies");
  await page.getByRole("button", { name: "代理 HTTP / 出口" }).click();
  const verified = page.locator(".detail-section").filter({ has: page.getByRole("heading", { name: "已验证范围" }) });
  await expect(verified).toContainText("节点 HTTP64 ms");
  await expect(verified).toContainText("出口 IP198.18.0.10（当前策略）");
  await expect(verified).toContainText("TCP 可达性未检测");
  await expect(verified).toContainText("代理握手未单独检测");
});

test("shows a probe failure without converting TCP or exit state to success", async ({ page }) => {
  await mockApi(page, { mihomoProbeFailure: true });
  await page.goto("/#proxies");
  await page.getByRole("button", { name: "代理 HTTP / 出口" }).click();
  await expect(page.getByRole("alert")).toContainText("Mihomo Controller 探测失败");
  const verified = page.locator(".detail-section").filter({ has: page.getByRole("heading", { name: "已验证范围" }) });
  await expect(verified).toContainText("节点 HTTP未检测");
  await expect(verified).toContainText("出口 IP未配置");
  await expect(verified).toContainText("TCP 可达性未检测");
});

test("requires impact acknowledgement before applying a device egress plan", async ({ page }) => {
  const calls = await mockApi(page);
  await page.goto("/#devices");
  await page.getByRole("combobox", { name: "选择出口" }).selectOption("blocked");
  await page.getByRole("button", { name: "生成应用计划" }).click();
  const dialog = page.getByRole("dialog", { name: "确认设备出口变更" });
  await expect(dialog).toContainText("拒绝设备转发流量");
  await expect(dialog).toContainText("foxos:egress:");
  const confirm = dialog.getByRole("button", { name: "确认应用出口策略" });
  await expect(confirm).toBeDisabled();
  await dialog.getByRole("checkbox").check();
  const refreshed = page.waitForResponse((response) => new URL(response.url()).pathname === "/api/v1/devices");
  const historyReloaded = page.waitForResponse((response) => new URL(response.url()).pathname.endsWith("/history"));
  await confirm.click();
  await expect.poll(() => calls.egressExecutions.length).toBe(1);
  await expect(page.getByRole("status")).toContainText("出口策略已执行、回读并持久化");
  await refreshed;
  await historyReloaded;
});

test("mobile navigation traps focus and closes with Escape", async ({ page }, testInfo) => {
  test.skip(testInfo.project.name !== "mobile", "mobile drawer behavior");
  await mockApi(page);
  await page.goto("/#overview");
  const menu = page.getByRole("button", { name: "打开导航" });
  await menu.click();
  await expect(menu).toHaveAttribute("aria-expanded", "true");
  const navigation = page.getByRole("navigation", { name: "主导航" });
  await expect(navigation).toBeVisible();
  await expect(navigation.getByRole("button", { name: "总览" })).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(menu).toHaveAttribute("aria-expanded", "false");
  await expect(menu).toBeFocused();
  await expect(navigation).toHaveCount(0);
});

test("traps focus in a dangerous-operation dialog and restores it on Escape", async ({ page }) => {
  await mockApi(page);
  await page.goto("/#proxies");
  const deleteButton = page.getByRole("button", { name: "删除", exact: true });
  await deleteButton.click();
  const dialog = page.getByRole("dialog", { name: "删除代理节点" });
  await expect(dialog).toBeVisible();
  const confirmButton = dialog.getByRole("button", { name: "确认删除节点" });
  await expect(confirmButton).toBeDisabled();
  await dialog.getByRole("checkbox").check();
  await expect(confirmButton).toBeEnabled();
  await confirmButton.focus();
  await page.keyboard.press("Tab");
  await expect(dialog.getByRole("button", { name: "关闭弹窗" })).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(dialog).toHaveCount(0);
  await expect(deleteButton).toBeFocused();
});

test("shows the persisted rollback result after Mihomo publish", async ({ page }) => {
  await mockApi(page, { mihomoRollback: true });
  await page.goto("/#proxies");
  await expect(page.getByRole("heading", { name: "Mihomo 配置发布" })).toBeVisible();
  await page.getByRole("button", { name: "生成预览" }).click();
  await expect(page.getByText("预览摘要")).toBeVisible();
  await page.getByRole("button", { name: "确认发布" }).click();
  const dialog = page.getByRole("dialog", { name: "确认发布 Mihomo 配置" });
  await dialog.getByRole("checkbox").check();
  await dialog.getByRole("button", { name: "确认执行" }).click();
  await expect(page.getByText("Mihomo 发布失败，已自动回滚到上一份快照")).toBeVisible({ timeout: 10_000 });
});

test("reports Mihomo publish success only after the persisted job succeeds", async ({ page }) => {
  const calls = await mockApi(page);
  await page.goto("/#proxies");
  await page.getByRole("button", { name: "生成预览" }).click();
  await page.getByRole("button", { name: "确认发布" }).click();
  const dialog = page.getByRole("dialog", { name: "确认发布 Mihomo 配置" });
  await dialog.getByRole("checkbox").check();
  await dialog.getByRole("button", { name: "确认执行" }).click();
  await expect.poll(() => calls.mihomoApply).toBe(1);
  await expect(page.getByRole("status")).toContainText("Mihomo 配置已发布并完成健康验证");
});

test("shows snapshot restore impact before accepting the confirmation", async ({ page }) => {
  await mockApi(page);
  await page.goto("/#proxies");
  const snapshot = page.locator(".snapshot-row").filter({ hasText: "发布前快照" });
  await snapshot.getByRole("button", { name: "恢复" }).click();
  const dialog = page.getByRole("dialog", { name: "确认恢复 Mihomo 快照" });
  await expect(dialog).toContainText("快照：snapshot-test");
  await expect(dialog).toContainText("失败时恢复当前运行配置");
  await expect(dialog.getByRole("button", { name: "确认执行" })).toBeDisabled();
});

test("meets the automated accessibility baseline on critical views", async ({ page }) => {
  await mockApi(page);
  for (const view of ["overview", "network", "devices", "proxies", "operations", "settings"]) {
    await page.goto(`/#${view}`);
    await expect(page.getByRole("main")).toBeVisible();
    await expectNoA11yViolations(page);
  }
});
