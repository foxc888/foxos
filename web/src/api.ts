export type ApiNode = {
  id: string;
  name: string;
  type: string;
  server: string;
  port: number;
  network?: string;
  sni?: string;
  udp?: boolean;
  tls?: boolean;
  hasCredential: boolean;
};

export type SiteService = {
  address: string;
  port: number;
  url?: string;
};

export type SiteManifest = {
  managementBridge: string;
  storageRoot: string;
  network: string;
  publicHostname: string;
  publicUrl?: string;
  httpRedirectUrl?: string;
  protectedAddresses: string[];
  services: Record<"routeros" | "mihomo" | "mosdns" | "foxos", SiteService>;
  https: {
    enabled: boolean;
    caSha256?: string;
    caDownloadPath?: string;
    trustRequired: boolean;
  };
};

export type RouterDevice = {
  macAddress: string;
  address: string;
  hostName?: string;
  interface?: string;
  dhcpServer?: string;
  status: string;
  dynamic: boolean;
  lastSeen?: string;
};

export type DeviceProfile = {
  macAddress: string;
  alias?: string;
  tags: string[];
  vendor?: string;
  hostName?: string;
  ipAddress?: string;
  interface?: string;
  dhcpServer?: string;
  online: boolean;
  lastKnownOnline: boolean;
  status: "online" | "offline" | "unavailable";
  firstSeen: string;
  lastSeen?: string;
  updatedAt: string;
};

export type DeviceInventory = {
  sourceAvailable: boolean;
  observedAt?: string;
  error?: string;
  devices: DeviceProfile[];
};

export type DevicePresenceEvent = {
  id: number;
  online: boolean;
  observedAt: string;
};

export type RouterOverview = {
  configured: boolean;
  online: boolean;
  resource?: Record<string, string>;
  interfaces?: Array<Record<string, string>>;
  devices?: RouterDevice[];
  error?: string;
};

export type MihomoOverview = {
  configured: boolean;
  online: boolean;
  version?: string;
  proxies?: Record<string, {
    name?: string;
    type?: string;
    now?: string;
    all?: string[];
    alive?: boolean;
    history?: Array<{ time?: string; delay?: number }>;
  }>;
  connections?: Array<Record<string, unknown>>;
  traffic?: {
    uploadTotal?: number;
    downloadTotal?: number;
    connectionCount?: number;
  };
  partial?: string[];
  error?: string;
};

export type MosDNSOverview = {
  configured: boolean;
  online: boolean;
  address?: string;
  error?: string;
};

export type L2TPClient = {
  id: string;
  name: string;
  connectTo: string;
  user: string;
  running: boolean;
  disabled: boolean;
  ownedByFoxos: boolean;
  addDefaultRoute: boolean;
  defaultRouteDistance?: string;
  usePeerDns: boolean;
  profile?: string;
};

export type RouterRoute = {
  ".id": string;
  "dst-address": string;
  gateway: string;
  distance: string;
  active: string;
  disabled: string;
  comment?: string;
};

export type RouterDHCPServer = {
  ".id": string;
  name: string;
  interface: string;
  "address-pool": string;
  disabled: string;
  running: string;
};

export type RouterContainer = {
  ".id": string;
  name: string;
  comment?: string;
  status: string;
  "root-dir": string;
  interface: string;
  "start-on-boot": string;
};

export type DHCPRange = {
  start: string;
  end: string;
  capacity: number;
};

export type DHCPConflict = {
  address?: string;
  kind: string;
  detail: string;
};

export type DHCPServerCapacity = {
  serverName: string;
  interface: string;
  running: boolean;
  poolNames: string[];
  network?: string;
  gateway?: string;
  ranges: DHCPRange[];
  configuredCapacity: number;
  excludedWithinPool: number;
  dynamicCapacity: number;
  dynamicOccupied: number;
  remaining: number;
  utilizationPercent: number;
  reservationSpaceCapacity: number;
  reservationUsed: number;
  reservationRemaining: number;
  risk: "normal" | "warning" | "critical" | "exhausted" | "unknown";
  conflicts: DHCPConflict[];
  ready: boolean;
  error?: string;
};

export type DHCPAddressPlan = {
  stateDigest: string;
  servers: DHCPServerCapacity[];
  ready: boolean;
};

export type DHCPExpansionAlternative = {
  strategy: "expand-to-/23" | "split-vlan" | string;
  executable: boolean;
  capacity: number;
  impact: string[];
};

export type DHCPExpansionPlan = {
  serverName: string;
  poolId?: string;
  poolName?: string;
  network?: string;
  currentRanges?: string;
  proposedRanges?: string;
  requestedCapacity: number;
  suggestedRanges: DHCPRange[];
  before: DHCPServerCapacity;
  after?: DHCPServerCapacity;
  alternatives: DHCPExpansionAlternative[];
  stateDigest: string;
  operation?: { method: string; path: string; summary: string };
  warnings: string[];
  executable: boolean;
  requiresConfirmation: boolean;
};

export type AuditEvent = {
  id: string;
  action: string;
  targetId: string;
  outcome: "STARTED" | "SUCCEEDED" | "FAILED";
  details?: Record<string, unknown>;
  createdAt: string;
  updatedAt: string;
};

export type EgressType = "direct" | "mihomo-node" | "proxy-chain" | "l2tp" | "blocked";

export type EgressCapability = {
  mode: EgressType;
  available: boolean;
  experimental: boolean;
  missing: string[];
  evidence?: string[];
};

export type EgressCapabilities = {
  routerosConfigured: boolean;
  routerosOnline?: boolean;
  modes: EgressCapability[];
  error?: string;
};

export type DevicePolicy = {
  id: string;
  name: string;
  macAddress: string;
  staticIp: string;
  dhcpServer: string;
  egress: EgressType;
  targetId?: string;
};

export type ProxyGroup = {
  id: string;
  name: string;
  type: "select" | "url-test" | "fallback" | "load-balance" | "chain";
  nodeIds: string[];
  groupIds?: string[];
  url?: string;
  interval?: number;
  tolerance?: number;
  strategy?: string;
};

export type LoadResult<T> =
  | { ok: true; data: T; loadedAt: string }
  | { ok: false; error: string; loadedAt: string };

export type LiveSnapshot = {
  routeros: LoadResult<RouterOverview>;
  mihomo: LoadResult<MihomoOverview>;
  mosdns: LoadResult<MosDNSOverview>;
  nodes: LoadResult<ApiNode[]>;
  l2tp: LoadResult<L2TPClient[]>;
  routes: LoadResult<RouterRoute[]>;
  dhcpServers: LoadResult<RouterDHCPServer[]>;
  containers: LoadResult<RouterContainer[]>;
  deviceInventory: LoadResult<DeviceInventory>;
  policies: LoadResult<DevicePolicy[]>;
  groups: LoadResult<ProxyGroup[]>;
  audit: LoadResult<AuditEvent[]>;
  capabilities: LoadResult<EgressCapabilities>;
  dhcpAddressPlan: LoadResult<DHCPAddressPlan>;
  jobs: LoadResult<Job[]>;
};

export type LiveResourceName = keyof LiveSnapshot;

export type NodeProbe = {
  reachable: boolean;
  latencyMs: number;
  error?: string;
};

export type MihomoProbeResult = {
  nodeId: string;
  nodeName: string;
  nodeHttp: { available: boolean; success: boolean; latencyMs?: number; error?: string };
  exit: { available: boolean; success: boolean; latencyMs?: number; error?: string; scope: "current-policy"; ipAddress?: string };
};

export type DeviceBindingPlan = {
  policyId: string;
  operations: Array<{ method: string; path: string; summary: string }>;
  warnings: string[];
  requiresConfirmation: boolean;
};

export type EgressOperation = {
  method: string;
  path: string;
  body?: Record<string, string>;
  summary: string;
  ownedComment: string;
  rollback?: EgressOperation;
};

export type EgressPlan = {
  policyId: string;
  staticIp: string;
  egress: EgressType;
  targetId?: string;
  policy: DevicePolicy;
  previousPolicy?: DevicePolicy;
  stateDigest: string;
  operations: EgressOperation[];
  warnings: string[];
  requiresConfirmation: boolean;
};

export class ApiError extends Error {
  readonly status: number;
  readonly code?: string;
  readonly references: string[];

  constructor(message: string, status: number, code?: string, references: string[] = []) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
    this.references = references;
  }
}

export type MihomoDraft = {
  id?: string;
  mode: string;
  mixedPort: number;
  allowLan: boolean;
  rules: string[];
  revision?: number;
  updatedAt?: string;
};

export type MihomoPreview = {
  draft: MihomoDraft;
  digest: string;
  yaml: string;
  diff: string;
  hasSecret: boolean;
};

export type MihomoSnapshot = {
  id: string;
  digest: string;
  label: string;
  createdAt: string;
};

export type Job = {
  id: string;
  kind: string;
  status: "QUEUED" | "RUNNING" | "VERIFYING" | "SUCCEEDED" | "FAILED" | "ROLLED_BACK";
  progress: number;
  result?: Record<string, unknown>;
  errorClass?: string;
  errorMessage?: string;
  attempts: number;
  createdAt: string;
  updatedAt: string;
};

export type Subscription = {
  id: string;
  name: string;
  url: string;
  enabled: boolean;
  interval: number;
  lastDigest?: string;
  lastSuccessAt?: string;
  lastAttemptAt?: string;
  lastError?: string;
};

export type SubscriptionUpdatePlan = {
  action: "subscription.update";
  subscriptionId: string;
  digest: string;
  nodeIds: string[];
  removedNodeIds: string[];
  existingCount: number;
  nodeCount: number;
  addCount: number;
  updateCount: number;
  removeCount: number;
	parseValidCount: number;
	parseSkippedCount: number;
	parseErrors?: Array<{ index: number; protocol?: string; reason: string }>;
	suspiciousReduction: boolean;
};

export type SubscriptionDeletePlan = {
  action: "subscription.delete";
	strategy: SubscriptionDeleteStrategy;
  subscriptionId: string;
  nodeIds: string[];
  nodeCount: number;
};

export type SubscriptionDeleteStrategy = "detach" | "cascade";

export type Alert = {
  id: string;
  key: string;
  severity: "info" | "warning" | "critical";
  title: string;
  message: string;
  acknowledged: boolean;
  firstSeen: string;
  lastSeen: string;
  resolvedAt?: string;
};

export type BackupManifest = {
  id: string;
  label: string;
  createdAt: string;
  database: string;
  mihomo?: string;
  fileCount: number;
  checksums: Record<string, string>;
};

const tokenKey = "foxos.apiToken";
export type BrowserSession = {
  csrfToken: string;
  expiresAt: string;
};

let apiToken = "";
let csrfToken = "";
let sessionExpiresAt = "";
let sessionState: "unknown" | "authenticated" | "none" = "unknown";
let sessionEpoch = 0;
let sessionProbe: { epoch: number; promise: Promise<boolean> } | null = null;
let legacyToken = migrateLegacyToken();
const sessionInvalidationListeners = new Set<() => void>();

function nextSessionEpoch(): number {
	sessionEpoch += 1;
	sessionProbe = null;
	return sessionEpoch;
}

function clearSessionState(state: "unknown" | "none" = "none"): void {
	apiToken = "";
	csrfToken = "";
	sessionExpiresAt = "";
	sessionState = state;
}

export function subscribeSessionInvalidation(listener: () => void): () => void {
	sessionInvalidationListeners.add(listener);
	return () => sessionInvalidationListeners.delete(listener);
}

function invalidateCurrentSession(): void {
	nextSessionEpoch();
	clearSessionState();
	for (const listener of sessionInvalidationListeners) listener();
}

function migrateLegacyToken(): string {
  if (typeof window === "undefined") return "";
  let legacy = "";
  try {
    legacy = window.sessionStorage.getItem(tokenKey)?.trim() ?? "";
    window.sessionStorage.removeItem(tokenKey);
    window.localStorage.removeItem(tokenKey);
  } catch {
    return "";
  }
  return legacy.length >= 32 ? legacy : "";
}

export function getApiToken(): string {
  return apiToken;
}

export function saveApiToken(token: string): void {
  const value = token.trim();
  if (value.length < 32) {
    throw new Error("FoxOS API Token 至少需要 32 个字符");
  }
	nextSessionEpoch();
	clearSessionState();
	apiToken = value;
	clearLegacyStorage();
}

function clearLegacyStorage(): void {
	if (typeof window === "undefined") return;
	try {
		window.localStorage.removeItem(tokenKey);
		window.sessionStorage.removeItem(tokenKey);
	} catch {
		// Storage may be disabled. Authentication state remains memory-only.
	}
}

function validSession(value: unknown): value is BrowserSession {
	if (!value || typeof value !== "object") return false;
	const candidate = value as Partial<BrowserSession>;
	return typeof candidate.csrfToken === "string" && candidate.csrfToken.length === 64
		&& typeof candidate.expiresAt === "string" && !Number.isNaN(Date.parse(candidate.expiresAt));
}

async function responseError(response: Response): Promise<ApiError> {
	const problem = await response.json().catch(() => null) as { message?: string; error?: string; references?: unknown } | null;
	const references = Array.isArray(problem?.references) ? problem.references.filter((item): item is string => typeof item === "string") : [];
	return new ApiError(problem?.message ?? `FoxOS API 请求失败（${response.status}）`, response.status, problem?.error, references);
}

async function createBrowserSession(token: string): Promise<BrowserSession> {
	const value = token.trim();
	if (value.length < 32) throw new Error("FoxOS API Token 至少需要 32 个字符");
	const epoch = nextSessionEpoch();
	const response = await fetch(new URL("/api/v1/session", window.location.origin), {
		method: "POST",
		credentials: "same-origin",
		headers: { Accept: "application/json", "Content-Type": "application/json" },
		body: JSON.stringify({ token: value }),
	});
	if (!response.ok) {
		const error = await responseError(response);
		throw error;
	}
	const session = await response.json() as unknown;
	if (!validSession(session)) throw new Error("FoxOS 返回了无效的浏览器会话");
	if (epoch !== sessionEpoch) throw new Error("浏览器会话已变化，请重试");
	csrfToken = session.csrfToken;
	sessionExpiresAt = session.expiresAt;
	sessionState = "authenticated";
	apiToken = "";
	legacyToken = "";
	clearLegacyStorage();
	return session;
}

export async function authenticateApiToken(token: string): Promise<BrowserSession> {
	return createBrowserSession(token);
}

export async function restoreBrowserSession(): Promise<boolean> {
	if (apiToken) return true;
	if (sessionState === "authenticated" && csrfToken && Date.parse(sessionExpiresAt) > Date.now()) return true;
	if (sessionState === "none" && !legacyToken) return false;
	const invalidateOnUnauthorized = sessionState === "authenticated";
	const epoch = sessionEpoch;
	if (sessionProbe?.epoch === epoch) return sessionProbe.promise;
	const promise = (async () => {
		try {
			const response = await fetch(new URL("/api/v1/session", window.location.origin), {
				credentials: "same-origin",
				headers: { Accept: "application/json" },
			});
			if (epoch !== sessionEpoch) return restoreBrowserSession();
			if (response.ok) {
				const session = await response.json() as unknown;
				if (!validSession(session)) throw new Error("FoxOS 返回了无效的浏览器会话");
				if (epoch !== sessionEpoch) return restoreBrowserSession();
				csrfToken = session.csrfToken;
				sessionExpiresAt = session.expiresAt;
				sessionState = "authenticated";
				legacyToken = "";
				return true;
			}
			if (response.status !== 401) throw await responseError(response);
			if (epoch !== sessionEpoch) return restoreBrowserSession();
			if (legacyToken) {
				const token = legacyToken;
				legacyToken = "";
				await createBrowserSession(token);
				return true;
			}
			if (invalidateOnUnauthorized) invalidateCurrentSession();
			else sessionState = "none";
			return false;
		} catch (error) {
			if (epoch !== sessionEpoch) return restoreBrowserSession();
			sessionState = "unknown";
			throw error;
		} finally {
			if (sessionProbe?.epoch === epoch) sessionProbe = null;
		}
	})();
	sessionProbe = { epoch, promise };
	return promise;
}

export async function deleteBrowserSession(): Promise<void> {
	let logoutCSRF = csrfToken;
	const epoch = nextSessionEpoch();
	clearSessionState("unknown");
	legacyToken = "";
	clearLegacyStorage();
	try {
		if (!logoutCSRF) {
			const probe = await fetch(new URL("/api/v1/session", window.location.origin), {
				credentials: "same-origin",
				headers: { Accept: "application/json" },
			});
			if (probe.status === 401) {
				if (epoch === sessionEpoch) clearSessionState();
				return;
			}
			if (!probe.ok) throw await responseError(probe);
			const session = await probe.json() as unknown;
			if (!validSession(session)) throw new Error("FoxOS 返回了无效的浏览器会话");
			logoutCSRF = session.csrfToken;
		}
		const response = await fetch(new URL("/api/v1/session", window.location.origin), {
			method: "DELETE",
			credentials: "same-origin",
			headers: { Accept: "application/json", "X-FoxOS-CSRF": logoutCSRF },
		});
		if (!response.ok && response.status !== 401) throw await responseError(response);
		if (epoch === sessionEpoch) clearSessionState();
	} catch (error) {
		if (epoch === sessionEpoch) {
			csrfToken = logoutCSRF;
			sessionState = logoutCSRF ? "authenticated" : "unknown";
		}
		throw error;
	}
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
	let token = getApiToken();
	  if (!token && !await restoreBrowserSession()) throw new Error("尚未建立 FoxOS 浏览器会话");
	token = getApiToken();
	const epoch = sessionEpoch;
  const method = (init?.method ?? "GET").toUpperCase();
  const response = await fetch(new URL(path, window.location.origin), {
    ...init,
		credentials: "same-origin",
    headers: {
      Accept: "application/json",
			...(token ? { Authorization: `Bearer ${token}` } : {}),
			...(!token && !["GET", "HEAD", "OPTIONS"].includes(method) ? { "X-FoxOS-CSRF": csrfToken } : {}),
      ...(init?.body ? { "Content-Type": "application/json" } : {}),
      ...init?.headers,
    },
  });
	  if (!response.ok) {
			const error = await responseError(response);
			if (response.status === 401 && epoch === sessionEpoch) invalidateCurrentSession();
			throw error;
  }
  if (response.status === 204) {
    return undefined as T;
  }
  return response.json() as Promise<T>;
}

export async function getSiteManifest(): Promise<SiteManifest> {
  const response = await fetch(new URL("/api/v1/site", window.location.origin), {
    headers: { Accept: "application/json" },
  });
  if (!response.ok) {
    throw new Error(`站点清单请求失败（${response.status}）`);
  }
  return response.json() as Promise<SiteManifest>;
}

async function settle<T>(promise: Promise<T>): Promise<LoadResult<T>> {
  try {
    return { ok: true, data: await promise, loadedAt: new Date().toISOString() };
  } catch (error) {
    return {
      ok: false,
      error: error instanceof Error ? error.message : "FoxOS API 请求失败",
      loadedAt: new Date().toISOString(),
    };
  }
}

const liveResourceNames: LiveResourceName[] = ["routeros", "mihomo", "mosdns", "nodes", "l2tp", "routes", "dhcpServers", "containers", "deviceInventory", "policies", "groups", "audit", "capabilities", "dhcpAddressPlan", "jobs"];

const liveLoaders: Record<LiveResourceName, () => Promise<unknown>> = {
  routeros: () => request<RouterOverview>("/api/v1/routeros/overview"),
  mihomo: () => request<MihomoOverview>("/api/v1/mihomo/overview"),
  mosdns: () => request<MosDNSOverview>("/api/v1/mosdns/overview"),
  nodes: () => request<ApiNode[]>("/api/v1/nodes"),
  l2tp: () => request<L2TPClient[]>("/api/v1/routeros/l2tp"),
  routes: () => request<RouterRoute[]>("/api/v1/routeros/routes"),
  dhcpServers: () => request<RouterDHCPServer[]>("/api/v1/routeros/dhcp-servers"),
  containers: () => request<RouterContainer[]>("/api/v1/routeros/containers"),
  deviceInventory: () => request<DeviceInventory>("/api/v1/devices"),
  policies: () => request<DevicePolicy[]>("/api/v1/device-policies"),
  groups: () => request<ProxyGroup[]>("/api/v1/proxy-groups"),
  audit: () => request<AuditEvent[]>("/api/v1/audit-events?limit=100"),
  capabilities: () => request<EgressCapabilities>("/api/v1/egress/capabilities"),
  dhcpAddressPlan: () => request<{ configured: boolean; plan: DHCPAddressPlan }>("/api/v1/routeros/dhcp/address-plan").then((result) => result.plan),
  jobs: () => request<Job[]>("/api/v1/jobs?limit=50"),
};

export async function loadLiveResources(names: LiveResourceName[]): Promise<Partial<LiveSnapshot>> {
  const unique = [...new Set(names)];
  const entries = await Promise.all(unique.map(async (name) => [name, await settle(liveLoaders[name]())] as const));
  return Object.fromEntries(entries) as Partial<LiveSnapshot>;
}

export async function loadLiveSnapshot(): Promise<LiveSnapshot> {
  return await loadLiveResources(liveResourceNames) as LiveSnapshot;
}

export async function getDHCPAddressPlan(): Promise<{ configured: boolean; plan: DHCPAddressPlan }> {
  return request<{ configured: boolean; plan: DHCPAddressPlan }>("/api/v1/routeros/dhcp/address-plan");
}

export async function getEgressCapabilities(): Promise<EgressCapabilities> {
  return request<EgressCapabilities>("/api/v1/egress/capabilities");
}

export async function listJobs(limit = 50): Promise<Job[]> {
  return request<Job[]>(`/api/v1/jobs?limit=${Math.max(1, Math.min(100, Math.trunc(limit)))}`);
}

export async function getRouterContainers(): Promise<RouterContainer[]> {
  return request<RouterContainer[]>("/api/v1/routeros/containers");
}

export async function commandRouterContainer(id: string, owner: string, command: "start" | "stop" | "restart", idempotencyKey: string): Promise<{ status: string; job: Job }> {
  return request<{ status: string; job: Job }>(`/api/v1/routeros/containers/${encodeURIComponent(id)}/commands/${command}`, {
    method: "POST",
    body: JSON.stringify({ owner, idempotencyKey }),
  });
}

export async function previewDHCPExpansion(input: { serverName: string; proposedRanges: string; requestedCapacity: number }): Promise<{ plan: DHCPExpansionPlan; confirmationToken: string; expiresInSeconds: number }> {
  return request<{ plan: DHCPExpansionPlan; confirmationToken: string; expiresInSeconds: number }>("/api/v1/routeros/plans/dhcp-expansion", {
    method: "POST",
    body: JSON.stringify(input),
  });
}

export async function executeDHCPExpansion(plan: DHCPExpansionPlan, confirmationToken: string): Promise<{ status: string; serverName: string; poolName: string; ranges: string; auditId: string }> {
  return request<{ status: string; serverName: string; poolName: string; ranges: string; auditId: string }>("/api/v1/routeros/plans/dhcp-expansion/execute", {
    method: "POST",
    body: JSON.stringify({ plan, confirmationToken }),
  });
}

export async function updateDeviceMetadata(macAddress: string, input: { alias: string; vendor: string; tags: string[] }): Promise<DeviceProfile> {
  return request<DeviceProfile>(`/api/v1/devices/${encodeURIComponent(macAddress)}`, { method: "PUT", body: JSON.stringify(input) });
}

export async function getDevicePresenceHistory(macAddress: string, limit = 100): Promise<DevicePresenceEvent[]> {
  return request<DevicePresenceEvent[]>(`/api/v1/devices/${encodeURIComponent(macAddress)}/history?limit=${limit}`);
}

export async function probeMihomoNode(id: string): Promise<MihomoProbeResult> {
  return request<MihomoProbeResult>(`/api/v1/mihomo/probes/${encodeURIComponent(id)}`, { method: "POST" });
}

export async function deleteNode(id: string): Promise<void> {
  await request<void>(`/api/v1/nodes/${encodeURIComponent(id)}`, { method: "DELETE" });
}

export async function createNode(input: {
  name: string;
  type: string;
  server: string;
  port: number;
  username?: string;
  password?: string;
  uuid?: string;
}): Promise<ApiNode> {
  return request<ApiNode>("/api/v1/nodes", { method: "POST", body: JSON.stringify(input) });
}

export async function importNodeLinks(links: string): Promise<{ format: string; imported: number; skipped: number; errors: Array<{ index: number; protocol?: string; reason: string }>; nodes: ApiNode[] }> {
  return request("/api/v1/nodes/import", { method: "POST", body: JSON.stringify({ links }) });
}

export async function probeNode(id: string): Promise<NodeProbe> {
  return request<NodeProbe>(`/api/v1/nodes/${encodeURIComponent(id)}/probe`, { method: "POST" });
}

export async function planDeviceBinding(input: {
  id: string;
  name: string;
  macAddress: string;
  staticIp: string;
  dhcpServer: string;
  egress: EgressType;
  targetId?: string;
}): Promise<{ plan: DeviceBindingPlan; confirmationToken: string; expiresInSeconds: number }> {
  return request("/api/v1/routeros/plans/device-binding", { method: "POST", body: JSON.stringify(input) });
}

export async function executeDeviceBinding(plan: DeviceBindingPlan, confirmationToken: string): Promise<void> {
  await request("/api/v1/routeros/plans/device-binding/execute", {
    method: "POST",
    body: JSON.stringify({ plan, confirmationToken }),
  });
}

export async function listDevicePolicies(): Promise<DevicePolicy[]> {
  return request<DevicePolicy[]>("/api/v1/device-policies");
}

export async function createDevicePolicy(policy: DevicePolicy): Promise<DevicePolicy> {
  return request<DevicePolicy>("/api/v1/device-policies", { method: "POST", body: JSON.stringify(policy) });
}

export async function updateDevicePolicy(policy: DevicePolicy): Promise<DevicePolicy> {
  return request<DevicePolicy>(`/api/v1/device-policies/${encodeURIComponent(policy.id)}`, { method: "PUT", body: JSON.stringify(policy) });
}

export async function deleteDevicePolicy(id: string): Promise<void> {
  await request<void>(`/api/v1/device-policies/${encodeURIComponent(id)}`, { method: "DELETE" });
}

export async function planDeviceEgress(id: string, policy?: DevicePolicy): Promise<{ plan: EgressPlan; confirmationToken?: string; expiresInSeconds?: number }> {
  return request(`/api/v1/routeros/plans/egress/${encodeURIComponent(id)}`, {
    method: "POST",
    ...(policy ? { body: JSON.stringify(policy) } : {}),
  });
}

export async function executeDeviceEgress(id: string, plan: EgressPlan, confirmationToken: string): Promise<{ status: string; job?: Job; auditId?: string }> {
  return request(`/api/v1/routeros/plans/egress/${encodeURIComponent(id)}/execute`, { method: "POST", body: JSON.stringify({ plan, confirmationToken }) });
}

export async function listProxyGroups(): Promise<ProxyGroup[]> {
  return request<ProxyGroup[]>("/api/v1/proxy-groups");
}

export async function createProxyGroup(group: Omit<ProxyGroup, "id"> & { id?: string }): Promise<ProxyGroup> {
  return request<ProxyGroup>("/api/v1/proxy-groups", { method: "POST", body: JSON.stringify(group) });
}

export async function updateProxyGroup(group: ProxyGroup): Promise<ProxyGroup> {
  return request<ProxyGroup>(`/api/v1/proxy-groups/${encodeURIComponent(group.id)}`, { method: "PUT", body: JSON.stringify(group) });
}

export async function deleteProxyGroup(id: string): Promise<void> {
  await request<void>(`/api/v1/proxy-groups/${encodeURIComponent(id)}`, { method: "DELETE" });
}

export async function getMihomoDraft(): Promise<MihomoDraft> {
  return request<MihomoDraft>("/api/v1/mihomo/draft");
}

export async function saveMihomoDraft(draft: MihomoDraft): Promise<MihomoDraft> {
  return request<MihomoDraft>("/api/v1/mihomo/draft", { method: "PUT", body: JSON.stringify(draft) });
}

export async function previewMihomoConfig(draft: MihomoDraft): Promise<{ preview: MihomoPreview; confirmationToken: string; expiresInSeconds: number }> {
  return request("/api/v1/mihomo/config/preview", { method: "POST", body: JSON.stringify(draft) });
}

export async function applyMihomoConfig(draft: MihomoDraft, digest: string, confirmationToken: string, label = ""): Promise<{ status: string; snapshotId?: string; job?: Job; rolledBack?: boolean }> {
  return request("/api/v1/mihomo/config/apply", { method: "POST", body: JSON.stringify({ draft, digest, confirmationToken, label }) });
}

export async function listMihomoSnapshots(): Promise<MihomoSnapshot[]> {
  return request<MihomoSnapshot[]>("/api/v1/mihomo/snapshots");
}

export async function planMihomoRestore(id: string): Promise<{ plan: { action: string; snapshotId: string }; confirmationToken: string; expiresInSeconds: number }> {
  return request(`/api/v1/mihomo/snapshots/${encodeURIComponent(id)}/restore/plan`, { method: "POST" });
}

export async function restoreMihomoSnapshot(id: string, confirmationToken: string, label = ""): Promise<{ status: string; snapshotId?: string; job?: Job; rolledBack?: boolean }> {
  return request(`/api/v1/mihomo/snapshots/${encodeURIComponent(id)}/restore`, { method: "POST", body: JSON.stringify({ confirmationToken, label }) });
}

export async function getJob(id: string): Promise<Job> {
  return request<Job>(`/api/v1/jobs/${encodeURIComponent(id)}`);
}

export async function retryJob(id: string): Promise<Job> {
  return request<Job>(`/api/v1/jobs/${encodeURIComponent(id)}/retry`, { method: "POST" });
}

export async function waitForJob(id: string, options: { timeoutMs?: number; intervalMs?: number } = {}): Promise<Job> {
  const timeoutMs = options.timeoutMs ?? 120_000;
  const intervalMs = options.intervalMs ?? 600;
  const started = Date.now();
  for (;;) {
    const job = await getJob(id);
    if (["SUCCEEDED", "FAILED", "ROLLED_BACK"].includes(job.status)) return job;
    if (Date.now() - started >= timeoutMs) throw new Error("任务等待超时，请在任务中心查看最终状态");
    await new Promise((resolve) => window.setTimeout(resolve, intervalMs));
  }
}

export async function listSubscriptions(): Promise<Subscription[]> {
  return request<Subscription[]>("/api/v1/subscriptions");
}

export async function createSubscription(input: Omit<Subscription, "id" | "lastDigest" | "lastSuccessAt" | "lastAttemptAt" | "lastError">): Promise<Subscription> {
  return request<Subscription>("/api/v1/subscriptions", { method: "POST", body: JSON.stringify(input) });
}

export async function setSubscriptionEnabled(id: string, enabled: boolean): Promise<Subscription> {
  return request<Subscription>(`/api/v1/subscriptions/${encodeURIComponent(id)}/enabled`, { method: "PATCH", body: JSON.stringify({ enabled }) });
}

export async function planSubscriptionDelete(id: string, strategy: SubscriptionDeleteStrategy): Promise<{ plan: SubscriptionDeletePlan; confirmationToken: string; expiresInSeconds: number; warnings: string[] }> {
	return request(`/api/v1/subscriptions/${encodeURIComponent(id)}/delete/plan`, { method: "POST", body: JSON.stringify({ strategy }) });
}

export async function deleteSubscription(id: string, strategy: SubscriptionDeleteStrategy, confirmationToken: string): Promise<{ status: string; strategy: SubscriptionDeleteStrategy; subscriptionId: string; nodeCount: number }> {
	return request(`/api/v1/subscriptions/${encodeURIComponent(id)}/delete`, { method: "POST", body: JSON.stringify({ strategy, confirmationToken }) });
}

export async function previewSubscription(id: string): Promise<{ digest: string; nodeCount: number; nodes: Array<{ id: string; name: string; type: string; server: string; port: number; hasCredential: boolean }>; plan: SubscriptionUpdatePlan; confirmationToken: string; expiresInSeconds: number }> {
  return request(`/api/v1/subscriptions/${encodeURIComponent(id)}/preview`, { method: "POST" });
}

export async function updateSubscription(id: string, plan: SubscriptionUpdatePlan, confirmationToken: string): Promise<{ status: string; digest?: string; nodeCount?: number; job?: Job }> {
  return request(`/api/v1/subscriptions/${encodeURIComponent(id)}/update`, { method: "POST", body: JSON.stringify({ plan, confirmationToken }) });
}

export async function listAlerts(): Promise<Alert[]> {
  return request<Alert[]>("/api/v1/alerts");
}

export async function acknowledgeAlert(id: string): Promise<void> {
  await request(`/api/v1/alerts/${encodeURIComponent(id)}/acknowledge`, { method: "POST" });
}

export async function listBackups(): Promise<BackupManifest[]> {
  return request<BackupManifest[]>("/api/v1/backups");
}

export async function createBackup(label = ""): Promise<{ manifest?: BackupManifest; job?: Job }> {
  const response = await request<BackupManifest | { job: Job }>("/api/v1/backups", { method: "POST", body: JSON.stringify({ label }) });
  return "id" in response ? { manifest: response } : response;
}

export async function planBackupRestore(id: string): Promise<{ plan: { action: string; backupId: string; digest: string; fileCount: number; mihomo: boolean }; confirmationToken: string; expiresInSeconds: number; warnings: string[] }> {
  return request(`/api/v1/backups/${encodeURIComponent(id)}/restore/plan`, { method: "POST" });
}

export async function restoreBackup(id: string, confirmationToken: string): Promise<{ status: string; backupId: string; job?: Job }> {
  return request(`/api/v1/backups/${encodeURIComponent(id)}/restore`, { method: "POST", body: JSON.stringify({ confirmationToken }) });
}
