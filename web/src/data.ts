export type ServiceStatus = {
  name: string;
  address: string;
  detail: string;
  tone: "orange" | "blue" | "purple";
};

export type Device = {
  id: number;
  name: string;
  kind: "phone" | "computer" | "server" | "tv" | "iot";
  ip: string;
  mac: string;
  iface: string;
  egress: string;
  latency: number | null;
  online: boolean;
  fixed: boolean;
  lastSeen: string;
};

export type ProxyNode = {
  id: number;
  name: string;
  protocol: string;
  server: string;
  region: string;
  source: string;
  latency: number | null;
  loss: number;
  status: "online" | "warning" | "offline";
  inUse: string;
};

export const services: ServiceStatus[] = [
  { name: "RouterOS", address: "10.0.0.1", detail: "负载 18% · 运行 12 天", tone: "orange" },
  { name: "Mihomo", address: "10.0.0.2", detail: "68 活跃连接 · 18 ms", tone: "blue" },
  { name: "MosDNS", address: "10.0.0.3:53", detail: "只读 · 92.4% 命中", tone: "purple" },
];

export const initialDevices: Device[] = [
  { id: 1, name: "iPhone 15 Pro", kind: "phone", ip: "192.168.88.21", mac: "6C:3E:6B:44:55:66", iface: "bridge-lan", egress: "US-LAX-01", latency: 162, online: true, fixed: true, lastSeen: "刚刚" },
  { id: 2, name: "MacBook Air", kind: "computer", ip: "192.168.88.42", mac: "3C:22:FB:66:77:88", iface: "bridge-lan", egress: "HK-01", latency: 24, online: true, fixed: true, lastSeen: "1 分钟前" },
  { id: 3, name: "Android 手机", kind: "phone", ip: "192.168.88.41", mac: "20:10:7A:33:44:55", iface: "bridge-lan", egress: "Mihomo 自动选择", latency: 18, online: true, fixed: false, lastSeen: "2 分钟前" },
  { id: 4, name: "Windows-PC", kind: "computer", ip: "192.168.88.22", mac: "00:1A:2B:3C:4D:5E", iface: "bridge-lan", egress: "直连 WAN", latency: 12, online: true, fixed: true, lastSeen: "2 分钟前" },
  { id: 5, name: "客厅电视", kind: "tv", ip: "192.168.88.30", mac: "AC:BC:32:11:22:33", iface: "bridge-lan", egress: "US-LAX-01", latency: 168, online: true, fixed: true, lastSeen: "3 分钟前" },
  { id: 6, name: "NAS-DS920", kind: "server", ip: "192.168.88.10", mac: "00:11:32:AA:BB:CC", iface: "bridge-lan", egress: "直连 WAN", latency: 9, online: true, fixed: true, lastSeen: "3 分钟前" },
  { id: 7, name: "PlayStation 5", kind: "computer", ip: "192.168.88.23", mac: "18:31:BF:77:88:99", iface: "bridge-lan", egress: "L2TP-日本", latency: 132, online: true, fixed: false, lastSeen: "5 分钟前" },
  { id: 8, name: "摄像头-客厅", kind: "iot", ip: "192.168.88.24", mac: "D0:76:6E:AA:BB:DD", iface: "iot-lan", egress: "Mihomo 自动选择", latency: 26, online: true, fixed: true, lastSeen: "6 分钟前" },
  { id: 9, name: "打印机", kind: "iot", ip: "192.168.88.26", mac: "44:D2:44:EE:FF:00", iface: "iot-lan", egress: "直连 WAN", latency: 8, online: true, fixed: true, lastSeen: "10 分钟前" },
  { id: 10, name: "旧手机", kind: "phone", ip: "192.168.88.50", mac: "58:3A:35:22:11:00", iface: "bridge-lan", egress: "未设置", latency: null, online: false, fixed: false, lastSeen: "1 天前" },
];

export const initialNodes: ProxyNode[] = [
  { id: 1, name: "HK-01", protocol: "VLESS", server: "103.116.7.12:443", region: "中国香港", source: "Alpha 订阅", latency: 28, loss: 0.2, status: "online", inUse: "链路 1" },
  { id: 2, name: "US-LAX-01", protocol: "VLESS", server: "198.18.0.10:443", region: "美国洛杉矶", source: "Alpha 订阅", latency: 162, loss: 0.1, status: "online", inUse: "链路 1" },
  { id: 3, name: "JP-Tokyo-01", protocol: "Trojan", server: "203.104.209.1:443", region: "日本东京", source: "Beta 订阅", latency: 86, loss: 0, status: "online", inUse: "—" },
  { id: 4, name: "SG-Relay-01", protocol: "Hysteria2", server: "139.99.125.1:443", region: "新加坡", source: "Beta 订阅", latency: 68, loss: 0.3, status: "online", inUse: "—" },
  { id: 5, name: "TW-Taipei-01", protocol: "Shadowsocks", server: "114.34.1.1:8388", region: "中国台湾", source: "手动添加", latency: 55, loss: 0.1, status: "online", inUse: "—" },
  { id: 6, name: "DE-Frankfurt-01", protocol: "VLESS", server: "185.1.85.10:443", region: "德国法兰克福", source: "Alpha 订阅", latency: 214, loss: 1.2, status: "warning", inUse: "—" },
  { id: 7, name: "US-NY-01", protocol: "Trojan", server: "45.33.2.1:443", region: "美国纽约", source: "Alpha 订阅", latency: null, loss: 100, status: "offline", inUse: "—" },
  { id: 8, name: "L2TP-HK-01", protocol: "L2TP", server: "hk-l2tp.example.com", region: "中国香港", source: "RouterOS 原生", latency: 92, loss: 0, status: "online", inUse: "设备 2" },
  { id: 9, name: "L2TP-US-01", protocol: "L2TP", server: "us-l2tp.example.com", region: "美国洛杉矶", source: "RouterOS 原生", latency: 164, loss: 0.1, status: "online", inUse: "—" },
];

export const logs = [
  { time: "14:35:12", level: "成功", source: "Mihomo", event: "链式代理已应用", detail: "RouterOS → HK-01 → US-LAX-01" },
  { time: "14:34:58", level: "信息", source: "RouterOS", event: "DHCP 租约续期", detail: "iPhone 15 Pro · 192.168.88.21" },
  { time: "14:33:41", level: "警告", source: "节点检测", event: "节点延迟过高", detail: "DE-Frankfurt-01 · 214 ms" },
  { time: "14:31:20", level: "成功", source: "FoxOS", event: "配置快照已创建", detail: "snapshot-20260726-143120" },
  { time: "14:28:09", level: "错误", source: "节点检测", event: "连接超时", detail: "US-NY-01 · 10s timeout" },
  { time: "14:22:18", level: "信息", source: "MosDNS", event: "运行状态采集", detail: "缓存命中率 92.4%（只读）" },
];
