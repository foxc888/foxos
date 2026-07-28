# FoxOS 架构

## 运行拓扑

```text
site-config.example.rsc -> 操作者封存的数据清单 -> 不可变 loader -> 管理桥与网段
  ├─ RouterOS REST :80（受限管理 LAN）
  ├─ Mihomo Controller :9090 / mixed :7890
  ├─ MosDNS :53
  └─ FoxOS HTTPS :443；HTTP :80 仅跳转；内部 :8090 仅 loopback
```

FoxOS 通过 RouterOS REST API 读取和执行受限操作，通过 Mihomo Controller 和共享配置挂载发布代理配置，通过 TCP 53 只读检查 MosDNS。后端从环境读取完整站点清单，前端从公开只读 site API 回读实际拓扑。SQLite 保存期望状态、资料、任务、告警、确认重放和审计。

## 分层

| 层 | 目录 | 职责 |
|---|---|---|
| Web | `web/src` | 状态呈现、预览、确认、任务轮询 |
| API | `internal/api` | 管理 Token/Bearer、Cookie 会话、CSRF、输入限制、错误分类 |
| Domain | `internal/domain` | 节点、组、设备、订阅、任务、告警 |
| Adapters | `internal/routeros`、`mihomo`、`mosdns` | 固定边界的外部读写 |
| Services | `internal/task`、`subscription`、`backup`、`alerting` | 长操作、调度、恢复与告警 |
| Storage | `internal/store/sqlite` | 事务、引用、历史、持久任务 |

## Mihomo 发布

1. 读取并校验可信 base YAML，再从 SQLite 读取草稿、节点、组和设备策略。
2. 结构化合并 FoxOS 管理字段，保留 Controller、secret、bind、UI、TUN、DNS 和日志等 base 运行字段。
3. 验证引用、组环、链式 hop、管理地址和协议字段；首次 mixed port 默认为 7890。
4. 生成临时 YAML、由确认密钥保护的 keyed digest 与脱敏 Diff。
5. 用户确认绑定草稿/digest 的计划。
6. 持久任务再次生成并比较 digest，并调用真实 Mihomo 二进制做配置语义校验。
7. 保存快照，原子替换配置，Controller 热重载并检查健康。
8. 失败时用仍可访问的 Controller 恢复旧文件并 reload；回滚失败单独分类。

chain 的 UI 顺序是 RouterOS -> 第 1 跳 -> 第 2 跳 -> ... -> Internet。按 Mihomo `dialer-proxy` 语义，第 N 跳通过第 N-1 跳建立连接，组最终选择最后一跳作为真实出口；两跳和三跳均有 YAML 测试。

## RouterOS 执行

1. 读取实际 Lease、Mangle、Filter、address-list、route、routing-table 与 L2TP。
2. 校验管理面、FastTrack、anchor、FIB 表、网关和所有权。
3. 返回精确 plan、warnings 与五分钟令牌。
4. 执行时重新读取并比较前态。
5. 只通过固定 REST 方法/路径/字段允许列表修改 `foxos:` 资源。
6. 回读每个结果。
7. 失败时逆序补偿已执行操作；审计保存结果和错误分类。

FoxOS 不自动创建影响全网的 anchor、默认路由、NAT、DNS 或 FastTrack 变更。当前 RouterOS Container/Mihomo 透明数据平面未完成入口、回程、管理旁路和真实出口证据，因此 `mihomo-node`、`proxy-chain` readiness 固定不可用。RouterOS 全机 backup 属于部署/升级前置步骤，不由每次 API 操作隐式执行。

## 升级写屏障

升级 coordinator 按 HTTP mutation、完整任务执行、SQLite writer 的顺序关闭准入并排空；快照成功后当前进程和候选进程都保持冻结。GET 与绑定 operation 的 checkpoint/promoted/aborted 控制请求例外可用。候选不会恢复中断任务、Mihomo journal、告警或调度，直到 promoted 审计和终态都耐久落盘；旧版本恢复失败路径同样必须在 live 后完成 aborted/restored，再以 ready 确认服务状态。checkpoint 文件与数据库替换都会在 rename 后同步目录，WAL/SHM 在主库替换前清理；启动会继续收敛 promotion、restore、abort 中间态和确定性审计。RouterOS 每次写 promoted 前都会重新执行运行时联合验收和槽位身份回读；最终验收失败或 promoted 响应无法确认时，只保留新 active 与停止的 rollback 并要求按新摘要幂等重试，避免在可能已解除冻结后盲目回滚并丢写。

## 持久任务

Mihomo apply/restore、出口策略、订阅更新、备份创建/恢复使用 SQLite 任务。任务以幂等键去重，进度和终态可查询。进程启动时先扫描每个 kind 的全部 `QUEUED/RUNNING/VERIFYING`，完成专用恢复后才整体启动 worker；坏任务 JSON、恢复状态写失败或缺少能力都会阻止监听。Mihomo 另用本地 v2 journal 关闭“运行配置已生效但 SQLite 快照未确认”的窗口：派生专用密钥用不同领域分别 HMAC 配置内容和完整 canonical 恢复 envelope，后者覆盖 version、operation、target digest、期望 snapshot label、backup、phase、error 与时间等全部决策字段。重启先认证 envelope，再同时对账 SQLite 快照 label/digest/body 和当前运行配置；精确提交只收尾，提交未知保留 journal 并失败关闭，确认未提交才验证性回滚。其他任务同样读取持久阶段和外部/数据库状态，已完成则收敛为成功，确认仍是精确前态才重排，部分状态要求人工对账。公开错误消息固定脱敏，详细分类进入 `errorClass`。

## 数据与密钥边界

- SQLite：节点密钥、期望配置、设备资料/策略、订阅、任务、告警和审计。
- Mihomo YAML：发布产物，不是唯一事实来源。
- RouterOS：真实网络资源，只允许 FoxOS owner 范围写入。
- 浏览器：管理 Token 只用于同源登录交换；随机会话放在 HttpOnly Cookie，CSRF 仅页面内存。8 小时会话保存在单个 FoxOS 进程内。
- 进程环境：API/确认/RouterOS/Mihomo 凭据；不序列化。
- MosDNS：FoxOS 只读 TCP 53 状态，不写配置；9099 API 仅容器 loopback，发布包不带独立管理 UI。

当前认证模型是单一管理 Token：非浏览器使用 Bearer，Web 使用短期服务端会话；JWT、多用户/RBAC 和跨实例会话尚未实现。
