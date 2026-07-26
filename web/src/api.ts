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

export type AuditEvent = {
  id: string;
  action: string;
  targetId: string;
  outcome: "STARTED" | "SUCCEEDED" | "FAILED";
  details?: Record<string, unknown>;
  createdAt: string;
  updatedAt: string;
};

export type LiveSnapshot = {
  routeros: RouterOverview;
  mihomo: MihomoOverview;
  nodes: ApiNode[];
  l2tp: L2TPClient[];
  audit: AuditEvent[];
  loadedAt: string;
};

export type NodeProbe = {
  reachable: boolean;
  latencyMs: number;
  error?: string;
};

export type DeviceBindingPlan = {
  policyId: string;
  operations: Array<{ method: string; path: string; summary: string }>;
  warnings: string[];
  requiresConfirmation: boolean;
};

const tokenKey = "foxos.apiToken";

export function getApiToken(): string {
  return window.localStorage.getItem(tokenKey)?.trim() ?? "";
}

export function saveApiToken(token: string): void {
  const value = token.trim();
  if (value.length < 32) {
    throw new Error("FoxOS API Token 至少需要 32 个字符");
  }
  window.localStorage.setItem(tokenKey, value);
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const token = getApiToken();
  if (!token) {
    throw new Error("尚未配置 FoxOS API Token");
  }
  const response = await fetch(path, {
    ...init,
    headers: {
      Accept: "application/json",
      Authorization: `Bearer ${token}`,
      ...(init?.body ? { "Content-Type": "application/json" } : {}),
      ...init?.headers,
    },
  });
  if (!response.ok) {
    const problem = await response.json().catch(() => null) as { message?: string } | null;
    throw new Error(problem?.message ?? `FoxOS API 请求失败（${response.status}）`);
  }
  if (response.status === 204) {
    return undefined as T;
  }
  return response.json() as Promise<T>;
}

export async function loadLiveSnapshot(): Promise<LiveSnapshot> {
  const [routeros, mihomo, nodes, l2tp, audit] = await Promise.all([
    request<RouterOverview>("/api/v1/routeros/overview"),
    request<MihomoOverview>("/api/v1/mihomo/overview"),
    request<ApiNode[]>("/api/v1/nodes"),
    request<L2TPClient[]>("/api/v1/routeros/l2tp").catch(() => []),
    request<AuditEvent[]>("/api/v1/audit-events?limit=100"),
  ]);
  return { routeros, mihomo, nodes, l2tp, audit, loadedAt: new Date().toISOString() };
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

export async function importNodeLinks(links: string): Promise<{ imported: number; nodes: ApiNode[] }> {
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
  egress: "direct" | "mihomo-node" | "proxy-chain" | "l2tp" | "blocked";
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
