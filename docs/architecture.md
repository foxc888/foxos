# FoxOS 架构

## 运行拓扑

```text
RouterOS 10.0.0.1
  └─ bridge-lan
     ├─ Mihomo 10.0.0.2:9090 / :7890
     ├─ MosDNS 10.0.0.3:53
     └─ FoxOS 10.0.0.4:8090
```

FoxOS 通过 RouterOS REST API 读取和执行受限操作，通过 Mihomo Controller 和共享配置挂载发布代理配置，通过 TCP 53 只读检查 MosDNS。SQLite 保存期望状态、资料、任务、告警、确认重放和审计。

## 分层

| 层 | 目录 | 职责 |
|---|---|---|
| Web | `web/src` | 状态呈现、预览、确认、任务轮询 |
| API | `internal/api` | Bearer 认证、输入限制、错误分类 |
| Domain | `internal/domain` | 节点、组、设备、订阅、任务、告警 |
| Adapters | `internal/routeros`、`mihomo`、`mosdns` | 固定边界的外部读写 |
| Services | `internal/task`、`subscription`、`backup`、`alerting` | 长操作、调度、恢复与告警 |
| Storage | `internal/store/sqlite` | 事务、引用、历史、持久任务 |

## Mihomo 发布

1. 从 SQLite 读取草稿、节点、组和设备策略。
2. 验证引用、组环、链式 hop、管理地址和协议字段。
3. 生成临时 YAML、digest 与脱敏 Diff。
4. 用户确认绑定草稿/digest 的计划。
5. 持久任务再次生成并比较 digest。
6. 校验 YAML，保存快照，原子替换配置。
7. Controller 热重载并检查健康。
8. 失败时恢复旧文件和 reload；任务标记 FAILED 或 ROLLED_BACK。

## RouterOS 执行

1. 读取实际 Lease、Mangle、Filter、address-list、route、routing-table 与 L2TP。
2. 校验管理面、FastTrack、anchor、FIB 表、网关和所有权。
3. 返回精确 plan、warnings 与五分钟令牌。
4. 执行时重新读取并比较前态。
5. 只通过固定 REST 方法/路径/字段允许列表修改 `foxos:` 资源。
6. 回读每个结果。
7. 失败时逆序补偿已执行操作；审计保存结果和错误分类。

FoxOS 不自动创建影响全网的 anchor、默认路由、NAT、DNS 或 FastTrack 变更。RouterOS 全机 backup 属于部署/升级前置步骤，不由每次 API 操作隐式执行。

## 持久任务

Mihomo apply/restore、出口策略、订阅更新、备份创建/恢复使用 SQLite 任务。任务以幂等键去重，进度和终态可查询；进程启动时恢复未完成状态并重新排队已注册类型。公开错误消息固定脱敏，详细分类进入 `errorClass`。

## 数据与密钥边界

- SQLite：节点密钥、期望配置、设备资料/策略、订阅、任务、告警和审计。
- Mihomo YAML：发布产物，不是唯一事实来源。
- RouterOS：真实网络资源，只允许 FoxOS owner 范围写入。
- 浏览器：API Token 仅当前页面内存。
- 进程环境：API/确认/RouterOS/Mihomo 凭据；不序列化。
- MosDNS：只读 TCP 状态，不写配置。

当前认证模型是单一 Bearer Token；多用户/RBAC 尚未实现。
