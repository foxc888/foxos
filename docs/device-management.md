# 设备管理与出口策略

FoxOS 合并 RouterOS DHCP Lease 与 ARP，使用 MAC 作为稳定标识。SQLite 保存用户资料、策略和在线历史，不把上次状态冒充为实时 RouterOS 结果。

## 设备资料

- 别名、厂商和最多 16 个标签。
- 首次发现、最后在线、当前在线/离线/不可用。
- 状态发生切换时写入在线历史。
- RouterOS 读取失败时保留资料，但库存响应标记 `sourceAvailable=false`。

## 静态 Lease

保存前读取实时 Lease 并生成计划：

- 同 MAC、同 IP 已经是匹配的 FoxOS 静态 Lease：无操作。
- 动态或内容不同：只计划修改对应 Lease。
- IP 被其他 MAC 使用、MAC/IP/Server 不完整、管理面地址：拒绝。

执行要求原样提交计划和五分钟确认令牌。Writer 只允许 DHCP Lease 路径和 `foxos:device:` comment；写后回读 MAC、IP、static 状态和 comment。验证失败时恢复原状态，审计记录错误分类和回滚结果。

## 出口类型

| 类型 | RouterOS 行为 | Mihomo 行为 |
|---|---|---|
| `direct` | 只移除该设备 FoxOS 自有规则 | 生成配置时目标为 DIRECT |
| `mihomo-node` | 将设备流量标记到 `foxos-mihomo` | `SRC-IP-CIDR` 指向节点 |
| `proxy-chain` | 将设备流量标记到 `foxos-mihomo` | `SRC-IP-CIDR` 指向 chain 组 |
| `l2tp` | 专用 `foxos-l2tp-{policyId}` 表和默认路由 | 设备规则保持 DIRECT |
| `blocked` | FoxOS forward 链拒绝设备 | 生成配置时目标为 REJECT |

策略保存到 SQLite 与 RouterOS 执行是两个明确阶段。出口 execute 只有在 RouterOS 回读验证成功后才持久化新策略。

## 安全前置条件

FoxOS 不自动接管或创建可能改变全网行为的基础规则。计划前必须存在：

- `foxos:anchor:mangle` 跳转到 `foxos-prerouting`，并且是首条活动 prerouting 规则。
- blocked 策略需要 `foxos:anchor:forward` 跳转到 `foxos-forward`，并且是首条活动 forward 规则。
- Mihomo 策略需要 `foxos-mihomo` FIB 表及带 `foxos:prerequisite:mihomo-transparent` 的 `10.0.0.2` 网关路由。
- L2TP 策略需要运行中的目标 client 和对应 FoxOS FIB 表。
- 不得存在会绕过上述链的活动 FastTrack。

前置条件缺失时计划失败，不会先写一半再尝试修复网络。

## 管理面保护

`10.0.0.1`、`10.0.0.2`、`10.0.0.3`、`10.0.0.4` 永久禁止成为设备策略地址。非 direct 操作维护 `foxos-management-plane` 地址列表，并通过目标否定确保管理面旁路。

所有新增、更新和删除操作都必须携带匹配的 `foxos:` owner comment。用户已有路由、Mangle、Filter、NAT、DNS 和 DHCP network 不在 Writer 允许列表中。

## 删除引用

节点和代理组仍被设备策略或其他组引用时不能删除。链式组按有序节点生成；保存链不会自动热重载 Mihomo，必须在运维页重新预览并发布配置。
