import axe from "axe-core";
import { type Page, type Route } from "@playwright/test";
import { expect, test } from "./fixtures";

type MockOptions = {
  routerosFailure?: boolean;
  routesFailure?: boolean;
  mihomoProbeFailure?: boolean;
  mihomoRollback?: boolean;
	mihomoFailure?: boolean;
	mihomoDraftUnavailableOnce?: boolean;
	subscriptionLifecycle?: boolean;
	jobReadbackDelayMs?: number;
	holdSecondNodeRead?: boolean;
	staleNodeRead401?: boolean;
	failedJobInitially?: boolean;
	jobRetryReadback?: "ok" | "503" | "stale" | "running";
	subscriptionCreateReadbackFailure?: boolean;
	subscriptionCreateReadbackMissing?: boolean;
};

type MockCalls = {
  containerCommands: unknown[];
  deviceMetadata: unknown[];
  egressExecutions: unknown[];
	mihomoApply: number;
	delayedNodeReads: number;
	completedNodeReads: number;
	expireCurrentSession: () => void;
	releaseNodeRead: () => void;
  resourceReads: Record<string, number>;
  sessionCreates: number;
	sessionDeletes: number;
	subscriptionCreates: unknown[];
	subscriptionDeletes: Array<{ id: string; strategy: string; outcome: string }>;
	subscriptionNodeCounts: Record<string, number>;
	subscriptionRetries: number;
	subscriptionUpdates: unknown[];
};

const observedAt = "2026-07-27T00:00:00Z";
const e2eToken = "e2e-token-".padEnd(40, "x");
const e2eSession = "e2e-browser-session";
const e2eCSRF = "c".repeat(64);

const node = {
  id: "node-test",
  name: "测试节点",
  type: "vless",
  server: "node.example.invalid",
  port: 443,
  hasCredential: true,
};

async function json(route: Route, body: unknown, status = 200, headers: Record<string, string> = {}) {
  await route.fulfill({
    status,
    contentType: "application/json; charset=utf-8",
    headers,
    body: JSON.stringify(body),
  });
}

async function mockApi(page: Page, options: MockOptions = {}) {
  let jobReads = 0;
	let mihomoDraftReads = 0;
  let containerStatus = "running";
  let containerCommand = "";
  let storedPolicy: Record<string, unknown> | undefined;
	let subscriptionJobCreated = false;
	let subscriptionJobStatus = "FAILED";
	let nodeReadCount = 0;
	let currentSessionExpired = false;
	let subscriptionCreateReadbackFailed = false;
	let releaseNodeRead = () => {};
	const nodeReadGate = new Promise<void>((resolve) => { releaseNodeRead = resolve; });
	const subscriptions: Array<{ id: string; name: string; url: string; enabled: boolean; interval: number; lastSuccessAt?: string }> = options.subscriptionLifecycle ? [
		{ id: "source-primary", name: "Primary", url: "https://subscriptions.example.invalid/primary", enabled: true, interval: 3600, lastSuccessAt: observedAt },
		{ id: "source-detach", name: "Detach source", url: "https://subscriptions.example.invalid/detach", enabled: true, interval: 3600, lastSuccessAt: observedAt },
		{ id: "source-cascade", name: "Cascade source", url: "https://subscriptions.example.invalid/cascade", enabled: true, interval: 3600, lastSuccessAt: observedAt },
	] : [];
	const calls: MockCalls = {
		containerCommands: [], deviceMetadata: [], egressExecutions: [], mihomoApply: 0, delayedNodeReads: 0, completedNodeReads: 0, expireCurrentSession: () => { currentSessionExpired = true; }, releaseNodeRead, resourceReads: {}, sessionCreates: 0, sessionDeletes: 0,
		subscriptionCreates: [], subscriptionDeletes: [], subscriptionNodeCounts: options.subscriptionLifecycle ? { "source-primary": 2, "source-detach": 2, "source-cascade": 2 } : {}, subscriptionRetries: 0, subscriptionUpdates: [],
	};
	if (options.failedJobInitially) subscriptionJobCreated = true;
	const subscriptionJob = () => ({
		id: "job-subscription", kind: "subscription.update", status: subscriptionJobStatus, progress: subscriptionJobStatus === "FAILED" ? 100 : 0,
		attempts: subscriptionJobStatus === "FAILED" ? 1 : 2, errorClass: subscriptionJobStatus === "FAILED" ? "subscription_source_changed" : "",
		errorMessage: subscriptionJobStatus === "FAILED" ? "远端内容变化，旧节点保持不变" : "", result: { phase: subscriptionJobStatus === "FAILED" ? "source_revalidation_failed" : "retry_queued" },
		createdAt: observedAt, updatedAt: "2026-07-27T00:00:01Z",
	});
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname;
    const method = request.method();
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
    if (path === "/api/v1/session" && method === "GET") {
      if (!(request.headers().cookie ?? "").includes(`foxos_session=${e2eSession}`)) {
        await json(route, { error: "unauthorized", message: "未认证" }, 401);
        return;
      }
      await json(route, { csrfToken: e2eCSRF, expiresAt: "2099-01-01T00:00:00Z" });
      return;
    }
    if (path === "/api/v1/session" && method === "POST") {
      const input = request.postDataJSON() as { token?: string };
      if (input.token !== e2eToken || request.headers().origin !== "http://127.0.0.1:4173") {
        await json(route, { error: "unauthorized", message: "未认证" }, 401);
        return;
      }
      calls.sessionCreates += 1;
      await json(route, { csrfToken: e2eCSRF, expiresAt: "2099-01-01T00:00:00Z" }, 201, {
        "set-cookie": `foxos_session=${e2eSession}; Path=/; HttpOnly; SameSite=Strict; Max-Age=28800`,
      });
      return;
    }
    if (path === "/api/v1/session" && method === "DELETE") {
      if (!(request.headers().cookie ?? "").includes(`foxos_session=${e2eSession}`) || request.headers()["x-foxos-csrf"] !== e2eCSRF) {
        await json(route, { error: "unauthorized", message: "未认证" }, 401);
        return;
      }
      calls.sessionDeletes += 1;
      await route.fulfill({ status: 204, headers: { "set-cookie": "foxos_session=; Path=/; HttpOnly; SameSite=Strict; Max-Age=0" } });
      return;
    }
    if (!(request.headers().cookie ?? "").includes(`foxos_session=${e2eSession}`)) {
      await json(route, { error: "unauthorized", message: "未认证" }, 401);
      return;
    }
    if (!["GET", "HEAD", "OPTIONS"].includes(method) && request.headers()["x-foxos-csrf"] !== e2eCSRF) {
      await json(route, { error: "csrf_rejected", message: "CSRF 校验失败" }, 403);
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
			nodeReadCount += 1;
				if (options.holdSecondNodeRead && nodeReadCount === 2) {
				calls.delayedNodeReads += 1;
				await nodeReadGate;
				if (options.staleNodeRead401) {
					calls.completedNodeReads += 1;
					await json(route, { error: "unauthorized", message: "旧会话请求已失效" }, 401);
					return;
					}
				}
				if (currentSessionExpired) {
					calls.completedNodeReads += 1;
					await json(route, { error: "unauthorized", message: "当前浏览器会话已失效" }, 401);
					return;
				}
      await json(route, [node]);
			calls.completedNodeReads += 1;
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
				if (calls.subscriptionRetries > 0 && options.jobRetryReadback === "503") {
					await json(route, { error: "jobs_unavailable", message: "任务列表回读失败" }, 503);
					return;
				}
					if (calls.subscriptionRetries > 0 && options.jobRetryReadback === "stale") {
						await json(route, [{ ...subscriptionJob(), status: "FAILED", progress: 100, attempts: 1, errorClass: "subscription_source_changed", errorMessage: "旧状态", result: { phase: "source_revalidation_failed" } }]);
						return;
					}
					if (calls.subscriptionRetries > 0 && options.jobRetryReadback === "running") {
						await json(route, [{ ...subscriptionJob(), status: "RUNNING", progress: 25, attempts: 2, errorClass: "", errorMessage: "", result: { phase: "retry_running" }, updatedAt: "2026-07-27T00:00:02Z" }]);
						return;
					}
				if (subscriptionJobStatus === "QUEUED" && options.jobReadbackDelayMs) await new Promise((resolve) => setTimeout(resolve, options.jobReadbackDelayMs));
			await json(route, options.subscriptionLifecycle && subscriptionJobCreated ? [subscriptionJob()] : []);
      return;
    }
    if (path === "/api/v1/mihomo/draft" && request.method() === "GET") {
			mihomoDraftReads += 1;
			if (options.mihomoDraftUnavailableOnce && mihomoDraftReads === 1) {
				await json(route, { error: "mihomo_unavailable", message: "Mihomo Controller 未配置" }, 503);
				return;
			}
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
        status: options.mihomoFailure ? "FAILED" : options.mihomoRollback && jobReads > 0 ? "ROLLED_BACK" : "SUCCEEDED",
        progress: 100,
        errorMessage: options.mihomoFailure ? "Mihomo 健康检查失败" : "",
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
		if (path === "/api/v1/jobs/job-subscription" && method === "GET") {
			await json(route, subscriptionJob());
			return;
		}
		if (path === "/api/v1/jobs/job-subscription/retry" && method === "POST") {
			calls.subscriptionRetries += 1;
			subscriptionJobStatus = "QUEUED";
			await json(route, subscriptionJob());
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
			if (!options.subscriptionLifecycle) {
				await json(route, []);
				return;
			}
				if (method === "GET") {
					if (options.subscriptionCreateReadbackFailure && calls.subscriptionCreates.length > 0 && !subscriptionCreateReadbackFailed) {
						subscriptionCreateReadbackFailed = true;
						await json(route, { error: "subscriptions_unavailable", message: "订阅列表回读失败" }, 503);
						return;
					}
					if (options.subscriptionCreateReadbackMissing && calls.subscriptionCreates.length > 0) {
						await json(route, subscriptions.filter((item) => !item.id.startsWith("source-created-")));
						return;
					}
					await json(route, subscriptions);
				return;
			}
			if (method === "POST") {
				const input = request.postDataJSON() as { name: string; url: string; enabled: boolean; interval: number };
				calls.subscriptionCreates.push(input);
				const created = { id: `source-created-${calls.subscriptionCreates.length}`, ...input };
				subscriptions.push(created);
				calls.subscriptionNodeCounts[created.id] = 0;
				await json(route, created, 201);
				return;
			}
		}
		if (options.subscriptionLifecycle && path.startsWith("/api/v1/subscriptions/") && path.endsWith("/preview") && method === "POST") {
			const id = path.split("/")[4];
			const existingCount = calls.subscriptionNodeCounts[id] ?? 0;
			const nodeCount = existingCount + 1;
			const digest = "b".repeat(64);
			await json(route, {
				digest,
				nodeCount,
				nodes: Array.from({ length: nodeCount }, (_, index) => ({ id: `${id}-node-${index + 1}`, name: `${id} / Node ${index + 1}`, type: "vless", server: "node.example.invalid", port: 443, hasCredential: true })),
				plan: { action: "subscription.update", subscriptionId: id, digest, nodeIds: Array.from({ length: nodeCount }, (_, index) => `${id}-node-${index + 1}`), removedNodeIds: [], existingCount, nodeCount, addCount: 1, updateCount: existingCount, removeCount: 0, parseValidCount: nodeCount, parseSkippedCount: 1, parseErrors: [{ index: nodeCount + 1, reason: "unsupported fixture" }], suspiciousReduction: false },
				confirmationToken: `preview-${id}`, expiresInSeconds: 300,
			});
			return;
		}
		if (options.subscriptionLifecycle && path.startsWith("/api/v1/subscriptions/") && path.endsWith("/update") && method === "POST") {
			calls.subscriptionUpdates.push(request.postDataJSON());
			subscriptionJobCreated = true;
			subscriptionJobStatus = "FAILED";
			await json(route, { status: "QUEUED", job: { ...subscriptionJob(), status: "QUEUED", progress: 0, errorClass: "", errorMessage: "" } }, 202);
			return;
		}
		if (options.subscriptionLifecycle && path.startsWith("/api/v1/subscriptions/") && path.endsWith("/delete/plan") && method === "POST") {
			const id = path.split("/")[4];
			const input = request.postDataJSON() as { strategy: string };
			const nodeCount = calls.subscriptionNodeCounts[id] ?? 0;
			await json(route, { plan: { action: "subscription.delete", strategy: input.strategy, subscriptionId: id, nodeIds: Array.from({ length: nodeCount }, (_, index) => `${id}-node-${index + 1}`), nodeCount }, confirmationToken: `delete-${id}-${input.strategy}`, expiresInSeconds: 300, warnings: ["计划只允许执行一次"] });
			return;
		}
		if (options.subscriptionLifecycle && path.startsWith("/api/v1/subscriptions/") && path.endsWith("/delete") && method === "POST") {
			const id = path.split("/")[4];
			const input = request.postDataJSON() as { strategy: string };
			if (id === "source-cascade" && input.strategy === "cascade") {
				calls.subscriptionDeletes.push({ id, strategy: input.strategy, outcome: "conflict" });
				await json(route, { error: "subscription_nodes_referenced", message: "来源节点仍被代理组引用，删除已回滚" }, 409);
				return;
			}
			const nodeCount = calls.subscriptionNodeCounts[id] ?? 0;
			const index = subscriptions.findIndex((item) => item.id === id);
			if (index >= 0) subscriptions.splice(index, 1);
			if (input.strategy === "cascade") calls.subscriptionNodeCounts[id] = 0;
			calls.subscriptionDeletes.push({ id, strategy: input.strategy, outcome: "succeeded" });
			await json(route, { status: "SUCCEEDED", strategy: input.strategy, subscriptionId: id, nodeCount });
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
    const selector = "main, .topbar, .page-content, h1, h2, .panel, .summary-card, .data-mode, button, summary, a[href], input, select, textarea, [tabindex], [data-grid-row]";
    const elements = Array.from(document.querySelectorAll<HTMLElement>(selector));
    const messages: string[] = [];
    for (const element of elements) {
      const style = getComputedStyle(element);
      if (style.display === "none" || style.visibility === "hidden" || !element.getClientRects().length || element.closest('[aria-hidden="true"]')) continue;
      const rect = element.getBoundingClientRect();
      const label = element.getAttribute("aria-label") || element.closest("label")?.textContent?.trim().slice(0, 48) || element.textContent?.trim().slice(0, 48) || element.tagName.toLowerCase();
      const intentionallyScrollable = Boolean(element.closest(".table-wrap, .chain-builder-row"));
      if (!intentionallyScrollable && (rect.left < -1 || rect.right > window.innerWidth + 1)) {
        messages.push(`${label}: horizontal bounds ${rect.left.toFixed(1)}..${rect.right.toFixed(1)} / ${window.innerWidth}`);
      }
      if (element.matches("h1, h2, .button, .container-command, .data-mode") && (element.scrollWidth > element.clientWidth + 1 || element.scrollHeight > element.clientHeight + 1)) {
        messages.push(`${label}: clipped ${element.clientWidth}x${element.clientHeight} < ${element.scrollWidth}x${element.scrollHeight}`);
      }
      if (isMobile && element.matches("button:not(.sidebar-scrim):not(.skip-link), summary, a[href], input, select, textarea, [tabindex]:not([tabindex='-1']), [data-grid-row]")) {
        const target = element.matches("input[type='checkbox'], input[type='radio']") ? element.closest("label") ?? element : element;
        const targetRect = target.getBoundingClientRect();
        if (targetRect.width < 43.5 || targetRect.height < 43.5) messages.push(`${label}: touch target ${targetRect.width.toFixed(1)}x${targetRect.height.toFixed(1)}`);
      }
    }
    return messages;
  }, { isMobile: mobile });
  expect(violations, `${view}:\n${violations.join("\n")}`).toEqual([]);
}

test.beforeEach(async ({ page }) => {
	await page.addInitScript((token) => {
		if (window.localStorage.getItem("foxos.e2eTokenSeeded") === "true") return;
		window.localStorage.setItem("foxos.e2eTokenSeeded", "true");
		window.sessionStorage.setItem("foxos.apiToken", token);
	}, e2eToken);
});

test("exchanges a legacy token once and restores the cookie session after reload", async ({ page }) => {
  const calls = await mockApi(page);
  await page.goto("/#overview");
  await expect(page.getByRole("heading", { name: "总览" })).toBeVisible();
  await expect.poll(() => calls.sessionCreates).toBe(1);
  await page.reload();
  await expect(page.getByRole("heading", { name: "总览" })).toBeVisible();
  expect(calls.sessionCreates).toBe(1);
});

test("clears protected state and ignores an old request after signing out", async ({ page }, testInfo) => {
  const calls = await mockApi(page, { holdSecondNodeRead: true });
  await page.goto("/#proxies");
	await expect(page.getByRole("row", { name: /测试节点/ })).toBeVisible();
	await page.getByRole("button", { name: "刷新节点" }).click();
	await expect.poll(() => calls.delayedNodeReads).toBe(1);
  if (testInfo.project.name === "mobile") await page.getByRole("button", { name: "打开导航" }).click();
  await page.getByRole("button", { name: "设置" }).click();
  await expect(page.getByText("HttpOnly 浏览器会话有效")).toBeVisible();
  await page.getByRole("button", { name: "注销会话" }).click();
  await expect.poll(() => calls.sessionDeletes).toBe(1);
  await expect(page).toHaveURL(/#settings$/);
  await expect(page.getByText("尚未建立浏览器会话", { exact: true }).first()).toBeVisible();
	calls.releaseNodeRead();
	await expect.poll(() => calls.completedNodeReads).toBe(2);

  await page.evaluate(() => { window.location.hash = "proxies"; });
  await expect(page.getByRole("heading", { level: 1, name: "代理与订阅" })).toBeVisible();
	await expect(page.getByRole("row", { name: /测试节点/ })).toHaveCount(0);
	await expect(page.locator("#mihomo-publish").getByRole("alert")).toContainText("Mihomo 配置服务不可用");
});

test("does not let an old 401 clear a re-authenticated session", async ({ page }, testInfo) => {
	const calls = await mockApi(page, { holdSecondNodeRead: true, staleNodeRead401: true });
	await page.goto("/#proxies");
	await expect(page.getByRole("row", { name: /测试节点/ })).toBeVisible();
	await page.getByRole("button", { name: "刷新节点" }).click();
	await expect.poll(() => calls.delayedNodeReads).toBe(1);
	if (testInfo.project.name === "mobile") await page.getByRole("button", { name: "打开导航" }).click();
	await page.getByRole("button", { name: "设置" }).click();
	await page.getByRole("button", { name: "注销会话" }).click();
	await expect.poll(() => calls.sessionDeletes).toBe(1);
	await page.getByRole("textbox", { name: "API Token" }).fill(e2eToken);
	await page.getByRole("button", { name: "建立安全会话" }).click();
	await expect(page.getByText("HttpOnly 浏览器会话有效")).toBeVisible();
	await expect.poll(() => calls.sessionCreates).toBe(2);
	await expect.poll(() => calls.completedNodeReads).toBe(2);
	calls.releaseNodeRead();
	await expect.poll(() => calls.completedNodeReads).toBe(3);
	await expect(page.getByText("HttpOnly 浏览器会话有效")).toBeVisible();
	await page.getByRole("button", { name: "注销会话" }).click();
	await expect.poll(() => calls.sessionDeletes).toBe(2);
	await page.reload();
	await expect(page.getByText("尚未建立浏览器会话", { exact: true }).first()).toBeVisible();
});

test("clears protected UI state when the current session receives a 401", async ({ page }) => {
	const calls = await mockApi(page);
	await page.goto("/#proxies");
	await expect(page.getByRole("row", { name: /测试节点/ })).toBeVisible();
	const completedBeforeExpiry = calls.completedNodeReads;
	calls.expireCurrentSession();
	await page.getByRole("button", { name: "刷新节点" }).click();
	await expect.poll(() => calls.completedNodeReads).toBeGreaterThan(completedBeforeExpiry);
	await expect(page).toHaveURL(/#settings$/);
	await expect(page.getByText("尚未建立浏览器会话", { exact: true }).first()).toBeVisible();

	await page.evaluate(() => { window.location.hash = "proxies"; });
	await expect(page.getByRole("heading", { level: 1, name: "代理与订阅" })).toBeVisible();
	await expect(page.getByRole("row", { name: /测试节点/ })).toHaveCount(0);
});

test("allows keyboard users to focus and scroll the proxy chain", async ({ page }, testInfo) => {
  test.skip(testInfo.project.name !== "mobile", "the chain overflows horizontally at the mobile viewport");
  await mockApi(page);
  await page.goto("/#proxies");
  const chain = page.getByRole("region", { name: "链式代理路径，可横向滚动" });
  await chain.focus();
  await expect(chain).toBeFocused();
  const before = await chain.evaluate((element) => element.scrollLeft);
  await chain.press("ArrowRight");
  await expect.poll(() => chain.evaluate((element) => element.scrollLeft)).toBeGreaterThan(before);
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
    await expectLayoutIntegrity(page, view, Boolean(testInfo.project.use.hasTouch));
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

test("backs off page polling after a resource failure", async ({ page, consoleErrorAllowlist }, testInfo) => {
  test.skip(testInfo.project.name !== "desktop", "one browser project is sufficient for timer semantics");
	consoleErrorAllowlist.allowStatus(503);
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

test("keeps a failed RouterOS resource unavailable while other data remains visible", async ({ page, consoleErrorAllowlist }) => {
	consoleErrorAllowlist.allowStatus(503);
  await mockApi(page, { routerosFailure: true });
  await page.goto("/#overview");
  const service = page.locator(".service-summary").filter({ hasText: "RouterOS" });
  await expect(service).toContainText("不可用");
  await expect(page.locator(".service-summary").filter({ hasText: "Mihomo" })).toContainText("在线");
  await expect(page.getByText("全部已验证")).toHaveCount(0);
});

test("degrades a failed route resource without hiding DHCP and container state", async ({ page, consoleErrorAllowlist }) => {
	consoleErrorAllowlist.allowStatus(503);
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

test("shows a probe failure without converting TCP or exit state to success", async ({ page, consoleErrorAllowlist }) => {
	consoleErrorAllowlist.allowStatus(503);
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

test("traps focus in a dangerous-operation dialog and restores it on Escape", async ({ page }, testInfo) => {
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
  if (testInfo.project.name === "mobile") await expectLayoutIntegrity(page, "danger dialog", true);
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

test("requires a fresh Mihomo preview after a failed publish job", async ({ page }) => {
  const calls = await mockApi(page, { mihomoFailure: true });
  await page.goto("/#proxies");
  const panel = page.locator("#mihomo-publish");
  await panel.getByRole("button", { name: "生成预览" }).click();
  await panel.getByRole("button", { name: "确认发布" }).click();
  const dialog = page.getByRole("dialog", { name: "确认发布 Mihomo 配置" });
  await dialog.getByRole("checkbox").check();
  await dialog.getByRole("button", { name: "确认执行" }).click();
  await expect(page.locator(".toast.warning")).toContainText("Mihomo 健康检查失败");
  await expect(dialog).toHaveCount(0);
  await expect(panel.getByRole("button", { name: "确认发布" })).toBeDisabled();
  expect(calls.mihomoApply).toBe(1);
  await panel.getByRole("button", { name: "生成预览" }).click();
	await expect(panel.getByRole("button", { name: "确认发布" })).toBeEnabled();
});

test("invalidates a Mihomo preview when the draft changes", async ({ page }) => {
	await mockApi(page);
	await page.goto("/#proxies");
	const panel = page.locator("#mihomo-publish");
	await panel.getByRole("button", { name: "生成预览" }).click();
	await expect(panel.getByText("预览摘要")).toBeVisible();
	await expect(panel.getByRole("button", { name: "确认发布" })).toBeEnabled();

	await panel.getByRole("spinbutton", { name: "Mixed Port" }).fill("7891");
	await expect(panel.getByText("预览摘要")).toHaveCount(0);
	await expect(panel.getByRole("button", { name: "确认发布" })).toBeDisabled();
});

test("keeps Mihomo editing unavailable after 503 until retry readback succeeds", async ({ page, consoleErrorAllowlist }) => {
	consoleErrorAllowlist.allowStatus(503);
	await mockApi(page, { mihomoDraftUnavailableOnce: true });
	await page.goto("/#proxies");
	const panel = page.locator("#mihomo-publish");
	await expect(panel.getByRole("alert")).toContainText("Mihomo 配置服务不可用");
	await expect(panel.locator(".operation-badge")).toHaveText("不可用");
	await expect(panel.getByRole("checkbox", { name: "允许局域网访问" })).toBeDisabled();
	await expect(panel.getByRole("button", { name: "保存草稿" })).toBeDisabled();
	await expect(panel.getByRole("button", { name: "生成预览" })).toBeDisabled();

	await panel.getByRole("button", { name: "重试加载" }).click();
	await expect(panel.getByRole("alert")).toHaveCount(0);
	await expect(panel.locator(".operation-badge")).toHaveText("SQLite 草稿");
	await expect(panel.getByRole("checkbox", { name: "允许局域网访问" })).toBeEnabled();
	await expect(panel.getByRole("button", { name: "生成预览" })).toBeEnabled();
});

test("covers subscription create, preview, failed replacement and task retry", async ({ page }, testInfo) => {
	test.skip(testInfo.project.name !== "desktop", "stateful subscription workflow is covered once; responsive layout is covered separately");
	const calls = await mockApi(page, { subscriptionLifecycle: true, jobReadbackDelayMs: 250 });
	await page.goto("/#proxies");
	const panel = page.locator("section.operations-panel").filter({ has: page.getByRole("heading", { name: "订阅源" }) });
	await panel.getByRole("textbox", { name: "名称" }).fill("New source");
	await panel.getByRole("textbox", { name: "HTTPS URL" }).fill("https://subscriptions.example.invalid/new");
	await panel.getByRole("button", { name: "添加" }).click();
	await expect(panel.getByText("New source", { exact: true })).toBeVisible();
	expect(calls.subscriptionCreates).toEqual([{ name: "New source", url: "https://subscriptions.example.invalid/new", enabled: true, interval: 21600 }]);

	const primary = panel.locator(".subscription-row").filter({ hasText: "Primary" });
	await primary.getByRole("button", { name: "预览", exact: true }).click();
	await expect(panel.locator(".operation-callout")).toContainText("3 个去重节点");
	await expect(panel.locator(".operation-callout")).toContainText("跳过 1");
	await primary.getByRole("button", { name: "更新", exact: true }).click();
	const dialog = page.getByRole("dialog", { name: "确认更新订阅" });
	await expect(dialog).toContainText("新增 1，更新 2，移除 0");
	await expect(dialog).toContainText("有 1 个无效条目被跳过");
	await dialog.getByRole("checkbox").check();
	await dialog.getByRole("button", { name: "确认更新订阅" }).click();
	await expect(page.locator(".toast.warning")).toContainText("远端内容变化，旧节点保持不变");
	await expect(dialog).toHaveCount(0);
	await expect(primary).toBeVisible();
	expect(calls.subscriptionNodeCounts["source-primary"]).toBe(2);
	expect(calls.subscriptionUpdates).toHaveLength(1);
	await primary.getByRole("button", { name: "更新", exact: true }).click();
	await expect(page.getByRole("dialog", { name: "确认更新订阅" })).toBeVisible();
	await page.getByRole("dialog", { name: "确认更新订阅" }).getByRole("button", { name: "关闭弹窗" }).click();

	await page.goto("/#operations");
	await page.reload();
	await expect(page.getByRole("heading", { level: 1, name: "任务告警与审计" })).toBeVisible();
	const job = page.getByRole("row").filter({ hasText: "subscription.update" });
	await expect(job).toContainText("FAILED");
	await job.getByRole("button", { name: "重试" }).click();
	await expect.poll(() => calls.subscriptionRetries).toBe(1);
	await expect(page.locator(".toast.success")).toContainText("subscription.update 重试已接受，任务列表回读为 QUEUED");
	await expect(job).toContainText("QUEUED");
	await expect(job.getByRole("button", { name: "重试" })).toHaveCount(0);
});

test("accepts a retried task that is RUNNING by list readback", async ({ page }, testInfo) => {
	test.skip(testInfo.project.name !== "desktop", "failure-state workflow is covered once");
	const calls = await mockApi(page, { subscriptionLifecycle: true, failedJobInitially: true, jobRetryReadback: "running" });
	await page.goto("/#operations");
	const job = page.getByRole("row").filter({ hasText: "subscription.update" });
	await expect(job).toContainText("FAILED");
	await job.getByRole("button", { name: "重试" }).click();
	await expect.poll(() => calls.subscriptionRetries).toBe(1);
	await expect(page.locator(".toast.success")).toContainText("任务列表回读为 RUNNING");
	await expect(job).toContainText("RUNNING");
	await expect(job.getByRole("button", { name: "等待回读" })).toHaveCount(0);
});

for (const readback of ["503", "stale"] as const) {
	test(`keeps a retried task locked when ${readback} does not confirm a new state`, async ({ page, consoleErrorAllowlist }, testInfo) => {
		test.skip(testInfo.project.name !== "desktop", "failure-state workflow is covered once");
		if (readback === "503") consoleErrorAllowlist.allowStatus(503);
		const calls = await mockApi(page, { subscriptionLifecycle: true, failedJobInitially: true, jobRetryReadback: readback });
		await page.goto("/#operations");
		const job = page.getByRole("row").filter({ hasText: "subscription.update" });
		await expect(job).toContainText("FAILED");
		await job.getByRole("button", { name: "重试" }).click();
		await expect.poll(() => calls.subscriptionRetries).toBe(1);
		await expect(job).toContainText("QUEUED（待回读）");
		await expect(job.getByRole("button", { name: "等待回读" })).toBeDisabled();
		await expect(page.locator(".toast.warning")).toContainText(readback === "503" ? "任务列表回读失败" : "任务列表尚未确认新的任务状态");
		expect(calls.subscriptionRetries).toBe(1);
	});
}

test("marks a created subscription pending when its list readback fails", async ({ page, consoleErrorAllowlist }, testInfo) => {
	test.skip(testInfo.project.name !== "desktop", "failure-state workflow is covered once");
	consoleErrorAllowlist.allowStatus(503);
	const calls = await mockApi(page, { subscriptionLifecycle: true, subscriptionCreateReadbackFailure: true });
	await page.goto("/#proxies");
	const panel = page.locator("section.operations-panel").filter({ has: page.getByRole("heading", { name: "订阅源" }) });
	await panel.getByRole("textbox", { name: "名称" }).fill("Pending source");
	await panel.getByRole("textbox", { name: "HTTPS URL" }).fill("https://subscriptions.example.invalid/pending");
	await panel.getByRole("button", { name: "添加" }).click();
	await expect(panel.getByText("Pending source", { exact: true })).toBeVisible();
	await expect(panel.getByText("等待列表回读确认")).toBeVisible();
	await expect(page.locator(".toast.warning")).toContainText("订阅列表回读失败");
	await expect(page.locator(".toast.success")).toHaveCount(0);
	expect(calls.subscriptionCreates).toHaveLength(1);
});

test("marks a created subscription pending when a successful list omits it", async ({ page }, testInfo) => {
	test.skip(testInfo.project.name !== "desktop", "failure-state workflow is covered once");
	const calls = await mockApi(page, { subscriptionLifecycle: true, subscriptionCreateReadbackMissing: true });
	await page.goto("/#proxies");
	const panel = page.locator("section.operations-panel").filter({ has: page.getByRole("heading", { name: "订阅源" }) });
	await panel.getByRole("textbox", { name: "名称" }).fill("Missing source");
	await panel.getByRole("textbox", { name: "HTTPS URL" }).fill("https://subscriptions.example.invalid/missing");
	await panel.getByRole("button", { name: "添加" }).click();
	await expect(panel.getByText("Missing source", { exact: true })).toBeVisible();
	await expect(panel.getByText("等待列表回读确认")).toBeVisible();
	await expect(page.locator(".toast.warning")).toContainText("列表回读未包含新订阅");
	await expect(page.locator(".toast.success")).toHaveCount(0);
	expect(calls.subscriptionCreates).toHaveLength(1);
});


test.describe("subscription conflict response", () => {
	test.use({ expectedConsoleErrorStatuses: [409] });
	test("keeps detach and cascade-conflict subscription deletion atomic", async ({ page }, testInfo) => {
		test.skip(testInfo.project.name !== "desktop", "stateful subscription workflow is covered once; responsive layout is covered separately");
		const calls = await mockApi(page, { subscriptionLifecycle: true });
		await page.goto("/#proxies");
		const panel = page.locator("section.operations-panel").filter({ has: page.getByRole("heading", { name: "订阅源" }) });

		await panel.getByRole("button", { name: "删除订阅 Detach source" }).click();
		let strategy = page.getByRole("dialog", { name: "选择节点处理方式" });
		await strategy.getByRole("button", { name: "生成删除计划" }).click();
		let confirm = page.getByRole("dialog", { name: "确认删除订阅" });
		await expect(confirm).toContainText("保留 2 个来源节点");
		await confirm.getByRole("checkbox").check();
		await confirm.getByRole("button", { name: "确认删除订阅" }).click();
		await expect(panel.getByText("Detach source", { exact: true })).toHaveCount(0);
		expect(calls.subscriptionNodeCounts["source-detach"]).toBe(2);
		expect(calls.subscriptionDeletes).toContainEqual({ id: "source-detach", strategy: "detach", outcome: "succeeded" });

		await panel.getByRole("button", { name: "删除订阅 Cascade source" }).click();
		strategy = page.getByRole("dialog", { name: "选择节点处理方式" });
		await strategy.getByRole("radio", { name: /级联删除来源节点/ }).check();
		await strategy.getByRole("button", { name: "生成删除计划" }).click();
		confirm = page.getByRole("dialog", { name: "确认删除订阅" });
		await expect(confirm).toContainText("删除 2 个来源节点");
		await confirm.getByRole("checkbox").check();
		await confirm.getByRole("button", { name: "确认删除订阅" }).click();
		await expect(page.locator(".toast.warning")).toContainText("来源节点仍被代理组引用，删除已回滚");
		await expect(confirm).toHaveCount(0);
		await expect(panel.getByText("Cascade source", { exact: true })).toBeVisible();
		expect(calls.subscriptionNodeCounts["source-cascade"]).toBe(2);
		expect(calls.subscriptionDeletes).toContainEqual({ id: "source-cascade", strategy: "cascade", outcome: "conflict" });
		await panel.getByRole("button", { name: "删除订阅 Cascade source" }).click();
		strategy = page.getByRole("dialog", { name: "选择节点处理方式" });
		await strategy.getByRole("radio", { name: /级联删除来源节点/ }).check();
		await strategy.getByRole("button", { name: "生成删除计划" }).click();
		await expect(page.getByRole("dialog", { name: "确认删除订阅" })).toBeVisible();
	});
});

test("reports Mihomo publish success only after the persisted job succeeds", async ({ page }, testInfo) => {
  const calls = await mockApi(page);
  await page.goto("/#proxies");
  await page.getByRole("button", { name: "生成预览" }).click();
  if (testInfo.project.name === "mobile") await expectLayoutIntegrity(page, "Mihomo preview", true);
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
