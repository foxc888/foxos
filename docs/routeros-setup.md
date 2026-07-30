# RouterOS REST 连接

FoxOS 使用 RouterOS v7 REST API，不通过 SSH 拼接命令。目标地址来自 `site-config.rsc`；全量包的脚本语法下限是 RouterOS 7.21，精确目标版本仍须通过同版本 CHR 兼容门禁。

## 专用账号

不要使用 `admin`。全量安装器创建或验证：

```routeros
/user/group/add name=foxos-rest policy=read,write,rest-api
/user/add name=foxos-service group=foxos-rest address=<FoxOS站点地址>/32 password="<随机强密码>" comment="foxos:service"
```

同名账号或组不带 FoxOS marker/comment 时安装失败，不接管用户资源。REST 服务必须限制在站点网段或更窄的 FoxOS `/32`，不得暴露 WAN。

全量包按站点 RouterOS 地址使用 HTTP REST。凭据在管理 LAN 内不是加密传输，因此该 LAN 必须可信、隔离且禁止 WAN 访问。改用 `www-ssl` 需要同时调整端点契约并提供 FoxOS 系统信任链可验证的证书；客户端不会跳过 TLS 校验。FoxOS 自身的浏览器/API 入口默认强制本地 CA HTTPS。

## 读取允许列表

- `/rest/system/resource`
- `/rest/interface`
- `/rest/ip/dhcp-server/lease`
- `/rest/ip/arp`
- `/rest/interface/l2tp-client`
- `/rest/ip/route`
- `/rest/ip/dhcp-server`
- `/rest/container`
- `/rest/routing/table`
- `/rest/ip/firewall/address-list`
- `/rest/ip/firewall/mangle`
- `/rest/ip/firewall/filter`

代码不接受调用者提供任意 RouterOS 路径。

## 写入允许列表

静态绑定只允许 DHCP Lease PUT/PATCH/DELETE，并要求 `foxos:device:` owner。出口执行只允许 FoxOS 自有：

- `/rest/ip/firewall/address-list`
- `/rest/ip/firewall/mangle`
- `/rest/ip/firewall/filter`
- `/rest/ip/route`

PUT 只能创建集合项，PATCH/DELETE 必须使用合法 RouterOS ID，并在写入前回读相同 comment。Writer 还验证 chain、action、routing mark、目标表、网关和管理地址字段。

## 设备识别

DHCP 提供主机名、Lease 和 server；ARP 提供接口邻居。FoxOS 规范化 MAC 并合并为库存，将实时状态与 SQLite 中的别名、厂商、标签、首次/最后在线和历史分开保存。

## 连接失败

依次检查：

1. `/ip/service/print where name=www && dynamic=no` 或可信 `www-ssl`。
2. `foxos-service` 地址限制与 policy。
3. `veth-foxos`、站点管理桥和 FoxOS 地址到 RouterOS 地址的路径。
4. RouterOS 日志中的 401/403。
5. FoxOS ready 的 RouterOS check。

不要把 RouterOS 密码、env list value 或 export 附在问题报告中。
