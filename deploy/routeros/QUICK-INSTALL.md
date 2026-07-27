# FoxOS 全栈快速安装（RouterOS x86_64）

本包用于备用 RouterOS 或 CHR 验收。仓库已完成自动化、浏览器、静态脚本和 Linux 网络命名空间验证，但尚未在实体 RouterOS 执行；容器 `running`、CI 绿色或模拟连通都不能记为实机部署成功。

## 0. 先审核站点清单

`site-config.rsc` 是 RouterOS 部署的唯一拓扑来源。默认值如下：

| 项目 | 默认值 |
|---|---|
| 管理桥 | `bridge-lan` |
| 存储根 | `disk1` |
| 管理网段 | `10.0.0.0/24` |
| RouterOS | `10.0.0.1` |
| Mihomo | `10.0.0.2` |
| MosDNS | `10.0.0.3` |
| FoxOS | `10.0.0.4` |
| 管理主机名 | `foxos.home.arpa` |

上传前编辑该文件；四个服务地址必须唯一、可用且位于管理网段内，RouterOS 地址必须以清单中的 prefix 配置在管理桥上。不要在其他脚本中改地址。每次运行 preflight、plan、install、DNS、upgrade、rollback 或 verify 前都重新 import 已审核的 `site-config.rsc`，因为这些脚本会拒绝缺失或版本不匹配的全局清单。

下文命令使用默认 `disk1`。如果修改了存储根，所有 `file-name=disk1/...` 都替换为实际目录。

x86_64 CPU 在 RouterOS 中的 `architecture-name` 是 `x86`；容器镜像架构是 Linux `amd64`。

## 安全边界

安装器只创建或复用匹配 FoxOS owner 的服务用户、env、mount、三个 veth/bridge port 和三个容器。它不会：

- 创建或接管管理桥、磁盘、RouterOS 管理地址或 REST 服务。
- 启用/修改 RouterOS DNS、DHCP server/network、DHCP 下发 DNS。
- 修改默认路由、NAT、Mangle、Filter、FastTrack 或用户防火墙。
- 建立 Mihomo TUN/redir/透明代理数据平面。
- 覆盖没有 `foxos:` marker/comment 的同名资源。

FoxOS 只检查 MosDNS TCP 53；MosDNS 9099 API 仅监听容器 loopback，包内不提供其独立管理 UI。

可选 DNS 脚本只在精确确认后添加一条 `foxos:dns:admin` A 记录，不启用 DNS，不改 upstream，不改 DHCP。只有原本使用 RouterOS DNS 的客户端才会得到该记录。

## 1. RouterOS 前置条件

必须全部满足：

- RouterOS 7.21 或更高，`architecture-name=x86`。
- 安装且启用与 RouterOS 完全同版本的 x86 `container` package。
- `/system/device-mode get container` 为 `yes`；物理确认由设备操作者按 MikroTik 官方流程单独完成。
- 清单指定的管理桥、RouterOS 地址和持久存储已存在且唯一。
- 上传完整包后存储仍至少有 512 MiB 可用。
- RouterOS `www`/REST 已启用在 TCP 80，并限制为清单网段或 FoxOS `/32`。
- Mihomo、MosDNS、FoxOS 三个保留地址未被 RouterOS address、DHCP Lease、其他 veth、ARP 或在线主机占用。

安装器会创建最小 `foxos-service` 账号。RouterOS REST 在管理 LAN 内仍是 HTTP，因此管理 LAN 必须可信且隔离；不得暴露到 WAN。FoxOS 浏览器/API 访问则强制使用本地 CA 保护的 HTTPS。

只读人工检查示例：

```routeros
/system/resource/print
/system/package/print where name="container"
/system/device-mode/print
/interface/bridge/print
/disk/print
/ip/address/print
/ip/service/print where name="www"
/container/print
```

## 2. 下载并验证

在 GitHub Actions 的绿色 `FoxOS Core CI` 运行底部下载 `foxos-full-amd64-<commit>`。解压 Actions ZIP 后得到 tarball 和外层 checksum：

```bash
sha256sum --check foxos-full-amd64-<commit>.tar.gz.sha256
tar -xzf foxos-full-amd64-<commit>.tar.gz
cd foxos-full-amd64-<commit>
sha256sum --check SHA256SUMS
```

macOS 使用 `shasum -a 256 -c`。三个镜像 tar 是 RouterOS 所需的单层、未压缩 Docker v1 archive，不要继续解压或转换。RouterOS preflight 只检查文件存在性和最小大小，密码学 checksum 必须在上传前由工作站验证。

## 3. 上传并保存回滚点

把解压目录中的全部内容上传到站点存储根，不要多套一层 release 目录。至少应有：

```text
disk1/site-config.rsc
disk1/foxos-amd64.tar
disk1/mihomo_amd64.tar
disk1/mosdns-amd64.tar
disk1/mihomo-config/
disk1/mosdns-config/
disk1/preflight.rsc
disk1/foxos-plan.rsc
disk1/foxos-full-install.rsc
disk1/foxos-start-all.rsc
disk1/foxos-verify.rsc
disk1/foxos-dns-plan.rsc
disk1/foxos-dns-apply.rsc
disk1/upgrade.rsc
disk1/upgrade-promote.rsc
disk1/rollback.rsc
disk1/SHA256SUMS
disk1/RELEASE-MANIFEST.txt
```

在任何写入前执行：

```routeros
/export hide-sensitive file=before-foxos
/system/backup/save name=before-foxos
```

确认两个文件存在并下载副本。RouterOS binary restore 会重启并覆盖设备配置，只能在维护窗口再次明确确认后执行。

## 4. 只读预检与精确计划

```routeros
/import file-name=disk1/site-config.rsc
/import file-name=disk1/preflight.rsc
/import file-name=disk1/site-config.rsc
/import file-name=disk1/foxos-plan.rsc
```

preflight 必须以 `PRECHECK PASSED` 结束。计划会列出站点地址、25 键 FoxOS env 安全基线、mount、veth、bridge port、容器、Mihomo secret 文件变更、不触碰项和回滚路径。到此没有写入。

若设备、清单、备份、计划或回滚路径有任何不确定，不执行下一节。

## 5. 创建资源并导入镜像

操作者确认精确计划后：

```routeros
/import file-name=disk1/site-config.rsc
/import file-name=disk1/foxos-full-install.rsc
```

首次执行随机生成 RouterOS 服务密码、Mihomo Controller Secret、FoxOS API Token 和 confirmation key。重复执行复用有效凭据；缺失的允许项可补齐，未知额外 env、固定值不匹配或同名非 FoxOS 资源会失败关闭。

`/container/add file=...` 异步导入。等待三个容器全部 `status=stopped`：

```routeros
/container/print
/log/print where topics~"container"
```

导入期间不要重启。

## 6. 启动、导入 CA、自动验证

```routeros
/import file-name=disk1/site-config.rsc
/import file-name=disk1/foxos-start-all.rsc
```

启动脚本按 Mihomo、MosDNS、FoxOS 顺序启动，只验证容器进入 `running`，并显示一次安装凭据。立即保存到离线密码库；不要截图、记录 env values 或粘贴到聊天。

FoxOS 首次启动在 `foxos-data/tls` 生成持久 ECDSA 本地 CA 和 397 天叶证书。先验证包来源和存储路径，再导入 CA：

```routeros
/certificate/import file-name=disk1/foxos-data/tls/foxos-local-ca.pem passphrase=""
/certificate/print detail where common-name="FoxOS Local CA"
```

确认只有一张预期 CA，核对 SHA-256 指纹后设为 trusted：

```routeros
/certificate/set [find where common-name="FoxOS Local CA"] trusted=yes
/import file-name=disk1/site-config.rsc
/import file-name=disk1/foxos-verify.rsc
```

`foxos-verify.rsc` 必须同时通过容器 running、live、ready、站点清单、页面和带认证只读 API。它使用 HTTPS 且不会跳过证书验证。该结果只证明 RouterOS 到 FoxOS 管理面，不证明设备流量经过 Mihomo。

## 7. 可选本地 DNS 与无端口访问

先运行只读计划：

```routeros
/import file-name=disk1/site-config.rsc
/import file-name=disk1/foxos-dns-plan.rsc
```

若计划显示要新增记录，按其输出设置完全一致的确认字符串，再执行：

```routeros
:global FoxOSDNSConfirmation "ADD foxos.home.arpa 10.0.0.4"
/import file-name=disk1/site-config.rsc
/import file-name=disk1/foxos-dns-apply.rsc
```

自定义站点必须使用计划输出中的实际 hostname 和地址，不能照抄默认确认串。已有同名非 FoxOS 记录或内容不同会失败关闭。

CA 已导入客户端信任库且 DNS 可解析后，访问 `https://foxos.home.arpa`。DNS 尚未配置时可用证书包含的 IP SAN 访问 `https://<FoxOS 地址>`。`http://<FoxOS 地址>` 只返回到 public hostname 的 308 跳转，不承载 Bearer Token。

## 8. 实体 RouterOS 验收清单

以下项目都要保存设备输出、API 回读和网络证据；本仓库当前尚未完成：

1. 三个容器 owner 唯一，运行状态与 start-on-boot 分别符合计划。
2. HTTPS CA、live、ready、页面、站点清单和只读依赖全部通过。
3. RouterOS、Mihomo、MosDNS 各自显示实时来源；单项失败不伪造在线。
4. DNS、DHCP network、默认路由、NAT、Mangle、Filter 和 FastTrack 与安装前 diff 符合“不接管”边界。
5. 使用可恢复测试设备完成动态 Lease 采用、确认、make-static、回读、审计和外部并发变更拒绝。
6. 验证 DHCP 容量口径：范围首尾包含，`.100-.200=101`、`.10-.254=245`，只有范围内排除一个保留地址才是 244。
7. 仅在 readiness 可用时验证 `blocked` 和 `l2tp`；主设备不得作为首个测试对象。
8. `mihomo-node` 和 `proxy-chain` 当前应返回不可用。不得用路由 marker、Controller 在线或 mixed port 出口替代透明入口、回程、管理旁路、FastTrack 和真实客户端出口 IP 证据。
9. 在维护窗口演练 pending 升级门禁、自动恢复和 SQLite 兼容回滚。
10. 下载并验证 RouterOS binary backup、FoxOS manifest 备份和旧镜像/root-dir 的恢复路径。

没有上述证据时，不得记录“实体部署成功”或“真实透明代理已启用”。

## 9. 清理与首次安装回滚

完成实体验收前不要删除上传镜像、旧 root-dir、`foxos-data`、`foxos-backups` 或 RouterOS backup。首次安装没有容器 rollback 槽；失败时先停止三个精确 owner 的容器：

```routeros
/container/stop [find where comment="foxos:active"]
/container/stop [find where comment="foxos:mosdns"]
/container/stop [find where comment="foxos:mihomo"]
```

然后按审核过的维护窗口恢复 `before-foxos.backup`。不要手工批量删除未知用户资源。

## 常见故障

| 现象 | 检查 |
|---|---|
| 脚本要求站点清单 | 每个操作前是否重新 import 正确存储根下的 `site-config.rsc` |
| `bad command name container` | 同版本 x86 container package 是否安装并重启 |
| `not allowed by device-mode` | `container=yes` 是否完成物理确认 |
| preflight 地址/磁盘失败 | `site-config.rsc` 是否与现有管理桥、地址和存储完全一致 |
| 镜像长期不为 stopped | checksum、文件大小、磁盘、package、container 日志 |
| FoxOS running 但 verify 失败 | CA 是否唯一且 trusted、HTTPS 443、SQLite/挂载、RouterOS REST、Mihomo 9090、MosDNS TCP 53 |
| hostname 不解析 | 客户端是否原本使用 RouterOS DNS；DNS plan/apply 是否完成 |
| API 返回 `https_required` | 是否仍在用普通 LAN HTTP 或直接访问内部 8090 |
| Mihomo 设备出口不可选 | 当前预期行为；查看 capability `missing`，不要伪造透明数据平面 |

升级和应用回滚见 [发布、升级与回滚](../../docs/release-and-routeros.md)。
