# 备份与恢复

FoxOS 管理备份覆盖 SQLite 数据和可选 Mihomo 配置。RouterOS binary backup/export 仍由部署操作者在设备上单独完成，不能被 FoxOS 文件备份替代。

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

## RouterOS 部署备份

运行安装或升级脚本前至少保存：

```routeros
/export hide-sensitive file=before-foxos
/system/backup/save name=before-foxos
```

同时保留旧 FoxOS 镜像/root-dir、`disk1/foxos-data` 和最近可用 FoxOS 备份。真实恢复前先在隔离环境验证版本兼容性；不要删除 rollback 槽位直到新版本完成 live/ready、状态读取和一台测试设备验收。
