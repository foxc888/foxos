import { FormEvent, useEffect, useMemo, useState } from "react";
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
  FileText,
  Globe2,
  HardDrive,
  Home,
  Laptop,
  Link2,
  ListFilter,
  LockKeyhole,
  Menu,
  Monitor,
  Network,
  Pencil,
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
import { deleteNode as deleteNodeApi, loadLiveSnapshot, saveApiToken } from "./api";
import { Device, initialDevices, initialNodes, logs, ProxyNode, services } from "./data";

type PageKey =
  | "overview"
  | "routeros"
  | "mosdns"
  | "proxies"
  | "devices"
  | "topology"
  | "logs"
  | "settings";

type Toast = { message: string; tone: "success" | "warning" };
type ConnectionMode = "loading" | "live" | "demo";

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

const navItems: { key: PageKey; label: string; icon: typeof Home }[] = [
  { key: "overview", label: "总览", icon: Home },
  { key: "routeros", label: "RouterOS", icon: Router },
  { key: "mosdns", label: "MosDNS", icon: Database },
  { key: "proxies", label: "代理节点", icon: Globe2 },
  { key: "devices", label: "设备管理", icon: Monitor },
  { key: "topology", label: "网络拓扑", icon: Network },
  { key: "logs", label: "日志", icon: FileText },
  { key: "settings", label: "设置", icon: Settings },
];

const pageTitle: Record<PageKey, { title: string; subtitle: string }> = {
  overview: { title: "总览", subtitle: "实时掌握 RouterOS、代理出口与局域网设备" },
  routeros: { title: "RouterOS", subtitle: "接口、路由、DHCP 与系统资源" },
  mosdns: { title: "MosDNS", subtitle: "只读运行状态与查询指标" },
  proxies: { title: "代理节点", subtitle: "节点、订阅、链式代理与 RouterOS 原生 L2TP" },
  devices: { title: "设备管理", subtitle: "静态 IP 绑定与设备出口策略" },
  topology: { title: "网络拓扑", subtitle: "以 RouterOS 为中心查看实时网络路径" },
  logs: { title: "日志", subtitle: "统一查看系统、网络与操作审计记录" },
  settings: { title: "设置", subtitle: "服务连接、安全操作与备份恢复" },
};

function StatusDot({ status = "ok" }: { status?: "ok" | "warning" | "offline" }) {
  return <span className={`status-dot ${status}`} aria-label={status} />;
}

function Button({
  children,
  icon: Icon,
  variant = "secondary",
  onClick,
  type = "button",
  disabled = false,
}: {
  children: React.ReactNode;
  icon?: typeof Plus;
  variant?: "primary" | "secondary" | "danger" | "ghost";
  onClick?: () => void;
  type?: "button" | "submit";
  disabled?: boolean;
}) {
  return (
    <button className={`button ${variant}`} onClick={onClick} type={type} disabled={disabled}>
      {Icon ? <Icon size={16} /> : null}
      {children}
    </button>
  );
}

function ServiceIcon({ name, tone }: { name: string; tone: string }) {
  const Icon = name === "RouterOS" ? Router : name === "Mihomo" ? Network : Database;
  return (
    <span className={`service-icon ${tone}`}>
      <Icon size={28} />
    </span>
  );
}

function DeviceIcon({ kind }: { kind: Device["kind"] }) {
  const Icon = kind === "phone" ? Smartphone : kind === "server" ? Server : kind === "tv" ? Tv : kind === "iot" ? Wifi : Laptop;
  return <Icon size={17} />;
}

function App() {
  const [page, setPage] = useState<PageKey>("overview");
  const [collapsed, setCollapsed] = useState(false);
  const [devices, setDevices] = useState(initialDevices);
  const [nodes, setNodes] = useState(initialNodes);
  const [selectedDeviceId, setSelectedDeviceId] = useState(1);
  const [selectedNodeId, setSelectedNodeId] = useState(1);
  const [toast, setToast] = useState<Toast | null>(null);
  const [scanning, setScanning] = useState(false);
  const [connectionMode, setConnectionMode] = useState<ConnectionMode>("loading");
  const [liveLogs, setLiveLogs] = useState(logs);

  const notify = (message: string, tone: Toast["tone"] = "success") => {
    setToast({ message, tone });
    window.setTimeout(() => setToast(null), 2800);
  };

  const refreshLiveData = async (showToast = false) => {
    setScanning(true);
    try {
      const snapshot = await loadLiveSnapshot();
      const apiNodes: ProxyNode[] = snapshot.nodes.map((node) => ({
        id: numericId(node.id),
        apiId: node.id,
        name: node.name,
        protocol: node.type.toUpperCase(),
        server: `${node.server}:${node.port}`,
        region: "待检测",
        source: "FoxOS 数据库",
        latency: null,
        loss: 0,
        status: snapshot.mihomo.online ? "online" : "warning",
        inUse: "—",
      }));
      const l2tpNodes: ProxyNode[] = snapshot.l2tp.map((client) => ({
        id: numericId(`l2tp:${client.id}`),
        name: client.name,
        protocol: "L2TP",
        server: client.connectTo,
        region: "待检测",
        source: "RouterOS 原生",
        latency: null,
        loss: 0,
        status: client.running && !client.disabled ? "online" : client.disabled ? "offline" : "warning",
        inUse: "—",
      }));
      setNodes([...apiNodes, ...l2tpNodes]);
      if (apiNodes.length + l2tpNodes.length > 0) setSelectedNodeId((apiNodes[0] ?? l2tpNodes[0]).id);

      const routerDevices: Device[] = (snapshot.routeros.devices ?? []).map((device) => ({
        id: numericId(device.macAddress),
        apiId: device.macAddress,
        name: device.hostName || device.macAddress,
        kind: deviceKind(device.hostName || ""),
        ip: device.address,
        mac: device.macAddress,
        iface: device.interface || device.dhcpServer || "—",
        egress: "未设置",
        latency: null,
        online: device.status === "bound",
        fixed: !device.dynamic,
        lastSeen: device.lastSeen || "刚刚",
      }));
      if (routerDevices.length > 0) {
        setDevices(routerDevices);
        setSelectedDeviceId(routerDevices[0].id);
      }
      setLiveLogs(snapshot.audit.map((event) => ({
        time: new Date(event.createdAt).toLocaleTimeString("zh-CN", { hour12: false }),
        level: event.outcome === "SUCCEEDED" ? "成功" : event.outcome === "FAILED" ? "错误" : "信息",
        source: event.action.startsWith("routeros") ? "RouterOS" : "FoxOS",
        event: event.action,
        detail: event.targetId,
      })));
      setConnectionMode("live");
      if (showToast) notify("已从 FoxOS API 刷新 RouterOS、Mihomo、节点、L2TP、设备与审计状态");
    } catch (error) {
      setConnectionMode("demo");
      if (showToast) notify(error instanceof Error ? error.message : "FoxOS API 连接失败", "warning");
    } finally {
      setScanning(false);
    }
  };

  useEffect(() => {
    void refreshLiveData(false);
  }, []);

  const runScan = () => {
    void refreshLiveData(true);
  };

  const navigate = (key: PageKey) => {
    setPage(key);
    window.history.replaceState(null, "", `#${key}`);
    window.scrollTo(0, 0);
  };

  return (
    <div className={`app-shell ${collapsed ? "sidebar-collapsed" : ""}`}>
      <aside className="sidebar">
        <div className="brand">
          <img src="/foxos-logo.svg" alt="FoxOS" />
          {!collapsed ? (
            <span>
              <strong>FoxOS</strong>
              <small>Network Command Center</small>
            </span>
          ) : null}
        </div>
        <nav aria-label="主导航">
          {navItems.map(({ key, label, icon: Icon }) => (
            <button
              className={page === key ? "active" : ""}
              key={key}
              onClick={() => navigate(key)}
              title={collapsed ? label : undefined}
            >
              <Icon size={19} />
              {!collapsed ? <span>{label}</span> : null}
            </button>
          ))}
        </nav>
        <div className="sidebar-footer">
          {!collapsed ? (
            <div className="system-health">
              <ShieldCheck size={20} />
              <span>
                <small>系统健康</small>
                <strong>全部正常</strong>
              </span>
            </div>
          ) : null}
          <button className="collapse-button" onClick={() => setCollapsed((value) => !value)}>
            {collapsed ? <ChevronRight size={18} /> : <ChevronLeft size={18} />}
            {!collapsed ? "收起侧栏" : null}
          </button>
        </div>
      </aside>

      <main className="main-shell">
        <header className="topbar">
          <div>
            <h1>{pageTitle[page].title}</h1>
            <p>{pageTitle[page].subtitle}</p>
          </div>
          <div className="topbar-meta">
            {services.map((service) => (
              <span className="top-service" key={service.name}>
                <StatusDot />
                {service.name}
              </span>
            ))}
            <span className={`data-mode ${connectionMode}`}><StatusDot status={connectionMode === "live" ? "ok" : connectionMode === "loading" ? "warning" : "offline"} />{connectionMode === "live" ? "实时数据" : connectionMode === "loading" ? "连接中" : "演示数据"}</span>
            <span className="top-time">{new Date().toLocaleString("zh-CN", { hour12: false })}</span>
            <span className="admin">
              <Users size={16} /> admin
            </span>
          </div>
        </header>

        <div className="page-content">
          {page === "overview" ? (
            <Overview devices={devices} runScan={runScan} scanning={scanning} navigate={navigate} />
          ) : null}
          {page === "routeros" ? <RouterOSPage notify={notify} /> : null}
          {page === "mosdns" ? <MosDNSPage /> : null}
          {page === "proxies" ? (
            <ProxyPage
              nodes={nodes}
              setNodes={setNodes}
              selectedNodeId={selectedNodeId}
              setSelectedNodeId={setSelectedNodeId}
              notify={notify}
            />
          ) : null}
          {page === "devices" ? (
            <DevicesPage
              devices={devices}
              setDevices={setDevices}
              selectedDeviceId={selectedDeviceId}
              setSelectedDeviceId={setSelectedDeviceId}
              notify={notify}
            />
          ) : null}
          {page === "topology" ? <TopologyPage navigate={navigate} /> : null}
          {page === "logs" ? <LogsPage items={liveLogs} /> : null}
          {page === "settings" ? <SettingsPage notify={notify} /> : null}
        </div>
      </main>

      {toast ? (
        <div className={`toast ${toast.tone}`}>
          {toast.tone === "success" ? <CheckCircle2 size={18} /> : <AlertTriangle size={18} />}
          {toast.message}
        </div>
      ) : null}
    </div>
  );
}

function Overview({
  devices,
  runScan,
  scanning,
  navigate,
}: {
  devices: Device[];
  runScan: () => void;
  scanning: boolean;
  navigate: (page: PageKey) => void;
}) {
  return (
    <div className="stack">
      <section className="service-strip">
        {services.map((service) => (
          <div className="service-summary" key={service.name}>
            <ServiceIcon name={service.name} tone={service.tone} />
            <div>
              <strong>{service.name}</strong>
              <span><StatusDot />运行正常</span>
            </div>
            <div className="service-detail">
              <span>{service.address}</span>
              <small>{service.detail}</small>
            </div>
          </div>
        ))}
        <Button variant="primary" icon={scanning ? RefreshCw : Activity} onClick={runScan} disabled={scanning}>
          {scanning ? "正在检测…" : "运行全链路检测"}
        </Button>
      </section>

      <section className="panel topology-panel">
        <div className="panel-heading">
          <div>
            <h2>实时网络路径</h2>
            <p>RouterOS 作为统一入口，当前策略路由已启用</p>
          </div>
          <span className="healthy-label"><StatusDot />最近 24 小时无严重故障</span>
        </div>
        <div className="topology-flow">
          <TopologyNode icon={Globe2} title="Internet / WAN" subtitle="可达 · 12 ms" metric="↓ 821 / ↑ 189 Mbps" />
          <FlowArrow label="WAN" />
          <TopologyNode icon={Router} title="RouterOS" subtitle="10.0.0.1 · 正常" metric="负载 18%" accent="orange" />
          <div className="topology-branch">
            <FlowArrow label="策略路由" />
            <TopologyNode icon={Network} title="Mihomo" subtitle="10.0.0.2 · 正常" metric="68 活跃连接" accent="blue" />
          </div>
          <FlowArrow label="代理出口" />
          <TopologyNode icon={Server} title="US-LAX-01" subtitle="美国洛杉矶" metric="162 ms" accent="blue" />
          <FlowArrow />
          <TopologyNode icon={Globe2} title="Internet" subtitle="代理出口可达" metric="0.2% 丢包" />
        </div>
        <div className="topology-subrow">
          <div className="sub-route">
            <Router size={18} />
            <ArrowRight size={16} />
            <Users size={18} />
            <span>局域网 / LAN</span>
            <strong>14 台在线</strong>
          </div>
          <div className="sub-route purple">
            <Network size={18} />
            <ArrowRight size={16} />
            <Database size={18} />
            <span>DNS 请求（只读）</span>
            <strong>123 QPS</strong>
          </div>
        </div>
      </section>

      <div className="overview-grid">
        <section className="panel compact-panel">
          <div className="panel-heading">
            <div>
              <h2>设备与策略摘要</h2>
              <p>在线设备与当前出口</p>
            </div>
            <button className="text-link" onClick={() => navigate("devices")}>查看全部设备 <ChevronRight size={15} /></button>
          </div>
          <div className="device-feature">
            <Smartphone size={24} />
            <div><strong>iPhone 15 Pro</strong><span>bridge-lan · 在线</span></div>
            <div><small>绑定 IP</small><strong>192.168.88.21</strong></div>
            <div><small>当前出口</small><strong className="blue-text">US-LAX-01</strong></div>
          </div>
          <div className="mini-table">
            {devices.slice(0, 4).map((device) => (
              <div className="mini-row" key={device.id}>
                <DeviceIcon kind={device.kind} />
                <strong>{device.name}</strong>
                <span>{device.ip}</span>
                <span>{device.egress}</span>
                <span className="latency">{device.latency ?? "—"}{device.latency ? " ms" : ""}</span>
              </div>
            ))}
          </div>
        </section>

        <section className="panel compact-panel">
          <div className="panel-heading">
            <div>
              <h2>活动链路摘要</h2>
              <p>链路 1 · 可回滚</p>
            </div>
            <button className="text-link" onClick={() => navigate("proxies")}>管理代理节点 <ChevronRight size={15} /></button>
          </div>
          <div className="chain-summary">
            {[
              ["RouterOS", "3 ms"],
              ["HK-01", "26 ms"],
              ["US-LAX-01", "162 ms"],
              ["Internet", "可达"],
            ].map(([name, detail], index) => (
              <div className="chain-piece" key={name}>
                <div><Server size={20} /><strong>{name}</strong><span>{detail}</span></div>
                {index < 3 ? <ArrowRight size={17} /> : null}
              </div>
            ))}
          </div>
          <div className="metric-row">
            <Metric label="链路总延迟" value="191 ms" />
            <Metric label="丢包率" value="0.2%" />
            <Metric label="可用率（24h）" value="99.8%" />
            <Metric label="抖动" value="6 ms" />
          </div>
        </section>
      </div>
    </div>
  );
}

function TopologyNode({
  icon: Icon,
  title,
  subtitle,
  metric,
  accent = "",
}: {
  icon: typeof Router;
  title: string;
  subtitle: string;
  metric: string;
  accent?: string;
}) {
  return (
    <div className={`topology-node ${accent}`}>
      <Icon size={31} />
      <strong>{title}</strong>
      <span>{subtitle}</span>
      <small>{metric}</small>
    </div>
  );
}

function FlowArrow({ label }: { label?: string }) {
  return (
    <div className="flow-arrow">
      {label ? <small>{label}</small> : null}
      <ArrowRight size={22} />
    </div>
  );
}

function Metric({ label, value }: { label: string; value: string }) {
  return <div className="metric"><span>{label}</span><strong>{value}</strong></div>;
}

function SummaryCard({ icon: Icon, label, value, tone = "blue" }: { icon: typeof Users; label: string; value: string | number; tone?: string }) {
  return (
    <div className="summary-card">
      <span className={`summary-icon ${tone}`}><Icon size={20} /></span>
      <div><small>{label}</small><strong>{value}</strong></div>
    </div>
  );
}

function RouterOSPage({ notify }: { notify: (message: string, tone?: Toast["tone"]) => void }) {
  const interfaces = [
    ["ether1", "WAN（PPPoE）", "203.0.113.45", "812 Mbps", "198 Mbps"],
    ["bridge", "LAN", "10.0.0.1/24", "412 Mbps", "96 Mbps"],
    ["veth-mihomo", "容器链路", "10.0.0.2/32", "412 Mbps", "96 Mbps"],
    ["veth-mosdns", "容器链路", "10.0.0.3/32", "123 QPS", "12 QPS"],
  ];
  return (
    <div className="stack">
      <div className="summary-grid">
        <SummaryCard icon={CircleGauge} label="CPU 使用率" value="23%" tone="orange" />
        <SummaryCard icon={HardDrive} label="内存使用率" value="46%" />
        <SummaryCard icon={Cable} label="活动接口" value="4" tone="green" />
        <SummaryCard icon={Users} label="DHCP 租约" value="14 / 254" tone="purple" />
      </div>
      <div className="page-grid two-thirds">
        <section className="panel">
          <div className="panel-heading">
            <div><h2>接口与流量</h2><p>来自 RouterOS 的只读实时状态</p></div>
            <Button icon={RefreshCw} onClick={() => notify("RouterOS 状态已同步")}>同步状态</Button>
          </div>
          <div className="traffic-legend"><span className="rx">接收 RX</span><span className="tx">发送 TX</span><span className="traffic-value">当前 812 / 198 Mbps</span></div>
          <div className="table-wrap">
            <table>
              <thead><tr><th>接口</th><th>角色</th><th>IP 地址</th><th>RX</th><th>TX</th><th>状态</th></tr></thead>
              <tbody>
                {interfaces.map((row) => <tr key={row[0]}>{row.map((cell) => <td key={cell}>{cell}</td>)}<td><StatusDot />在线</td></tr>)}
              </tbody>
            </table>
          </div>
          <div className="route-list">
            <div><span>默认路由</span><strong>0.0.0.0/0 → ether1</strong><em><StatusDot />活动</em></div>
            <div><span>策略路由</span><strong>routing-mark: to-mihomo → 10.0.0.2</strong><em><StatusDot />活动</em></div>
            <div><span>NAT 规则</span><strong>srcnat (PPPoE) · masquerade</strong><em><StatusDot />活动</em></div>
            <div><span>L2TP 客户端</span><strong>2 个配置 · 1 个在线</strong><em><StatusDot status="warning" />检查</em></div>
          </div>
        </section>
        <aside className="stack">
          <section className="panel">
            <div className="panel-heading"><div><h2>WAN 状态</h2><p>PPPoE 已连接</p></div><StatusDot /></div>
            <dl className="definition-list">
              <div><dt>公网 IP</dt><dd>203.0.113.45</dd></div>
              <div><dt>网关</dt><dd>203.0.113.1</dd></div>
              <div><dt>丢包率</dt><dd>0.00%</dd></div>
              <div><dt>延迟</dt><dd>12 ms</dd></div>
              <div><dt>运行时间</dt><dd>12 天 04:35:28</dd></div>
            </dl>
          </section>
          <section className="panel danger-zone">
            <div className="panel-heading"><div><h2>安全操作</h2><p>操作前自动创建快照</p></div></div>
            <Button icon={ShieldCheck} onClick={() => notify("配置验证通过，未发现冲突")}>验证规则</Button>
            <Button icon={Save} onClick={() => notify("RouterOS 配置快照已创建")}>备份配置</Button>
          </section>
        </aside>
      </div>
    </div>
  );
}

function MosDNSPage() {
  return (
    <div className="stack">
      <div className="readonly-banner"><LockKeyhole size={18} /><span><strong>只读模式</strong> DNS 配置暂不由 FoxOS 修改；这里只显示 MosDNS 运行状态。</span></div>
      <div className="summary-grid">
        <SummaryCard icon={Activity} label="实时查询" value="123 QPS" tone="purple" />
        <SummaryCard icon={Database} label="缓存命中率" value="92.4%" tone="green" />
        <SummaryCard icon={Zap} label="平均响应" value="18 ms" />
        <SummaryCard icon={AlertTriangle} label="失败率" value="0.02%" tone="orange" />
      </div>
      <div className="page-grid two-thirds">
        <section className="panel">
          <div className="panel-heading"><div><h2>DNS 流量路径</h2><p>客户端与 Mihomo 查询均进入 MosDNS</p></div><span className="healthy-label"><StatusDot />运行正常</span></div>
          <div className="dns-flow">
            <TopologyNode icon={Users} title="LAN 客户端" subtitle="14 台设备" metric="78 QPS" />
            <FlowArrow />
            <TopologyNode icon={Database} title="MosDNS" subtitle="10.0.0.3:53" metric="92.4% 命中" accent="purple" />
            <FlowArrow />
            <div className="dns-destinations">
              <div><Globe2 size={21} /><span><strong>国内上游</strong><small>65 QPS · 9 ms</small></span><StatusDot /></div>
              <div><Globe2 size={21} /><span><strong>海外上游</strong><small>38 QPS · 78 ms</small></span><StatusDot /></div>
              <div><ShieldCheck size={21} /><span><strong>广告/恶意域名</strong><small>20 QPS · 拦截</small></span><StatusDot status="warning" /></div>
            </div>
          </div>
          <div className="table-wrap">
            <table>
              <thead><tr><th>时间</th><th>域名</th><th>客户端</th><th>路由</th><th>响应</th><th>结果</th></tr></thead>
              <tbody>
                <tr><td>14:35:27</td><td>www.taobao.com</td><td>192.168.88.21</td><td>国内上游</td><td>12 ms</td><td className="green-text">成功</td></tr>
                <tr><td>14:35:26</td><td>www.google.com</td><td>10.0.0.2</td><td>海外上游</td><td>46 ms</td><td className="green-text">成功</td></tr>
                <tr><td>14:35:25</td><td>adservice.google.com</td><td>192.168.88.30</td><td>拦截</td><td>—</td><td className="orange-text">拦截</td></tr>
              </tbody>
            </table>
          </div>
        </section>
        <aside className="panel">
          <div className="panel-heading"><div><h2>服务状态</h2><p>最后采集于刚刚</p></div><StatusDot /></div>
          <dl className="definition-list">
            <div><dt>进程状态</dt><dd className="green-text">运行中</dd></div>
            <div><dt>监听端口</dt><dd>UDP/TCP 53</dd></div>
            <div><dt>配置文件</dt><dd>config_custom.yaml</dd></div>
            <div><dt>缓存大小</dt><dd>128.6 MB</dd></div>
            <div><dt>控制端口</dt><dd>10.0.0.3:9090</dd></div>
            <div><dt>指标端点</dt><dd>/metrics</dd></div>
          </dl>
        </aside>
      </div>
    </div>
  );
}

function DevicesPage({
  devices,
  setDevices,
  selectedDeviceId,
  setSelectedDeviceId,
  notify,
}: {
  devices: Device[];
  setDevices: React.Dispatch<React.SetStateAction<Device[]>>;
  selectedDeviceId: number;
  setSelectedDeviceId: (id: number) => void;
  notify: (message: string, tone?: Toast["tone"]) => void;
}) {
  const [search, setSearch] = useState("");
  const [status, setStatus] = useState("all");
  const [egress, setEgress] = useState("");
  const selected = devices.find((device) => device.id === selectedDeviceId) ?? devices[0];
  const filtered = devices.filter((device) => {
    const matchesSearch = `${device.name}${device.ip}${device.mac}`.toLowerCase().includes(search.toLowerCase());
    const matchesStatus = status === "all" || (status === "online" ? device.online : !device.online);
    return matchesSearch && matchesStatus;
  });
  const fixIp = () => {
    setDevices((items) => items.map((item) => item.id === selected.id ? { ...item, fixed: true } : item));
    notify(`${selected.name} 已固定为 ${selected.ip}`);
  };
  const applyEgress = () => {
    const nextEgress = egress || selected.egress;
    setDevices((items) => items.map((item) => item.id === selected.id ? { ...item, egress: nextEgress } : item));
    notify(`已应用 ${selected.name} 的出口策略，并通过连通性检测`);
  };
  return (
    <div className="stack">
      <div className="summary-grid">
        <SummaryCard icon={Users} label="在线设备" value={devices.filter((d) => d.online).length} tone="green" />
        <SummaryCard icon={LockKeyhole} label="已固定 IP" value={devices.filter((d) => d.fixed).length} />
        <SummaryCard icon={Link2} label="未绑定" value={devices.filter((d) => !d.fixed).length} tone="orange" />
        <SummaryCard icon={AlertTriangle} label="异常设备" value="1" tone="red" />
      </div>
      <div className="split-view">
        <section className="panel table-panel">
          <div className="toolbar">
            <label className="search-box"><Search size={17} /><input value={search} onChange={(event) => setSearch(event.target.value)} placeholder="搜索设备名称 / IP / MAC" /></label>
            <select value={status} onChange={(event) => setStatus(event.target.value)}><option value="all">全部状态</option><option value="online">在线</option><option value="offline">离线</option></select>
            <select><option>全部接口</option><option>bridge-lan</option><option>iot-lan</option></select>
            <Button icon={RefreshCw} onClick={() => notify("设备列表已从 RouterOS 刷新")}>刷新</Button>
          </div>
          <div className="table-wrap">
            <table className="interactive-table">
              <thead><tr><th>设备名称</th><th>IP 地址</th><th>MAC 地址</th><th>RouterOS 接口</th><th>出口策略</th><th>状态</th><th>延迟</th></tr></thead>
              <tbody>
                {filtered.map((device) => (
                  <tr className={selected.id === device.id ? "selected" : ""} key={device.id} onClick={() => setSelectedDeviceId(device.id)}>
                    <td><span className="name-cell"><DeviceIcon kind={device.kind} /><strong>{device.name}</strong></span></td>
                    <td>{device.ip}</td><td>{device.mac}</td><td>{device.iface}</td>
                    <td className={device.egress.includes("Mihomo") || device.egress.includes("US-") || device.egress.includes("HK-") ? "purple-text" : "blue-text"}>{device.egress}</td>
                    <td><StatusDot status={device.online ? "ok" : "offline"} />{device.online ? "在线" : "离线"}</td>
                    <td className="latency">{device.latency ? `${device.latency} ms` : "—"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>
        <aside className="detail-panel">
          <div className="detail-heading"><div><small>设备详情</small><h2>{selected.name}</h2><span><StatusDot status={selected.online ? "ok" : "offline"} />{selected.online ? "在线" : "离线"} · {selected.lastSeen}</span></div><DeviceIcon kind={selected.kind} /></div>
          <DetailSection title="设备信息">
            <dl className="definition-list">
              <div><dt>IP 地址</dt><dd>{selected.ip}</dd></div><div><dt>MAC 地址</dt><dd>{selected.mac}</dd></div>
              <div><dt>RouterOS 接口</dt><dd>{selected.iface}</dd></div><div><dt>当前出口</dt><dd className="purple-text">{selected.egress}</dd></div>
            </dl>
          </DetailSection>
          <DetailSection title="IP 绑定状态">
            <div className="inline-status"><StatusDot status={selected.fixed ? "ok" : "warning"} />{selected.fixed ? "RouterOS DHCP 静态租约" : "当前为动态租约"}</div>
            <Button icon={LockKeyhole} onClick={fixIp} disabled={selected.fixed}>{selected.fixed ? "IP 已固定" : "固定 IP"}</Button>
          </DetailSection>
          <DetailSection title="出口策略">
            <label className="field"><span>选择出口</span><select value={egress || selected.egress} onChange={(event) => setEgress(event.target.value)}><option>直连 WAN</option><option>Mihomo 自动选择</option><option>HK-01</option><option>US-LAX-01</option><option>链式代理</option><option>L2TP-日本</option></select></label>
            <div className="warning-note"><AlertTriangle size={16} />修改将更新 RouterOS 标记与路由规则，应用前会自动创建快照。</div>
          </DetailSection>
          <DetailSection title="出口检测">
            <dl className="definition-list"><div><dt>出口 IP</dt><dd>23.47.108.56</dd></div><div><dt>地区</dt><dd>美国 洛杉矶</dd></div><div><dt>延迟</dt><dd className="green-text">{selected.latency ?? "—"} ms</dd></div><div><dt>丢包率</dt><dd>0%</dd></div></dl>
          </DetailSection>
          <div className="detail-actions"><Button variant="primary" icon={Play} onClick={applyEgress}>应用并检测</Button></div>
        </aside>
      </div>
    </div>
  );
}

function ProxyPage({
  nodes,
  setNodes,
  selectedNodeId,
  setSelectedNodeId,
  notify,
}: {
  nodes: ProxyNode[];
  setNodes: React.Dispatch<React.SetStateAction<ProxyNode[]>>;
  selectedNodeId: number;
  setSelectedNodeId: (id: number) => void;
  notify: (message: string, tone?: Toast["tone"]) => void;
}) {
  const [search, setSearch] = useState("");
  const [protocol, setProtocol] = useState("all");
  const [modal, setModal] = useState<"add" | "import" | null>(null);
  const [testing, setTesting] = useState(false);
  const [chain, setChain] = useState([1, 2]);
  const selected = nodes.find((node) => node.id === selectedNodeId) ?? nodes[0];
  const filtered = nodes.filter((node) => {
    const matchesSearch = `${node.name}${node.server}${node.region}`.toLowerCase().includes(search.toLowerCase());
    return matchesSearch && (protocol === "all" || node.protocol === protocol);
  });
  const runTests = () => {
    setTesting(true);
    window.setTimeout(() => {
      setTesting(false);
      notify(`已完成 ${nodes.length} 个节点检测，发现 1 个离线节点`, "warning");
    }, 1400);
  };
  const deleteNode = async () => {
    if (selected.protocol === "L2TP") {
      notify("RouterOS 原生 L2TP 需在 RouterOS 页面删除", "warning");
      return;
    }
    if (selected.apiId) {
      try {
        await deleteNodeApi(selected.apiId);
      } catch (error) {
        notify(error instanceof Error ? error.message : "节点删除失败", "warning");
        return;
      }
    }
    setNodes((items) => items.filter((item) => item.id !== selected.id));
    setSelectedNodeId(nodes.find((item) => item.id !== selected.id)?.id ?? 1);
    notify(`${selected.name} 已删除`);
  };
  return (
    <div className="stack">
      <div className="proxy-header-row">
        <div className="summary-grid">
          <SummaryCard icon={Globe2} label="可用节点" value={`${nodes.filter((n) => n.status === "online").length} / ${nodes.length}`} tone="green" />
          <SummaryCard icon={Upload} label="订阅源" value="3" tone="purple" />
          <SummaryCard icon={Activity} label="平均延迟" value="128 ms" />
          <SummaryCard icon={AlertTriangle} label="异常节点" value={nodes.filter((n) => n.status !== "online").length} tone="orange" />
        </div>
        <div className="header-actions"><Button variant="primary" icon={Plus} onClick={() => setModal("add")}>添加节点</Button><Button icon={Upload} onClick={() => setModal("import")}>导入订阅</Button><Button icon={testing ? RefreshCw : Activity} onClick={runTests} disabled={testing}>{testing ? "检测中…" : "检测全部"}</Button></div>
      </div>

      <section className="panel chain-builder">
        <div className="panel-heading"><div><h2>链式代理路线</h2><p>从左到右按访问顺序执行 · 应用前自动验证</p></div><span className="healthy-label"><StatusDot />链路状态正常</span></div>
        <div className="chain-builder-row">
          <ChainNode icon={Router} name="本机 / RouterOS" detail="设备出口" locked />
          {chain.map((id) => {
            const node = nodes.find((item) => item.id === id);
            if (!node) return null;
            return <ChainNode key={id} icon={Server} name={node.name} detail={`${node.region} · ${node.latency ?? "—"} ms`} onRemove={() => setChain((items) => items.filter((item) => item !== id))} />;
          })}
          <div className="add-stage">
            <select defaultValue="" onChange={(event) => { const id = Number(event.target.value); if (id && !chain.includes(id)) setChain((items) => [...items, id]); event.currentTarget.value = ""; }}>
              <option value="">＋ 添加链路节点</option>
              {nodes.filter((node) => node.status !== "offline").map((node) => <option key={node.id} value={node.id}>{node.name}</option>)}
            </select>
          </div>
          <ChainNode icon={Globe2} name="Internet" detail="目标网络" locked />
          <Button variant="primary" icon={Zap} onClick={() => notify("链式代理已验证并应用，快照已保存")}>应用链式代理</Button>
        </div>
      </section>

      <div className="split-view">
        <section className="panel table-panel">
          <div className="toolbar">
            <label className="search-box"><Search size={17} /><input value={search} onChange={(event) => setSearch(event.target.value)} placeholder="搜索节点名称 / 服务器 / 地区" /></label>
            <select><option>全部地区</option><option>中国香港</option><option>美国</option><option>日本</option></select>
            <select value={protocol} onChange={(event) => setProtocol(event.target.value)}><option value="all">全部协议</option>{[...new Set(nodes.map((node) => node.protocol))].map((item) => <option key={item}>{item}</option>)}</select>
            <select><option>全部来源</option><option>Alpha 订阅</option><option>RouterOS 原生</option></select>
          </div>
          <div className="table-wrap">
            <table className="interactive-table">
              <thead><tr><th>节点名称</th><th>协议</th><th>服务器</th><th>地区</th><th>来源</th><th>延迟</th><th>丢包率</th><th>状态</th><th>使用中</th></tr></thead>
              <tbody>
                {filtered.map((node) => (
                  <tr className={selected.id === node.id ? "selected" : ""} key={node.id} onClick={() => setSelectedNodeId(node.id)}>
                    <td><strong>{node.name}</strong></td><td>{node.protocol}</td><td>{node.server}</td><td>{node.region}</td><td>{node.source}</td>
                    <td className={node.latency && node.latency > 200 ? "orange-text" : "green-text"}>{node.latency ? `${node.latency} ms` : "—"}</td><td>{node.loss}%</td>
                    <td><StatusDot status={node.status === "online" ? "ok" : node.status} />{node.status === "online" ? "在线" : node.status === "warning" ? "警告" : "离线"}</td><td>{node.inUse}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>
        <aside className="detail-panel">
          <div className="detail-heading"><div><small>节点详情</small><h2>{selected.name}</h2><span><StatusDot status={selected.status === "online" ? "ok" : selected.status} />{selected.status === "online" ? "在线" : "异常"} · 最近检测 14:35:12</span></div><Server size={24} /></div>
          <div className="detail-button-row"><Button icon={Activity} onClick={() => notify(`${selected.name} 检测完成：${selected.latency ?? "超时"} ms`)}>测试</Button><Button icon={Pencil} onClick={() => notify("编辑功能将在后端节点 API 接入后保存")}>编辑</Button><Button variant="danger" icon={Trash2} onClick={deleteNode}>删除</Button></div>
          <DetailSection title="连接信息"><dl className="definition-list"><div><dt>服务器</dt><dd>{selected.server}</dd></div><div><dt>协议</dt><dd>{selected.protocol}</dd></div><div><dt>地区</dt><dd>{selected.region}</dd></div><div><dt>来源</dt><dd>{selected.source}</dd></div></dl></DetailSection>
          <DetailSection title="健康状态"><dl className="definition-list"><div><dt>TCP 延迟</dt><dd className="green-text">{selected.latency ?? "—"} ms</dd></div><div><dt>丢包率</dt><dd>{selected.loss}%</dd></div><div><dt>连续在线</dt><dd>2 天 06:18</dd></div><div><dt>失败次数（24h）</dt><dd>{selected.status === "offline" ? 4 : 0}</dd></div></dl></DetailSection>
          <div className="history-bars" aria-label="最近延迟历史">{[45, 66, 52, 74, 63, 82, 58, 70, 61, 86, 68, 76].map((height, index) => <span key={index} style={{ height: `${height}%` }} />)}</div>
        </aside>
      </div>
      {modal ? <ProxyModal mode={modal} onClose={() => setModal(null)} onCreate={(node) => { setNodes((items) => [...items, node]); setModal(null); notify(`${node.name} 已添加`); }} notify={notify} /> : null}
    </div>
  );
}

function ChainNode({ icon: Icon, name, detail, locked, onRemove }: { icon: typeof Router; name: string; detail: string; locked?: boolean; onRemove?: () => void }) {
  return (
    <div className="chain-node">
      <Icon size={22} /><span><strong>{name}</strong><small>{detail}</small></span>
      {!locked ? <button aria-label={`移除 ${name}`} onClick={onRemove}><X size={14} /></button> : null}
    </div>
  );
}

function ProxyModal({ mode, onClose, onCreate, notify }: { mode: "add" | "import"; onClose: () => void; onCreate: (node: ProxyNode) => void; notify: (message: string) => void }) {
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    if (mode === "import") {
      notify("订阅测试成功：发现 24 个节点，已加入同步任务");
      onClose();
      return;
    }
    onCreate({
      id: Date.now(), name: String(data.get("name") || "新节点"), protocol: String(data.get("protocol") || "VLESS"),
      server: String(data.get("server") || "example.com:443"), region: "待检测", source: "手动添加", latency: null, loss: 0, status: "warning", inUse: "—",
    });
  };
  return (
    <div className="modal-backdrop" onMouseDown={onClose}>
      <form className="modal" onSubmit={submit} onMouseDown={(event) => event.stopPropagation()}>
        <div className="modal-heading"><div><small>{mode === "add" ? "手动配置" : "订阅源"}</small><h2>{mode === "add" ? "添加代理节点" : "导入订阅"}</h2></div><button type="button" onClick={onClose}><X size={19} /></button></div>
        {mode === "add" ? (
          <div className="form-grid">
            <label className="field"><span>节点名称</span><input name="name" required placeholder="例如 HK-02" /></label>
            <label className="field"><span>协议</span><select name="protocol"><option>VLESS</option><option>Trojan</option><option>Shadowsocks</option><option>Hysteria2</option><option>SOCKS5</option></select></label>
            <label className="field full"><span>服务器地址</span><input name="server" required placeholder="server.example.com:443" /></label>
            <label className="field"><span>UUID / 用户名</span><input name="identity" placeholder="按协议填写" /></label>
            <label className="field"><span>密码</span><input name="password" type="password" placeholder="按协议填写" /></label>
          </div>
        ) : (
          <div className="form-grid">
            <label className="field full"><span>订阅名称</span><input required placeholder="例如 Alpha 订阅" /></label>
            <label className="field full"><span>订阅 URL</span><input required type="url" placeholder="https://example.com/subscription" /></label>
            <label className="field"><span>同步模式</span><select><option>智能合并</option><option>追加节点</option><option>替换此订阅节点</option></select></label>
            <label className="field"><span>更新间隔</span><select><option>每 24 小时</option><option>每 12 小时</option><option>手动更新</option></select></label>
          </div>
        )}
        <div className="modal-actions"><Button onClick={onClose}>取消</Button><Button variant="primary" icon={mode === "add" ? Plus : Upload} type="submit">{mode === "add" ? "添加并检测" : "测试并导入"}</Button></div>
      </form>
    </div>
  );
}

function DetailSection({ title, children }: { title: string; children: React.ReactNode }) {
  return <section className="detail-section"><h3>{title}</h3>{children}</section>;
}

function TopologyPage({ navigate }: { navigate: (page: PageKey) => void }) {
  return (
    <div className="stack">
      <section className="panel topology-large">
        <div className="panel-heading"><div><h2>实时网络拓扑</h2><p>点击组件进入对应管理页面</p></div><Button icon={RefreshCw}>刷新拓扑</Button></div>
        <div className="topology-stage">
          <button onClick={() => navigate("routeros")}><TopologyNode icon={Router} title="RouterOS" subtitle="10.0.0.1 · 核心网关" metric="↓ 812 / ↑ 198 Mbps" accent="orange" /></button>
          <div className="topology-columns">
            <div className="topology-group">
              <h3>网络入口</h3><TopologyNode icon={Globe2} title="Internet / WAN" subtitle="PPPoE · 可达" metric="12 ms" />
            </div>
            <div className="topology-group">
              <h3>局域网</h3><TopologyNode icon={Users} title="LAN 设备" subtitle="14 台在线" metric="3 个网段" />
              <TopologyNode icon={Wifi} title="IoT 设备" subtitle="iot-lan" metric="4 台在线" />
            </div>
            <div className="topology-group">
              <h3>代理出口</h3><button onClick={() => navigate("proxies")}><TopologyNode icon={Network} title="Mihomo" subtitle="10.0.0.2" metric="68 活跃连接" accent="blue" /></button>
              <TopologyNode icon={Server} title="US-LAX-01" subtitle="当前出口" metric="162 ms" accent="blue" />
            </div>
            <div className="topology-group">
              <h3>DNS（只读）</h3><button onClick={() => navigate("mosdns")}><TopologyNode icon={Database} title="MosDNS" subtitle="10.0.0.3:53" metric="123 QPS" accent="purple" /></button>
            </div>
          </div>
        </div>
      </section>
      <div className="summary-grid">
        <SummaryCard icon={Cable} label="活跃链路" value="6" tone="green" />
        <SummaryCard icon={Users} label="在线设备" value="14" />
        <SummaryCard icon={Activity} label="活动连接" value="68" tone="purple" />
        <SummaryCard icon={AlertTriangle} label="拓扑异常" value="0" tone="orange" />
      </div>
    </div>
  );
}

function LogsPage({ items }: { items: typeof logs }) {
  const [level, setLevel] = useState("all");
  const filtered = items.filter((item) => level === "all" || item.level === level);
  return (
    <section className="panel">
      <div className="toolbar">
        <label className="search-box"><Search size={17} /><input placeholder="搜索事件、来源或详情" /></label>
        <select value={level} onChange={(event) => setLevel(event.target.value)}><option value="all">全部级别</option><option>成功</option><option>信息</option><option>警告</option><option>错误</option></select>
        <select><option>全部来源</option><option>RouterOS</option><option>Mihomo</option><option>MosDNS</option></select>
        <Button icon={RefreshCw}>刷新</Button>
      </div>
      <div className="table-wrap">
        <table><thead><tr><th>时间</th><th>级别</th><th>来源</th><th>事件</th><th>详情</th></tr></thead>
          <tbody>{filtered.map((item) => <tr key={`${item.time}-${item.event}`}><td>{item.time}</td><td><span className={`log-level ${item.level}`}>{item.level}</span></td><td>{item.source}</td><td><strong>{item.event}</strong></td><td>{item.detail}</td></tr>)}</tbody>
        </table>
      </div>
    </section>
  );
}

function SettingsPage({ notify }: { notify: (message: string, tone?: Toast["tone"]) => void }) {
  const [saved, setSaved] = useState(false);
  const save = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    try {
      saveApiToken(String(data.get("apiToken") || ""));
      setSaved(true);
      notify("FoxOS API Token 已保存在当前浏览器，请返回总览运行全链路检测");
      window.setTimeout(() => setSaved(false), 1800);
    } catch (error) {
      notify(error instanceof Error ? error.message : "Token 保存失败", "warning");
    }
  };
  return (
    <div className="page-grid two-thirds">
      <form className="panel" onSubmit={save}>
        <div className="panel-heading"><div><h2>服务连接</h2><p>敏感凭据仅写入，不在页面回显</p></div><ShieldCheck size={22} className="green-text" /></div>
        <div className="settings-section"><h3>FoxOS API</h3><div className="form-grid"><label className="field full"><span>API Token</span><input name="apiToken" type="password" minLength={32} required placeholder="至少 32 个字符；仅保存在当前浏览器" autoComplete="off" /></label></div></div>
        <div className="settings-section"><h3>RouterOS</h3><div className="form-grid"><label className="field"><span>地址</span><input defaultValue="10.0.0.1" /></label><label className="field"><span>REST 端口</span><input defaultValue="80" /></label><label className="field"><span>用户名</span><input defaultValue="admin" /></label><label className="field"><span>新密码</span><input type="password" placeholder="留空表示不修改" /></label></div></div>
        <div className="settings-section"><h3>Mihomo</h3><div className="form-grid"><label className="field"><span>控制器地址</span><input defaultValue="http://10.0.0.2:9090" /></label><label className="field"><span>配置文件</span><input defaultValue="/var/lib/foxos/managed/mihomo/config.yaml" /></label></div></div>
        <div className="settings-section readonly-settings"><h3>MosDNS（只读）</h3><div className="form-grid"><label className="field"><span>状态地址</span><input defaultValue="http://10.0.0.3:9090" readOnly /></label><label className="field"><span>配置文件</span><input defaultValue="config_custom.yaml" readOnly /></label></div></div>
        <div className="form-actions"><Button variant="primary" icon={saved ? Check : Save} type="submit">{saved ? "已保存" : "验证并保存"}</Button></div>
      </form>
      <aside className="stack">
        <section className="panel">
          <div className="panel-heading"><div><h2>备份与恢复</h2><p>安全操作默认创建快照</p></div></div>
          <div className="action-list"><button onClick={() => notify("控制平面快照已创建")}><Save size={18} /><span><strong>创建配置快照</strong><small>RouterOS 与 FoxOS 控制状态</small></span><ChevronRight size={16} /></button><button onClick={() => notify("诊断包已导出")}><Upload size={18} /><span><strong>导出诊断包</strong><small>日志与脱敏运行状态</small></span><ChevronRight size={16} /></button></div>
        </section>
        <section className="panel">
          <div className="panel-heading"><div><h2>系统信息</h2><p>FoxOS v1.0.0</p></div><StatusDot /></div>
          <dl className="definition-list"><div><dt>运行环境</dt><dd>RouterOS x86</dd></div><div><dt>数据库</dt><dd>SQLite · 正常</dd></div><div><dt>API 版本</dt><dd>v1</dd></div><div><dt>最后快照</dt><dd>2026-07-26 14:31</dd></div></dl>
        </section>
      </aside>
    </div>
  );
}

export default App;
