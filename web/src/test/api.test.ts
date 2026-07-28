import { http, HttpResponse } from "msw";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError, applyMihomoConfig, authenticateApiToken, commandRouterContainer, deleteBrowserSession, deleteProxyGroup, getApiToken, getSiteManifest, loadLiveResources, loadLiveSnapshot, planDeviceEgress, previewMihomoConfig, restoreBrowserSession, saveApiToken, subscribeSessionInvalidation, type DevicePolicy, type EgressPlan, type MihomoDraft } from "../api";
import { server } from "./setup";

const token = "a".repeat(40);

function okHandlers() {
  return [
    http.get("/api/v1/routeros/overview", () => HttpResponse.json({ configured: true, online: true, interfaces: [], devices: [] })),
    http.get("/api/v1/mihomo/overview", () => HttpResponse.json({ configured: true, online: true })),
    http.get("/api/v1/mosdns/overview", () => HttpResponse.json({ configured: true, online: true })),
    http.get("/api/v1/nodes", () => HttpResponse.json([])),
    http.get("/api/v1/routeros/l2tp", () => HttpResponse.json([])),
    http.get("/api/v1/routeros/routes", () => HttpResponse.json([])),
    http.get("/api/v1/routeros/dhcp-servers", () => HttpResponse.json([])),
    http.get("/api/v1/routeros/containers", () => HttpResponse.json([])),
    http.get("/api/v1/devices", () => HttpResponse.json({ sourceAvailable: true, observedAt: "2026-07-27T00:00:00Z", devices: [] })),
    http.get("/api/v1/device-policies", () => HttpResponse.json([])),
    http.get("/api/v1/proxy-groups", () => HttpResponse.json([])),
    http.get("/api/v1/audit-events", () => HttpResponse.json([])),
    http.get("/api/v1/egress/capabilities", () => HttpResponse.json({ routerosConfigured: true, routerosOnline: true, modes: [] })),
    http.get("/api/v1/routeros/dhcp/address-plan", () => HttpResponse.json({ configured: true, plan: { stateDigest: "d".repeat(64), ready: true, servers: [] } })),
    http.get("/api/v1/jobs", () => HttpResponse.json([])),
  ];
}

describe("FoxOS browser API", () => {
  beforeEach(() => {
    window.localStorage.clear();
    window.sessionStorage.clear();
    saveApiToken(token);
  });

  it("keeps each resource result when another endpoint fails", async () => {
    server.use(
      http.get("/api/v1/mihomo/overview", () => HttpResponse.json({ message: "controller unavailable" }, { status: 503 })),
      ...okHandlers(),
    );

    const snapshot = await loadLiveSnapshot();

    expect(snapshot.routeros.ok).toBe(true);
    expect(snapshot.nodes.ok).toBe(true);
    expect(snapshot.mihomo).toMatchObject({ ok: false });
    expect(snapshot.mihomo.ok ? "" : snapshot.mihomo.error).toContain("controller unavailable");
  });

  it("loads the public site manifest without sending a bearer token", async () => {
    let authorization = "not-observed";
    server.use(http.get("/api/v1/site", ({ request }) => {
      authorization = request.headers.get("Authorization") ?? "";
      return HttpResponse.json({ managementBridge: "lan", storageRoot: "storage", network: "192.168.40.0/24", publicHostname: "foxos.home.arpa", protectedAddresses: [], services: {}, https: { enabled: true, trustRequired: true } });
    }));
    const site = await getSiteManifest();
    expect(site.network).toBe("192.168.40.0/24");
    expect(authorization).toBe("");
  });

	it("keeps the bearer token only in page memory", () => {
		expect(getApiToken()).toBe(token);
		expect(window.localStorage.getItem("foxos.apiToken")).toBeNull();
		expect(window.sessionStorage.getItem("foxos.apiToken")).toBeNull();
	});

	it("exchanges the API token once and uses cookie plus CSRF afterwards", async () => {
		let sessionBody: unknown;
		let commandAuthorization = "not-observed";
		let commandCSRF = "";
		server.use(
			http.post("/api/v1/session", async ({ request }) => {
				sessionBody = await request.json();
				expect(request.headers.get("Authorization")).toBeNull();
				return HttpResponse.json({ csrfToken: "c".repeat(64), expiresAt: "2099-01-01T00:00:00Z" }, { status: 201 });
			}),
			http.post("/api/v1/routeros/containers/:id/commands/:command", ({ request }) => {
				commandAuthorization = request.headers.get("Authorization") ?? "";
				commandCSRF = request.headers.get("X-FoxOS-CSRF") ?? "";
				return HttpResponse.json({ status: "QUEUED", job: { id: "job-session", kind: "routeros.container-command", status: "QUEUED", progress: 0, attempts: 0, createdAt: "2026-07-27T00:00:00Z", updatedAt: "2026-07-27T00:00:00Z" } }, { status: 202 });
			}),
		);

		await authenticateApiToken(token);
		await commandRouterContainer("*c1", "foxos:active", "restart", "session-test-0123456789");

		expect(sessionBody).toEqual({ token });
		expect(getApiToken()).toBe("");
		expect(commandAuthorization).toBe("");
		expect(commandCSRF).toBe("c".repeat(64));
	});

	it("keeps stale 401 responses and probes from clearing a newer browser session", async () => {
		let sessionCreates = 0;
		let sessionDeletes = 0;
		let releaseNodeRead: (() => void) | undefined;
		let nodeReadStarted: (() => void) | undefined;
		const started = new Promise<void>((resolve) => { nodeReadStarted = resolve; });
		const gate = new Promise<void>((resolve) => { releaseNodeRead = resolve; });
		server.use(
			http.post("/api/v1/session", () => {
				sessionCreates += 1;
				return HttpResponse.json({ csrfToken: String(sessionCreates).repeat(64), expiresAt: "2099-01-01T00:00:00Z" }, { status: 201 });
			}),
			http.delete("/api/v1/session", () => {
				sessionDeletes += 1;
				return new HttpResponse(null, { status: 204 });
			}),
			http.get("/api/v1/session", () => HttpResponse.json({ error: "unauthorized" }, { status: 401 })),
			http.get("/api/v1/nodes", async () => {
				nodeReadStarted?.();
				await gate;
				return HttpResponse.json({ error: "unauthorized" }, { status: 401 });
			}),
		);

		await authenticateApiToken("1".repeat(40));
		const staleRead = loadLiveResources(["nodes"]);
		await started;
		await deleteBrowserSession();
		await authenticateApiToken("2".repeat(40));
		releaseNodeRead?.();
		await staleRead;
		await deleteBrowserSession();

		expect(sessionCreates).toBe(2);
		expect(sessionDeletes).toBe(2);
		await expect(restoreBrowserSession()).resolves.toBe(false);
	});

	it("invalidates an authenticated session when its expiry probe returns 401", async () => {
		let nodeReads = 0;
		const invalidated = vi.fn();
		server.use(
			http.post("/api/v1/session", () => HttpResponse.json({ csrfToken: "e".repeat(64), expiresAt: "2000-01-01T00:00:00Z" }, { status: 201 })),
			http.get("/api/v1/session", () => HttpResponse.json({ error: "unauthorized", message: "浏览器会话已过期" }, { status: 401 })),
			http.get("/api/v1/nodes", () => {
				nodeReads += 1;
				return HttpResponse.json([]);
			}),
		);
		await authenticateApiToken("e".repeat(40));
		const unsubscribe = subscribeSessionInvalidation(invalidated);

		const result = await loadLiveResources(["nodes"]);

		unsubscribe();
		expect(result.nodes).toMatchObject({ ok: false });
		expect(nodeReads).toBe(0);
		expect(invalidated).toHaveBeenCalledTimes(1);
		await expect(restoreBrowserSession()).resolves.toBe(false);
	});

	it("keeps or restores the existing browser session after a replacement token is rejected", async () => {
		let sessionCreates = 0;
		let commandCSRF = "";
		server.use(
			http.post("/api/v1/session", () => {
				sessionCreates += 1;
				if (sessionCreates === 2) return HttpResponse.json({ error: "unauthorized", message: "Token 无效" }, { status: 401 });
				return HttpResponse.json({ csrfToken: "f".repeat(64), expiresAt: "2099-01-01T00:00:00Z" }, { status: 201 });
			}),
			http.get("/api/v1/session", () => HttpResponse.json({ csrfToken: "f".repeat(64), expiresAt: "2099-01-01T00:00:00Z" })),
			http.post("/api/v1/routeros/containers/:id/commands/:command", ({ request }) => {
				commandCSRF = request.headers.get("X-FoxOS-CSRF") ?? "";
				return HttpResponse.json({ status: "QUEUED", job: { id: "job-replacement", kind: "routeros.container-command", status: "QUEUED", progress: 0, attempts: 0, createdAt: "2026-07-27T00:00:00Z", updatedAt: "2026-07-27T00:00:00Z" } }, { status: 202 });
			}),
		);
		await authenticateApiToken("f".repeat(40));

		await expect(authenticateApiToken("x".repeat(40))).rejects.toMatchObject({ status: 401 });
		await expect(restoreBrowserSession()).resolves.toBe(true);
		await commandRouterContainer("*c1", "foxos:active", "restart", "replacement-session-test-01");

		expect(commandCSRF).toBe("f".repeat(64));
	});

	it("migrates and clears a legacy session token only once", async () => {
		const legacyToken = "l".repeat(40);
		window.sessionStorage.setItem("foxos.apiToken", legacyToken);
		let exchanged = "";
		server.use(
			http.get("/api/v1/session", () => HttpResponse.json({ error: "unauthorized" }, { status: 401 })),
			http.post("/api/v1/session", async ({ request }) => {
				exchanged = String((await request.json() as { token?: string }).token ?? "");
				return HttpResponse.json({ csrfToken: "d".repeat(64), expiresAt: "2099-01-01T00:00:00Z" }, { status: 201 });
			}),
			http.get("/api/v1/nodes", () => HttpResponse.json([])),
		);
		vi.resetModules();
		const migrated = await import("../api");
		expect(migrated.getApiToken()).toBe("");
		expect(window.sessionStorage.getItem("foxos.apiToken")).toBeNull();
		await migrated.loadLiveResources(["nodes"]);
		expect(exchanged).toBe(legacyToken);

		vi.resetModules();
		const refreshed = await import("../api");
		expect(refreshed.getApiToken()).toBe("");
	});

  it("attempts all independent endpoints even when several fail", async () => {
    const requested: string[] = [];
    const record = (request: Request) => requested.push(new URL(request.url).pathname);
    server.use(
      http.get("/api/v1/routeros/overview", ({ request }) => { requested.push(new URL(request.url).pathname); return HttpResponse.error(); }),
      http.get("/api/v1/mosdns/overview", ({ request }) => { requested.push(new URL(request.url).pathname); return HttpResponse.error(); }),
      http.get("/api/v1/mihomo/overview", ({ request }) => { record(request); return HttpResponse.json({ configured: true, online: true }); }),
      http.get("/api/v1/nodes", ({ request }) => { record(request); return HttpResponse.json([]); }),
      http.get("/api/v1/routeros/l2tp", ({ request }) => { record(request); return HttpResponse.json([]); }),
      http.get("/api/v1/routeros/routes", ({ request }) => { record(request); return HttpResponse.json([]); }),
      http.get("/api/v1/routeros/dhcp-servers", ({ request }) => { record(request); return HttpResponse.json([]); }),
      http.get("/api/v1/routeros/containers", ({ request }) => { record(request); return HttpResponse.json([]); }),
      http.get("/api/v1/devices", ({ request }) => { record(request); return HttpResponse.json({ sourceAvailable: true, devices: [] }); }),
      http.get("/api/v1/device-policies", ({ request }) => { record(request); return HttpResponse.json([]); }),
      http.get("/api/v1/proxy-groups", ({ request }) => { record(request); return HttpResponse.json([]); }),
      http.get("/api/v1/audit-events", ({ request }) => { record(request); return HttpResponse.json([]); }),
      http.get("/api/v1/egress/capabilities", ({ request }) => { record(request); return HttpResponse.json({ routerosConfigured: true, routerosOnline: true, modes: [] }); }),
      http.get("/api/v1/routeros/dhcp/address-plan", ({ request }) => { record(request); return HttpResponse.json({ configured: true, plan: { stateDigest: "d".repeat(64), ready: true, servers: [] } }); }),
      http.get("/api/v1/jobs", ({ request }) => { record(request); return HttpResponse.json([]); }),
    );
    await loadLiveSnapshot();
    expect(new Set(requested)).toEqual(new Set([
      "/api/v1/routeros/overview",
      "/api/v1/mihomo/overview",
      "/api/v1/mosdns/overview",
      "/api/v1/nodes",
      "/api/v1/routeros/l2tp",
      "/api/v1/routeros/routes",
      "/api/v1/routeros/dhcp-servers",
      "/api/v1/routeros/containers",
      "/api/v1/devices",
      "/api/v1/device-policies",
      "/api/v1/proxy-groups",
      "/api/v1/audit-events",
      "/api/v1/egress/capabilities",
      "/api/v1/routeros/dhcp/address-plan",
      "/api/v1/jobs",
    ]));
  });

  it("sends Mihomo preview and publish payloads without inventing success", async () => {
    const draft: MihomoDraft = { mode: "rule", mixedPort: 7890, allowLan: false, rules: ["MATCH,DIRECT"] };
    let appliedBody = "";
    server.use(
      http.post("/api/v1/mihomo/config/preview", async ({ request }) => {
        expect(await request.json()).toEqual(draft);
        return HttpResponse.json({ preview: { draft, digest: "d".repeat(64), yaml: "mode: rule", diff: "+mode", hasSecret: true }, confirmationToken: "token", expiresInSeconds: 300 });
      }),
      http.post("/api/v1/mihomo/config/apply", async ({ request }) => {
        appliedBody = JSON.stringify(await request.json());
        return HttpResponse.json({ status: "SUCCEEDED", snapshotId: "snapshot-1" });
      }),
    );
    const preview = await previewMihomoConfig(draft);
    expect(preview.preview.hasSecret).toBe(true);
    await applyMihomoConfig(draft, preview.preview.digest, preview.confirmationToken);
    expect(appliedBody).toContain("confirmationToken");
    expect(appliedBody).toContain(preview.preview.digest);
  });

  it("keeps RouterOS egress plans explicit and surfaces prerequisite failures", async () => {
    const policy: DevicePolicy = { id: "device-1", name: "Laptop", macAddress: "AA:BB:CC:DD:EE:FF", staticIp: "192.168.1.20", dhcpServer: "dhcp-lan", egress: "blocked" };
    const plan: EgressPlan = { policyId: "device-1", staticIp: "192.168.1.20", egress: "blocked", policy, stateDigest: "d".repeat(64), operations: [{ method: "PUT", path: "/rest/ip/firewall/filter", summary: "reject", ownedComment: "foxos:egress:device-1" }], warnings: ["anchor"], requiresConfirmation: true };
    server.use(http.post("/api/v1/routeros/plans/egress/device-1", async ({ request }) => {
      expect(await request.json()).toEqual(policy);
      return HttpResponse.json({ plan, confirmationToken: "confirmation", expiresInSeconds: 300 });
    }));
    const result = await planDeviceEgress("device-1", policy);
    expect(result.plan.requiresConfirmation).toBe(true);
    expect(result.plan.operations[0].ownedComment).toMatch(/^foxos:/);
  });

  it("preserves a direct database-only plan without inventing RouterOS operations", async () => {
    const policy: DevicePolicy = { id: "device-2", name: "Phone", macAddress: "AA:BB:CC:DD:EE:00", staticIp: "192.168.1.21", dhcpServer: "dhcp-lan", egress: "direct" };
    const plan: EgressPlan = { policyId: policy.id, staticIp: policy.staticIp, egress: "direct", policy, stateDigest: "e".repeat(64), operations: [], warnings: ["persist after verify"], requiresConfirmation: true };
    server.use(http.post("/api/v1/routeros/plans/egress/device-2", () => HttpResponse.json({ plan, confirmationToken: "confirmation", expiresInSeconds: 300 })));
    const result = await planDeviceEgress(policy.id, policy);
    expect(result.plan.operations).toEqual([]);
    expect(result.plan.requiresConfirmation).toBe(true);
  });

  it("submits an owned RouterOS container command with the caller idempotency key", async () => {
    let body: unknown;
    server.use(http.post("/api/v1/routeros/containers/:id/commands/:command", async ({ params, request }) => {
      expect(params).toEqual({ id: "*c1", command: "restart" });
      body = await request.json();
      return HttpResponse.json({ status: "QUEUED", job: { id: "job-container", kind: "routeros.container-command", status: "QUEUED", progress: 0, attempts: 0, createdAt: "2026-07-27T00:00:00Z", updatedAt: "2026-07-27T00:00:00Z" } }, { status: 202 });
    }));

    const result = await commandRouterContainer("*c1", "foxos:active", "restart", "container-restart-0123456789");
    expect(result.job.kind).toBe("routeros.container-command");
    expect(body).toEqual({ owner: "foxos:active", idempotencyKey: "container-restart-0123456789" });
  });

  it("keeps structured references on deletion conflicts", async () => {
    server.use(http.delete("/api/v1/proxy-groups/chain-a", () => HttpResponse.json({ error: "group_in_use", message: "proxy group is referenced", references: ["device-policy:laptop"] }, { status: 409 })));
    await expect(deleteProxyGroup("chain-a")).rejects.toMatchObject({
      name: "ApiError",
      status: 409,
      code: "group_in_use",
      references: ["device-policy:laptop"],
    } satisfies Partial<ApiError>);
  });
});
