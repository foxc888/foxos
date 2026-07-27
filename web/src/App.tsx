import { FormEvent, useCallback, useEffect, useRef, useState } from "react";
import {
  Activity,
  AlertTriangle,
  ArrowRight,
  Cable,
  Check,
  CheckCircle2,
  ChevronLeft,
  ChevronRight,
  CircleGauge,
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
  createProxyGroup,
  createNode as createNodeApi,
  deleteNode as deleteNodeApi,
  deleteProxyGroup,
  type DeviceBindingPlan,
  type DeviceInventory,
  type DevicePolicy,
  type DevicePresenceEvent,
  type DeviceProfile,
  type EgressPlan,
  type EgressType,
  executeDeviceBinding,
  executeDeviceEgress,
  getDevicePresenceHistory,
  importNodeLinks,
  type L2TPClient,
  loadLiveSnapshot,
  type MihomoOverview,
  type MihomoProbeResult,
  type MosDNSOverview,
  planDeviceBinding,
  planDeviceEgress,
  probeNode,
  probeMihomoNode,
  type ProxyGroup,
  type RouterContainer,
  type RouterDHCPServer,
  type RouterOverview,
  type RouterRoute,
  saveApiToken,
  updateProxyGroup,
  updateDeviceMetadata,
  waitForJob,
} from "./api";
import { ConfirmDialog, Dialog } from "./components/Dialog";
import { AlertBackupOperations, MihomoOperations, SubscriptionOperations } from "./components/OperationsPanels";
import { type Device, type ProxyNode, services } from "./data";
import { egressLabel, policyForDevice, proposedDevicePolicy } from "./policy-state";
import {
  expireResource,
  initialResource,
  markLoading,
  mergeResult,
  phaseLabel,
  type ResourceState,
} from "./live-state";

export type PageKey =
  | "overview"
  | "routeros"
  | "mosdns"
  | "proxies"
  | "devices"
  | "topology"
  | "logs"
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
};

const pageKeys: PageKey[] = ["overview", "routeros", "mosdns", "proxies", "devices", "topology", "logs", "operations", "settings"];

export function pageFromHash(hash: string): PageKey {
  const candidate = hash.replace(/^#/, "") as PageKey;
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
  { key: "routeros", label: "RouterOS", icon: Router },
  { key: "mosdns", label: "MosDNS", icon: Database },
  { key: "proxies", label: "代理节点", icon: Globe2 },
  { key: "devices", label: "设备管理", icon: Monitor },
  { key: "topology", label: "网络拓扑", icon: Network },
  { key: "logs", label: "日志", icon: FileText },
  { key: "operations", label: "运维任务", icon: Activity },
  { key: "settings", label: "设置", icon: Settings },
];

const pageTitle: Record<PageKey, { title: string; subtitle: string }> = {
  overview: { title: "总览", subtitle: "RouterOS、Mihomo、MosDNS 与局域网设备的可验证状态" },
  routeros: { title: "RouterOS", subtitle: "资源、接口与 DHCP 只读数据" },
  mosdns: { title: "MosDNS", subtitle: "只读运行状态，不接管 DNS 配置" },
  proxies: { title: "代理节点", subtitle: "FoxOS 节点与 RouterOS 原生 L2TP" },
  devices: { title: "设备管理", subtitle: "RouterOS 设备读取与受控静态租约" },
  topology: { title: "网络拓扑", subtitle: "仅展示已由 API 验证的管理面组件" },
  logs: { title: "审计日志", subtitle: "FoxOS API 返回的操作记录" },
  operations: { title: "运维任务", subtitle: "配置发布、订阅、告警与备份" },
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
  let nextIndex = currentIndex;
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
  const [clock, setClock] = useState(() => new Date());
  const toastTimer = useRef<number | undefined>(undefined);
  const mainRef = useRef<HTMLElement>(null);
  const mobileMenuRef = useRef<HTMLButtonElement>(null);
  const sidebarRef = useRef<HTMLElement>(null);
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

  const refreshLiveData = useCallback(async (showToast = false) => {
    setScanning(true);
    setResources((current) => ({
      routeros: markLoading(current.routeros),
      mihomo: markLoading(current.mihomo),
      mosdns: markLoading(current.mosdns),
      nodes: markLoading(current.nodes),
      l2tp: markLoading(current.l2tp),
      routes: markLoading(current.routes),
      dhcpServers: markLoading(current.dhcpServers),
      containers: markLoading(current.containers),
      deviceInventory: markLoading(current.deviceInventory),
      policies: markLoading(current.policies),
      groups: markLoading(current.groups),
      audit: markLoading(current.audit),
    }));
    try {
      const snapshot = await loadLiveSnapshot();
      setResources((current) => ({
        routeros: mergeResult(current.routeros, snapshot.routeros),
        mihomo: mergeResult(current.mihomo, snapshot.mihomo),
        mosdns: mergeResult(current.mosdns, snapshot.mosdns),
        nodes: mergeResult(current.nodes, snapshot.nodes),
        l2tp: mergeResult(current.l2tp, snapshot.l2tp),
        routes: mergeResult(current.routes, snapshot.routes),
        dhcpServers: mergeResult(current.dhcpServers, snapshot.dhcpServers),
        containers: mergeResult(current.containers, snapshot.containers),
        deviceInventory: mergeResult(current.deviceInventory, snapshot.deviceInventory),
        policies: mergeResult(current.policies, snapshot.policies),
        groups: mergeResult(current.groups, snapshot.groups),
        audit: mergeResult(current.audit, snapshot.audit),
      }));

      if (snapshot.nodes.ok) {
        const next = snapshot.nodes.data.map(mapApiNode);
        setNodes((current) => [...next, ...current.filter((node) => node.protocol === "L2TP")]);
        setSelectedNodeId((current) => next.some((node) => node.id === current) ? current : next[0]?.id ?? 0);
      }
      if (snapshot.l2tp.ok) {
        const next = snapshot.l2tp.data.map(mapL2TPNode);
        setNodes((current) => [...current.filter((node) => node.protocol !== "L2TP"), ...next]);
        setSelectedNodeId((current) => current || next[0]?.id || 0);
      }
      if (snapshot.routeros.ok) {
        const profiles = snapshot.deviceInventory.ok ? new Map(snapshot.deviceInventory.data.devices.map((profile) => [profile.macAddress.toUpperCase(), profile])) : new Map<string, DeviceProfile>();
        const next = (snapshot.routeros.data.devices ?? []).map((device) => mapDevice(device, profiles.get(device.macAddress.toUpperCase())));
        setDevices(next);
        setSelectedDeviceId((current) => next.some((device) => device.id === current) ? current : next[0]?.id ?? 0);
      }
      if (snapshot.audit.ok) setLiveLogs(snapshot.audit.data.map(mapAudit));

      if (showToast) {
        const failures = Object.values(snapshot).filter((result) => !result.ok).length;
        notify(failures ? `刷新完成，${failures} 个数据源不可用；其他数据已保留` : "全部数据源刷新完成", failures ? "warning" : "success");
      }
    } catch (error) {
      if (showToast) notify(error instanceof Error ? error.message : "刷新过程异常", "warning");
    } finally {
      setScanning(false);
    }
  }, [notify]);

  useEffect(() => {
    void refreshLiveData(false);
  }, [refreshLiveData]);

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
    const timer = window.setInterval(() => {
      setClock(new Date());
      setResources((current) => ({
        routeros: expireResource(current.routeros),
        mihomo: expireResource(current.mihomo),
        mosdns: expireResource(current.mosdns),
        nodes: expireResource(current.nodes),
        l2tp: expireResource(current.l2tp),
        routes: expireResource(current.routes),
        dhcpServers: expireResource(current.dhcpServers),
        containers: expireResource(current.containers),
        deviceInventory: expireResource(current.deviceInventory),
        policies: expireResource(current.policies),
        groups: expireResource(current.groups),
        audit: expireResource(current.audit),
      }));
    }, 30_000);
    return () => window.clearInterval(timer);
  }, []);

  const navigate = (key: PageKey) => {
    if (window.location.hash === `#${key}`) {
      setPage(key);
      setMobileNavOpen(false);
      return;
    }
    window.location.hash = key;
  };

  const criticalServices = [resources.routeros, resources.mihomo, resources.mosdns];
  const healthy = criticalServices.every((resource) => resource.phase === "live" && resource.data?.online);
  const availableCount = Object.values(resources).filter((resource) => resource.phase === "live").length;
  const resourceCount = Object.keys(resources).length;

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
          {page === "overview" ? <Overview devices={devices} nodes={nodes} onRefresh={() => void refreshLiveData(true)} resources={resources} scanning={scanning} /> : null}
          {page === "routeros" ? <RouterOSPage containers={resources.containers} dhcpServers={resources.dhcpServers} navigate={navigate} onRefresh={() => void refreshLiveData(true)} resource={resources.routeros} routes={resources.routes} scanning={scanning} /> : null}
          {page === "mosdns" ? <MosDNSPage resource={resources.mosdns} /> : null}
          {page === "proxies" ? <ProxyPage groupResource={resources.groups} l2tpResource={resources.l2tp} mihomoResource={resources.mihomo} navigate={navigate} nodeResource={resources.nodes} nodes={nodes} notify={notify} onRefresh={(showToast = true) => void refreshLiveData(showToast)} selectedNodeId={selectedNodeId} setNodes={setNodes} setSelectedNodeId={setSelectedNodeId} /> : null}
          {page === "devices" ? <DevicesPage devices={devices} groupResource={resources.groups} inventoryResource={resources.deviceInventory} l2tpResource={resources.l2tp} nodeResource={resources.nodes} notify={notify} onRefresh={(showToast = true) => void refreshLiveData(showToast)} policyResource={resources.policies} resource={resources.routeros} selectedDeviceId={selectedDeviceId} setDevices={setDevices} setSelectedDeviceId={setSelectedDeviceId} /> : null}
          {page === "topology" ? <TopologyPage devices={devices} navigate={navigate} nodes={nodes} onRefresh={() => void refreshLiveData(true)} resources={resources} /> : null}
          {page === "logs" ? <LogsPage items={liveLogs} onRefresh={() => void refreshLiveData(true)} resource={resources.audit} /> : null}
          {page === "operations" ? <OperationsPage notify={notify} /> : null}
          {page === "settings" ? <SettingsPage notify={notify} /> : null}
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

function OperationsPage({ notify }: { notify: (message: string, tone?: Toast["tone"]) => void }) {
  return <div className="stack"><MihomoOperations notify={notify} /><SubscriptionOperations notify={notify} /><AlertBackupOperations notify={notify} /></div>;
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

function Overview({ devices, nodes, resources, onRefresh, scanning }: { devices: Device[]; nodes: ProxyNode[]; resources: LiveResources; onRefresh: () => void; scanning: boolean }) {
  return (
    <div className="stack">
      <section aria-label="服务状态" className="service-strip">
        <ServiceSummary {...services[0]} resource={resources.routeros} />
        <ServiceSummary {...services[1]} resource={resources.mihomo} />
        <ServiceSummary {...services[2]} resource={resources.mosdns} />
        <Button disabled={scanning} icon={RefreshCw} onClick={onRefresh} variant="primary">{scanning ? "正在刷新…" : "刷新全部状态"}</Button>
      </section>

      <section className="panel topology-panel">
        <div className="panel-heading"><div><h2>管理面状态路径</h2><p>仅表示 FoxOS 已完成的独立 API 检查，不代表互联网出口可用</p></div></div>
        <div className="topology-flow verified-flow">
          <TopologyNode accent="orange" icon={Router} metric={resources.routeros.data?.resource?.["cpu-load"] ? `CPU ${resources.routeros.data.resource["cpu-load"]}%` : "指标未采集"} subtitle={`10.0.0.1 · ${serviceLabel(resources.routeros)}`} title="RouterOS" />
          <FlowArrow label="REST" />
          <TopologyNode accent="blue" icon={Network} metric={resources.mihomo.phase === "live" && resources.mihomo.data?.online ? "控制器健康检查通过" : "控制器未验证"} subtitle={`10.0.0.2 · ${serviceLabel(resources.mihomo)}`} title="Mihomo" />
          <FlowArrow label="只读" />
          <TopologyNode accent="purple" icon={Database} metric="DNS 接管未开放" subtitle={`10.0.0.3 · ${serviceLabel(resources.mosdns)}`} title="MosDNS" />
        </div>
      </section>

      <div className="overview-grid">
        <section className="panel compact-panel">
          <div className="panel-heading"><div><h2>RouterOS 设备</h2><p>来自 DHCP 与 ARP 的合并结果</p></div><ResourceMeta resource={resources.routeros} /></div>
          {devices.length ? <div aria-label="RouterOS 设备列表" className="mini-table" role="region" tabIndex={0}>{devices.slice(0, 6).map((device) => <div className="mini-row" key={device.id}><DeviceIcon kind={device.kind} /><strong>{device.name}</strong><span>{device.ip}</span><span>{device.iface}</span><span>{resources.routeros.phase === "live" ? device.online ? "在线" : "离线" : "上次状态"}</span></div>)}</div> : <EmptyState detail="RouterOS 数据源成功返回后才会显示设备。" title="没有可验证的设备数据" />}
        </section>
        <section className="panel compact-panel">
          <div className="panel-heading"><div><h2>代理节点</h2><p>在线仅由节点探测结果判定</p></div><ResourceMeta resource={resources.nodes} /></div>
          {nodes.length ? <div aria-label="代理节点列表" className="mini-table" role="region" tabIndex={0}>{nodes.slice(0, 6).map((node) => <div className="mini-row" key={node.id}><Server aria-hidden="true" size={17} /><strong>{node.name}</strong><span>{node.protocol}</span><span>{node.server}</span><span>{proxyStatusLabel(node, node.verification === "routeros-session" ? resources.l2tp.phase : resources.nodes.phase)}</span></div>)}</div> : <EmptyState detail="SQLite 节点或 RouterOS L2TP 成功加载后才会显示。" title="没有节点数据" />}
        </section>
      </div>
    </div>
  );
}

function TopologyNode({ icon: Icon, title, subtitle, metric, accent = "" }: { icon: typeof Router; title: string; subtitle: string; metric: string; accent?: string }) {
  return <div className={`topology-node ${accent}`}><Icon aria-hidden="true" size={31} /><strong>{title}</strong><span>{subtitle}</span><small>{metric}</small></div>;
}

function FlowArrow({ label }: { label?: string }) {
  return <div aria-hidden="true" className="flow-arrow">{label ? <small>{label}</small> : null}<ArrowRight size={22} /></div>;
}

function RouterOSPage({ resource, routes, dhcpServers, containers, onRefresh, scanning, navigate }: { resource: ResourceState<RouterOverview>; routes: ResourceState<RouterRoute[]>; dhcpServers: ResourceState<RouterDHCPServer[]>; containers: ResourceState<RouterContainer[]>; onRefresh: () => void; scanning: boolean; navigate: (key: PageKey) => void }) {
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
  return (
    <div className="stack">
      <section className="panel status-band"><ResourceMeta resource={resource} /><Button disabled={scanning} icon={RefreshCw} onClick={onRefresh}>{scanning ? "刷新中…" : "刷新 RouterOS"}</Button></section>
      <div className="summary-grid">
        <SummaryCard icon={CircleGauge} label="CPU 使用率" value={system["cpu-load"] ? `${system["cpu-load"]}%` : "—"} tone="orange" />
        <SummaryCard icon={HardDrive} label="内存使用率" value={usagePercent(system["free-memory"], system["total-memory"])} />
        <SummaryCard icon={Globe2} label="活动默认路由" value={routes.phase === "live" ? activeDefaultRoutes.length : "—"} tone={activeDefaultRoutes.length ? "green" : "red"} />
        <SummaryCard icon={Server} label="运行容器" value={containers.phase === "live" ? `${runningContainers.length} / ${containerItems.length}` : "—"} tone="green" />
      </div>
      <div className="page-grid two-thirds">
        <section className="panel">
          <div className="panel-heading"><div><h2>接口状态</h2><p>接收和发送字节为 RouterOS 累计计数</p></div></div>
          {interfaces.length ? <div className="table-wrap"><table><thead><tr><th>接口</th><th>类型</th><th>MAC 地址</th><th>RX</th><th>TX</th><th>状态</th></tr></thead><tbody>{interfaces.map((item) => {
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
        <section className="panel"><div className="panel-heading"><div><h2>路由与 WAN</h2><p>默认路由仅用于状态判断，不修改用户路由</p></div><ResourceMeta resource={routes} /></div>{routeItems.length ? <div className="table-wrap"><table><thead><tr><th>目标</th><th>网关</th><th>距离</th><th>状态</th><th>所有权</th></tr></thead><tbody>{routeItems.map((item) => <tr key={item[".id"]}><td>{item["dst-address"] || "—"}</td><td>{item.gateway || "—"}</td><td>{item.distance || "—"}</td><td><StatusDot status={routes.phase === "live" && item.active === "true" && item.disabled !== "true" ? "ok" : routes.phase === "live" ? "offline" : "warning"} />{routes.phase === "live" ? item.disabled === "true" ? "禁用" : item.active === "true" ? "活动" : "非活动" : "上次状态"}</td><td>{item.comment?.startsWith("foxos:") ? "FoxOS" : "用户 / 系统"}</td></tr>)}</tbody></table></div> : <EmptyState detail={routes.error || "RouterOS 未返回路由条目。"} title="路由数据不可用" />}</section>
        <aside className="stack">
          <section className="panel"><div className="panel-heading"><div><h2>DHCP server</h2><p>{devices.length} 个 DHCP / ARP 设备已读取</p></div><ResourceMeta resource={dhcpServers} /></div>{dhcpItems.length ? <dl className="definition-list">{dhcpItems.map((item) => <div key={item[".id"]}><dt>{item.name}</dt><dd><StatusDot status={dhcpServers.phase === "live" && item.running === "true" && item.disabled !== "true" ? "ok" : dhcpServers.phase === "live" ? "offline" : "warning"} />{item.interface} · {item["address-pool"]}</dd></div>)}</dl> : <EmptyState detail={dhcpServers.error || "RouterOS 未返回 DHCP server。"} title="DHCP 数据不可用" />}</section>
          <section className="panel"><div className="panel-heading"><div><h2>容器</h2><p>状态来自 RouterOS /container 回读</p></div><ResourceMeta resource={containers} /></div>{containerItems.length ? <dl className="definition-list">{containerItems.map((item) => <div key={item[".id"]}><dt>{item.name || item.comment || item[".id"]}</dt><dd><StatusDot status={containers.phase === "live" && item.status.toLowerCase() === "running" ? "ok" : containers.phase === "live" ? "offline" : "warning"} />{item.status || "未知"} · {item.interface || "无接口"}</dd></div>)}</dl> : <EmptyState detail={containers.error || "RouterOS 未返回容器。"} title="容器数据不可用" />}</section>
        </aside>
      </div>
    </div>
  );
}

function MosDNSPage({ resource }: { resource: ResourceState<MosDNSOverview> }) {
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
      <section className="panel"><div className="panel-heading"><div><h2>服务状态</h2><p>不会用静态样例填充 DNS 指标</p></div><StatusDot status={statusTone(resource.phase, live)} /></div>{live ? <dl className="definition-list"><div><dt>状态</dt><dd className="green-text">只读检查通过</dd></div><div><dt>地址</dt><dd>{resource.data?.address || "10.0.0.3"}</dd></div><div><dt>配置写入</dt><dd>未开放</dd></div></dl> : <EmptyState detail={resource.error || "等待 MosDNS 状态 API 返回。"} title="MosDNS 在线状态未确认" />}</section>
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
  onRefresh: (showToast?: boolean) => void;
};

function DevicesPage({ devices, setDevices, selectedDeviceId, setSelectedDeviceId, notify, resource, inventoryResource, policyResource, nodeResource, groupResource, l2tpResource, onRefresh }: DevicesPageProps) {
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
  const [metadataBusy, setMetadataBusy] = useState(false);
  const [history, setHistory] = useState<DevicePresenceEvent[]>([]);
  const [historyPhase, setHistoryPhase] = useState<"loading" | "live" | "unavailable">("loading");
  const selected = devices.find((device) => device.id === selectedDeviceId) ?? devices[0];
  const policies = policyResource.data ?? [];
  const selectedPolicy = policyForDevice(policies, selected);
  const filtered = devices.filter((device) => `${device.name}${device.ip}${device.mac}${device.vendor ?? ""}${device.tags.join(" ")}`.toLowerCase().includes(search.toLowerCase()) && (status === "all" || (status === "online" ? device.online : !device.online)));
  const keyboardActiveId = filtered.some((device) => device.id === selected?.id) ? selected?.id : filtered[0]?.id;
  const live = resource.phase === "live" && Boolean(resource.data?.online);
  const targetRequired = egress === "mihomo-node" || egress === "proxy-chain" || egress === "l2tp";
  const targetSourceLive = egress === "mihomo-node" ? nodeResource.phase === "live" : egress === "proxy-chain" ? groupResource.phase === "live" : egress === "l2tp" ? l2tpResource.phase === "live" : true;
  const managementProtected = Boolean(selected && ["10.0.0.1", "10.0.0.2", "10.0.0.3", "10.0.0.4"].includes(selected.ip));
  const canPrepareEgress = Boolean(selected && live && policyResource.phase === "live" && selected.fixed && (selectedPolicy?.dhcpServer || selected.dhcpServer) && !managementProtected && targetSourceLive && (!targetRequired || targetId));

  useEffect(() => {
    setEgress(selectedPolicy?.egress ?? "direct");
    setTargetId(selectedPolicy?.targetId ?? "");
  }, [selected?.id, selectedPolicy?.id, selectedPolicy?.egress, selectedPolicy?.targetId]);

  useEffect(() => {
    setAlias(selected?.alias ?? "");
    setVendor(selected?.vendor ?? "");
    setTagsText(selected?.tags.join(", ") ?? "");
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

  const prepareBinding = async () => {
    if (!selected) return;
    try {
      if (!selected.dhcpServer) throw new Error("RouterOS 未返回该租约所属 DHCP server，不能生成安全写入计划");
      const result = await planDeviceBinding({ id: selectedPolicy?.id ?? `device-${selected.id.toString(16)}`, name: selected.name, macAddress: selected.mac, staticIp: selected.ip, dhcpServer: selected.dhcpServer, egress: selectedPolicy?.egress ?? "direct", targetId: selectedPolicy?.targetId });
      if (!result.plan.requiresConfirmation) {
        setDevices((items) => items.map((item) => item.id === selected.id ? { ...item, fixed: true } : item));
        notify(`${selected.name} 已经是 FoxOS 管理的静态租约`);
        return;
      }
      setPending({ plan: result.plan, token: result.confirmationToken, device: selected });
    } catch (error) {
      notify(error instanceof Error ? error.message : "生成 RouterOS 计划失败", "warning");
    }
  };

  const executeBinding = async () => {
    if (!pending) return;
    setExecuting(true);
    try {
      await executeDeviceBinding(pending.plan, pending.token);
      setDevices((items) => items.map((item) => item.id === pending.device.id ? { ...item, fixed: true } : item));
      notify(`${pending.device.name} 已执行并完成回读校验`);
      setPending(null);
      onRefresh(false);
    } catch (error) {
      notify(error instanceof Error ? error.message : "RouterOS 写入失败", "warning");
    } finally {
      setExecuting(false);
    }
  };

  const prepareEgress = async () => {
    if (!selected) return;
    try {
      const policy = proposedDevicePolicy(selected, selectedPolicy, egress, targetId);
      if (!policy.dhcpServer) throw new Error("缺少 RouterOS DHCP server，不能保存设备策略");
      const result = await planDeviceEgress(policy.id, policy);
      if (!result.plan.requiresConfirmation) {
        notify(`${selected.name} 的数据库策略与 RouterOS 自有资源已经一致`);
        return;
      }
      if (!result.confirmationToken) throw new Error("服务端未返回出口策略确认令牌");
      setPendingEgress({ plan: result.plan, token: result.confirmationToken, device: selected });
    } catch (error) {
      notify(error instanceof Error ? error.message : "生成设备出口计划失败", "warning");
    }
  };

  const executeEgress = async () => {
    if (!pendingEgress) return;
    setExecutingEgress(true);
    try {
      const result = await executeDeviceEgress(pendingEgress.plan.policyId, pendingEgress.plan, pendingEgress.token);
      const job = result.job ? await waitForJob(result.job.id) : undefined;
      if (job?.status === "ROLLED_BACK") {
        notify(job.errorMessage || `${pendingEgress.device.name} 出口策略应用失败，已回滚 RouterOS 变更`, "warning");
        setPendingEgress(null);
        onRefresh(false);
        return;
      }
      if (job?.status === "FAILED") throw new Error(job.errorMessage || "设备出口策略任务失败");
      if (job && job.status !== "SUCCEEDED") throw new Error(`设备出口策略任务结束于 ${job.status}`);
      if (!job && result.status !== "SUCCEEDED") throw new Error(`设备出口策略执行结束于 ${result.status}`);
      notify(`${pendingEgress.device.name} 的出口策略已执行、回读并持久化`);
      setPendingEgress(null);
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

  const changeEgress = (value: EgressType) => {
    setEgress(value);
    const currentTarget = value === selectedPolicy?.egress ? selectedPolicy?.targetId : "";
    setTargetId(currentTarget ?? "");
  };

  return (
    <div className="stack">
      <section className="panel multi-resource-band"><ResourceMeta resource={resource} /><ResourceMeta resource={inventoryResource} /><ResourceMeta resource={policyResource} /><Button icon={RefreshCw} onClick={onRefresh}>刷新设备</Button></section>
      <div className="summary-grid"><SummaryCard icon={Users} label="在线设备" value={live ? devices.filter((device) => device.online).length : "—"} tone="green" /><SummaryCard icon={LockKeyhole} label="静态租约" value={resource.data ? devices.filter((device) => device.fixed).length : "—"} /><SummaryCard icon={Link2} label="动态租约" value={resource.data ? devices.filter((device) => !device.fixed).length : "—"} tone="orange" /><SummaryCard icon={AlertTriangle} label="状态未确认" value={live ? devices.filter((device) => !device.online).length : devices.length || "—"} tone="red" /></div>
      <div className="split-view">
        <section className="panel table-panel">
          <div className="toolbar"><label className="search-box"><Search aria-hidden="true" size={17} /><span className="sr-only">搜索设备</span><input onChange={(event) => setSearch(event.target.value)} placeholder="搜索设备名称 / IP / MAC" value={search} /></label><select aria-label="筛选设备状态" onChange={(event) => setStatus(event.target.value)} value={status}><option value="all">全部状态</option><option value="online">在线</option><option value="offline">离线</option></select></div>
          {filtered.length ? <div className="table-wrap"><table aria-label="RouterOS 设备" className="interactive-table" role="grid"><thead><tr><th role="columnheader">设备名称</th><th role="columnheader">IP 地址</th><th role="columnheader">MAC 地址</th><th role="columnheader">RouterOS 接口</th><th role="columnheader">出口策略</th><th role="columnheader">状态</th></tr></thead><tbody>{filtered.map((device) => { const policy = policyForDevice(policies, device); return <tr aria-selected={selected?.id === device.id} className={selected?.id === device.id ? "selected" : ""} data-grid-row key={device.id} onClick={() => setSelectedDeviceId(device.id)} onKeyDown={(event) => handleGridRowKeyDown(event, () => setSelectedDeviceId(device.id))} tabIndex={keyboardActiveId === device.id ? 0 : -1}><td><span className="name-cell"><DeviceIcon kind={device.kind} /><strong>{device.name}</strong></span></td><td>{device.ip}</td><td>{device.mac}</td><td>{device.iface}</td><td>{egressLabel(policy?.egress)}</td><td><StatusDot status={live ? device.online ? "ok" : "offline" : "warning"} />{live ? device.online ? "在线" : "离线" : "上次状态"}</td></tr>; })}</tbody></table></div> : <EmptyState detail="设备接口失败不会回退到演示清单。" title="没有设备数据" />}
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
                  <label className="field"><span>别名</span><input disabled={!selected.profileTracked || metadataBusy} maxLength={128} onChange={(event) => setAlias(event.target.value)} value={alias} /></label>
                  <label className="field"><span>厂商</span><input disabled={!selected.profileTracked || metadataBusy} maxLength={128} onChange={(event) => setVendor(event.target.value)} value={vendor} /></label>
                  <label className="field full"><span>标签</span><input disabled={!selected.profileTracked || metadataBusy} onChange={(event) => setTagsText(event.target.value)} placeholder="work, trusted" value={tagsText} /></label>
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
                <label className="field"><span>选择出口</span><select disabled={!live || policyResource.phase !== "live"} onChange={(event) => changeEgress(event.target.value as EgressType)} value={egress}><option value="direct">直连</option><option value="mihomo-node">Mihomo 节点</option><option value="proxy-chain">链式代理</option><option value="l2tp">RouterOS L2TP</option><option value="blocked">阻断</option></select></label>
                {targetRequired ? <label className="field"><span>目标</span><select disabled={!targetSourceLive} onChange={(event) => setTargetId(event.target.value)} value={targetId}><option value="">请选择目标</option>{targetOptions.map((option) => <option key={option.id} value={option.id}>{option.label}</option>)}</select></label> : null}
                {!selected.fixed ? <div className="warning-note"><AlertTriangle aria-hidden="true" size={16} />先将租约固定，避免 DHCP 地址变化后策略命中错误设备。</div> : managementProtected ? <div className="warning-note"><ShieldCheck aria-hidden="true" size={16} />10.0.0.1 至 10.0.0.4 为永久旁路管理面，禁止创建出口策略。</div> : policyResource.phase !== "live" || !targetSourceLive ? <div className="warning-note"><AlertTriangle aria-hidden="true" size={16} />策略或目标数据源不是实时状态，写入已禁用。</div> : null}
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
        <div className="chain-builder-row"><ChainNode detail="设备出口" icon={Router} locked name="RouterOS" />{chainNodeIds.map((id, index) => { const node = foxosNodes.find((item) => item.apiId === id); return <ChainNode detail={node ? `${node.protocol} · 第 ${index + 1} 跳` : `缺失引用 · 第 ${index + 1} 跳`} icon={node ? Server : AlertTriangle} key={`${id}-${index}`} name={node?.name ?? id} onMoveLeft={index > 0 ? () => moveChainNode(index, -1) : undefined} onMoveRight={index < chainNodeIds.length - 1 ? () => moveChainNode(index, 1) : undefined} onRemove={() => setChainNodeIds((items) => items.filter((_, itemIndex) => itemIndex !== index))} />; })}<div className="add-stage"><select aria-label="添加链路节点" disabled={groupResource.phase !== "live" || chainBusy} defaultValue="" onChange={(event) => { const id = event.target.value; if (id && !chainNodeIds.includes(id)) setChainNodeIds((items) => [...items, id]); event.currentTarget.value = ""; }}><option value="">添加链路节点</option>{foxosNodes.filter((node) => !chainNodeIds.includes(node.apiId)).map((node) => <option key={node.apiId} value={node.apiId}>{node.name}</option>)}</select></div><ChainNode detail="目标网络" icon={Globe2} locked name="Internet" /></div>
        <div className="chain-publish-note"><span><FileText aria-hidden="true" size={16} />保存组不会热重载 Mihomo。发布前必须检查生成配置与脱敏 Diff。</span><Button icon={ArrowRight} onClick={() => navigate("operations")} variant="secondary">预览并发布</Button></div>
      </section>
      <div className="split-view">
        <section className="panel table-panel"><div className="toolbar"><label className="search-box"><Search aria-hidden="true" size={17} /><span className="sr-only">搜索节点</span><input onChange={(event) => setSearch(event.target.value)} placeholder="搜索节点 / 服务器" value={search} /></label><select aria-label="筛选节点协议" onChange={(event) => setProtocol(event.target.value)} value={protocol}><option value="all">全部协议</option>{[...new Set(nodes.map((node) => node.protocol))].map((item) => <option key={item}>{item}</option>)}</select></div>{filtered.length ? <div className="table-wrap"><table aria-label="代理节点" className="interactive-table" role="grid"><thead><tr><th role="columnheader">节点名称</th><th role="columnheader">协议</th><th role="columnheader">服务器</th><th role="columnheader">来源</th><th role="columnheader">Mihomo 延迟</th><th role="columnheader">TCP 延迟</th><th role="columnheader">验证状态</th></tr></thead><tbody>{filtered.map((node) => { const sourcePhase = node.verification === "routeros-session" ? l2tpResource.phase : nodeResource.phase; const runtimeDelay = latestMihomoDelay(mihomoResource, node.name); return <tr aria-selected={selected?.id === node.id} className={selected?.id === node.id ? "selected" : ""} data-grid-row key={node.id} onClick={() => setSelectedNodeId(node.id)} onKeyDown={(event) => handleGridRowKeyDown(event, () => setSelectedNodeId(node.id))} tabIndex={keyboardActiveId === node.id ? 0 : -1}><td><strong>{node.name}</strong></td><td>{node.protocol}</td><td>{node.server}</td><td>{node.source}</td><td>{runtimeDelay === undefined ? "未返回" : `${runtimeDelay} ms`}</td><td>{node.verification !== "tcp" || node.latency === null ? "未检测" : `${node.latency} ms`}</td><td><StatusDot status={proxyStatusTone(node, sourcePhase)} />{proxyStatusLabel(node, sourcePhase)}</td></tr>; })}</tbody></table></div> : <EmptyState detail="节点读取失败时不会显示演示节点。" title="没有节点数据" />}</section>
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
        notify(`已原子导入 ${result.imported} 个节点；尚未发布至 Mihomo`);
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
        {mode === "add" ? <div className="form-grid"><label className="field"><span>节点名称</span><input autoFocus data-autofocus name="name" required /></label><label className="field"><span>协议</span><select name="protocol"><option>VLESS</option><option>Trojan</option><option>Shadowsocks</option><option>Hysteria2</option><option>SOCKS5</option></select></label><label className="field full"><span>服务器地址</span><input name="server" placeholder="server.example.com:443" required /></label><label className="field"><span>UUID / 用户名</span><input name="identity" /></label><label className="field"><span>密码</span><input autoComplete="new-password" name="password" type="password" /></label></div> : <div className="form-grid"><label className="field full"><span>分享链接（每行一个）</span><textarea autoFocus data-autofocus name="links" required rows={8} /></label><div className="warning-note full"><ShieldCheck aria-hidden="true" size={16} />链接由 FoxOS 本地解析并整批校验；此入口不会抓取订阅 URL。</div></div>}
        <div className="modal-actions"><Button disabled={submitting} onClick={onClose}>取消</Button><Button disabled={submitting} icon={mode === "add" ? Plus : Upload} type="submit" variant="primary">{submitting ? "正在保存…" : mode === "add" ? "保存节点" : "校验并导入"}</Button></div>
      </form>
    </Dialog>
  );
}

function DetailSection({ title, children }: { title: string; children: React.ReactNode }) {
  return <section className="detail-section"><h3>{title}</h3>{children}</section>;
}

function TopologyPage({ resources, devices, nodes, navigate, onRefresh }: { resources: LiveResources; devices: Device[]; nodes: ProxyNode[]; navigate: (page: PageKey) => void; onRefresh: () => void }) {
  const failures = [resources.routeros, resources.mihomo, resources.mosdns].filter((resource) => resource.phase !== "live" || !resource.data?.online).length;
  return <div className="stack"><section className="panel topology-large"><div className="panel-heading"><div><h2>已验证管理拓扑</h2><p>接口失败会保留在对应组件上，不推断 WAN 或代理出口可用</p></div><Button icon={RefreshCw} onClick={onRefresh}>刷新拓扑</Button></div><div className="topology-stage"><button onClick={() => navigate("routeros")} type="button"><TopologyNode accent="orange" icon={Router} metric={resources.routeros.source} subtitle={`10.0.0.1 · ${serviceLabel(resources.routeros)}`} title="RouterOS" /></button><div className="topology-columns compact-topology"><div className="topology-group"><h3>代理控制面</h3><button onClick={() => navigate("proxies")} type="button"><TopologyNode accent="blue" icon={Network} metric={resources.mihomo.source} subtitle={`10.0.0.2 · ${serviceLabel(resources.mihomo)}`} title="Mihomo" /></button></div><div className="topology-group"><h3>DNS（只读）</h3><button onClick={() => navigate("mosdns")} type="button"><TopologyNode accent="purple" icon={Database} metric={resources.mosdns.source} subtitle={`10.0.0.3 · ${serviceLabel(resources.mosdns)}`} title="MosDNS" /></button></div><div className="topology-group"><h3>管理服务</h3><TopologyNode icon={Server} metric="当前页面" subtitle="10.0.0.4:8090" title="FoxOS" /></div></div></div></section><div className="summary-grid"><SummaryCard icon={Cable} label="RouterOS 接口" value={resources.routeros.data?.interfaces?.length ?? "—"} tone="green" /><SummaryCard icon={Users} label="已读取设备" value={resources.routeros.data ? devices.length : "—"} /><SummaryCard icon={Activity} label="代理 / L2TP 节点" value={nodes.length} tone="purple" /><SummaryCard icon={AlertTriangle} label="未确认服务" value={failures} tone="orange" /></div></div>;
}

function LogsPage({ items, resource, onRefresh }: { items: LogItem[]; resource: ResourceState<AuditEvent[]>; onRefresh: () => void }) {
  const [level, setLevel] = useState("all");
  const [search, setSearch] = useState("");
  const filtered = items.filter((item) => (level === "all" || item.level === level) && `${item.source}${item.event}${item.detail}`.toLowerCase().includes(search.toLowerCase()));
  return <div className="stack"><section className="panel status-band"><ResourceMeta resource={resource} /><Button icon={RefreshCw} onClick={onRefresh}>刷新审计</Button></section><section className="panel table-panel"><div className="toolbar"><label className="search-box"><Search aria-hidden="true" size={17} /><span className="sr-only">搜索审计事件</span><input onChange={(event) => setSearch(event.target.value)} placeholder="搜索事件、来源或详情" value={search} /></label><select aria-label="筛选日志级别" onChange={(event) => setLevel(event.target.value)} value={level}><option value="all">全部级别</option><option>成功</option><option>信息</option><option>错误</option></select></div>{filtered.length ? <div className="table-wrap"><table><thead><tr><th>时间</th><th>级别</th><th>来源</th><th>事件</th><th>目标</th></tr></thead><tbody>{filtered.map((item) => <tr key={`${item.time}-${item.event}-${item.detail}`}><td>{item.time}</td><td><span className={`log-level ${item.level}`}>{item.level}</span></td><td>{item.source}</td><td><strong>{item.event}</strong></td><td>{item.detail}</td></tr>)}</tbody></table></div> : <EmptyState detail="审计接口失败时不会显示样例日志。" title="没有审计记录" />}</section></div>;
}

function SettingsPage({ notify }: { notify: (message: string, tone?: Toast["tone"]) => void }) {
  const [saved, setSaved] = useState(false);
  const save = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    try {
      saveApiToken(String(data.get("apiToken") || ""));
      setSaved(true);
			notify("API Token 仅保存在页面内存；刷新总览后生效");
      window.setTimeout(() => setSaved(false), 1800);
      event.currentTarget.reset();
    } catch (error) {
      notify(error instanceof Error ? error.message : "Token 保存失败", "warning");
    }
  };
  return (
    <div className="page-grid two-thirds">
      <form className="panel" onSubmit={save}>
				<div className="panel-heading"><div><h2>FoxOS API 凭据</h2><p>仅保存在页面内存，刷新或关闭标签页后清除</p></div><ShieldCheck aria-hidden="true" className="green-text" size={22} /></div>
        <div className="settings-section"><div className="form-grid"><label className="field full"><span>API Token</span><input autoComplete="off" minLength={32} name="apiToken" required type="password" /></label></div></div>
        <div className="form-actions"><Button icon={saved ? Check : Save} type="submit" variant="primary">{saved ? "已保存" : "保存当前会话凭据"}</Button></div>
      </form>
      <aside className="stack">
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
