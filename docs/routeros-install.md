# RouterOS x86_64 部署说明

可直接部署的主入口是 [全量 QUICK-INSTALL](../deploy/routeros/QUICK-INSTALL.md)。本页说明资产选择和边界。

## 目标要求

- RouterOS 7.21+，x86_64 CPU 对应 `architecture-name=x86`。
- 与 RouterOS 完全同版本的 x86 `container` package。
- 由设备操作者在物理控制台确认并启用 `container=yes`。
- 已存在 `bridge-lan` 与 `10.0.0.1/24`。
- 已挂载 `disk1`，上传后至少 512 MiB 可用。
- 管理 IP `10.0.0.2`、`.3`、`.4` 未被占用。

地址和目录不是可随意替换的示例；全量包当前固定使用上述管理面。需要不同拓扑时必须重新审阅脚本，不能直接运行。

## 发布资产

Core CI 每个提交生成 `foxos-full-amd64-<sha>.tar.gz` 与外部 `.sha256`。包内包含三张 RouterOS 本地导入镜像、完整 Mihomo/MosDNS 配置、两阶段安装、升级、回滚、只读预检、计划、说明和内部 `SHA256SUMS`。

镜像已经转换为 RouterOS 兼容的单层、未压缩 Docker v1 tar。不能把 GitHub ZIP、外层 `.tar.gz` 或 OCI layout 直接交给 `/container/add file=`。

## 安装顺序

1. 工作站验证两层 checksum。
2. 上传解压目录内容到 `disk1/` 根。
3. 保存 RouterOS export 与 binary backup。
4. import `disk1/preflight.rsc`，只读。
5. import `disk1/foxos-plan.rsc`，只读。
6. 操作者明确确认精确影响与回滚。
7. import `disk1/foxos-full-install.rsc`。
8. 等三个容器均为 stopped。
9. import `disk1/foxos-start-all.rsc`。
10. 保存首次凭据并完成 live/ready、页面、依赖和测试设备验收。

## 持久数据

| RouterOS 路径 | 容器路径 | 内容 |
|---|---|---|
| `disk1/mihomo-config` | Mihomo `/root/.config/mihomo`、FoxOS `/data/mihomo` | 运行配置 |
| `disk1/mosdns-config` | `/cus/mosdns` | MosDNS 配置 |
| `disk1/foxos-data` | `/data` | SQLite |
| `disk1/foxos-backups` | `/backups` | Mihomo/FoxOS 备份 |

升级只切换 FoxOS root-dir，复用上述数据。不要在新版本验收前删除旧 root-dir、旧镜像或 rollback 槽位。

## DNS 和所有权边界

安装脚本不修改 DNS、DHCP、默认路由、NAT、Mangle 或防火墙。MosDNS 保持只读接入。所有脚本仅复用匹配 `foxos:` comment/marker 的资源，遇到同名用户资源则停止。

当前仓库没有提供自动卸载脚本。卸载或 RouterOS binary restore 都是破坏性操作，必须先列出精确目标、备份和恢复路径，并再次由设备操作者确认。
