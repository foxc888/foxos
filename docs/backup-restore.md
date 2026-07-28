# 备份与恢复

FoxOS 管理备份覆盖 SQLite 数据和可选 Mihomo 配置。RouterOS binary backup/export 仍由部署操作者在设备上单独完成，不能被 FoxOS 文件备份替代。

备份 manifest 对文件使用 SHA-256 完整性校验；Mihomo 发布快照的对外 digest 则使用 `FOXOS_CONFIRMATION_KEY` 保护的领域化 HMAC-SHA256。轮换确认密钥或从使用裸 SHA-256 digest 的早期 Alpha 升级后，旧发布快照会失败关闭，不能直接恢复；先保留原配置文件，在维护窗口重新预览并发布以生成 keyed 快照。普通 SQLite/Mihomo 文件备份仍按 manifest 流程验证和恢复。

## 创建与保留

`POST /api/v1/backups` 提交持久任务。备份服务：

1. 校验 label，创建独立临时目录。
2. 使用 SQLite 在线备份能力生成一致数据库副本。
3. 若已配置 Mihomo 路径，同时复制当前配置。
4. 为每个文件计算 SHA-256 并写 manifest。
5. 原子发布备份目录。
6. 默认只保留最近 20 份，清理更旧的完整备份。

调用方可以提供最长 128 字符的 `Idempotency-Key`，避免重复创建。

## 恢复预览

`POST /api/v1/backups/{id}/restore/plan` 会先验证目录、manifest、文件数量和 SHA-256，返回：

- backup ID、digest、文件数量。
- 是否包含 Mihomo。
- 将被替换的节点、代理组、设备策略、订阅和告警状态提示。
- 五分钟单次确认令牌。

任务、确认重放记录和审计不会被旧数据库内容覆盖。

## 恢复执行

确认后提交 `POST /api/v1/backups/{id}/restore`。任务：

1. 再次检查 manifest 与 digest。
2. 在替换前创建当前 SQLite 的恢复点。
3. 恢复并执行 SQLite integrity check。
4. 如包含 Mihomo，执行校验、原子替换、热重载和健康检查。
5. 任一步失败时恢复操作前 SQLite，并由 Mihomo applier 恢复旧配置。
6. 写入 `SUCCEEDED`、`FAILED` 或 `ROLLED_BACK` 任务结果及审计。

恢复任务持久记录 operation ID、backup ID、manifest digest、阶段和操作前回滚点。进程在“外部文件/数据库已替换但任务终态尚未保存”处中断时，会检查 operation marker、目标 digest、SQLite integrity 和回滚点：完整目标收敛为成功，仍是可重试前态才重排，已回滚收敛为 `ROLLED_BACK`，部分或外部变化状态失败关闭。不会把所有 `RUNNING/VERIFYING` 直接重新执行。

备份创建同样使用 operation ID 命名发布结果；恢复时若已存在完整且 manifest 可验证的结果，任务直接收敛成功，不再生成第二份备份。

## RouterOS 部署备份

运行安装或升级脚本前至少保存：

```routeros
/export hide-sensitive file=before-foxos
/system/backup/save name=before-foxos password="<unique-offline-password>" encryption=aes-sha256
```

把占位符替换为唯一离线密码；RouterOS 官方语义中未提供密码的 v7 binary backup 不会加密。确认 `.rsc` 与 `.backup` 都存在，下载到离线位置并验证可读性。binary restore 会重启并覆盖设备配置，只能在同版本 RouterOS、同一设备的维护窗口再次确认后执行。

同时保留旧 FoxOS 镜像/root-dir、站点存储中的 `foxos-data`、本地 CA 和最近可用 FoxOS 备份。升级 promote 前旧版本会先排空写入再创建 SQLite schema 兼容检查点；旧二进制回滚启动时在打开数据库前校验并按需恢复。旧槽的 ready 只证明依赖可用，必须再取得同一 operation 的 `aborted` 或 `restored` 响应，才能确认升级写冻结已解除。不要归档 rollback 槽位或处理检查点，直到新版本完成 live/ready、页面、只读依赖、一台测试设备验收和回滚演练。
