# FoxOS API 参考

所有响应使用 JSON。非浏览器客户端访问受保护接口时必须携带：

```http
Authorization: Bearer <FOXOS_API_TOKEN>
Accept: application/json
```

写请求使用 `Content-Type: application/json`。服务端限制请求体大小；API 响应设置 `Cache-Control: no-store`。

Web UI 使用同源会话：`POST /api/v1/session` 以管理 Token 换取 8 小时 HttpOnly Cookie 和页面内存 CSRF；`GET /api/v1/session` 回读当前会话，`DELETE /api/v1/session` 主动退出。Cookie 写请求还必须携带 `X-FoxOS-CSRF` 并通过 Origin/Sec-Fetch-Site 检查。会话只存在当前进程，不是 JWT、多用户或持久登录。

## 健康检查

| 方法 | 路径 | 认证 | 说明 |
|---|---|---|---|
| GET | `/api/v1/health/live` | 否 | 进程存活、版本与服务端时间 |
| GET | `/api/v1/health/ready` | 否 | SQLite、关键挂载、已配置 RouterOS/Mihomo/MosDNS |
| GET | `/api/v1/site` | 否 | 实际站点、服务地址、受保护地址、HTTPS/CA 指纹 |
| GET | `/api/v1/site/ca` | 否 | HTTPS 启用时下载本地 CA PEM |

未配置的可选依赖在 ready 中显示 `not_configured`，不会冒充 `ok`；已配置但不可达会返回 HTTP 503。

## 升级控制

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/api/v1/system/upgrade/checkpoint` | 排空 HTTP mutation 与完整任务 handler，冻结 SQLite 写入并创建兼容快照 |
| POST | `/api/v1/system/upgrade/promoted` | 幂等记录 promoted 审计和终态，再解除写冻结 |
| POST | `/api/v1/system/upgrade/aborted` | 仅允许检查点源版本在 schema 未变化或已恢复时幂等取消，再解除写冻结 |

三个请求都使用 `{"operationId":"<release-id>"}`，需要 Bearer 认证，并在响应中回显同一 `operationId` 与持久状态。checkpoint 后 GET 和这三个控制端点仍可用，其他 HTTP 写请求返回 503 `upgrade_maintenance`；任务 Submit/Retry、worker claim 和 SQLite mutator 同样失败关闭。`aborted` 在旧版本已自动恢复数据库时可返回 `restored`，两者都表示该 operation 不再持有写冻结。调用方必须按同一 operation 重试不确定响应；promoted 无法确认时不得自动回滚已经运行的新槽。

## 节点与代理组

| 方法 | 路径 | 说明 |
|---|---|---|
| GET / POST | `/api/v1/nodes` | 列表、创建 |
| GET / PUT / DELETE | `/api/v1/nodes/{id}` | 读取、替换、删除 |
| POST | `/api/v1/nodes/import` | 多行分享链接原子导入 |
| POST | `/api/v1/nodes/{id}/probe` | FoxOS 到目标的 TCP 建连检查 |
| GET / POST | `/api/v1/proxy-groups` | 列表、创建 |
| PUT / DELETE | `/api/v1/proxy-groups/{id}` | 替换、删除 |

导入支持 SS、VMess、VLESS、Trojan、Hysteria2、SOCKS5、HTTP(S)。任一链接解析或整批保存失败时不写入任何节点。读取接口不返回密码、UUID 或完整凭据。

策略组支持 `select`、`url-test`、`fallback`、`load-balance` 和 `chain`。链式组要求至少两个有序节点，生成器用 Mihomo `dialer-proxy` 串联。节点或组仍被其他组、设备策略或订阅结果引用时，DELETE 返回 409 和脱敏引用列表。

`nodes/{id}/probe` 只验证 DNS 解析与 TCP 建连，不代表代理握手、HTTP、DNS、出口 IP、抖动或丢包。

## 服务与 RouterOS 状态

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/v1/routeros/overview` | 资源、接口、DHCP/ARP 设备概览 |
| GET | `/api/v1/routeros/routes` | 路由列表 |
| GET | `/api/v1/routeros/dhcp-servers` | DHCP server 状态 |
| GET | `/api/v1/routeros/containers` | Container 状态 |
| GET | `/api/v1/routeros/l2tp` | L2TP client，只读且不返回密码 |
| GET | `/api/v1/routeros/dhcp/address-plan` | pool/network/lease/address/ARP 容量和冲突 |
| GET | `/api/v1/egress/capabilities` | 五种设备出口的 availability、missing 和证据 |
| GET | `/api/v1/mihomo/overview` | Controller、连接、流量、选择器和延迟 |
| GET | `/api/v1/mosdns/overview` | MosDNS TCP 状态，只读 |
| POST | `/api/v1/mihomo/probes/{nodeId}` | 节点 HTTP 与当前策略出口并行检查 |

RouterOS overview 的资源读取失败会返回 503；接口或设备子读取失败会在 `partialErrors` 中标记。Mihomo probe 把 `nodeHttp` 和 `exit.scope=current-policy` 分开返回，当前出口 IP 不是指定节点的专属出口证明。

## 设备库存与策略

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/v1/devices` | 合并实时 RouterOS 设备与持久资料 |
| PUT | `/api/v1/devices/{mac}` | 保存别名、厂商和标签 |
| GET | `/api/v1/devices/{mac}/history?limit=100` | 首次/最后在线及状态切换 |
| GET | `/api/v1/device-policies` | 已执行策略列表 |
| GET | `/api/v1/device-policies/{id}` | 读取已执行策略 |

出口类型：

- `direct`：移除该设备的 FoxOS 自有出口规则。
- `mihomo-node`：实验性且当前固定不可用；可生成 Mihomo 规则，但没有透明数据平面证据。
- `proxy-chain`：实验性且当前固定不可用；可生成 chain 规则，但没有透明数据平面证据。
- `l2tp`：使用预置 L2TP client 与专用 FoxOS FIB 表。
- `blocked`：在 FoxOS forward 链拒绝该设备。

站点清单中的 RouterOS、Mihomo、MosDNS 和 FoxOS 地址永远不能成为设备策略地址。POST/PUT/DELETE `/device-policies` 返回 405 `egress_workflow_required`，不能绕过 RouterOS 执行器直接写 SQLite。

## RouterOS 计划与执行

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/api/v1/routeros/plans/device-binding` | 生成静态 Lease 计划与令牌 |
| POST | `/api/v1/routeros/plans/device-binding/execute` | 执行、回读、审计与补偿 |
| POST | `/api/v1/routeros/plans/dhcp-expansion` | 预览完整 pool 范围与容量影响 |
| POST | `/api/v1/routeros/plans/dhcp-expansion/execute` | 确认后修改单个 owned pool、回读与补偿 |
| POST | `/api/v1/routeros/plans/egress/{policyId}` | 从已保存或请求中的策略生成出口计划 |
| POST | `/api/v1/routeros/plans/egress/{policyId}/execute` | 提交持久化出口任务 |

设备绑定输入示例：

```json
{
  "id": "device-phone",
  "name": "Phone",
  "macAddress": "AA:BB:CC:DD:EE:FF",
  "staticIp": "10.0.0.21",
  "dhcpServer": "dhcp-lan",
  "egress": "direct"
}
```

plan 响应包含 `plan`、`warnings`、`confirmationToken` 和 `expiresInSeconds`。执行请求必须原样提交计划和令牌。普通动态 Lease 采用使用 RouterOS `make-static` 后再写 owner；未知静态 Lease 不接管。服务端会重新读取 Lease/server/network/pool/address/ARP 前态；计划或摘要变化时返回 409，必须重新预览。新建或换址必须位于对应 DHCP network/接口网段且在动态池外的保留范围。

DHCP 范围首尾计入容量；扩容请求提交目标容量和完整拟议范围，不接受盲目开关。超过单个 `/24` 的需求会返回 `/23` 与 VLAN 拆分提示。

出口执行器不会创建关键基础设施。direct、blocked、l2tp 是否可执行以 capability API 为准：

- 首条活动 jump anchor：`foxos:anchor:mangle` 或 `foxos:anchor:forward`。
- 对应 `foxos-l2tp-{policyId}` FIB 表与已运行目标 L2TP client。
- 没有会绕过策略链的活动 FastTrack。

写入只允许 RouterOS REST allowlist 中的路径与 `foxos:` comment，完成后回读字段；部分失败会反向补偿已执行操作。Mihomo 节点/链即使存在旧 route marker 也保持 `available=false`，直到透明入口、回程、管理旁路、FastTrack 和出口 IP 全部有证据。

## Mihomo 配置闭环

| 方法 | 路径 | 说明 |
|---|---|---|
| GET / PUT | `/api/v1/mihomo/draft` | 读取、保存草稿 |
| POST | `/api/v1/mihomo/config/preview` | 生成 YAML、脱敏 Diff、keyed digest 和确认令牌 |
| POST | `/api/v1/mihomo/config/validate` | 校验提交的 YAML |
| POST | `/api/v1/mihomo/config/apply` | 提交发布任务 |
| GET | `/api/v1/mihomo/snapshots` | 最近 100 个发布快照 |
| POST | `/api/v1/mihomo/snapshots/{id}/restore/plan` | 生成恢复计划 |
| POST | `/api/v1/mihomo/snapshots/{id}/restore` | 提交恢复任务 |

preview 以可信 base YAML 为基线，结构化合并 FoxOS 管理字段并保留 Controller、secret、bind、UI、TUN、DNS 和日志；首次 mixed port 默认 7890。配置 digest 使用确认密钥保护的领域化 HMAC-SHA256，与草稿共同绑定确认令牌，不暴露密码或 UUID 的裸摘要。apply 再次生成并比较 digest，调用真实 Mihomo 二进制校验，持久化操作 journal 后原子替换、热重载、健康检查，再保存 SQLite 快照并按 ID、HMAC digest 和完整 body 回读。只有回读一致才清 journal；保存结果未知时失败关闭并由下次启动对账，不会立即覆盖运行态。明确未提交时才恢复旧文件，且旧文件原子恢复、reload、health 和 digest 回读全部成功后才报告 `ROLLED_BACK`。

草稿规则会与 SQLite 中的节点、代理组和设备策略一起生成。保存节点或组本身不会改变运行配置，必须单独 preview/apply。

## 持久任务

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/v1/jobs/{id}` | 状态、进度、尝试次数和脱敏错误分类 |
| POST | `/api/v1/jobs/{id}/retry` | 仅重试 `FAILED` 或 `ROLLED_BACK` |

任务状态：`QUEUED`、`RUNNING`、`VERIFYING`、`SUCCEEDED`、`FAILED`、`ROLLED_BACK`。创建备份可通过 `Idempotency-Key` 指定幂等键；其他高风险任务使用确认令牌摘要作为幂等键。启动会按 kind 扫描全部可恢复任务，不受列表 API 的 500 条展示上限影响；所有 kind 恢复并注册成功前 worker、监控、调度和 HTTP 监听都不会启动。重启恢复按任务类型和阶段回读，不会统一重新执行中断任务。

## 订阅

| 方法 | 路径 | 说明 |
|---|---|---|
| GET / POST | `/api/v1/subscriptions` | 列表、创建 |
| PUT | `/api/v1/subscriptions/{id}` | 修改名称、URL、启用和周期 |
| DELETE | `/api/v1/subscriptions/{id}` | 始终返回 409，要求确认流程 |
| POST | `/api/v1/subscriptions/{id}/preview` | 抓取、解析、去重并返回变更计划 |
| POST | `/api/v1/subscriptions/{id}/update` | 提交确认后的更新任务 |
| POST | `/api/v1/subscriptions/{id}/delete/plan` | 返回关联节点范围与令牌 |
| POST | `/api/v1/subscriptions/{id}/delete` | 按已签名的 `detach` 或 `cascade` 计划删除 |

抓取仅允许 HTTPS 443 且无 URL 凭据，默认只允许公共 IP；管理员可以用 `FOXOS_SUBSCRIPTION_PRIVATE_CIDRS` 精确开放 RFC1918/ULA 前缀，但 loopback、link-local、multicast、unspecified 和 metadata 类地址始终拒绝。每次连接及重定向重新解析，最多三次重定向、2 MiB、15 秒。失败会保留上一份节点集和错误时间。

## 告警、备份与审计

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/v1/alerts?includeResolved=true` | 活动告警，可选包含已恢复 |
| POST | `/api/v1/alerts/{id}/acknowledge` | 确认告警 |
| GET / POST | `/api/v1/backups` | 列表、创建备份任务 |
| POST | `/api/v1/backups/{id}/restore/plan` | 校验清单并返回影响与令牌 |
| POST | `/api/v1/backups/{id}/restore` | 提交恢复任务 |
| GET | `/api/v1/audit-events?limit=100` | 最近 1 到 500 条审计 |

备份默认包含 SQLite，配置后同时包含 Mihomo YAML；manifest 保存 SHA-256 并默认保留 20 份。恢复不会用旧备份覆盖任务、确认令牌和审计记录。

审计记录操作者模型（`api-token` 或 `browser-session`）、来源 IP、目标、变更摘要、关联任务、结果和错误分类。不会保存 Authorization、Cookie、CSRF、确认令牌、RouterOS/Mihomo 凭据或节点密钥。

## 错误与安全语义

错误响应包含稳定的 `error` 分类与适合 UI 展示的 `message`。常见状态：

- 400：JSON、limit、幂等键或请求格式错误。
- 401：Bearer Token 或浏览器会话缺失、过期或不匹配。
- 403：浏览器写请求的同源或 CSRF 校验失败。
- 426：生产 HTTPS 模式下通过普通 HTTP 访问受保护接口。
- 404：资源不存在。
- 409：计划过期、引用冲突、确认缺失/重放、失败后已回滚。
- 422：领域校验、受保护目标或订阅 URL 被拒绝。
- 502/503：已配置依赖写入、回读或健康检查失败。

客户端不得把 2xx 之外的响应显示为成功，也不得用历史/静态数据替代实时在线状态。
