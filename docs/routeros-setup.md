# RouterOS REST 连接

FoxOS 使用 RouterOS v7 REST API，不通过 SSH 拼接命令。目标部署固定为 `10.0.0.1`，全量包要求 RouterOS 7.21+。

## 专用账号

不要使用 `admin`。全量安装器创建或验证：

```routeros
/user/group/add name=foxos-rest policy=read,write,rest-api
/user/add name=foxos-service group=foxos-rest address=10.0.0.4/32 password="<随机强密码>" comment="foxos:service"
```

同名账号或组不带 FoxOS marker/comment 时安装失败，不接管用户资源。REST 服务必须限制在 `10.0.0.0/24` 或更窄的 `10.0.0.4/32`，不得暴露 WAN。

全量包当前使用 `http://10.0.0.1`。HTTP 凭据在管理 LAN 内不是加密传输；生产环境可以改为 `www-ssl`，但必须提供 FoxOS 系统信任链可验证的证书，客户端不会跳过 TLS 校验。

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

1. `/ip/service/print where name=www` 或可信 `www-ssl`。
2. `foxos-service` 地址限制与 policy。
3. `veth-foxos`、`bridge-lan` 和 `10.0.0.4 -> 10.0.0.1`。
4. RouterOS 日志中的 401/403。
5. FoxOS ready 的 RouterOS check。

不要把 RouterOS 密码、env list value 或 export 附在问题报告中。
