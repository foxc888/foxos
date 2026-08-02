# 审计与告警

## 审计

高风险操作在执行前写 `STARTED`，完成后更新为 `SUCCEEDED` 或 `FAILED`。任务自身还会区分 `ROLLED_BACK`。

记录内容包括：

- actor（`api-token`、`browser-session` 或未认证安全事件）与请求来源 IP。
- 动作、目标、RouterOS 方法/路径摘要。
- 设备策略 before/after、订阅增删改数量、备份/Mihomo digest。
- 脱敏且最大 64 KiB 的 Mihomo Diff。
- job ID、结果、rolledBack 和稳定 errorClass。

不保存 Authorization、Cookie、CSRF、确认令牌、RouterOS/Mihomo 密钥、节点密码/UUID、订阅原始正文或完整分享链接。

`GET /api/v1/audit-events?limit=100` 接受 1 到 500。

## 告警

后台每分钟评估一次；依赖读取失败会单独形成 telemetry 告警，不把未知状态推断为业务故障。

覆盖：

- 连续两次没有活动默认路由。
- FoxOS 所有权容器连续两次非 running。
- RouterOS 磁盘少于 512 MiB/10%，严重阈值为 128 MiB/5%。
- Mihomo 节点连续三次 `alive=false`。
- MosDNS 连续两次 TCP 状态失败。
- Mihomo、出口、备份恢复、订阅任务失败或自动回滚。
- 启用订阅超过更新周期。

告警可确认，条件恢复后记录 resolved 时间。读取接口失败不会删除或伪造已有告警。
