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
- 选定 server 上无 comment 的普通动态 Lease：明确计划 `make-static`，再写入 FoxOS owner；不会创建第二条 Lease 冒充采用。
- 已有 FoxOS 静态 Lease：只计划更新匹配 owner 的对象；未知静态 Lease 不接管。
- 新建或改变地址时，地址必须位于唯一 DHCP network、对应 server 接口网段和动态池外的保留范围；采用当前动态地址是唯一允许保留原动态池地址的流程。
- IP 被其他 MAC/ARP/RouterOS address 使用、拓扑歧义、字段不完整或管理面地址：拒绝。

计划包含 Lease、server、network、pool 和必要冲突数据的精确前态摘要。执行要求原样提交计划和五分钟确认令牌，并在写入前重新读取 RouterOS；摘要变化即拒绝过期计划。Writer 只允许 DHCP Lease 与受控 `make-static` 路径；写后回读 MAC、IP、static 状态和 comment。补偿前也比较当前状态，确认后出现外部修改时拒绝覆盖，审计单独记录 stale、回滚成功或回滚失败。

## DHCP 地址规划

规划器读取 `/ip/pool`、`/ip/dhcp-server/network`、DHCP server/lease、RouterOS address 和 ARP，计算范围、静态/基础设施保留、动态占用、冲突、利用率、剩余容量和耗尽风险。范围首尾包含：`.100-.200` 是 101，`.10-.254` 是 245；只有保留地址落在范围内才扣减，例如排除一个后为 244。

扩容不是开关。调用方必须提交目标容量和完整拟议范围；服务端返回 before/after、冲突、精确 pool PATCH、状态摘要和一次性确认，再在执行前回读并在失败时受控补偿。需要超过单个 `/24` 的容量时，预览会提示比较 `/23` 的广播域扩大与 VLAN 拆分的隔离/运维影响。

## 出口类型

| 类型 | RouterOS 行为 | Mihomo 行为 |
|---|---|---|
| `direct` | 只移除该设备 FoxOS 自有规则 | 生成配置时目标为 DIRECT |
| `mihomo-node` | 当前不执行；capability 固定不可用 | YAML 可生成 `SRC-IP-CIDR` 节点规则，但不代表流量进入 Mihomo |
| `proxy-chain` | 当前不执行；capability 固定不可用 | YAML 可生成 chain 规则，但不代表流量进入 Mihomo |
| `l2tp` | 专用 `foxos-l2tp-{policyId}` 表和默认路由 | 设备规则保持 DIRECT |
| `blocked` | FoxOS forward 链拒绝设备 | 生成配置时目标为 REJECT |

策略不能通过独立 SQLite 写入口绕过执行器。出口 execute 只有在 RouterOS 回读验证成功后才持久化新策略；若外部写入后进程中断，任务恢复会同时回读 RouterOS 和数据库，再完成持久化、精确重试或失败关闭。

## 安全前置条件

FoxOS 不自动接管或创建可能改变全网行为的基础规则。计划前必须存在：

- blocked 策略需要 `foxos:anchor:forward` 跳转到 `foxos-forward`，并且是首条活动 forward 规则。
- L2TP 策略需要首条活动 prerouting anchor、运行中的目标 client 和对应 FoxOS FIB 表。
- 不得存在会绕过上述链的活动 FastTrack。

前置条件缺失时计划失败，不会先写一半再尝试修复网络。`GET /api/v1/egress/capabilities` 明确返回 direct、blocked、mihomo-node、proxy-chain、l2tp 的 `available`、`experimental`、`missing` 和证据。

Mihomo 两种模式不会因为存在 `foxos:prerequisite:mihomo-transparent` marker 就变为可用。当前基础配置 `tun.enable=false`，且 RouterOS Container 的透明入口、路由/回程、管理面旁路、FastTrack 处理和真实客户端出口 IP 尚无联合证据；readiness 固定列出这些缺失项。

## 管理面保护

站点清单中的 RouterOS、Mihomo、MosDNS 和 FoxOS 地址永久禁止成为设备策略地址。站点清单未加载时前端失败关闭策略写入。非 direct 操作只维护精确 FoxOS owner 资源；不会改用户 DNS、NAT 或默认路由。

所有新增、更新和删除操作都必须携带匹配的 `foxos:` owner comment。用户已有路由、Mangle、Filter、NAT、DNS 和 DHCP network 不在 Writer 允许列表中。

## 删除引用

节点和代理组仍被设备策略或其他组引用时不能删除。链式组按有序节点生成；保存链不会自动热重载 Mihomo，必须在运维页重新预览并发布配置。
