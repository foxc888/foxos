# 操作计划与确认

所有可能改变 RouterOS、Mihomo、订阅节点集或备份恢复状态的操作，都使用“预览/计划 -> 明确确认 -> 执行 -> 验证 -> 审计”。

## 令牌约束

- HMAC-SHA256 使用 `FOXOS_CONFIRMATION_KEY`。
- 令牌绑定规范化后的完整计划，而不是按钮名称或资源 ID。
- 默认五分钟过期。
- 服务端持久记录令牌摘要，成功消费后不能重放。
- 计划、资源前态或依赖摘要改变时必须重新生成。
- API Token 与确认密钥必须不同。

## 前端确认

危险 Dialog 展示：

- 操作目标与当前/目标状态。
- 精确方法、路径、owner comment 或文件范围。
- 受影响设备、节点、订阅或快照数量。
- 前置警告、失败补偿和回滚结果。

确认按钮在影响范围被明确勾选前不可用。Dialog 具备语义标题、焦点锁定、Escape 关闭和焦点恢复；执行期间防止重复提交。

## RouterOS 执行

静态 Lease 和出口策略都连接真实 RouterOS REST Writer。Writer 在三层拒绝越界：

1. 计划器只产生允许的资源与字段。
2. 执行器再次比较实时状态、前态摘要和 owner。
3. Writer 对 HTTP 方法、REST 路径、RouterOS ID、字段和 `foxos:` comment 使用允许列表。

写后回读验证。中途失败按已执行操作逆序补偿；补偿失败与成功回滚使用不同错误分类。API 和审计不会把“请求已发送”当作成功。

## Mihomo、订阅和恢复

- Mihomo apply 令牌绑定草稿和生成配置 digest。
- Mihomo snapshot restore 令牌绑定 snapshot ID。
- 订阅 update 绑定抓取 digest、增删改数量和删除节点 ID。
- 订阅 delete 绑定关联节点完整列表。
- 备份 restore 绑定 backup ID、manifest digest、文件数和 Mihomo 是否存在。

这些操作由持久任务执行。前端轮询任务直到终态。重启时不会把所有 `RUNNING/VERIFYING` 统一改回队列：Mihomo 按配置 digest/快照回读，RouterOS 出口对照真实规则与数据库策略，订阅对照内容和节点集 digest，备份创建对照已发布 manifest，备份恢复对照 operation marker/回滚点。只有外部和数据库仍是可安全重试的精确前态才重新排队；已写成功则收敛成功，部分状态失败关闭。

## 审计

审计记录 actor、来源 IP、动作、目标、脱敏 Diff/变更列表、关联任务、结果、回滚标志和错误分类。确认令牌、Authorization、密码、Controller Secret、节点 UUID/密码不会写入审计。
