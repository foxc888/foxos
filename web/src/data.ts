export type ServiceStatus = {
  name: string;
  address: string;
  tone: "orange" | "blue" | "purple";
};

export type Device = {
  id: number;
  apiId?: string;
  name: string;
  kind: "phone" | "computer" | "server" | "tv" | "iot";
  ip: string;
  mac: string;
  iface: string;
  dhcpServer?: string;
  alias?: string;
  vendor?: string;
  tags: string[];
  firstSeen?: string;
  profileTracked: boolean;
  egress: string;
  latency: number | null;
  online: boolean;
  fixed: boolean;
  lastSeen: string;
};

export type ProxyNode = {
  id: number;
  apiId?: string;
  name: string;
  protocol: string;
  server: string;
  region: string;
  source: string;
  latency: number | null;
  loss: number;
  status: "online" | "warning" | "offline";
  verification: "unverified" | "tcp" | "routeros-session";
  inUse: string;
};

export const services: ServiceStatus[] = [
  { name: "RouterOS", address: "10.0.0.1", tone: "orange" },
  { name: "Mihomo", address: "10.0.0.2", tone: "blue" },
  { name: "MosDNS", address: "10.0.0.3:53", tone: "purple" },
];
