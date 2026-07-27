# RouterOS x86_64 部署说明

可直接部署的主入口是 [全量 QUICK-INSTALL](../deploy/routeros/QUICK-INSTALL.md)。本页说明资产选择和边界。

## 目标要求

- RouterOS 7.21+，x86_64 CPU 对应 `architecture-name=x86`。
- 与 RouterOS 完全同版本的 x86 `container` package。
- 由设备操作者在物理控制台确认并启用 `container=yes`。
- 已审核 `site-config.rsc`；其中管理桥、RouterOS 地址和存储已存在且唯一。
- 清单存储在上传后至少有 512 MiB 可用。
- 清单中的 Mihomo、MosDNS、FoxOS 地址未被占用。

默认地址只是站点清单样例。不得在其他脚本中单独改值；预检、安装、升级、后端和前端均读取同一清单。

## 发布资产

Core CI 每个提交生成 `foxos-full-amd64-<sha>.tar.gz` 与外部 `.sha256`。包内包含三张 RouterOS 本地导入镜像、完整 Mihomo/MosDNS 配置、两阶段安装、升级、回滚、只读预检、计划、说明和内部 `SHA256SUMS`。

镜像已经转换为 RouterOS 兼容的单层、未压缩 Docker v1 tar。不能把 GitHub ZIP、外层 `.tar.gz` 或 OCI layout 直接交给 `/container/add file=`。

## 安装顺序

1. 工作站验证两层 checksum。
2. 编辑并审核 `site-config.rsc`，上传解压目录内容到清单存储根。
3. 保存 RouterOS export 与 binary backup。
4. 每个步骤先 import `site-config.rsc`，再 import `preflight.rsc`，只读。
5. 重新加载清单，再 import `foxos-plan.rsc`，只读。
6. 操作者明确确认精确影响与回滚。
7. import `disk1/foxos-full-install.rsc`。
8. 等三个容器均为 stopped。
9. import `disk1/foxos-start-all.rsc`。
10. 保存首次凭据，导入并信任生成的本地 CA，运行 `foxos-verify.rsc`。
11. 需要 hostname 时单独执行 DNS plan、精确确认和 apply；不启用或接管 DNS/DHCP。
12. 完成 live/ready、页面、依赖和测试设备实体验收。

## 持久数据

| RouterOS 路径 | 容器路径 | 内容 |
|---|---|---|
| `<storage>/mihomo-config` | Mihomo `/root/.config/mihomo`、FoxOS `/data/mihomo` | base 与运行配置 |
| `<storage>/mosdns-config` | `/cus/mosdns` | MosDNS 配置 |
| `<storage>/foxos-data` | `/data` | SQLite、升级状态、本地 CA/TLS |
| `<storage>/foxos-backups` | `/backups` | Mihomo/FoxOS/升级检查点备份 |

升级只切换 FoxOS root-dir，复用上述数据。不要在新版本验收前删除旧 root-dir、旧镜像或 rollback 槽位。

## DNS 和所有权边界

安装脚本不修改 DNS、DHCP、默认路由、NAT、Mangle、FastTrack 或防火墙，也不创建透明代理。MosDNS 保持只读接入。可选 DNS 脚本只添加一条精确确认的 owned A 记录。所有脚本仅复用匹配 `foxos:` comment/marker 的资源，遇到同名用户资源则停止。

当前仓库没有提供自动卸载脚本。卸载或 RouterOS binary restore 都是破坏性操作，必须先列出精确目标、备份和恢复路径，并再次由设备操作者确认。
