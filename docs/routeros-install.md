# RouterOS x86_64 部署说明

可直接部署的主入口是 [全量 QUICK-INSTALL](../deploy/routeros/QUICK-INSTALL.md)。本页说明资产选择和边界。

## 目标要求

- RouterOS 7.21 是脚本语法下限；官方 amd64 环境通常为 `architecture-name=x86`，非标准目标返回的 `x86_64` 也会归一到 Linux `amd64`，但仍需单独验收；目标完整版本必须先通过同版本 CHR 的 `envlists`/`mountlists`、五个命名挂载 `mode=rw`、唯一 `foxos-secrets` 挂载 `mode=ro`、四文件可读、敏感 env 零绑定、管理容器 `logging=no` 和 add/get/delete 零残留门禁。
- 与 RouterOS 完全同版本的 x86 `container` package。
- 由设备操作者按 MikroTik 官方流程确认并启用 `container=yes` 与 `scheduler=yes`；两项 device-mode 更新都可能要求物理确认，FoxOS 脚本不会代为修改。
- 已从不可变 `site-config.example.rsc` 生成、审核并独立封存 `site-config.rsc`；其中管理桥和 RouterOS 地址已存在且唯一。外置模式填写唯一 `/disk` 槽位；无 `/disk` 对象的 x86 系统盘模式必须精确填写保留根 `foxos`。
- 清单存储在上传后至少有 512 MiB 可用。
- 清单中的 Mihomo、MosDNS、FoxOS 地址未被占用。

默认地址只是站点清单样例。不得在其他脚本中单独改值；预检、安装、升级、后端和前端均读取同一清单。

## 发布资产

Core CI 每个提交生成 `foxos-full-amd64-<sha>.tar.gz` 与外部 `.sha256`。包内包含三张 RouterOS 本地导入镜像、组件 provenance lock、完整 Mihomo/MosDNS 配置、不可变站点模板与封存工具、可丢弃 CHR `envlists` smoke、严格只读 host doctor、唯一 full-install 入口、版本化升级、回滚、确认式清理/卸载、只读检查和内部 `SHA256SUMS`。可编辑 `site-config.rsc`、密钥示例和旧 installer 不在包内。

镜像已经转换为 RouterOS 兼容的单层、未压缩 Docker v1 tar。不能把 GitHub ZIP、外层 `.tar.gz` 或 OCI layout 直接交给 `/container/add file=`。

FoxOS、Mihomo、MosDNS 三张 amd64 输入镜像在 workflow 内从固定源码构建并分别通过运行契约与 Trivy 后才组包；仓库不携带预制运行时 tar。工作站除校验两层 checksum 外，还应审核 `provenance/*.lock.json` 与目标提交的 Dockerfile。

## 安装顺序

1. 工作站验证两层 checksum；复制模板为 `site-config.rsc`，编辑后运行 `seal-site-config.sh` 生成独立 `.sha512`。
2. 目标精确版本先在可丢弃 CHR 上运行 `chr-envlists-smoke.rsc`，确认基础 env/mount 契约并取得零残留 PASS；再用同 SHA Artifact 验证完整 secret、只读挂载、日志和生命周期契约。
3. 实体目标接收任何 FoxOS 文件前，保存脱敏 RouterOS export 和带唯一离线密码、`aes-sha256` 的 binary backup，并把两个文件下载到离线位置。
4. 逐项确认所有顶层上传目标计数为零后，才上传解压目录、站点清单及其摘要；随后 import 包内固定的 `load-site-config.rsc`，由它校验清单摘要和赋值白名单，再运行 `foxos-doctor.rsc`。doctor 只读输出主机前置项和首装冲突，不修改 device-mode、桥、地址、磁盘或 REST。
5. 运行 `foxos-plan.rsc`；plan 自动执行 preflight 与共享 inspector，只读输出逐项 `CREATE/REUSE/FAIL` 和摘要。不得直接 import 可编辑的 `site-config.rsc`。
6. 操作者核对影响、备份和回滚路径，把计划摘要原样设置为确认值。
7. 从清单存储根 import 唯一正式入口 `<storage>/foxos-full-install.rsc`；它在首次写入前重新回读并拒绝过期计划。
8. 等三个容器均为 stopped，再运行可重入 `foxos-start-all.rsc`；此时 autostart 仍关闭。
9. 导入并信任生成的本地 CA，运行 `foxos-verify.rsc`；全部健康门禁通过后才启用 owned 顺序启动 scheduler。三个容器始终保持 `start-on-boot=no`。
10. 通过 WinBox Files 或工作站 SCP 安全下载 `<storage>/foxos-secrets/api-token` 到权限 `0600` 的临时文件，从文件导入离线密码库后销毁临时副本，再登录 HTTPS 管理页；禁止从 env 或终端输出秘密值。
11. 需要 hostname 时单独执行 DNS plan、精确确认和 apply；不启用或接管 DNS/DHCP。
12. 完成 live/ready、页面、依赖和测试设备实体验收。

## 持久数据

| RouterOS 路径 | 容器路径 | 内容 |
|---|---|---|
| `<storage>/mihomo-config` | Mihomo `/root/.config/mihomo`、FoxOS `/data/mihomo` | base 与运行配置 |
| `<storage>/mosdns-config` | `/cus/mosdns` | MosDNS 配置 |
| `<storage>/foxos-data` | `/data` | SQLite、升级状态、本地 CA/TLS |
| `<storage>/foxos-backups` | `/backups` | Mihomo/FoxOS/升级检查点备份 |
| `<storage>/foxos-secrets` | `/run/secrets/foxos` | `api-token`、`confirmation-key`、`routeros-password`、`mihomo-secret`；仅 FoxOS 管理容器只读绑定 |

升级使用构建时绑定 release ID 的版本化 FoxOS root-dir，复用上述数据、四个 secret files 和唯一只读 secret mount；升级 payload 不包含也不轮换秘密。不要在新版本验收和回滚演练前归档 rollback；归档只把旧槽标记为 retained，不删除 root-dir、镜像或检查点。

## DNS 和所有权边界

安装脚本不修改 DNS、DHCP、默认路由、NAT、Mangle、FastTrack 或防火墙，也不创建透明代理。MosDNS 保持只读接入。可选 DNS 脚本只添加一条精确确认的 owned A 记录。所有脚本仅复用匹配 `foxos:` comment/marker 的资源，遇到同名用户资源则停止。

FoxOS 管理容器不持有敏感 env；四项秘密只通过唯一的 `foxos-secrets` 只读挂载进入 `/run/secrets/foxos`，这是生产秘密边界。其 RouterOS `logging` 仍必须保持 `no`，但只作为纵深防护；验收时如果发现任何敏感键名或值进入新日志，立即停止容器并轮换本次全部凭据。Mihomo 与 MosDNS 不继承 `foxos-env`，仍保留运行日志用于诊断。

`uninstall-plan.rsc` 只读列出精确 owned 资源并生成 SHA-512；存在 Mihomo pending apply journal 时拒绝生成卸载摘要。`uninstall-apply.rsc` 仅在摘要确认且前态未变化时停止资源，并在所有容器停止后再次确认 journal 不存在，才删除 RouterOS 资源、四个 secret files、空的 secret 目录和唯一只读 secret mount。数据、镜像、配置、版本化 root-dir、备份、站点清单和本地 CA 仍保留；这些内容仍绑定原四项秘密，复用时必须从验证过的离线备份恢复原四文件集合，不能只恢复 confirmation key。全新安装前则先验证备份并把旧数据与备份归档到非活动路径。RouterOS binary restore 仍是会重启并覆盖设备配置的独立破坏性操作，只能在维护窗口再次确认。
