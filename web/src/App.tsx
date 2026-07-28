import { FormEvent, useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import {
  Activity,
  AlertTriangle,
  ArrowRight,
  Ban,
  Calculator,
  Check,
  CheckCircle2,
  ChevronLeft,
  ChevronRight,
  CircleGauge,
  Square,
  Database,
  Download,
  FileText,
  Globe2,
  HardDrive,
  Home,
  Laptop,
  Link2,
  ListChecks,
  LockKeyhole,
  Menu,
  Monitor,
  Network,
  Play,
  Plus,
  RefreshCw,
  Router,
  Save,
  Search,
  Server,
  Settings,
  ShieldCheck,
  Smartphone,
  Trash2,
  Tv,
  Upload,
  Users,
  Wifi,
  X,
  Zap,
} from "lucide-react";
import {
  ApiError,
  type ApiNode,
  type AuditEvent,
  commandRouterContainer,
  createProxyGroup,
  createNode as createNodeApi,
  deleteNode as deleteNodeApi,
  deleteProxyGroup,
  type DeviceBindingPlan,
  type DHCPAddressPlan,
  type DeviceInventory,
  type DevicePolicy,
  type DevicePresenceEvent,
  type DeviceProfile,
  type EgressPlan,
  type EgressCapabilities,
  type EgressType,
  executeDeviceBinding,
  executeDeviceEgress,
  getDevicePresenceHistory,
  getRouterContainers,
  getSiteManifest,
  importNodeLinks,
	listJobs,
  type Job,
  type L2TPClient,
  loadLiveSnapshot,
  loadLiveResources,
  type LiveSnapshot,
  type LiveResourceName,
  type MihomoOverview,
  type MihomoProbeResult,
  type MosDNSOverview,
  planDeviceBinding,
  planDeviceEgress,
  probeNode,
  probeMihomoNode,
  retryJob,
  type ProxyGroup,
  type RouterContainer,
  type RouterDHCPServer,
  type RouterOverview,
  type RouterRoute,
  type SiteManifest,
  authenticateApiToken,
  deleteBrowserSession,
  restoreBrowserSession,
  subscribeSessionInvalidation,
  updateProxyGroup,
  updateDeviceMetadata,
  waitForJob,
} from "./api";
import { ConfirmDialog, Dialog } from "./components/Dialog";
import { DHCPPlannerPanel } from "./components/DHCPPlannerPanel";
import { AlertBackupOperations, MihomoOperations, SubscriptionOperations } from "./components/OperationsPanels";
import { type Device, type ProxyNode, type ServiceStatus } from "./data";
import { egressLabel, policyForDevice, proposedDevicePolicy } from "./policy-state";
import {
  initialResource,
  markLoading,
  mergeResult,
  phaseLabel,
  type ResourceState,
} from "./live-state";

export type PageKey =
  | "overview"
  | "network"
  | "proxies"
  | "devices"
  | "operations"
  | "settings";

type Toast = { message: string; tone: "success" | "warning" };
type LogItem = { time: string; level: "成功" | "错误" | "信息"; source: string; event: string; detail: string };

type LiveResources = {
  routeros: ResourceState<RouterOverview>;
  mihomo: ResourceState<MihomoOverview>;
  mosdns: ResourceState<MosDNSOverview>;
  nodes: ResourceState<ApiNode[]>;
  l2tp: ResourceState<L2TPClient[]>;
  routes: ResourceState<RouterRoute[]>;
  dhcpServers: ResourceState<RouterDHCPServer[]>;
  containers: ResourceState<RouterContainer[]>;
  deviceInventory: ResourceState<DeviceInventory>;
  policies: ResourceState<DevicePolicy[]>;
  groups: ResourceState<ProxyGroup[]>;
  audit: ResourceState<AuditEvent[]>;
  capabilities: ResourceState<EgressCapabilities>;
  dhcpAddressPlan: ResourceState<DHCPAddressPlan>;
  jobs: ResourceState<Job[]>;
};

const pageKeys: PageKey[] = ["overview", "network", "devices", "proxies", "operations", "settings"];
const legacyPageAliases: Record<string, PageKey> = {
  routeros: "network",
  mosdns: "network",
  topology: "network",
  logs: "operations",
};

export function pageFromHash(hash: string): PageKey {
  const raw = hash.replace(/^#/, "");
  const candidate = (legacyPageAliases[raw] ?? raw) as PageKey;
  return pageKeys.includes(candidate) ? candidate : "overview";
}

function initialResources(): LiveResources {
  return {
    routeros: initialResource("FoxOS API / RouterOS REST"),
    mihomo: initialResource("FoxOS API / Mihomo Controller"),
    mosdns: initialResource("FoxOS API / MosDNS（只读）"),
    nodes: initialResource("FoxOS API / SQLite"),
    l2tp: initialResource("FoxOS API / RouterOS REST"),
    routes: initialResource("FoxOS API / RouterOS 路由表"),
    dhcpServers: initialResource("FoxOS API / RouterOS DHCP"),
    containers: initialResource("FoxOS API / RouterOS Container"),
    deviceInventory: initialResource("FoxOS API / SQLite 设备库存"),
    policies: initialResource("FoxOS API / SQLite 设备策略"),
    groups: initialResource("FoxOS API / SQLite 代理组"),
    audit: initialResource("FoxOS API / SQLite"),
    capabilities: initialResource("FoxOS API / RouterOS 出口就绪度"),
    dhcpAddressPlan: initialResource("FoxOS API / RouterOS 地址规划"),
    jobs: initialResource("FoxOS API / SQLite 任务状态"),
  };
}

const allLiveResourceNames: LiveResourceName[] = ["routeros", "mihomo", "mosdns", "nodes", "l2tp", "routes", "dhcpServers", "containers", "deviceInventory", "policies", "groups", "audit", "capabilities", "dhcpAddressPlan", "jobs"];

const pollingProfiles: Record<PageKey, { intervalMs: number; resources: LiveResourceName[] }> = {
  overview: { intervalMs: 15_000, resources: ["routeros", "mihomo", "mosdns", "dhcpAddressPlan", "jobs", "audit"] },
  network: { intervalMs: 30_000, resources: ["routeros", "mosdns", "routes", "dhcpServers", "containers", "dhcpAddressPlan"] },
  devices: { intervalMs: 20_000, resources: ["routeros", "deviceInventory", "policies", "nodes", "groups", "l2tp", "capabilities"] },
  proxies: { intervalMs: 25_000, resources: ["mihomo", "nodes", "groups", "l2tp", "capabilities"] },
  operations: { intervalMs: 15_000, resources: ["jobs", "audit"] },
  settings: { intervalMs: 60_000, resources: ["routeros", "mihomo", "mosdns", "capabilities"] },
};

function markSelectedLoading(current: LiveResources, names: LiveResourceName[]): LiveResources {
  const selected = new Set(names);
  return {
    routeros: selected.has("routeros") ? markLoading(current.routeros) : current.routeros,
    mihomo: selected.has("mihomo") ? markLoading(current.mihomo) : current.mihomo,
    mosdns: selected.has("mosdns") ? markLoading(current.mosdns) : current.mosdns,
    nodes: selected.has("nodes") ? markLoading(current.nodes) : current.nodes,
    l2tp: selected.has("l2tp") ? markLoading(current.l2tp) : current.l2tp,
    routes: selected.has("routes") ? markLoading(current.routes) : current.routes,
    dhcpServers: selected.has("dhcpServers") ? markLoading(current.dhcpServers) : current.dhcpServers,
    containers: selected.has("containers") ? markLoading(current.containers) : current.containers,
    deviceInventory: selected.has("deviceInventory") ? markLoading(current.deviceInventory) : current.deviceInventory,
    policies: selected.has("policies") ? markLoading(current.policies) : current.policies,
    groups: selected.has("groups") ? markLoading(current.groups) : current.groups,
    audit: selected.has("audit") ? markLoading(current.audit) : current.audit,
    capabilities: selected.has("capabilities") ? markLoading(current.capabilities) : current.capabilities,
    dhcpAddressPlan: selected.has("dhcpAddressPlan") ? markLoading(current.dhcpAddressPlan) : current.dhcpAddressPlan,
    jobs: selected.has("jobs") ? markLoading(current.jobs) : current.jobs,
  };
}

function mergeLiveResources(current: LiveResources, snapshot: Partial<LiveSnapshot>): LiveResources {
  return {
    routeros: snapshot.routeros ? mergeResult(current.routeros, snapshot.routeros) : current.routeros,
    mihomo: snapshot.mihomo ? mergeResult(current.mihomo, snapshot.mihomo) : current.mihomo,
    mosdns: snapshot.mosdns ? mergeResult(current.mosdns, snapshot.mosdns) : current.mosdns,
    nodes: snapshot.nodes ? mergeResult(current.nodes, snapshot.nodes) : current.nodes,
    l2tp: snapshot.l2tp ? mergeResult(current.l2tp, snapshot.l2tp) : current.l2tp,
    routes: snapshot.routes ? mergeResult(current.routes, snapshot.routes) : current.routes,
    dhcpServers: snapshot.dhcpServers ? mergeResult(current.dhcpServers, snapshot.dhcpServers) : current.dhcpServers,
    containers: snapshot.containers ? mergeResult(current.containers, snapshot.containers) : current.containers,
    deviceInventory: snapshot.deviceInventory ? mergeResult(current.deviceInventory, snapshot.deviceInventory) : current.deviceInventory,
    policies: snapshot.policies ? mergeResult(current.policies, snapshot.policies) : current.policies,
    groups: snapshot.groups ? mergeResult(current.groups, snapshot.groups) : current.groups,
    audit: snapshot.audit ? mergeResult(current.audit, snapshot.audit) : current.audit,
    capabilities: snapshot.capabilities ? mergeResult(current.capabilities, snapshot.capabilities) : current.capabilities,
    dhcpAddressPlan: snapshot.dhcpAddressPlan ? mergeResult(current.dhcpAddressPlan, snapshot.dhcpAddressPlan) : current.dhcpAddressPlan,
    jobs: snapshot.jobs ? mergeResult(current.jobs, snapshot.jobs) : current.jobs,
  };
}

function numericId(value: string): number {
  let hash = 0;
  for (const character of value) hash = (hash * 31 + character.charCodeAt(0)) >>> 0;
  return hash || 1;
}

function deviceKind(name: string): Device["kind"] {
  const value = name.toLowerCase();
  if (value.includes("phone") || value.includes("iphone") || value.includes("android")) return "phone";
  if (value.includes("tv") || value.includes("电视")) return "tv";
  if (value.includes("nas") || value.includes("server")) return "server";
  if (value.includes("camera") || value.includes("摄像") || value.includes("printer") || value.includes("打印")) return "iot";
  return "computer";
}

function mapApiNode(node: ApiNode): ProxyNode {
  return {
    id: numericId(node.id),
    apiId: node.id,
    name: node.name,
    protocol: node.type.toUpperCase(),
    server: `${node.server}:${node.port}`,
    region: "未采集",
    source: "FoxOS 数据库",
    latency: null,
    loss: 0,
    status: "warning",
    verification: "unverified",
    inUse: "—",
  };
}

function mapL2TPNode(client: L2TPClient): ProxyNode {
  return {
    id: numericId(`l2tp:${client.id}`),
    name: client.name,
    protocol: "L2TP",
    server: client.connectTo,
    region: "未采集",
    source: "RouterOS 原生",
    latency: null,
    loss: 0,
    status: client.running && !client.disabled ? "online" : client.disabled ? "offline" : "warning",
    verification: "routeros-session",
    inUse: "—",
  };
}

function siteServiceAddress(site: SiteManifest | null, name: "routeros" | "mihomo" | "mosdns"): string {
  const service = site?.services[name];
  return service ? `${service.address}:${service.port}` : "站点清单未加载";
}

function mapDevice(device: NonNullable<RouterOverview["devices"]>[number], profile?: DeviceProfile): Device {
  const displayName = profile?.alias || device.hostName || device.macAddress;
  return {
    id: numericId(device.macAddress),
    apiId: device.macAddress,
    name: displayName,
    kind: deviceKind(displayName),
    ip: device.address,
    mac: device.macAddress,
    iface: device.interface || device.dhcpServer || "—",
    dhcpServer: device.dhcpServer,
    alias: profile?.alias,
    vendor: profile?.vendor,
    tags: profile?.tags ?? [],
    firstSeen: profile?.firstSeen,
    profileTracked: Boolean(profile),
    egress: "未设置",
    latency: null,
    online: device.status === "bound",
    fixed: !device.dynamic,
    lastSeen: profile?.lastSeen || device.lastSeen || "未提供",
  };
}

function mapAudit(event: AuditEvent): LogItem {
  return {
    time: new Date(event.createdAt).toLocaleString("zh-CN", { hour12: false }),
    level: event.outcome === "SUCCEEDED" ? "成功" : event.outcome === "FAILED" ? "错误" : "信息",
    source: event.action.startsWith("routeros") ? "RouterOS" : "FoxOS",
    event: event.action,
    detail: event.targetId || "—",
  };
}

const navItems: { key: PageKey; label: string; icon: typeof Home }[] = [
  { key: "overview", label: "总览", icon: Home },
  { key: "network", label: "网络与地址", icon: Router },
  { key: "devices", label: "设备与策略", icon: Monitor },
  { key: "proxies", label: "代理与订阅", icon: Globe2 },
  { key: "operations", label: "任务告警与审计", icon: Activity },
  { key: "settings", label: "设置", icon: Settings },
];

const pageTitle: Record<PageKey, { title: string; subtitle: string }> = {
  overview: { title: "总览", subtitle: "RouterOS、Mihomo、MosDNS 与局域网设备的可验证状态" },
  network: { title: "网络与地址", subtitle: "RouterOS 资源、路由、DHCP 容量与 DNS 只读状态" },
  proxies: { title: "代理与订阅", subtitle: "节点、链路、Mihomo 发布与订阅源" },
  devices: { title: "设备与策略", subtitle: "设备画像、固定租约与受控出口工作流" },
  operations: { title: "任务告警与审计", subtitle: "持久化任务、告警、备份与操作证据" },
  settings: { title: "设置", subtitle: "当前标签页凭据与服务端配置边界" },
};

function statusTone(phase: ResourceState<unknown>["phase"], online?: boolean): "ok" | "warning" | "offline" {
  if (phase === "live") return online === false ? "offline" : "ok";
  if (phase === "loading" || phase === "stale") return "warning";
  return "offline";
}

function serviceLabel<T extends { configured: boolean; online: boolean }>(resource: ResourceState<T>): string {
  if (resource.phase !== "live") return phaseLabel(resource.phase);
  if (!resource.data?.configured) return "未配置";
  return resource.data.online ? "在线" : "离线";
}

function formatUpdated(value?: string): string {
  if (!value) return "尚未成功加载";
  const parsed = new Date(value);
  if (Number.isNaN(parsed.getTime())) return "时间未知";
  return parsed.toLocaleString("zh-CN", { hour12: false });
}

function readableBytes(value?: string): string {
  const bytes = Number(value);
  if (!Number.isFinite(bytes) || bytes < 0) return "—";
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let amount = bytes;
  let unit = 0;
  while (amount >= 1024 && unit < units.length - 1) {
    amount /= 1024;
    unit += 1;
  }
  return `${amount.toFixed(unit === 0 ? 0 : 1)} ${units[unit]}`;
}

function usagePercent(free?: string, total?: string): string {
  const freeValue = Number(free);
  const totalValue = Number(total);
  if (!Number.isFinite(freeValue) || !Number.isFinite(totalValue) || totalValue <= 0) return "—";
  return `${Math.max(0, Math.min(100, Math.round((1 - freeValue / totalValue) * 100)))}%`;
}

function useMediaQuery(query: string): boolean {
  const getMatch = () => typeof window.matchMedia === "function" && window.matchMedia(query).matches;
  const [matches, setMatches] = useState(getMatch);
  useEffect(() => {
    if (typeof window.matchMedia !== "function") return;
    const media = window.matchMedia(query);
    const update = () => setMatches(media.matches);
    update();
    media.addEventListener("change", update);
    return () => media.removeEventListener("change", update);
  }, [query]);
  return matches;
}

function proxyStatusLabel(node: ProxyNode, sourcePhase: ResourceState<unknown>["phase"]): string {
  if (node.verification === "tcp") {
    return node.status === "online" ? "TCP 可达" : node.status === "offline" ? "TCP 不可达" : "TCP 未确认";
  }
  if (node.verification === "routeros-session") {
    if (sourcePhase !== "live") return "上次会话状态";
    return node.status === "online" ? "L2TP 会话运行" : node.status === "offline" ? "L2TP 已禁用" : "L2TP 会话未确认";
  }
  return "未检测";
}

function proxyStatusTone(node: ProxyNode, sourcePhase: ResourceState<unknown>["phase"]): "ok" | "warning" | "offline" {
  if (node.verification === "routeros-session" && sourcePhase !== "live") return "warning";
  return node.status === "online" ? "ok" : node.status;
}

function handleGridRowKeyDown(event: React.KeyboardEvent<HTMLTableRowElement>, select: () => void) {
  if (event.key === "Enter" || event.key === " ") {
    event.preventDefault();
    select();
    return;
  }
  const rows = Array.from(event.currentTarget.closest("tbody")?.querySelectorAll<HTMLTableRowElement>("tr[data-grid-row]") ?? []);
  const currentIndex = rows.indexOf(event.currentTarget);
  let nextIndex: number;
  if (event.key === "ArrowDown") nextIndex = Math.min(rows.length - 1, currentIndex + 1);
  else if (event.key === "ArrowUp") nextIndex = Math.max(0, currentIndex - 1);
  else if (event.key === "Home") nextIndex = 0;
  else if (event.key === "End") nextIndex = rows.length - 1;
  else return;
  event.preventDefault();
  const next = rows[nextIndex];
  next?.focus();
  next?.click();
}

function StatusDot({ status = "ok" }: { status?: "ok" | "warning" | "offline" }) {
  const label = status === "ok" ? "正常" : status === "warning" ? "待确认" : "不可用";
  return <span aria-label={label} className={`status-dot ${status}`} role="img" />;
}

function Button({
  children,
  icon: Icon,
  variant = "secondary",
  onClick,
  type = "button",
  disabled = false,
  title,
}: {
  children: React.ReactNode;
  icon?: typeof Plus;
  variant?: "primary" | "secondary" | "danger" | "ghost";
  onClick?: () => void;
  type?: "button" | "submit";
  disabled?: boolean;
  title?: string;
}) {
  return (
    <button className={`button ${variant}`} disabled={disabled} onClick={onClick} title={title} type={type}>
      {Icon ? <Icon aria-hidden="true" size={16} /> : null}
      {children}
    </button>
  );
}

function ServiceIcon({ name, tone }: { name: string; tone: string }) {
  const Icon = name === "RouterOS" ? Router : name === "Mihomo" ? Network : Database;
  return <span className={`service-icon ${tone}`}><Icon aria-hidden="true" size={28} /></span>;
}

function DeviceIcon({ kind }: { kind: Device["kind"] }) {
  const Icon = kind === "phone" ? Smartphone : kind === "server" ? Server : kind === "tv" ? Tv : kind === "iot" ? Wifi : Laptop;
  return <Icon aria-hidden="true" size={17} />;
}

function ResourceMeta<T>({ resource }: { resource: ResourceState<T> }) {
  return (
    <div aria-live="polite" className={`resource-meta ${resource.phase}`}>
      <span><StatusDot status={statusTone(resource.phase)} />{phaseLabel(resource.phase)}</span>
      <span>来源：{resource.source}</span>
      <span>更新：{formatUpdated(resource.updatedAt)}</span>
      {resource.error ? <span className="resource-error">{resource.error}</span> : null}
    </div>
  );
}

function EmptyState({ title, detail }: { title: string; detail: string }) {
  return (
    <div className="empty-state" role="status">
      <AlertTriangle aria-hidden="true" size={20} />
      <div><strong>{title}</strong><span>{detail}</span></div>
    </div>
  );
}

function SummaryCard({ icon: Icon, label, value, tone = "blue" }: { icon: typeof Users; label: string; value: string | number; tone?: string }) {
  return (
    <div className="summary-card">
      <span className={`summary-icon ${tone}`}><Icon aria-hidden="true" size={20} /></span>
      <div><small>{label}</small><strong>{value}</strong></div>
    </div>
  );
}

function App() {
  const [page, setPage] = useState<PageKey>(() => pageFromHash(window.location.hash));
  const [collapsed, setCollapsed] = useState(false);
  const [mobileNavOpen, setMobileNavOpen] = useState(false);
  const [devices, setDevices] = useState<Device[]>([]);
  const [nodes, setNodes] = useState<ProxyNode[]>([]);
  const [selectedDeviceId, setSelectedDeviceId] = useState(0);
  const [selectedNodeId, setSelectedNodeId] = useState(0);
  const [toast, setToast] = useState<Toast | null>(null);
  const [scanning, setScanning] = useState(false);
  const [liveLogs, setLiveLogs] = useState<LogItem[]>([]);
  const [resources, setResources] = useState<LiveResources>(initialResources);
  const [site, setSite] = useState<SiteManifest | null>(null);
  const [siteError, setSiteError] = useState("");
  const [clock, setClock] = useState(() => new Date());
  const toastTimer = useRef<number | undefined>(undefined);
  const mainRef = useRef<HTMLElement>(null);
  const mobileMenuRef = useRef<HTMLButtonElement>(null);
  const sidebarRef = useRef<HTMLElement>(null);
  const refreshInFlight = useRef<{ generation: number; promise: Promise<number> } | null>(null);
  const refreshGeneration = useRef(0);
  const siteRefreshGeneration = useRef(0);
  const protectedReadsEnabled = useRef(true);
  const isMobile = useMediaQuery("(max-width: 760px)");

  const notify = useCallback((message: string, tone: Toast["tone"] = "success") => {
    if (toastTimer.current !== undefined) window.clearTimeout(toastTimer.current);
    setToast({ message, tone });
    toastTimer.current = window.setTimeout(() => {
      setToast(null);
      toastTimer.current = undefined;
    }, 3500);
  }, []);

  useEffect(() => () => {
    if (toastTimer.current !== undefined) window.clearTimeout(toastTimer.current);
  }, []);

  const handleSignedOut = useCallback(() => {
    refreshGeneration.current += 1;
    protectedReadsEnabled.current = false;
    setScanning(false);
    setDevices([]);
    setNodes([]);
    setSelectedDeviceId(0);
    setSelectedNodeId(0);
    setLiveLogs([]);
    setResources(initialResources());
    setPage("settings");
    setMobileNavOpen(false);
    if (window.location.hash !== "#settings") window.location.hash = "settings";
  }, []);

  useEffect(() => subscribeSessionInvalidation(handleSignedOut), [handleSignedOut]);

  const refreshLiveData = useCallback(async (showToast = false, names: LiveResourceName[] = allLiveResourceNames, background = false): Promise<number> => {
    if (!protectedReadsEnabled.current) return 0;
		const generation = refreshGeneration.current;
		while (refreshInFlight.current?.generation === generation) {
      if (background) return 0;
			await refreshInFlight.current.promise;
			if (!protectedReadsEnabled.current || generation !== refreshGeneration.current) return 0;
    }
    const refresh = (async (): Promise<number> => {
      if (!background) {
        setScanning(true);
        setResources((current) => markSelectedLoading(current, names));
      }
      try {
        const snapshot = names.length === allLiveResourceNames.length ? await loadLiveSnapshot() : await loadLiveResources(names);
        if (!protectedReadsEnabled.current || generation !== refreshGeneration.current) return 0;
        setResources((current) => mergeLiveResources(current, snapshot));

        if (snapshot.nodes?.ok) {
          const next = snapshot.nodes.data.map(mapApiNode);
          setNodes((current) => [...next, ...current.filter((node) => node.protocol === "L2TP")]);
          setSelectedNodeId((current) => next.some((node) => node.id === current) ? current : next[0]?.id ?? 0);
        }
        if (snapshot.l2tp?.ok) {
          const next = snapshot.l2tp.data.map(mapL2TPNode);
          setNodes((current) => [...current.filter((node) => node.protocol !== "L2TP"), ...next]);
          setSelectedNodeId((current) => current || next[0]?.id || 0);
        }
        if (snapshot.routeros?.ok) {
          const profiles = snapshot.deviceInventory?.ok ? new Map(snapshot.deviceInventory.data.devices.map((profile) => [profile.macAddress.toUpperCase(), profile])) : new Map<string, DeviceProfile>();
          const next = (snapshot.routeros.data.devices ?? []).map((device) => mapDevice(device, profiles.get(device.macAddress.toUpperCase())));
          setDevices(next);
          setSelectedDeviceId((current) => next.some((device) => device.id === current) ? current : next[0]?.id ?? 0);
        }
        if (snapshot.audit?.ok) setLiveLogs(snapshot.audit.data.map(mapAudit));

        const failures = Object.values(snapshot).filter((result) => result && !result.ok).length;
        if (showToast) {
          notify(failures ? `刷新完成，${failures} 个数据源不可用；其他数据已保留` : "全部数据源刷新完成", failures ? "warning" : "success");
        }
        return failures;
      } catch (error) {
        if (!protectedReadsEnabled.current || generation !== refreshGeneration.current) return 0;
        if (showToast) notify(error instanceof Error ? error.message : "刷新过程异常", "warning");
        return 1;
      } finally {
        if (!background && protectedReadsEnabled.current && generation === refreshGeneration.current) setScanning(false);
      }
    })();
		const activeRefresh = { generation, promise: refresh };
		refreshInFlight.current = activeRefresh;
    try {
      return await refresh;
    } finally {
			if (refreshInFlight.current === activeRefresh) refreshInFlight.current = null;
    }
  }, [notify]);

  useEffect(() => {
    void refreshLiveData(false);
  }, [refreshLiveData]);

  const refreshSite = useCallback(async () => {
    const generation = ++siteRefreshGeneration.current;
    try {
      const manifest = await getSiteManifest();
      if (generation !== siteRefreshGeneration.current) return;
      setSite(manifest);
      setSiteError("");
    } catch (error) {
      if (generation !== siteRefreshGeneration.current) return;
      setSite(null);
      setSiteError(error instanceof Error ? error.message : "站点清单不可用");
    }
  }, []);

  useEffect(() => {
    void refreshSite();
    return () => { siteRefreshGeneration.current += 1; };
  }, [refreshSite]);

  useEffect(() => {
    const syncFromLocation = () => {
      setPage(pageFromHash(window.location.hash));
      setMobileNavOpen(false);
      window.scrollTo({ top: 0 });
    };
    if (!window.location.hash || pageFromHash(window.location.hash) === "overview" && window.location.hash !== "#overview") {
      window.history.replaceState(null, "", "#overview");
    }
    window.addEventListener("hashchange", syncFromLocation);
    window.addEventListener("popstate", syncFromLocation);
    return () => {
      window.removeEventListener("hashchange", syncFromLocation);
      window.removeEventListener("popstate", syncFromLocation);
    };
  }, []);

  useEffect(() => {
    mainRef.current?.focus({ preventScroll: true });
  }, [page]);

  useEffect(() => {
    if (!isMobile || !mobileNavOpen) return;
    const sidebar = sidebarRef.current;
    const opener = mobileMenuRef.current;
    const focusable = () => Array.from(sidebar?.querySelectorAll<HTMLElement>("button:not([disabled]), [href], [tabindex]:not([tabindex='-1'])") ?? []);
    (sidebar?.querySelector<HTMLElement>("[aria-current='page']") ?? focusable()[0])?.focus();
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        setMobileNavOpen(false);
        return;
      }
      if (event.key !== "Tab") return;
      const items = focusable();
      if (!items.length) return;
      const first = items[0];
      const last = items[items.length - 1];
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first.focus();
      }
    };
    document.addEventListener("keydown", onKeyDown);
    return () => {
      document.removeEventListener("keydown", onKeyDown);
      opener?.focus();
    };
  }, [isMobile, mobileNavOpen]);

  useEffect(() => {
    const timer = window.setInterval(() => setClock(new Date()), 30_000);
    return () => window.clearInterval(timer);
  }, []);

  useEffect(() => {
    const profile = pollingProfiles[page];
    let timer: number | undefined;
    let stopped = false;
    let failures = 0;
    const schedule = (delay: number) => {
      if (!stopped) timer = window.setTimeout(() => void poll(), delay);
    };
    const poll = async () => {
      if (stopped) return;
      if (document.hidden) {
        schedule(profile.intervalMs);
        return;
      }
      failures = await refreshLiveData(false, profile.resources, true);
      const backoff = Math.min(120_000, profile.intervalMs * 2 ** Math.min(failures, 3));
      schedule(backoff);
    };
    const onVisibility = () => {
      if (document.hidden || stopped) return;
      if (timer !== undefined) window.clearTimeout(timer);
      schedule(250);
    };
    document.addEventListener("visibilitychange", onVisibility);
    schedule(profile.intervalMs);
    return () => {
      stopped = true;
      if (timer !== undefined) window.clearTimeout(timer);
      document.removeEventListener("visibilitychange", onVisibility);
    };
  }, [page, refreshLiveData]);

  const navigate = (key: PageKey) => {
    if (window.location.hash === `#${key}`) {
      setPage(key);
      setMobileNavOpen(false);
      return;
    }
    window.location.hash = key;
  };

  const handleAuthenticated = () => {
    refreshGeneration.current += 1;
    protectedReadsEnabled.current = true;
    void refreshSite();
    void refreshLiveData(true);
  };

  const criticalServices = [resources.routeros, resources.mihomo, resources.mosdns];
  const healthy = criticalServices.every((resource) => resource.phase === "live" && resource.data?.online);
  const availableCount = Object.values(resources).filter((resource) => resource.phase === "live").length;
  const resourceCount = Object.keys(resources).length;
  const serviceItems: ServiceStatus[] = [
    { name: "RouterOS", address: siteServiceAddress(site, "routeros"), tone: "orange" },
    { name: "Mihomo", address: siteServiceAddress(site, "mihomo"), tone: "blue" },
    { name: "MosDNS", address: siteServiceAddress(site, "mosdns"), tone: "purple" },
  ];

  return (
    <div className={`app-shell ${collapsed ? "sidebar-collapsed" : ""} ${mobileNavOpen ? "mobile-nav-open" : ""}`}>
      <button className="skip-link" onClick={() => mainRef.current?.focus()} type="button">跳到主要内容</button>
      <button aria-hidden="true" className="sidebar-scrim" onClick={() => setMobileNavOpen(false)} tabIndex={-1} type="button" />
      <aside aria-hidden={isMobile && !mobileNavOpen ? "true" : undefined} className="sidebar" inert={isMobile && !mobileNavOpen ? true : undefined} ref={sidebarRef}>
        <div className="brand">
          <img alt="FoxOS" src="/foxos-logo.svg" />
          {!collapsed ? <span><strong>FoxOS</strong><small>Network Command Center</small></span> : null}
          <button aria-label="关闭导航" className="mobile-nav-close" onClick={() => setMobileNavOpen(false)} type="button"><X aria-hidden="true" size={20} /></button>
        </div>
        <nav aria-label="主导航">
          {navItems.map(({ key, label, icon: Icon }) => (
            <button aria-current={page === key ? "page" : undefined} className={page === key ? "active" : ""} key={key} onClick={() => navigate(key)} title={collapsed ? label : undefined} type="button">
              <Icon aria-hidden="true" size={19} />
              {!collapsed ? <span>{label}</span> : null}
            </button>
          ))}
        </nav>
        <div className="sidebar-footer">
          {!collapsed ? (
            <div className={`system-health ${healthy ? "healthy" : "unknown"}`}>
              {healthy ? <ShieldCheck aria-hidden="true" size={20} /> : <AlertTriangle aria-hidden="true" size={20} />}
              <span><small>系统健康</small><strong>{healthy ? "全部已验证" : "存在未确认状态"}</strong></span>
            </div>
          ) : null}
          <button aria-label={collapsed ? "展开侧栏" : "收起侧栏"} className="collapse-button" onClick={() => setCollapsed((value) => !value)} type="button">
            {collapsed ? <ChevronRight aria-hidden="true" size={18} /> : <ChevronLeft aria-hidden="true" size={18} />}
            {!collapsed ? "收起侧栏" : null}
          </button>
        </div>
      </aside>

      <main aria-labelledby="page-title" className="main-shell" id="main-content" ref={mainRef} tabIndex={-1}>
        <header className="topbar">
          <button aria-expanded={mobileNavOpen} aria-label="打开导航" className="mobile-menu" onClick={() => setMobileNavOpen(true)} ref={mobileMenuRef} type="button"><Menu aria-hidden="true" size={20} /></button>
          <div className="topbar-title"><h1 id="page-title">{pageTitle[page].title}</h1><p>{pageTitle[page].subtitle}</p></div>
          <div className="topbar-meta">
            <span aria-live="polite" className="data-mode"><StatusDot status={availableCount === resourceCount ? "ok" : availableCount ? "warning" : "offline"} />{availableCount} / {resourceCount} 数据源实时</span>
            <span className="top-time">{clock.toLocaleString("zh-CN", { hour12: false })}</span>
            <span className="admin"><Users aria-hidden="true" size={16} />本地操作员</span>
          </div>
        </header>

        <div className="page-content">
          {page === "overview" ? <Overview navigate={navigate} onRefresh={() => void refreshLiveData(true, pollingProfiles.overview.resources)} resources={resources} scanning={scanning} services={serviceItems} /> : null}
          {page === "network" ? <div className="stack"><RouterOSPage containers={resources.containers} dhcpServers={resources.dhcpServers} navigate={navigate} notify={notify} onRefresh={(showToast = true) => refreshLiveData(showToast, pollingProfiles.network.resources)} resource={resources.routeros} routes={resources.routes} scanning={scanning} /><MosDNSPage address={siteServiceAddress(site, "mosdns")} resource={resources.mosdns} /></div> : null}
          {page === "proxies" ? <div className="stack"><ProxyPage groupResource={resources.groups} l2tpResource={resources.l2tp} mihomoResource={resources.mihomo} navigate={navigate} nodeResource={resources.nodes} nodes={nodes} notify={notify} onRefresh={(showToast = true) => void refreshLiveData(showToast, pollingProfiles.proxies.resources)} selectedNodeId={selectedNodeId} setNodes={setNodes} setSelectedNodeId={setSelectedNodeId} /><MihomoOperations notify={notify} /><SubscriptionOperations notify={notify} /></div> : null}
          {page === "devices" ? <DevicesPage capabilitiesResource={resources.capabilities} devices={devices} groupResource={resources.groups} inventoryResource={resources.deviceInventory} l2tpResource={resources.l2tp} nodeResource={resources.nodes} notify={notify} onRefresh={(showToast = true) => void refreshLiveData(showToast, pollingProfiles.devices.resources)} policyResource={resources.policies} protectedAddresses={site?.protectedAddresses ?? []} resource={resources.routeros} selectedDeviceId={selectedDeviceId} setDevices={setDevices} setSelectedDeviceId={setSelectedDeviceId} siteReady={Boolean(site)} /> : null}
          {page === "operations" ? <OperationsPage items={liveLogs} jobs={resources.jobs} notify={notify} onRefresh={(showToast = true) => refreshLiveData(showToast, pollingProfiles.operations.resources)} resource={resources.audit} /> : null}
          {page === "settings" ? <SettingsPage notify={notify} onAuthenticated={handleAuthenticated} onSignedOut={handleSignedOut} site={site} siteError={siteError} /> : null}
        </div>
      </main>

      <div aria-atomic="true" aria-live="polite" className="toast-region">
        {toast ? <div className={`toast ${toast.tone}`} role={toast.tone === "warning" ? "alert" : "status"}>
          {toast.tone === "success" ? <CheckCircle2 aria-hidden="true" size={18} /> : <AlertTriangle aria-hidden="true" size={18} />}{toast.message}
        </div> : null}
      </div>
    </div>
  );
}

function OperationsPage({ notify, jobs, items, resource, onRefresh }: { notify: (message: string, tone?: Toast["tone"]) => void; jobs: ResourceState<Job[]>; items: LogItem[]; resource: ResourceState<AuditEvent[]>; onRefresh: (showToast?: boolean) => Promise<number> }) {
  return <div className="stack"><TaskPanel notify={notify} onRefresh={onRefresh} resource={jobs} /><AlertBackupOperations notify={notify} /><LogsPage items={items} onRefresh={() => void onRefresh(true)} resource={resource} /></div>;
}

function TaskPanel({ resource, notify, onRefresh }: { resource: ResourceState<Job[]>; notify: (message: string, tone?: Toast["tone"]) => void; onRefresh: (showToast?: boolean) => Promise<number> }) {
  const [retryStates, setRetryStates] = useState<Record<string, { phase: "checking" | "unconfirmed" | "confirmed"; job: Job; originalUpdatedAt: string }>>({});
  const items = resource.data ?? [];
  useEffect(() => {
    setRetryStates((current) => {
      let changed = false;
      const next = { ...current };
      for (const [id, state] of Object.entries(current)) {
        const readback = items.find((job) => job.id === id);
        if (readback && readback.updatedAt !== state.originalUpdatedAt) {
          delete next[id];
          changed = true;
        }
      }
      return changed ? next : current;
    });
  }, [items]);
  const retry = async (job: Job) => {
    if (retryStates[job.id]) return;
    setRetryStates((current) => ({ ...current, [job.id]: { phase: "checking", job, originalUpdatedAt: job.updatedAt } }));
    let acceptedByServer = false;
    try {
      const accepted = await retryJob(job.id);
      acceptedByServer = true;
      if (accepted.status !== "QUEUED") throw new Error(`重试请求返回 ${accepted.status}，未确认重新排队`);
      setRetryStates((current) => ({ ...current, [job.id]: { phase: "unconfirmed", job: accepted, originalUpdatedAt: job.updatedAt } }));
      const readback = (await listJobs(50)).find((candidate) => candidate.id === job.id);
			const progressed = readback && (readback.attempts > job.attempts || readback.updatedAt !== job.updatedAt);
			if (!readback || !progressed || !["QUEUED", "RUNNING", "VERIFYING", "SUCCEEDED", "FAILED", "ROLLED_BACK"].includes(readback.status)) {
				throw new Error("重试已被接受，但任务列表尚未确认新的任务状态；为避免重复提交，重试保持锁定");
			}
      setRetryStates((current) => ({ ...current, [job.id]: { phase: "confirmed", job: readback, originalUpdatedAt: job.updatedAt } }));
      notify(`${job.kind} 重试已接受，任务列表回读为 ${readback.status}`, readback.status === "FAILED" || readback.status === "ROLLED_BACK" ? "warning" : "success");
      void onRefresh(false);
    } catch (error) {
      if (!acceptedByServer && error instanceof ApiError) {
        setRetryStates((current) => {
          if (!current[job.id]) return current;
          const next = { ...current };
          delete next[job.id];
          return next;
        });
      }
      notify(apiFailureMessage(error, "任务不能重试"), "warning");
    }
  };
  return <section className="panel"><div className="panel-heading"><div><h2>持久化任务</h2><p>阶段、最终状态和恢复结果来自 SQLite</p></div><ResourceMeta resource={resource} /></div>{items.length ? <div aria-label="可横向滚动的数据表" className="table-wrap" role="region" tabIndex={0}><table aria-label="持久化任务"><thead><tr><th>任务</th><th>状态</th><th>阶段</th><th>进度</th><th>更新时间</th><th>操作</th></tr></thead><tbody>{items.map((job) => { const retryState = retryStates[job.id]; const displayed = retryState?.job ?? job; const phase = typeof displayed.result?.phase === "string" ? displayed.result.phase : "—"; const retryable = job.status === "FAILED" || job.status === "ROLLED_BACK"; const waiting = retryState?.phase === "checking" ? "重试中…" : retryState?.phase === "unconfirmed" ? "等待回读" : ""; return <tr key={job.id}><td><strong>{displayed.kind}</strong><small className="table-subline">{displayed.id}</small></td><td><span className={`job-status ${displayed.status.toLowerCase()}`}>{displayed.status}{retryState?.phase === "unconfirmed" ? "（待回读）" : ""}</span></td><td>{phase}</td><td>{displayed.progress}%</td><td>{formatUpdated(displayed.updatedAt)}</td><td>{retryState ? waiting ? <button className="button secondary compact" disabled type="button"><RefreshCw aria-hidden="true" size={14} />{waiting}</button> : "—" : retryable ? <button className="button secondary compact" onClick={() => void retry(job)} type="button"><RefreshCw aria-hidden="true" size={14} />重试</button> : "—"}</td></tr>; })}</tbody></table></div> : <EmptyState detail={resource.error || "任务创建后会在此显示。"} title="暂无任务记录" />}</section>;
}

function ServiceSummary<T extends { configured: boolean; online: boolean }>({ name, address, tone, resource }: { name: string; address: string; tone: string; resource: ResourceState<T> }) {
  const online = resource.phase === "live" && Boolean(resource.data?.online);
  return (
    <div className="service-summary">
      <ServiceIcon name={name} tone={tone} />
      <div><strong>{name}</strong><span className={online ? "green-text" : "muted-text"}><StatusDot status={statusTone(resource.phase, online)} />{serviceLabel(resource)}</span></div>
      <div className="service-detail"><span>{address}</span><small>{resource.source}</small><small>{formatUpdated(resource.updatedAt)}</small></div>
    </div>
  );
}

function Overview({ resources, onRefresh, scanning, navigate, services }: { resources: LiveResources; onRefresh: () => void; scanning: boolean; navigate: (key: PageKey) => void; services: ServiceStatus[] }) {
  const capacities = resources.dhcpAddressPlan.data?.servers ?? [];
  const readyCapacities = capacities.filter((item) => item.ready);
  const minimumRemaining = readyCapacities.length ? Math.min(...readyCapacities.map((item) => item.remaining)) : undefined;
  const capacityIssues = capacities.filter((item) => !item.ready || item.risk === "critical" || item.risk === "exhausted" || item.conflicts.length > 0);
  const failedJobs = (resources.jobs.data ?? []).filter((job) => job.status === "FAILED" || job.status === "ROLLED_BACK");
  const activeJobs = (resources.jobs.data ?? []).filter((job) => job.status === "QUEUED" || job.status === "RUNNING" || job.status === "VERIFYING");
  const serviceIssues = [
    { name: "RouterOS", resource: resources.routeros },
    { name: "Mihomo", resource: resources.mihomo },
    { name: "MosDNS", resource: resources.mosdns },
  ].filter((item) => item.resource.phase !== "live" || !item.resource.data?.online);
  const recentFailures = (resources.audit.data ?? []).filter((event) => event.outcome === "FAILED").slice(0, 6);
  return (
    <div className="stack">
      <section aria-label="服务状态" className="service-strip">
        <ServiceSummary {...services[0]} resource={resources.routeros} />
        <ServiceSummary {...services[1]} resource={resources.mihomo} />
        <ServiceSummary {...services[2]} resource={resources.mosdns} />
        <Button disabled={scanning} icon={RefreshCw} onClick={onRefresh} variant="primary">{scanning ? "正在刷新…" : "刷新全部状态"}</Button>
      </section>

      <div className="summary-grid">
        <SummaryCard icon={AlertTriangle} label="当前异常" value={serviceIssues.length + capacityIssues.length} tone={serviceIssues.length + capacityIssues.length ? "red" : "green"} />
        <SummaryCard icon={CircleGauge} label="DHCP 最少剩余" value={minimumRemaining ?? "—"} tone={minimumRemaining !== undefined && minimumRemaining <= 20 ? "orange" : "green"} />
        <SummaryCard icon={Activity} label="执行中任务" value={resources.jobs.phase === "live" ? activeJobs.length : "—"} />
        <SummaryCard icon={X} label="最近失败任务" value={resources.jobs.phase === "live" ? failedJobs.length : "—"} tone={failedJobs.length ? "red" : "green"} />
      </div>

      <div className="overview-ops-grid">
        <section className="panel"><div className="panel-heading"><div><h2>异常</h2><p>只显示实时读取到的不可用、冲突或容量风险</p></div></div><div className="overview-list">{serviceIssues.map((item) => <div key={item.name}><AlertTriangle aria-hidden="true" size={15} /><strong>{item.name}</strong><span>{item.resource.error || phaseLabel(item.resource.phase)}</span></div>)}{capacityIssues.map((item) => <div key={item.serverName}><AlertTriangle aria-hidden="true" size={15} /><strong>{item.serverName}</strong><span>{item.error || `${item.risk} · 剩余 ${item.remaining} · 冲突 ${item.conflicts.length}`}</span></div>)}{!serviceIssues.length && !capacityIssues.length ? <div><CheckCircle2 aria-hidden="true" size={15} /><strong>没有已确认异常</strong><span>未读取的数据不会被计为正常</span></div> : null}</div></section>
        <section className="panel"><div className="panel-heading"><div><h2>地址容量</h2><p>配置容量包含首尾；安全动态容量已扣除保留和冲突</p></div><ResourceMeta resource={resources.dhcpAddressPlan} /></div>{capacities.length ? <div className="capacity-overview">{capacities.map((item) => <div key={item.serverName}><strong>{item.serverName}</strong><span>{item.network || "网段未确认"}</span><span>{item.dynamicOccupied} / {item.dynamicCapacity}</span><b>{item.remaining} 剩余</b></div>)}</div> : <EmptyState detail={resources.dhcpAddressPlan.error || "等待 RouterOS 地址规划回读。"} title="容量未确认" />}</section>
        <section className="panel"><div className="panel-heading"><div><h2>最近失败</h2><p>任务与审计失败均保留为可追溯证据</p></div></div><div className="overview-list">{failedJobs.slice(0, 3).map((job) => <div key={job.id}><X aria-hidden="true" size={15} /><strong>{job.kind}</strong><span>{job.errorClass || job.status} · {formatUpdated(job.updatedAt)}</span></div>)}{recentFailures.slice(0, Math.max(0, 6 - failedJobs.length)).map((event) => <div key={event.id}><X aria-hidden="true" size={15} /><strong>{event.action}</strong><span>{event.targetId || "—"} · {formatUpdated(event.updatedAt)}</span></div>)}{!failedJobs.length && !recentFailures.length ? <div><CheckCircle2 aria-hidden="true" size={15} /><strong>没有最近失败记录</strong><span>任务和审计数据源必须保持实时</span></div> : null}</div></section>
        <section className="panel"><div className="panel-heading"><div><h2>常用操作</h2><p>高风险变更仍会进入计划、确认和执行流程</p></div></div><div className="quick-actions"><Button icon={LockKeyhole} onClick={() => navigate("devices")}>固定租约与出口</Button><Button icon={Calculator} onClick={() => navigate("network")}>查看 DHCP 容量</Button><Button icon={Globe2} onClick={() => navigate("proxies")}>代理发布与订阅</Button><Button icon={Activity} onClick={() => navigate("operations")}>任务与恢复</Button></div></section>
      </div>
    </div>
  );
}

const managedContainerOwners = new Set(["foxos:active", "foxos:pending", "foxos:rollback", "foxos:failed", "foxos:mihomo", "foxos:mosdns"]);

function containerOperationID(command: "start" | "stop" | "restart"): string {
  const entropy = globalThis.crypto?.randomUUID?.() ?? `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`;
  return `container-${command}-${entropy}`;
}

function RouterOSPage({ resource, routes, dhcpServers, containers, onRefresh, scanning, navigate, notify }: { resource: ResourceState<RouterOverview>; routes: ResourceState<RouterRoute[]>; dhcpServers: ResourceState<RouterDHCPServer[]>; containers: ResourceState<RouterContainer[]>; onRefresh: (showToast?: boolean) => Promise<number>; scanning: boolean; navigate: (key: PageKey) => void; notify: (message: string, tone?: Toast["tone"]) => void }) {
  const [containerBusy, setContainerBusy] = useState<Record<string, "start" | "stop" | "restart">>({});
  const containerBusyRef = useRef(new Set<string>());
  const overview = resource.data;
  const live = resource.phase === "live" && Boolean(overview?.online);
  const system = overview?.resource ?? {};
  const interfaces = overview?.interfaces ?? [];
  const devices = overview?.devices ?? [];
  const routeItems = routes.data ?? [];
  const dhcpItems = dhcpServers.data ?? [];
  const containerItems = containers.data ?? [];
  const activeDefaultRoutes = routeItems.filter((item) => item["dst-address"] === "0.0.0.0/0" && item.active === "true" && item.disabled !== "true");
  const runningContainers = containerItems.filter((item) => item.status.toLowerCase() === "running");
  const runContainerCommand = async (item: RouterContainer, command: "start" | "stop" | "restart") => {
    const id = item[".id"];
    const owner = item.comment?.trim() ?? "";
    if (!id || !managedContainerOwners.has(owner) || containerBusyRef.current.has(id)) return;
    containerBusyRef.current.add(id);
    setContainerBusy((current) => ({ ...current, [id]: command }));
    try {
      const submitted = await commandRouterContainer(id, owner, command, containerOperationID(command));
      const completed = await waitForJob(submitted.job.id);
      if (completed.status !== "SUCCEEDED") {
        throw new Error(`容器${command === "start" ? "启动" : command === "stop" ? "停止" : "重启"}未完成：${completed.errorClass || completed.status}`);
      }
      const readback = await getRouterContainers();
      const verified = readback.find((candidate) => candidate[".id"] === id && candidate.comment === owner);
      const desiredStatus = command === "stop" ? "stopped" : "running";
      if (!verified || verified.status.toLowerCase() !== desiredStatus) {
        throw new Error("容器任务已结束，但 RouterOS 精确回读未确认目标状态");
      }
      await onRefresh(false);
      notify(`${item.name || owner} 已${command === "start" ? "启动" : command === "stop" ? "停止" : "重启"}，RouterOS 状态已回读`);
    } catch (error) {
      notify(apiFailureMessage(error, "容器命令失败"), "warning");
    } finally {
      containerBusyRef.current.delete(id);
      setContainerBusy((current) => {
        const next = { ...current };
        delete next[id];
        return next;
      });
    }
  };
  return (
    <div className="stack">
      <section className="panel status-band"><ResourceMeta resource={resource} /><Button disabled={scanning} icon={RefreshCw} onClick={() => void onRefresh(true)}>{scanning ? "刷新中…" : "刷新 RouterOS"}</Button></section>
      <div className="summary-grid">
        <SummaryCard icon={CircleGauge} label="CPU 使用率" value={system["cpu-load"] ? `${system["cpu-load"]}%` : "—"} tone="orange" />
        <SummaryCard icon={HardDrive} label="内存使用率" value={usagePercent(system["free-memory"], system["total-memory"])} />
        <SummaryCard icon={Globe2} label="活动默认路由" value={routes.phase === "live" ? activeDefaultRoutes.length : "—"} tone={activeDefaultRoutes.length ? "green" : "red"} />
        <SummaryCard icon={Server} label="运行容器" value={containers.phase === "live" ? `${runningContainers.length} / ${containerItems.length}` : "—"} tone="green" />
      </div>
      <div className="page-grid two-thirds">
        <section className="panel">
          <div className="panel-heading"><div><h2>接口状态</h2><p>接收和发送字节为 RouterOS 累计计数</p></div></div>
          {interfaces.length ? <div aria-label="可横向滚动的数据表" className="table-wrap" role="region" tabIndex={0}><table><thead><tr><th>接口</th><th>类型</th><th>MAC 地址</th><th>RX</th><th>TX</th><th>状态</th></tr></thead><tbody>{interfaces.map((item) => {
            const running = live && item.running === "true" && item.disabled !== "true";
            return <tr key={item[".id"] || item.name}><td><strong>{item.name}</strong></td><td>{item.type || "—"}</td><td>{item["mac-address"] || "—"}</td><td>{readableBytes(item["rx-byte"])}</td><td>{readableBytes(item["tx-byte"])}</td><td><StatusDot status={running ? "ok" : resource.phase === "live" ? "offline" : "warning"} />{resource.phase === "live" ? running ? "运行" : "停止" : "上次状态"}</td></tr>;
          })}</tbody></table></div> : <EmptyState detail="该接口读取失败不会影响其他服务状态。" title="接口数据不可用" />}
        </section>
        <aside className="stack">
          <section className="panel"><div className="panel-heading"><div><h2>系统资源</h2><p>{live ? "RouterOS 回读成功" : "当前不可确认"}</p></div><StatusDot status={statusTone(resource.phase, live)} /></div><dl className="definition-list"><div><dt>版本</dt><dd>{system.version || "—"}</dd></div><div><dt>架构</dt><dd>{system["architecture-name"] || "—"}</dd></div><div><dt>平台</dt><dd>{system["board-name"] || "—"}</dd></div><div><dt>运行时间</dt><dd>{system.uptime || "—"}</dd></div><div><dt>磁盘可用</dt><dd>{readableBytes(system["free-hdd-space"])}</dd></div></dl></section>
          <section className="panel danger-zone"><div className="panel-heading"><div><h2>受控运维</h2><p>配置发布、快照和恢复均要求预览与确认</p></div></div><Button icon={ShieldCheck} onClick={() => navigate("operations")}>配置预览与校验</Button><Button icon={Save} onClick={() => navigate("operations")}>备份与恢复</Button></section>
        </aside>
      </div>
      <div className="page-grid two-thirds">
        <section className="panel"><div className="panel-heading"><div><h2>路由与 WAN</h2><p>默认路由仅用于状态判断，不修改用户路由</p></div><ResourceMeta resource={routes} /></div>{routeItems.length ? <div aria-label="可横向滚动的数据表" className="table-wrap" role="region" tabIndex={0}><table><thead><tr><th>目标</th><th>网关</th><th>距离</th><th>状态</th><th>所有权</th></tr></thead><tbody>{routeItems.map((item) => <tr key={item[".id"]}><td>{item["dst-address"] || "—"}</td><td>{item.gateway || "—"}</td><td>{item.distance || "—"}</td><td><StatusDot status={routes.phase === "live" && item.active === "true" && item.disabled !== "true" ? "ok" : routes.phase === "live" ? "offline" : "warning"} />{routes.phase === "live" ? item.disabled === "true" ? "禁用" : item.active === "true" ? "活动" : "非活动" : "上次状态"}</td><td>{item.comment?.startsWith("foxos:") ? "FoxOS" : "用户 / 系统"}</td></tr>)}</tbody></table></div> : <EmptyState detail={routes.error || "RouterOS 未返回路由条目。"} title="路由数据不可用" />}</section>
        <aside className="stack">
          <section className="panel"><div className="panel-heading"><div><h2>DHCP server</h2><p>{devices.length} 个 DHCP / ARP 设备已读取</p></div><ResourceMeta resource={dhcpServers} /></div>{dhcpItems.length ? <dl className="definition-list">{dhcpItems.map((item) => <div key={item[".id"]}><dt>{item.name}</dt><dd><StatusDot status={dhcpServers.phase === "live" && item.running === "true" && item.disabled !== "true" ? "ok" : dhcpServers.phase === "live" ? "offline" : "warning"} />{item.interface} · {item["address-pool"]}</dd></div>)}</dl> : <EmptyState detail={dhcpServers.error || "RouterOS 未返回 DHCP server。"} title="DHCP 数据不可用" />}</section>
          <section className="panel container-panel"><div className="panel-heading"><div><h2>容器</h2><p>运行状态与开机自动启动分别来自 RouterOS 回读</p></div><ResourceMeta resource={containers} /></div>{containerItems.length ? <div className="container-list">{containerItems.map((item) => {
            const id = item[".id"];
            const status = item.status.toLowerCase();
            const managed = managedContainerOwners.has(item.comment?.trim() ?? "");
            const busy = containerBusy[id];
            const commandsDisabled = containers.phase !== "live" || !managed || Boolean(busy);
            return <section aria-label={item.name || item.comment || id} className="container-row" key={id}><div className="container-title"><strong>{item.name || item.comment || id}</strong><small>{item.comment || "无 FoxOS 所有权标记"} · {item.interface || "无接口"}</small></div><dl><div><dt>当前运行状态</dt><dd><StatusDot status={containers.phase === "live" && status === "running" ? "ok" : containers.phase === "live" && status === "stopped" ? "offline" : "warning"} />{containers.phase === "live" ? status === "running" ? "运行中" : status === "stopped" ? "已停止" : item.status || "未知" : "上次状态"}</dd></div><div><dt>开机自动启动</dt><dd>{item["start-on-boot"] === "true" ? "已启用" : item["start-on-boot"] === "false" ? "未启用" : "未回读"}</dd></div></dl><div aria-live="polite" className="container-command-row"><button className="container-command" disabled={commandsDisabled || status !== "stopped"} onClick={() => void runContainerCommand(item, "start")} type="button"><Play aria-hidden="true" size={14} />{busy === "start" ? "启动中…" : "启动"}</button><button className="container-command" disabled={commandsDisabled || status !== "running"} onClick={() => void runContainerCommand(item, "stop")} type="button"><Square aria-hidden="true" size={13} />{busy === "stop" ? "停止中…" : "停止"}</button><button className="container-command" disabled={commandsDisabled || status !== "running"} onClick={() => void runContainerCommand(item, "restart")} type="button"><RefreshCw aria-hidden="true" size={14} />{busy === "restart" ? "重启中…" : "重启"}</button></div>{!managed ? <p className="container-ownership-note">非 FoxOS 精确所有权资源，命令已禁用。</p> : null}</section>;
          })}</div> : <EmptyState detail={containers.error || "RouterOS 未返回容器。"} title="容器数据不可用" />}</section>
        </aside>
      </div>
      <DHCPPlannerPanel notify={notify} />
    </div>
  );
}

function MosDNSPage({ resource, address }: { resource: ResourceState<MosDNSOverview>; address: string }) {
  const live = resource.phase === "live" && Boolean(resource.data?.online);
  return (
    <div className="stack">
      <div className="readonly-banner"><LockKeyhole aria-hidden="true" size={18} /><span><strong>只读模式</strong>FoxOS 不修改 DNS、DHCP 下发 DNS 或 MosDNS 配置。</span></div>
      <section className="panel status-band"><ResourceMeta resource={resource} /></section>
      <div className="summary-grid">
        <SummaryCard icon={Activity} label="进程状态" value={live ? "在线" : resource.phase === "loading" ? "加载中" : "不可用"} tone={live ? "green" : "orange"} />
        <SummaryCard icon={Database} label="实时查询" value="未采集" tone="purple" />
        <SummaryCard icon={Zap} label="平均响应" value="未采集" />
        <SummaryCard icon={AlertTriangle} label="失败率" value="未采集" tone="orange" />
      </div>
      <section className="panel"><div className="panel-heading"><div><h2>服务状态</h2><p>不会用静态样例填充 DNS 指标</p></div><StatusDot status={statusTone(resource.phase, live)} /></div>{live ? <dl className="definition-list"><div><dt>状态</dt><dd className="green-text">只读检查通过</dd></div><div><dt>地址</dt><dd>{resource.data?.address || address}</dd></div><div><dt>配置写入</dt><dd>未开放</dd></div></dl> : <EmptyState detail={resource.error || "等待 MosDNS 状态 API 返回。"} title="MosDNS 在线状态未确认" />}</section>
    </div>
  );
}

type PendingBinding = { plan: DeviceBindingPlan; token: string; device: Device };
type PendingEgress = { plan: EgressPlan; token: string; device: Device };
type DevicesPageProps = {
  devices: Device[];
  setDevices: React.Dispatch<React.SetStateAction<Device[]>>;
  selectedDeviceId: number;
  setSelectedDeviceId: (id: number) => void;
  notify: (message: string, tone?: Toast["tone"]) => void;
  resource: ResourceState<RouterOverview>;
  inventoryResource: ResourceState<DeviceInventory>;
  policyResource: ResourceState<DevicePolicy[]>;
  nodeResource: ResourceState<ApiNode[]>;
  groupResource: ResourceState<ProxyGroup[]>;
  l2tpResource: ResourceState<L2TPClient[]>;
  capabilitiesResource: ResourceState<EgressCapabilities>;
  protectedAddresses: string[];
  siteReady: boolean;
  onRefresh: (showToast?: boolean) => void;
};

function DevicesPage({ devices, setDevices, selectedDeviceId, setSelectedDeviceId, notify, resource, inventoryResource, policyResource, nodeResource, groupResource, l2tpResource, capabilitiesResource, protectedAddresses, siteReady, onRefresh }: DevicesPageProps) {
  const [search, setSearch] = useState("");
  const [status, setStatus] = useState("all");
  const [pending, setPending] = useState<PendingBinding | null>(null);
  const [pendingEgress, setPendingEgress] = useState<PendingEgress | null>(null);
  const [executing, setExecuting] = useState(false);
  const [executingEgress, setExecutingEgress] = useState(false);
  const [egress, setEgress] = useState<EgressType>("direct");
  const [targetId, setTargetId] = useState("");
  const [alias, setAlias] = useState("");
  const [vendor, setVendor] = useState("");
  const [tagsText, setTagsText] = useState("");
  const [metadataDirty, setMetadataDirty] = useState(false);
  const metadataDeviceId = useRef<number | undefined>(undefined);
  const [metadataBusy, setMetadataBusy] = useState(false);
  const [history, setHistory] = useState<DevicePresenceEvent[]>([]);
  const [inlineDrafts, setInlineDrafts] = useState<Record<number, { egress: EgressType; targetId: string }>>({});
  const [historyPhase, setHistoryPhase] = useState<"loading" | "live" | "unavailable">("loading");
  const selected = devices.find((device) => device.id === selectedDeviceId) ?? devices[0];
  const policies = policyResource.data ?? [];
  const selectedPolicy = policyForDevice(policies, selected);
  const filtered = devices.filter((device) => `${device.name}${device.ip}${device.mac}${device.vendor ?? ""}${device.tags.join(" ")}`.toLowerCase().includes(search.toLowerCase()) && (status === "all" || (status === "online" ? device.online : !device.online)));
  const keyboardActiveId = filtered.some((device) => device.id === selected?.id) ? selected?.id : filtered[0]?.id;
  const live = resource.phase === "live" && Boolean(resource.data?.online);
  const capabilityByMode = new Map((capabilitiesResource.data?.modes ?? []).map((capability) => [capability.mode, capability]));
  const selectedCapability = capabilityByMode.get(egress);
  const targetRequired = egress === "mihomo-node" || egress === "proxy-chain" || egress === "l2tp";
  const targetSourceLive = egress === "mihomo-node" ? nodeResource.phase === "live" : egress === "proxy-chain" ? groupResource.phase === "live" : egress === "l2tp" ? l2tpResource.phase === "live" : true;
  const protectedAddressSet = new Set(protectedAddresses);
  const managementProtected = Boolean(selected && protectedAddressSet.has(selected.ip));
  const canPrepareEgress = Boolean(siteReady && selected && live && policyResource.phase === "live" && capabilitiesResource.phase === "live" && selectedCapability?.available && selected.fixed && (selectedPolicy?.dhcpServer || selected.dhcpServer) && !managementProtected && targetSourceLive && (!targetRequired || targetId));
  const selectedTagsText = selected?.tags.join(", ") ?? "";

  useEffect(() => {
    setEgress(selectedPolicy?.egress ?? "direct");
    setTargetId(selectedPolicy?.targetId ?? "");
  }, [selected?.id, selectedPolicy?.id, selectedPolicy?.egress, selectedPolicy?.targetId]);

  useLayoutEffect(() => {
    const selectionChanged = metadataDeviceId.current !== selected?.id;
    if (!selectionChanged && metadataDirty) return;
    metadataDeviceId.current = selected?.id;
    setAlias(selected?.alias ?? "");
    setVendor(selected?.vendor ?? "");
    setTagsText(selectedTagsText);
    if (metadataDirty) setMetadataDirty(false);
  }, [metadataDirty, selected?.alias, selected?.id, selected?.vendor, selectedTagsText]);

  useEffect(() => {
    setHistory([]);
    if (!selected?.profileTracked || inventoryResource.phase !== "live") {
      setHistoryPhase("unavailable");
      return;
    }
    let active = true;
    setHistoryPhase("loading");
    void getDevicePresenceHistory(selected.mac, 20).then((items) => {
      if (!active) return;
      setHistory(items);
      setHistoryPhase("live");
    }).catch(() => {
      if (active) setHistoryPhase("unavailable");
    });
    return () => { active = false; };
  }, [selected?.id, selected?.profileTracked, inventoryResource.updatedAt]);

  const saveMetadata = async () => {
    if (!selected?.profileTracked) return;
    setMetadataBusy(true);
    try {
      const tags = tagsText.split(",").map((item) => item.trim()).filter(Boolean);
      const saved = await updateDeviceMetadata(selected.mac, { alias: alias.trim(), vendor: vendor.trim(), tags });
      setAlias(saved.alias ?? "");
      setVendor(saved.vendor ?? "");
      setTagsText(saved.tags.join(", "));
      setMetadataDirty(false);
      setDevices((items) => items.map((item) => item.id === selected.id ? {
        ...item,
        alias: saved.alias,
        vendor: saved.vendor,
        tags: saved.tags,
        name: saved.alias || saved.hostName || item.name,
        firstSeen: saved.firstSeen,
        lastSeen: saved.lastSeen || item.lastSeen,
      } : item));
      notify(`${saved.alias || saved.hostName || saved.macAddress} 的设备画像已保存到 SQLite 并写入审计；RouterOS 未修改`);
      onRefresh(false);
    } catch (error) {
      notify(apiFailureMessage(error, "设备画像保存失败"), "warning");
    } finally {
      setMetadataBusy(false);
    }
  };

  const prepareBinding = async (device = selected) => {
    if (!device) return;
    try {
      const storedPolicy = policyForDevice(policies, device);
      if (!device.dhcpServer) throw new Error("RouterOS 未返回该租约所属 DHCP server，不能生成安全写入计划");
      const result = await planDeviceBinding({ id: storedPolicy?.id ?? `device-${device.id.toString(16)}`, name: device.name, macAddress: device.mac, staticIp: device.ip, dhcpServer: device.dhcpServer, egress: storedPolicy?.egress ?? "direct", targetId: storedPolicy?.targetId });
      if (!result.plan.requiresConfirmation) {
        setDevices((items) => items.map((item) => item.id === device.id ? { ...item, fixed: true } : item));
        notify(`${device.name} 已经是 FoxOS 管理的静态租约`);
        return;
      }
      setPending({ plan: result.plan, token: result.confirmationToken, device });
    } catch (error) {
      notify(error instanceof Error ? error.message : "生成 RouterOS 计划失败", "warning");
    }
  };

  const executeBinding = async () => {
    if (!pending) return;
		const execution = pending;
		setPending(null);
    setExecuting(true);
    try {
			await executeDeviceBinding(execution.plan, execution.token);
			setDevices((items) => items.map((item) => item.id === execution.device.id ? { ...item, fixed: true } : item));
			notify(`${execution.device.name} 已执行并完成回读校验`);
      onRefresh(false);
    } catch (error) {
      notify(error instanceof Error ? error.message : "RouterOS 写入失败", "warning");
    } finally {
      setExecuting(false);
    }
  };

  const prepareEgress = async (device = selected, desiredEgress = egress, desiredTargetId = targetId) => {
    if (!device) return;
    try {
      const storedPolicy = policyForDevice(policies, device);
      const capability = capabilityByMode.get(desiredEgress);
      if (!capability?.available) throw new Error(`该出口当前不可用：${capability?.missing.join("、") || "就绪状态未返回"}`);
      const policy = proposedDevicePolicy(device, storedPolicy, desiredEgress, desiredTargetId);
      if (!policy.dhcpServer) throw new Error("缺少 RouterOS DHCP server，不能保存设备策略");
      const result = await planDeviceEgress(policy.id, policy);
      if (!result.plan.requiresConfirmation) {
        notify(`${selected.name} 的数据库策略与 RouterOS 自有资源已经一致`);
        return;
      }
      if (!result.confirmationToken) throw new Error("服务端未返回出口策略确认令牌");
      setPendingEgress({ plan: result.plan, token: result.confirmationToken, device });
    } catch (error) {
      notify(error instanceof Error ? error.message : "生成设备出口计划失败", "warning");
    }
  };

  const executeEgress = async () => {
    if (!pendingEgress) return;
		const execution = pendingEgress;
		setPendingEgress(null);
    setExecutingEgress(true);
    try {
			const result = await executeDeviceEgress(execution.plan.policyId, execution.plan, execution.token);
      const job = result.job ? await waitForJob(result.job.id) : undefined;
      if (job?.status === "ROLLED_BACK") {
				notify(job.errorMessage || `${execution.device.name} 出口策略应用失败，已回滚 RouterOS 变更`, "warning");
        onRefresh(false);
        return;
      }
      if (job?.status === "FAILED") throw new Error(job.errorMessage || "设备出口策略任务失败");
      if (job && job.status !== "SUCCEEDED") throw new Error(`设备出口策略任务结束于 ${job.status}`);
      if (!job && result.status !== "SUCCEEDED") throw new Error(`设备出口策略执行结束于 ${result.status}`);
			notify(`${execution.device.name} 的出口策略已执行、回读并持久化`);
      onRefresh(false);
    } catch (error) {
      notify(error instanceof Error ? error.message : "设备出口策略执行失败", "warning");
    } finally {
      setExecutingEgress(false);
    }
  };

  const targetOptions = egress === "mihomo-node"
    ? (nodeResource.data ?? []).map((node) => ({ id: node.id, label: node.name }))
    : egress === "proxy-chain"
      ? (groupResource.data ?? []).filter((group) => group.type === "chain").map((group) => ({ id: group.id, label: group.name }))
      : egress === "l2tp"
        ? (l2tpResource.data ?? []).map((client) => ({ id: client.name, label: `${client.name}${client.running && !client.disabled ? "（运行中）" : "（不可用）"}` }))
        : [];

  const targetOptionsFor = (mode: EgressType) => mode === "mihomo-node"
    ? (nodeResource.data ?? []).map((node) => ({ id: node.id, label: node.name }))
    : mode === "proxy-chain"
      ? (groupResource.data ?? []).filter((group) => group.type === "chain").map((group) => ({ id: group.id, label: group.name }))
      : mode === "l2tp"
        ? (l2tpResource.data ?? []).map((client) => ({ id: client.name, label: client.name }))
        : [];

  const changeEgress = (value: EgressType) => {
    setEgress(value);
    const currentTarget = value === selectedPolicy?.egress ? selectedPolicy?.targetId : "";
    setTargetId(currentTarget ?? "");
  };

  return (
    <div className="stack">
      <section className="panel multi-resource-band"><ResourceMeta resource={resource} /><ResourceMeta resource={inventoryResource} /><ResourceMeta resource={policyResource} /><ResourceMeta resource={capabilitiesResource} /><Button icon={RefreshCw} onClick={onRefresh}>刷新设备</Button></section>
      <div className="summary-grid"><SummaryCard icon={Users} label="在线设备" value={live ? devices.filter((device) => device.online).length : "—"} tone="green" /><SummaryCard icon={LockKeyhole} label="静态租约" value={resource.data ? devices.filter((device) => device.fixed).length : "—"} /><SummaryCard icon={Link2} label="动态租约" value={resource.data ? devices.filter((device) => !device.fixed).length : "—"} tone="orange" /><SummaryCard icon={AlertTriangle} label="状态未确认" value={live ? devices.filter((device) => !device.online).length : devices.length || "—"} tone="red" /></div>
      <div className="split-view">
        <section className="panel table-panel">
          <div className="toolbar"><label className="search-box"><Search aria-hidden="true" size={17} /><span className="sr-only">搜索设备</span><input onChange={(event) => setSearch(event.target.value)} placeholder="搜索设备名称 / IP / MAC" value={search} /></label><select aria-label="筛选设备状态" onChange={(event) => setStatus(event.target.value)} value={status}><option value="all">全部状态</option><option value="online">在线</option><option value="offline">离线</option></select></div>
          {filtered.length ? <div aria-label="可横向滚动的数据表" className="table-wrap" role="region" tabIndex={0}><table aria-label="RouterOS 设备" className="interactive-table device-policy-table" role="grid"><thead><tr><th role="columnheader">设备名称</th><th role="columnheader">IP 地址</th><th role="columnheader">MAC 地址</th><th role="columnheader">RouterOS 接口</th><th role="columnheader">出口策略</th><th role="columnheader">状态</th><th role="columnheader">原位操作</th></tr></thead><tbody>{filtered.map((device) => { const policy = policyForDevice(policies, device); const draft = inlineDrafts[device.id] ?? { egress: policy?.egress ?? "direct", targetId: policy?.targetId ?? "" }; const requiresTarget = draft.egress === "mihomo-node" || draft.egress === "proxy-chain" || draft.egress === "l2tp"; const options = targetOptionsFor(draft.egress); const capability = capabilityByMode.get(draft.egress); const protectedDevice = protectedAddressSet.has(device.ip); const canApply = siteReady && live && device.fixed && !protectedDevice && policyResource.phase === "live" && capabilitiesResource.phase === "live" && Boolean(capability?.available) && (!requiresTarget || Boolean(draft.targetId)); return <tr aria-selected={selected?.id === device.id} className={selected?.id === device.id ? "selected" : ""} data-grid-row key={device.id} onClick={() => setSelectedDeviceId(device.id)} onKeyDown={(event) => handleGridRowKeyDown(event, () => setSelectedDeviceId(device.id))} tabIndex={keyboardActiveId === device.id ? 0 : -1}><td><span className="name-cell"><DeviceIcon kind={device.kind} /><strong>{device.name}</strong></span></td><td>{device.ip}</td><td>{device.mac}</td><td>{device.iface}</td><td>{egressLabel(policy?.egress)}</td><td><StatusDot status={live ? device.online ? "ok" : "offline" : "warning"} />{live ? device.online ? "在线" : "离线" : "上次状态"}</td><td><div className="inline-policy-controls" onClick={(event) => event.stopPropagation()} onKeyDown={(event) => event.stopPropagation()}><button aria-label={`固定租约 ${device.name}`} className="icon-button" disabled={device.fixed || !live || !device.dhcpServer || executing} onClick={() => void prepareBinding(device)} title={device.fixed ? "租约已固定" : "生成固定租约计划"} type="button"><LockKeyhole aria-hidden="true" size={15} /></button><select aria-label={`出口选择 ${device.name}`} disabled={!siteReady || !device.fixed || protectedDevice || capabilitiesResource.phase !== "live" || executingEgress} onChange={(event) => { const next = event.target.value as EgressType; setInlineDrafts((current) => ({ ...current, [device.id]: { egress: next, targetId: next === policy?.egress ? policy?.targetId ?? "" : "" } })); }} value={draft.egress}><option disabled={!capabilityByMode.get("direct")?.available} value="direct">直连</option><option disabled={!capabilityByMode.get("mihomo-node")?.available} value="mihomo-node">Mihomo 节点</option><option disabled={!capabilityByMode.get("proxy-chain")?.available} value="proxy-chain">链式代理</option><option disabled={!capabilityByMode.get("l2tp")?.available} value="l2tp">L2TP</option><option disabled={!capabilityByMode.get("blocked")?.available} value="blocked">阻断</option></select>{requiresTarget ? <select aria-label={`出口目标 ${device.name}`} disabled={executingEgress} onChange={(event) => setInlineDrafts((current) => ({ ...current, [device.id]: { ...draft, targetId: event.target.value } }))} value={draft.targetId}><option value="">选择目标</option>{options.map((option) => <option key={option.id} value={option.id}>{option.label}</option>)}</select> : null}<button aria-label={`应用出口 ${device.name}`} className="icon-button apply-icon" disabled={!canApply || executingEgress} onClick={() => void prepareEgress(device, draft.egress, draft.targetId)} title="生成出口应用计划" type="button"><Play aria-hidden="true" size={15} /></button><button aria-label={`阻断设备 ${device.name}`} className="icon-button danger-icon" disabled={!siteReady || !device.fixed || protectedDevice || !capabilityByMode.get("blocked")?.available || executingEgress} onClick={() => void prepareEgress(device, "blocked", "")} title="生成阻断计划" type="button"><Ban aria-hidden="true" size={15} /></button></div></td></tr>; })}</tbody></table></div> : <EmptyState detail="设备接口失败不会回退到演示清单。" title="没有设备数据" />}
        </section>
        <aside className="detail-panel">
          {selected ? (
            <>
              <div className="detail-heading">
                <div>
                  <small>设备详情</small>
                  <h2>{selected.name}</h2>
                  <span><StatusDot status={live ? selected.online ? "ok" : "offline" : "warning"} />{live ? selected.online ? "在线" : "离线" : "状态未确认"} · {selected.lastSeen}</span>
                </div>
                <DeviceIcon kind={selected.kind} />
              </div>
              <DetailSection title="设备信息">
                <dl className="definition-list">
                  <div><dt>IP 地址</dt><dd>{selected.ip}</dd></div>
                  <div><dt>MAC 地址</dt><dd>{selected.mac}</dd></div>
                  <div><dt>RouterOS 接口</dt><dd>{selected.iface}</dd></div>
                  <div><dt>DHCP server</dt><dd>{selected.dhcpServer || "未回读"}</dd></div>
                  <div><dt>当前出口</dt><dd>{egressLabel(selectedPolicy?.egress)}{selectedPolicy?.targetId ? ` · ${selectedPolicy.targetId}` : ""}</dd></div>
                </dl>
              </DetailSection>
              <DetailSection title="设备画像">
                <div className="profile-form">
                  <label className="field"><span>别名</span><input disabled={!selected.profileTracked || metadataBusy} maxLength={128} onChange={(event) => { setAlias(event.target.value); setMetadataDirty(true); }} value={alias} /></label>
                  <label className="field"><span>厂商</span><input disabled={!selected.profileTracked || metadataBusy} maxLength={128} onChange={(event) => { setVendor(event.target.value); setMetadataDirty(true); }} value={vendor} /></label>
                  <label className="field full"><span>标签</span><input disabled={!selected.profileTracked || metadataBusy} onChange={(event) => { setTagsText(event.target.value); setMetadataDirty(true); }} placeholder="work, trusted" value={tagsText} /></label>
                  <Button disabled={!selected.profileTracked || inventoryResource.phase !== "live" || metadataBusy} icon={Save} onClick={() => void saveMetadata()} variant="secondary">{metadataBusy ? "保存中…" : "保存画像"}</Button>
                </div>
                {!selected.profileTracked ? <div className="warning-note"><AlertTriangle aria-hidden="true" size={16} />SQLite 设备库存不可用，画像编辑已禁用。</div> : null}
              </DetailSection>
              <DetailSection title="在线历史">
                <dl className="definition-list">
                  <div><dt>首次发现</dt><dd>{selected.firstSeen ? new Date(selected.firstSeen).toLocaleString("zh-CN", { hour12: false }) : "未记录"}</dd></div>
                  <div><dt>最后在线</dt><dd>{selected.lastSeen === "未提供" ? selected.lastSeen : new Date(selected.lastSeen).toLocaleString("zh-CN", { hour12: false })}</dd></div>
                </dl>
                {historyPhase === "loading" ? <span className="muted-text">加载中…</span> : historyPhase === "unavailable" ? <span className="muted-text">历史不可用</span> : history.length ? <ol className="presence-history">{history.slice(0, 8).map((event) => <li key={event.id}><StatusDot status={event.online ? "ok" : "offline"} /><span>{event.online ? "上线" : "离线"}</span><time dateTime={event.observedAt}>{new Date(event.observedAt).toLocaleString("zh-CN", { hour12: false })}</time></li>)}</ol> : <span className="muted-text">暂无状态切换记录</span>}
              </DetailSection>
              <DetailSection title="IP 绑定">
                <div className="inline-status"><StatusDot status={selected.fixed ? "ok" : "warning"} />{selected.fixed ? "静态租约" : "当前为动态租约"}</div>
                <Button disabled={selected.fixed || !live || !selected.dhcpServer} icon={LockKeyhole} onClick={() => void prepareBinding()}>{selected.fixed ? "IP 已固定" : "生成固定 IP 计划"}</Button>
              </DetailSection>
              <DetailSection title="出口策略">
                <label className="field"><span>选择出口</span><select disabled={!live || policyResource.phase !== "live" || capabilitiesResource.phase !== "live"} onChange={(event) => changeEgress(event.target.value as EgressType)} value={egress}><option disabled={!capabilityByMode.get("direct")?.available} value="direct">直连</option><option disabled={!capabilityByMode.get("mihomo-node")?.available} value="mihomo-node">Mihomo 节点</option><option disabled={!capabilityByMode.get("proxy-chain")?.available} value="proxy-chain">链式代理</option><option disabled={!capabilityByMode.get("l2tp")?.available} value="l2tp">RouterOS L2TP</option><option disabled={!capabilityByMode.get("blocked")?.available} value="blocked">阻断</option></select></label>
                {targetRequired ? <label className="field"><span>目标</span><select disabled={!targetSourceLive} onChange={(event) => setTargetId(event.target.value)} value={targetId}><option value="">请选择目标</option>{targetOptions.map((option) => <option key={option.id} value={option.id}>{option.label}</option>)}</select></label> : null}
                {!siteReady ? <div className="warning-note"><AlertTriangle aria-hidden="true" size={16} />站点清单未加载，设备策略写入已关闭。</div> : !selected.fixed ? <div className="warning-note"><AlertTriangle aria-hidden="true" size={16} />先将租约固定，避免 DHCP 地址变化后策略命中错误设备。</div> : managementProtected ? <div className="warning-note"><ShieldCheck aria-hidden="true" size={16} />该地址属于站点清单中的旁路管理面，禁止创建出口策略。</div> : !selectedCapability?.available ? <div className="warning-note"><AlertTriangle aria-hidden="true" size={16} />该出口当前不可用：{selectedCapability?.missing.join("、") || "就绪状态未返回"}</div> : policyResource.phase !== "live" || !targetSourceLive ? <div className="warning-note"><AlertTriangle aria-hidden="true" size={16} />策略或目标数据源不是实时状态，写入已禁用。</div> : null}
              </DetailSection>
              <div className="detail-actions"><Button disabled={!canPrepareEgress || executingEgress} icon={Play} onClick={() => void prepareEgress()} variant="primary">生成应用计划</Button></div>
            </>
          ) : <EmptyState detail="加载 RouterOS 设备后可查看详情。" title="未选择设备" />}
        </aside>
      </div>
      {pending ? <ConfirmDialog busy={executing} confirmLabel="确认写入 RouterOS" description={`将为 ${pending.device.name} 执行 FoxOS 所有权范围内的静态租约计划。`} impacts={pending.plan.operations.map((operation) => `${operation.method} ${operation.path}：${operation.summary}`)} onCancel={() => setPending(null)} onConfirm={() => void executeBinding()} title="确认固定设备 IP" warnings={pending.plan.warnings} /> : null}
      {pendingEgress ? <ConfirmDialog busy={executingEgress} confirmLabel="确认应用出口策略" description="服务端会重新检查策略前态和 RouterOS 摘要，逐项执行、回读验证并写审计；失败时补偿已应用的 FoxOS 自有资源。" impacts={[`设备：${pendingEgress.device.name} (${pendingEgress.plan.staticIp})`, `策略：${egressLabel(pendingEgress.plan.previousPolicy?.egress)} → ${egressLabel(pendingEgress.plan.policy.egress)}${pendingEgress.plan.policy.targetId ? ` (${pendingEgress.plan.policy.targetId})` : ""}`, ...(pendingEgress.plan.operations.length ? pendingEgress.plan.operations.map((operation) => `${operation.method} ${operation.path}：${operation.summary} [${operation.ownedComment}]`) : ["RouterOS：无需变更；只在任务验证后持久化策略"])]} onCancel={() => !executingEgress && setPendingEgress(null)} onConfirm={() => void executeEgress()} title="确认设备出口变更" warnings={pendingEgress.plan.warnings} /> : null}
    </div>
  );
}

type ProxyPageProps = {
  nodes: ProxyNode[];
  setNodes: React.Dispatch<React.SetStateAction<ProxyNode[]>>;
  selectedNodeId: number;
  setSelectedNodeId: (id: number) => void;
  notify: (message: string, tone?: Toast["tone"]) => void;
  nodeResource: ResourceState<ApiNode[]>;
  mihomoResource: ResourceState<MihomoOverview>;
  l2tpResource: ResourceState<L2TPClient[]>;
  groupResource: ResourceState<ProxyGroup[]>;
  onRefresh: (showToast?: boolean) => void;
  navigate: (key: PageKey) => void;
};

function formatBytes(value: number | undefined): string {
  if (value === undefined || !Number.isFinite(value) || value < 0) return "—";
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let amount = value;
  let unit = 0;
  while (amount >= 1024 && unit < units.length - 1) {
    amount /= 1024;
    unit += 1;
  }
  return `${amount >= 10 || unit === 0 ? amount.toFixed(0) : amount.toFixed(1)} ${units[unit]}`;
}

function latestMihomoDelay(resource: ResourceState<MihomoOverview>, name: string): number | undefined {
  if (resource.phase !== "live") return undefined;
  const history = resource.data?.proxies?.[name]?.history ?? [];
  for (let index = history.length - 1; index >= 0; index -= 1) {
    const delay = history[index]?.delay;
    if (typeof delay === "number" && Number.isFinite(delay) && delay > 0) return delay;
  }
  return undefined;
}

function apiFailureMessage(error: unknown, fallback: string): string {
  if (error instanceof ApiError) {
    return error.references.length ? `${error.message}；引用：${error.references.join("、")}` : error.message;
  }
  return error instanceof Error ? error.message : fallback;
}

function ProxyPage({ nodes, setNodes, selectedNodeId, setSelectedNodeId, notify, nodeResource, mihomoResource, l2tpResource, groupResource, onRefresh, navigate }: ProxyPageProps) {
  const [search, setSearch] = useState("");
  const [protocol, setProtocol] = useState("all");
  const [modal, setModal] = useState<"add" | "import" | null>(null);
  const [testing, setTesting] = useState(false);
  const [chainId, setChainId] = useState("");
  const [chainName, setChainName] = useState("");
  const [chainNodeIds, setChainNodeIds] = useState<string[]>([]);
  const [chainBusy, setChainBusy] = useState(false);
  const [chainDelete, setChainDelete] = useState<ProxyGroup | null>(null);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [mihomoChecks, setMihomoChecks] = useState<Record<number, MihomoProbeResult>>({});
  const [probingMihomo, setProbingMihomo] = useState(false);
  const selected = nodes.find((node) => node.id === selectedNodeId) ?? nodes[0];
  const filtered = nodes.filter((node) => `${node.name}${node.server}${node.region}`.toLowerCase().includes(search.toLowerCase()) && (protocol === "all" || node.protocol === protocol));
  const keyboardActiveId = filtered.some((node) => node.id === selected?.id) ? selected?.id : filtered[0]?.id;
  const chains = (groupResource.data ?? []).filter((group) => group.type === "chain");
  const foxosNodes = nodes.filter((node): node is ProxyNode & { apiId: string } => Boolean(node.apiId));
  const runtimeLive = mihomoResource.phase === "live" && Boolean(mihomoResource.data?.online);
  const runtimeProxies = runtimeLive ? mihomoResource.data?.proxies ?? {} : {};
  const runtimeSelectors = Object.entries(runtimeProxies).filter(([, proxy]) => typeof proxy.now === "string" && proxy.now);
  const selectedRuntime = selected ? runtimeProxies[selected.name] : undefined;
  const selectedRuntimeDelay = selected ? latestMihomoDelay(mihomoResource, selected.name) : undefined;
  const selectedBy = selected ? runtimeSelectors.filter(([, proxy]) => proxy.now === selected.name).map(([name]) => name) : [];
  const selectedCheck = selected ? mihomoChecks[selected.id] : undefined;

  const runMihomoProbe = async () => {
    if (!selected?.apiId) return;
    setProbingMihomo(true);
    try {
      const result = await probeMihomoNode(selected.apiId);
      setMihomoChecks((items) => ({ ...items, [selected.id]: result }));
      const messages = [result.nodeHttp.success ? `节点 HTTP ${result.nodeHttp.latencyMs} ms` : "节点 HTTP 失败"];
      messages.push(result.exit.success ? `当前策略出口 ${result.exit.ipAddress}` : result.exit.available ? "当前策略出口检测失败" : "当前策略出口未配置");
      notify(`${selected.name}：${messages.join("；")}`, result.nodeHttp.success && (!result.exit.available || result.exit.success) ? "success" : "warning");
    } catch (error) {
      notify(apiFailureMessage(error, "Mihomo 代理探测失败"), "warning");
    } finally {
      setProbingMihomo(false);
    }
  };

  const runTCPProbe = async () => {
    if (!selected?.apiId) return;
    try {
      const result = await probeNode(selected.apiId);
      setNodes((items) => items.map((item) => item.id === selected.id ? { ...item, latency: result.latencyMs, status: result.reachable ? "online" : "offline", verification: "tcp" } : item));
      notify(`${selected.name}：${result.reachable ? "TCP 可达" : "TCP 不可达"}，${result.latencyMs} ms`, result.reachable ? "success" : "warning");
    } catch (error) {
      notify(error instanceof Error ? error.message : "节点检测失败", "warning");
    }
  };

  const editChain = (group?: ProxyGroup) => {
    setChainId(group?.id ?? "");
    setChainName(group?.name ?? "");
    setChainNodeIds(group?.nodeIds ?? []);
  };

  useEffect(() => {
    const selectedGroup = chains.find((group) => group.id === chainId) ?? chains[0];
    editChain(selectedGroup);
  }, [groupResource.updatedAt]);

  const runTests = async () => {
    const candidates = nodes.filter((node) => node.apiId);
    if (!candidates.length) {
      notify("没有可由 FoxOS TCP 探测的节点", "warning");
      return;
    }
    setTesting(true);
    const results = await Promise.allSettled(candidates.map(async (node) => ({ node, result: await probeNode(node.apiId!) })));
    setNodes((items) => items.map((item) => {
      const match = results.find((entry) => entry.status === "fulfilled" && entry.value.node.id === item.id);
      if (!match || match.status !== "fulfilled") return item;
      return { ...item, latency: match.value.result.latencyMs, status: match.value.result.reachable ? "online" : "offline", verification: "tcp" };
    }));
    const requestFailures = results.filter((result) => result.status === "rejected").length;
    const unreachable = results.filter((result) => result.status === "fulfilled" && !result.value.result.reachable).length;
    notify(`TCP 探测完成：${results.length - requestFailures} 个有结果，${unreachable} 个不可达，${requestFailures} 个请求失败`, requestFailures || unreachable ? "warning" : "success");
    setTesting(false);
  };

  const deleteSelected = async () => {
    if (!selected?.apiId) return;
    setDeleting(true);
    try {
      await deleteNodeApi(selected.apiId);
      setNodes((items) => items.filter((item) => item.id !== selected.id));
      setSelectedNodeId(nodes.find((item) => item.id !== selected.id)?.id ?? 0);
      notify(`${selected.name} 已删除`);
      setDeleteOpen(false);
    } catch (error) {
      notify(apiFailureMessage(error, "节点删除失败"), "warning");
    } finally {
      setDeleting(false);
    }
  };

  const saveChain = async () => {
    if (!chainName.trim()) {
      notify("请输入链式代理名称", "warning");
      return;
    }
    if (chainNodeIds.length < 2) {
      notify("链式代理至少需要两个有序节点", "warning");
      return;
    }
    setChainBusy(true);
    try {
      const payload = { id: chainId || undefined, name: chainName.trim(), type: "chain" as const, nodeIds: chainNodeIds, groupIds: [] };
      const saved = chainId
        ? await updateProxyGroup({ ...payload, id: chainId })
        : await createProxyGroup(payload);
      editChain(saved);
      notify(`${saved.name} 已保存到 SQLite；Mihomo 运行配置尚未改变`);
      onRefresh(false);
    } catch (error) {
      notify(apiFailureMessage(error, "链式代理保存失败"), "warning");
    } finally {
      setChainBusy(false);
    }
  };

  const removeChain = async () => {
    if (!chainDelete) return;
    setChainBusy(true);
    try {
      await deleteProxyGroup(chainDelete.id);
      notify(`${chainDelete.name} 已从 SQLite 删除；Mihomo 运行配置尚未改变`);
      setChainDelete(null);
      editChain();
      onRefresh(false);
    } catch (error) {
      notify(apiFailureMessage(error, "链式代理删除失败"), "warning");
    } finally {
      setChainBusy(false);
    }
  };

  const moveChainNode = (index: number, offset: -1 | 1) => {
    const nextIndex = index + offset;
    if (nextIndex < 0 || nextIndex >= chainNodeIds.length) return;
    setChainNodeIds((items) => {
      const next = [...items];
      [next[index], next[nextIndex]] = [next[nextIndex], next[index]];
      return next;
    });
  };

  return (
    <div className="stack">
      <section className="panel multi-resource-band"><ResourceMeta resource={nodeResource} /><ResourceMeta resource={mihomoResource} /><ResourceMeta resource={l2tpResource} /><ResourceMeta resource={groupResource} /><Button icon={RefreshCw} onClick={onRefresh}>刷新节点</Button></section>
      <div className="proxy-header-row"><div className="summary-grid"><SummaryCard icon={Activity} label="Mihomo 活动连接" value={runtimeLive ? mihomoResource.data?.traffic?.connectionCount ?? mihomoResource.data?.connections?.length ?? 0 : "—"} tone="green" /><SummaryCard icon={Download} label="累计下载" value={runtimeLive ? formatBytes(mihomoResource.data?.traffic?.downloadTotal) : "—"} /><SummaryCard icon={Upload} label="累计上传" value={runtimeLive ? formatBytes(mihomoResource.data?.traffic?.uploadTotal) : "—"} tone="purple" /><SummaryCard icon={ListChecks} label="运行中选择器" value={runtimeLive ? runtimeSelectors.length : "—"} tone="orange" /></div><div className="header-actions"><Button icon={Plus} onClick={() => setModal("add")} variant="primary">添加节点</Button><Button icon={Upload} onClick={() => setModal("import")}>导入分享链接</Button><Button disabled={testing} icon={Activity} onClick={() => void runTests()}>{testing ? "检测中…" : "TCP 检测"}</Button></div></div>
      <section className="panel runtime-policy-panel"><div className="panel-heading"><div><h2>Mihomo 当前策略</h2><p>直接读取 Controller /proxies；不代表 HTTP、DNS 或出口 IP 已验证</p></div><span className="unavailable-label"><StatusDot status={runtimeLive ? "ok" : "warning"} />{runtimeLive ? `v${mihomoResource.data?.version || "未知"}` : phaseLabel(mihomoResource.phase)}</span></div>{runtimeSelectors.length ? <dl className="definition-list compact-definition">{runtimeSelectors.map(([name, proxy]) => <div key={name}><dt>{name}</dt><dd>{proxy.now}</dd></div>)}</dl> : <EmptyState detail="Controller 未返回选择器状态时不会推断当前代理策略。" title="没有可验证的选择器数据" />}</section>
      <section className="panel chain-builder">
        <div className="panel-heading"><div><h2>链式代理组</h2><p>有序节点保存在 SQLite；运行配置需单独预览和发布</p></div><span className="unavailable-label"><StatusDot status={groupResource.phase === "live" ? "ok" : "warning"} />{groupResource.phase === "live" ? `${chains.length} 个链` : phaseLabel(groupResource.phase)}</span></div>
        <div className="chain-toolbar">
          <label className="field"><span>编辑链</span><select disabled={groupResource.phase !== "live" || chainBusy} onChange={(event) => editChain(chains.find((group) => group.id === event.target.value))} value={chainId}><option value="">新建链式代理</option>{chains.map((group) => <option key={group.id} value={group.id}>{group.name}</option>)}</select></label>
          <label className="field operation-grow"><span>名称</span><input disabled={groupResource.phase !== "live" || chainBusy} onChange={(event) => setChainName(event.target.value)} placeholder="例如：工作出口链" value={chainName} /></label>
          <Button disabled={groupResource.phase !== "live" || chainBusy} icon={Plus} onClick={() => editChain()}>新建</Button>
          <Button disabled={groupResource.phase !== "live" || chainBusy || chainNodeIds.length < 2 || !chainName.trim()} icon={Save} onClick={() => void saveChain()} variant="primary">保存组</Button>
          <Button disabled={!chainId || chainBusy} icon={Trash2} onClick={() => setChainDelete(chains.find((group) => group.id === chainId) ?? null)} variant="danger">删除组</Button>
        </div>
        <div aria-label="链式代理路径，可横向滚动" className="chain-builder-row" role="region" tabIndex={0}><ChainNode detail="设备出口" icon={Router} locked name="RouterOS" />{chainNodeIds.map((id, index) => { const node = foxosNodes.find((item) => item.apiId === id); return <ChainNode detail={node ? `${node.protocol} · 第 ${index + 1} 跳` : `缺失引用 · 第 ${index + 1} 跳`} icon={node ? Server : AlertTriangle} key={`${id}-${index}`} name={node?.name ?? id} onMoveLeft={index > 0 ? () => moveChainNode(index, -1) : undefined} onMoveRight={index < chainNodeIds.length - 1 ? () => moveChainNode(index, 1) : undefined} onRemove={() => setChainNodeIds((items) => items.filter((_, itemIndex) => itemIndex !== index))} />; })}<div className="add-stage"><select aria-label="添加链路节点" disabled={groupResource.phase !== "live" || chainBusy} defaultValue="" onChange={(event) => { const id = event.target.value; if (id && !chainNodeIds.includes(id)) setChainNodeIds((items) => [...items, id]); event.currentTarget.value = ""; }}><option value="">添加链路节点</option>{foxosNodes.filter((node) => !chainNodeIds.includes(node.apiId)).map((node) => <option key={node.apiId} value={node.apiId}>{node.name}</option>)}</select></div><ChainNode detail="目标网络" icon={Globe2} locked name="Internet" /></div>
        <div className="chain-publish-note"><span><FileText aria-hidden="true" size={16} />保存组不会热重载 Mihomo。发布前必须检查生成配置与脱敏 Diff。</span><Button icon={ArrowRight} onClick={() => document.getElementById("mihomo-publish")?.scrollIntoView({ block: "start" })} variant="secondary">预览并发布</Button></div>
      </section>
      <div className="split-view">
        <section className="panel table-panel"><div className="toolbar"><label className="search-box"><Search aria-hidden="true" size={17} /><span className="sr-only">搜索节点</span><input onChange={(event) => setSearch(event.target.value)} placeholder="搜索节点 / 服务器" value={search} /></label><select aria-label="筛选节点协议" onChange={(event) => setProtocol(event.target.value)} value={protocol}><option value="all">全部协议</option>{[...new Set(nodes.map((node) => node.protocol))].map((item) => <option key={item}>{item}</option>)}</select></div>{filtered.length ? <div aria-label="可横向滚动的数据表" className="table-wrap" role="region" tabIndex={0}><table aria-label="代理节点" className="interactive-table" role="grid"><thead><tr><th role="columnheader">节点名称</th><th role="columnheader">协议</th><th role="columnheader">服务器</th><th role="columnheader">来源</th><th role="columnheader">Mihomo 延迟</th><th role="columnheader">TCP 延迟</th><th role="columnheader">验证状态</th></tr></thead><tbody>{filtered.map((node) => { const sourcePhase = node.verification === "routeros-session" ? l2tpResource.phase : nodeResource.phase; const runtimeDelay = latestMihomoDelay(mihomoResource, node.name); return <tr aria-selected={selected?.id === node.id} className={selected?.id === node.id ? "selected" : ""} data-grid-row key={node.id} onClick={() => setSelectedNodeId(node.id)} onKeyDown={(event) => handleGridRowKeyDown(event, () => setSelectedNodeId(node.id))} tabIndex={keyboardActiveId === node.id ? 0 : -1}><td><strong>{node.name}</strong></td><td>{node.protocol}</td><td>{node.server}</td><td>{node.source}</td><td>{runtimeDelay === undefined ? "未返回" : `${runtimeDelay} ms`}</td><td>{node.verification !== "tcp" || node.latency === null ? "未检测" : `${node.latency} ms`}</td><td><StatusDot status={proxyStatusTone(node, sourcePhase)} />{proxyStatusLabel(node, sourcePhase)}</td></tr>; })}</tbody></table></div> : <EmptyState detail="节点读取失败时不会显示演示节点。" title="没有节点数据" />}</section>
        <aside className="detail-panel">
          {selected ? (
            <>
              <div className="detail-heading">
                <div>
                  <small>节点详情</small>
                  <h2>{selected.name}</h2>
                  <span><StatusDot status={proxyStatusTone(selected, selected.verification === "routeros-session" ? l2tpResource.phase : nodeResource.phase)} />{proxyStatusLabel(selected, selected.verification === "routeros-session" ? l2tpResource.phase : nodeResource.phase)}</span>
                </div>
                <Server aria-hidden="true" size={24} />
              </div>
              <div className="detail-button-row">
                <Button disabled={!selected.apiId} icon={Activity} onClick={() => void runTCPProbe()}>TCP 测试</Button>
                <Button disabled={!selected.apiId || probingMihomo} icon={Globe2} onClick={() => void runMihomoProbe()}>{probingMihomo ? "探测中…" : "代理 HTTP / 出口"}</Button>
                <Button disabled={!selected.apiId} icon={Trash2} onClick={() => setDeleteOpen(true)} variant="danger">删除</Button>
              </div>
              <DetailSection title="连接信息">
                <dl className="definition-list">
                  <div><dt>服务器</dt><dd>{selected.server}</dd></div>
                  <div><dt>协议</dt><dd>{selected.protocol}</dd></div>
                  <div><dt>来源</dt><dd>{selected.source}</dd></div>
                  <div><dt>引用</dt><dd>{selected.inUse}</dd></div>
                </dl>
              </DetailSection>
              <DetailSection title="Mihomo 运行态">
                <dl className="definition-list">
                  <div><dt>Controller 条目</dt><dd>{selectedRuntime ? selectedRuntime.type || "已返回" : runtimeLive ? "未找到同名节点" : "不可用"}</dd></div>
                  <div><dt>历史延迟</dt><dd>{selectedRuntimeDelay === undefined ? "未返回" : selectedRuntimeDelay + " ms"}</dd></div>
                  <div><dt>当前被选择器使用</dt><dd>{selectedBy.length ? selectedBy.join("、") : "未发现"}</dd></div>
                </dl>
              </DetailSection>
              <DetailSection title="已验证范围">
                <dl className="definition-list">
                  <div><dt>TCP 可达性</dt><dd>{selected.verification === "tcp" && selected.latency !== null ? selected.latency + " ms" : "未检测"}</dd></div>
                  <div><dt>RouterOS 会话</dt><dd>{selected.verification === "routeros-session" ? proxyStatusLabel(selected, l2tpResource.phase) : "不适用"}</dd></div>
                  <div><dt>代理握手</dt><dd>未单独检测</dd></div>
                  <div><dt>节点 HTTP</dt><dd>{selectedCheck?.nodeHttp.success ? selectedCheck.nodeHttp.latencyMs + " ms" : selectedCheck?.nodeHttp.available ? "失败" : "未检测"}</dd></div>
                  <div><dt>DNS 解析</dt><dd>未单独检测</dd></div>
                  <div><dt>出口 IP</dt><dd>{selectedCheck?.exit.success ? selectedCheck.exit.ipAddress + "（当前策略）" : selectedCheck?.exit.available ? "检测失败" : "未配置"}</dd></div>
                  <div><dt>出口 HTTP 延迟</dt><dd>{selectedCheck?.exit.success ? selectedCheck.exit.latencyMs + " ms" : "未检测"}</dd></div>
                  <div><dt>抖动 / 丢包</dt><dd>未检测</dd></div>
                </dl>
              </DetailSection>
            </>
          ) : <EmptyState detail="加载节点后可查看详情。" title="未选择节点" />}
        </aside>
      </div>
      {modal ? <ProxyModal mode={modal} notify={notify} onClose={() => setModal(null)} onCreate={(node) => { setNodes((items) => [...items, node]); setSelectedNodeId(node.id); setModal(null); notify(`${node.name} 已保存，尚未执行连通性检测`); }} onImported={() => onRefresh(false)} /> : null}
      {deleteOpen && selected ? <ConfirmDialog busy={deleting} confirmLabel="确认删除节点" description="删除后节点凭据与定义将从 FoxOS 数据库移除；Mihomo 运行配置不会在本阶段自动发布。" impacts={[`节点：${selected.name}`, `服务器：${selected.server}`, `已知引用：${selected.inUse}`]} onCancel={() => setDeleteOpen(false)} onConfirm={() => void deleteSelected()} title="删除代理节点" warnings={["服务端会再次检查代理组和设备策略引用；存在引用时必须拒绝删除"]} /> : null}
      {chainDelete ? <ConfirmDialog busy={chainBusy} confirmLabel="确认删除代理链" description="只删除 SQLite 中的代理组定义；当前 Mihomo 运行配置保持不变，直到下一次显式发布。" impacts={[`代理链：${chainDelete.name}`, `有序节点：${chainDelete.nodeIds.length} 个`, `组 ID：${chainDelete.id}`]} onCancel={() => !chainBusy && setChainDelete(null)} onConfirm={() => void removeChain()} title="确认删除链式代理" warnings={["服务端会检查设备策略和其他代理组引用；存在引用时拒绝删除"]} /> : null}
    </div>
  );
}

function ChainNode({ icon: Icon, name, detail, locked, onRemove, onMoveLeft, onMoveRight }: { icon: typeof Router; name: string; detail: string; locked?: boolean; onRemove?: () => void; onMoveLeft?: () => void; onMoveRight?: () => void }) {
  return <div className="chain-node"><Icon aria-hidden="true" size={22} /><span><strong>{name}</strong><small>{detail}</small></span>{!locked ? <span className="chain-node-actions"><button aria-label={`前移 ${name}`} disabled={!onMoveLeft} onClick={onMoveLeft} title="前移" type="button"><ChevronLeft size={14} /></button><button aria-label={`后移 ${name}`} disabled={!onMoveRight} onClick={onMoveRight} title="后移" type="button"><ChevronRight size={14} /></button><button aria-label={`移除 ${name}`} onClick={onRemove} title="移除" type="button"><X size={14} /></button></span> : null}</div>;
}

function ProxyModal({ mode, onClose, onCreate, onImported, notify }: { mode: "add" | "import"; onClose: () => void; onCreate: (node: ProxyNode) => void; onImported: () => void; notify: (message: string, tone?: Toast["tone"]) => void }) {
  const [submitting, setSubmitting] = useState(false);
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    setSubmitting(true);
    try {
      if (mode === "import") {
        const result = await importNodeLinks(String(data.get("links") || ""));
			notify(result.skipped ? `已原子导入 ${result.imported} 个节点，跳过 ${result.skipped} 个无效条目；尚未发布至 Mihomo` : `已原子导入 ${result.imported} 个节点；尚未发布至 Mihomo`, result.skipped ? "warning" : "success");
        onClose();
        onImported();
        return;
      }
      const address = String(data.get("server") || "");
      const splitAt = address.lastIndexOf(":");
      if (splitAt < 1) throw new Error("服务器地址必须包含端口，例如 example.com:443");
      const protocol = String(data.get("protocol") || "VLESS");
      const identity = String(data.get("identity") || "");
      const created = await createNodeApi({ name: String(data.get("name") || "新节点"), type: protocol.toLowerCase() === "shadowsocks" ? "ss" : protocol.toLowerCase(), server: address.slice(0, splitAt).replace(/^\[|\]$/g, ""), port: Number(address.slice(splitAt + 1)), username: ["SOCKS5", "HTTP"].includes(protocol) ? identity : undefined, uuid: ["VLESS", "VMess"].includes(protocol) ? identity : undefined, password: String(data.get("password") || "") });
      onCreate(mapApiNode(created));
    } catch (error) {
      notify(error instanceof Error ? error.message : "节点保存失败", "warning");
    } finally {
      setSubmitting(false);
    }
  };
  return (
    <Dialog eyebrow={mode === "add" ? "手动配置" : "本地分享链接"} onClose={() => !submitting && onClose()} title={mode === "add" ? "添加代理节点" : "导入代理节点"}>
      <form className="modal-form" onSubmit={submit}>
        {mode === "add" ? <div className="form-grid"><label className="field"><span>节点名称</span><input autoFocus data-autofocus name="name" required /></label><label className="field"><span>协议</span><select name="protocol"><option>VLESS</option><option>Trojan</option><option>Shadowsocks</option><option>Hysteria2</option><option>SOCKS5</option></select></label><label className="field full"><span>服务器地址</span><input name="server" placeholder="server.example.com:443" required /></label><label className="field"><span>UUID / 用户名</span><input name="identity" /></label><label className="field"><span>密码</span><input autoComplete="new-password" name="password" type="password" /></label></div> : <div className="form-grid"><label className="field full"><span>分享链接（每行一个）</span><textarea autoFocus data-autofocus name="links" required rows={8} /></label><div className="warning-note full"><ShieldCheck aria-hidden="true" size={16} />链接由 FoxOS 本地逐项解析；有效项原子导入，无效项返回脱敏错误。</div></div>}
        <div className="modal-actions"><Button disabled={submitting} onClick={onClose}>取消</Button><Button disabled={submitting} icon={mode === "add" ? Plus : Upload} type="submit" variant="primary">{submitting ? "正在保存…" : mode === "add" ? "保存节点" : "校验并导入"}</Button></div>
      </form>
    </Dialog>
  );
}

function DetailSection({ title, children }: { title: string; children: React.ReactNode }) {
  return <section className="detail-section"><h3>{title}</h3>{children}</section>;
}

function LogsPage({ items, resource, onRefresh }: { items: LogItem[]; resource: ResourceState<AuditEvent[]>; onRefresh: () => void }) {
  const [level, setLevel] = useState("all");
  const [search, setSearch] = useState("");
  const filtered = items.filter((item) => (level === "all" || item.level === level) && `${item.source}${item.event}${item.detail}`.toLowerCase().includes(search.toLowerCase()));
  return <div className="stack"><section className="panel status-band"><ResourceMeta resource={resource} /><Button icon={RefreshCw} onClick={onRefresh}>刷新审计</Button></section><section className="panel table-panel"><div className="toolbar"><label className="search-box"><Search aria-hidden="true" size={17} /><span className="sr-only">搜索审计事件</span><input onChange={(event) => setSearch(event.target.value)} placeholder="搜索事件、来源或详情" value={search} /></label><select aria-label="筛选日志级别" onChange={(event) => setLevel(event.target.value)} value={level}><option value="all">全部级别</option><option>成功</option><option>信息</option><option>错误</option></select></div>{filtered.length ? <div aria-label="可横向滚动的数据表" className="table-wrap" role="region" tabIndex={0}><table><thead><tr><th>时间</th><th>级别</th><th>来源</th><th>事件</th><th>目标</th></tr></thead><tbody>{filtered.map((item) => <tr key={`${item.time}-${item.event}-${item.detail}`}><td>{item.time}</td><td><span className={`log-level ${item.level}`}>{item.level}</span></td><td>{item.source}</td><td><strong>{item.event}</strong></td><td>{item.detail}</td></tr>)}</tbody></table></div> : <EmptyState detail="审计接口失败时不会显示样例日志。" title="没有审计记录" />}</section></div>;
}

function SettingsPage({ notify, onAuthenticated, onSignedOut, site, siteError }: { notify: (message: string, tone?: Toast["tone"]) => void; onAuthenticated: () => void; onSignedOut: () => void; site: SiteManifest | null; siteError: string }) {
  const [authenticated, setAuthenticated] = useState<boolean | null>(null);
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    let active = true;
    void restoreBrowserSession().then((value) => {
      if (active) setAuthenticated(value);
    }).catch(() => {
      if (active) setAuthenticated(false);
    });
    return () => { active = false; };
  }, []);
  useEffect(() => subscribeSessionInvalidation(() => setAuthenticated(false)), []);
  const save = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (busy) return;
    const data = new FormData(event.currentTarget);
    const form = event.currentTarget;
    setBusy(true);
    try {
			await authenticateApiToken(String(data.get("apiToken") || ""));
			setAuthenticated(true);
			notify("浏览器安全会话已建立");
			form.reset();
				onAuthenticated();
    } catch (error) {
      const restored = await restoreBrowserSession().catch(() => false);
				setAuthenticated(restored);
      notify(error instanceof Error ? error.message : "会话建立失败", "warning");
		} finally {
			setBusy(false);
    }
  };
	const signOut = async () => {
		if (busy) return;
		setBusy(true);
		try {
				await deleteBrowserSession();
				setAuthenticated(false);
				onSignedOut();
				notify("浏览器会话已注销");
		} catch (error) {
			notify(error instanceof Error ? error.message : "注销失败", "warning");
		} finally {
			setBusy(false);
		}
	};
  return (
    <div className="page-grid two-thirds">
      <form className="panel" onSubmit={save}>
				<div className="panel-heading"><div><h2>FoxOS API 凭据</h2><p>{authenticated === null ? "正在检查浏览器会话" : authenticated ? "HttpOnly 浏览器会话有效" : "尚未建立浏览器会话"}</p></div><ShieldCheck aria-hidden="true" className={authenticated ? "green-text" : undefined} size={22} /></div>
        <div className="settings-section"><div className="form-grid"><label className="field full"><span>API Token</span><input autoComplete="off" minLength={32} name="apiToken" required type="password" /></label></div></div>
				<div className="form-actions"><Button disabled={busy} icon={authenticated ? Check : Save} type="submit" variant="primary">{busy ? "处理中…" : authenticated ? "重新建立会话" : "建立安全会话"}</Button>{authenticated ? <Button disabled={busy} icon={LockKeyhole} onClick={() => void signOut()}>注销会话</Button> : null}</div>
      </form>
      <aside className="stack">
        <section className="panel">
          <div className="panel-heading"><div><h2>站点清单</h2><p>来自当前 FoxOS 后端</p></div>{site?.https.enabled ? <ShieldCheck aria-hidden="true" className="green-text" size={22} /> : <AlertTriangle aria-hidden="true" size={22} />}</div>
          {site ? <><dl className="definition-list"><div><dt>管理网段</dt><dd>{site.network}</dd></div><div><dt>管理桥</dt><dd>{site.managementBridge}</dd></div><div><dt>存储</dt><dd>{site.storageRoot}</dd></div><div><dt>访问地址</dt><dd>{site.publicUrl || site.services.foxos.url || "未配置"}</dd></div><div><dt>HTTPS</dt><dd>{site.https.enabled ? "已启用" : "未启用"}</dd></div>{site.https.caSha256 ? <div><dt>本地 CA SHA-256</dt><dd className="fingerprint">{site.https.caSha256}</dd></div> : null}</dl>{site.https.caDownloadPath ? <a className="button secondary" download href={site.https.caDownloadPath}><Download aria-hidden="true" size={16} />下载本地 CA</a> : null}</> : <EmptyState detail={siteError || "等待站点 API 返回。"} title="站点清单不可用" />}
        </section>
        <section className="panel">
          <div className="panel-heading"><div><h2>服务端配置边界</h2><p>以下内容仅通过容器环境变量配置</p></div></div>
          <dl className="definition-list"><div><dt>RouterOS</dt><dd>FOXOS_ROUTEROS_*</dd></div><div><dt>Mihomo</dt><dd>FOXOS_MIHOMO_*</dd></div><div><dt>确认密钥</dt><dd>FOXOS_CONFIRMATION_KEY</dd></div><div><dt>MosDNS</dt><dd>只读状态</dd></div></dl>
        </section>
        <section className="panel">
          <div className="panel-heading"><div><h2>诊断导出</h2><p>暂未开放</p></div></div>
          <div className="action-list"><button disabled type="button"><Upload size={18} /><span><strong>导出脱敏诊断包</strong><small>等待后端导出 API</small></span><ChevronRight size={16} /></button></div>
        </section>
      </aside>
    </div>
  );
}

export default App;
