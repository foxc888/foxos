# FoxOS 全栈快速安装（RouterOS x86_64）

本包固定用于：

| 组件 | 地址 | RouterOS 资源 |
|---|---|---|
| RouterOS | `10.0.0.1/24` | 已存在于 `bridge-lan` |
| Mihomo | `10.0.0.2/24` | `veth-mihomo` / `foxos:mihomo` |
| MosDNS | `10.0.0.3/24` | `veth-mosdns` / `foxos:mosdns` |
| FoxOS | `10.0.0.4/24:8090` | `veth-foxos` / `foxos:active` |
| 存储 | `disk1` | 镜像、配置、root-dir、数据和备份 |

x86_64 CPU 在 RouterOS 中的 `architecture-name` 是 `x86`；容器镜像架构是 Linux `amd64`。

> 当前资产已通过静态检查、自动化测试和模拟组包，尚未在真实 RouterOS 上执行。任何真实写入前必须先保存备份、审阅精确计划，并由操作者明确确认。

## 安全边界

安装器不会新增或修改：

- RouterOS DNS、DHCP server/network 或 DHCP 下发 DNS。
- 默认路由、NAT、Mangle、Filter 或用户防火墙。
- Mihomo DNS、MosDNS DNS 规则或全 LAN 代理。
- 没有 FoxOS marker/comment 的同名用户资源。

安装器只创建或复用 `foxos-service`、`foxos-env`、`foxos-*` mounts、三个 veth/bridge port 和三个容器；只改包内 `mihomo-config/config.yaml` 的顶层 `secret` 字段。

## 1. RouterOS 前置条件

必须全部满足：

- RouterOS 7.21 或更高。
- `architecture-name=x86`。
- 安装且启用与 RouterOS 完全同版本的 x86 `container` package。
- `/system/device-mode get container` 为 `yes`；启用过程按设备提示完成物理确认。
- 唯一 `bridge-lan`，其上已有唯一 `10.0.0.1/24`。
- 已挂载唯一 `disk1`；上传完整包后仍至少有 512 MiB 可用。
- RouterOS `www`/REST 已启用在 TCP 80，并限制为 `10.0.0.0/24` 或 `10.0.0.4/32`。
- `10.0.0.2`、`.3`、`.4` 没有被 RouterOS address、DHCP Lease、其他 veth、ARP 或在线主机占用。

只读检查命令：

```routeros
/system/resource/print
/system/package/print where name="container"
/system/device-mode/print
/interface/bridge/print where name="bridge-lan"
/disk/print
/ip/address/print where address~"10.0.0.1/"
/ip/service/print where name="www"
/container/print
```

如果尚未启用 container，`/system/device-mode/update container=yes` 会改变设备状态并可能要求断电确认，不属于 FoxOS 安装脚本；请按 MikroTik 官方流程单独完成。

## 2. 下载并验证

在 GitHub Actions 的绿色 `FoxOS Core CI` 运行底部下载：

```text
foxos-full-amd64-<commit>
```

Actions 下载的是 ZIP，解压后得到：

```text
foxos-full-amd64-<commit>.tar.gz
foxos-full-amd64-<commit>.tar.gz.sha256
```

先验证外层，再解压 tar：

```bash
sha256sum --check foxos-full-amd64-<commit>.tar.gz.sha256
tar -xzf foxos-full-amd64-<commit>.tar.gz
cd foxos-full-amd64-<commit>
sha256sum --check SHA256SUMS
```

macOS 使用：

```bash
shasum -a 256 -c foxos-full-amd64-<commit>.tar.gz.sha256
shasum -a 256 -c SHA256SUMS
```

三个 `.tar` 是 RouterOS 所需的单层、未压缩 Docker v1 archive，不要继续解压或转换。

## 3. 上传完整目录内容

使用 WinBox 把解压目录中的全部文件和目录上传到 RouterOS `disk1/` 根。不要多套一层 `foxos-full-amd64-<commit>/`。

至少应存在：

```text
disk1/foxos-amd64.tar
disk1/mihomo_amd64.tar
disk1/mosdns-amd64.tar
disk1/mihomo-config/
disk1/mosdns-config/
disk1/preflight.rsc
disk1/foxos-plan.rsc
disk1/foxos-full-install.rsc
disk1/foxos-start-all.rsc
disk1/install.rsc
disk1/upgrade.rsc
disk1/upgrade-promote.rsc
disk1/rollback.rsc
disk1/SHA256SUMS
disk1/RELEASE-MANIFEST.txt
disk1/QUICK-INSTALL.md
```

RouterOS preflight 只检查存在性和最小大小；密码学 checksum 必须在上传前由工作站验证。

## 4. 保存回滚点

在任何写入前执行：

```routeros
/export hide-sensitive file=before-foxos
/system/backup/save name=before-foxos
```

确认 `before-foxos.rsc` 和 `before-foxos.backup` 已生成，并把副本下载到工作站。保留上传的镜像和 `disk1/foxos-data`，直到验收完成。

## 5. 运行只读预检

```routeros
/import file-name=disk1/preflight.rsc
```

必须以以下内容结束：

```text
PRECHECK PASSED: no RouterOS configuration was changed.
```

任何 `ERROR` 都要先处理。预检检查版本、架构、package、device-mode、bridge、disk、REST 范围、管理地址占用、文件和已有 FoxOS owner。

## 6. 展示精确计划并确认

```routeros
/import file-name=disk1/foxos-plan.rsc
```

计划明确列出：

- 创建/复用的用户、14 项 env、mount、veth、bridge port 和容器。
- 修改 `disk1/mihomo-config/config.yaml` 的 Secret。
- 明确不触碰的 DNS、DHCP、路由、NAT、Mangle 和防火墙。
- RouterOS backup 与停止容器/恢复 backup 的回滚路径。

到此为止没有写入。只有操作者确认设备、计划、备份和回滚路径无误后，才执行下一节。

## 7. 第一阶段：创建资源并导入镜像

```routeros
/import file-name=disk1/foxos-full-install.rsc
```

首次执行随机生成并保存：

- `foxos-service` 密码。
- Mihomo Controller Secret。
- FoxOS API Token。
- FoxOS confirmation key。

重复执行会复用有效凭据，不自动轮换。已有 FoxOS marker 但配置不完整时只补齐允许项；未知额外 env、固定端点不匹配或同名资源不属于 FoxOS 时失败关闭。

`/container/add file=...` 是异步导入。反复检查：

```routeros
/container/print
/log/print where topics~"container"
```

等待 `foxos-mihomo`、`foxos-mosdns`、`foxos-active` 三个容器全部 `status=stopped`。导入期间不要重启。

## 8. 第二阶段：按顺序启动

```routeros
/import file-name=disk1/foxos-start-all.rsc
```

脚本依次验证 Mihomo、MosDNS、FoxOS 进入 `running`；前一项失败时不会继续启动后续服务。完成后终端只显示一次四项凭据，请立即保存到离线密码库，不要截图或粘贴到聊天。

## 9. 验收

工作站执行：

```bash
curl -fsS http://10.0.0.4:8090/api/v1/health/live
curl -fsS http://10.0.0.4:8090/api/v1/health/ready
```

浏览器打开 `http://10.0.0.4:8090`，输入安装时显示的 API Token，验证：

1. RouterOS、Mihomo、MosDNS 各自显示实时来源和更新时间。
2. RouterOS 资源、接口、路由、DHCP 与三个容器可读取。
3. Mihomo Controller、活动连接/流量和当前选择器可读取。
4. MosDNS 只显示 TCP 状态，没有写入入口。
5. `10.0.0.1` 至 `.4` 不能保存为设备策略。
6. 先用可恢复测试设备验证静态 Lease 计划、确认、回读和 audit。
7. 出口策略只有在预置 FoxOS anchor/表/网关并审核计划后才测试；不要先操作主要设备。

没有真实设备输出和流量回读时，不得记录“已部署成功”。

## 10. 清理与回滚

确认三容器稳定、凭据已保存且 backup 可恢复后，可以删除三个上传镜像释放空间：

```routeros
/file/remove [find where name="disk1/foxos-amd64.tar"]
/file/remove [find where name="disk1/mihomo_amd64.tar"]
/file/remove [find where name="disk1/mosdns-amd64.tar"]
```

不要删除 `disk1/mihomo-config`、`disk1/mosdns-config`、`disk1/foxos-data`、`disk1/foxos-backups` 或正在使用的 root-dir。安装脚本本身不包含生成后的密钥；实际 Secret 位于 RouterOS env list 和 Mihomo 配置，严禁导出/截图 values。

安装失败时先停止 FoxOS 所有权容器：

```routeros
/container/stop [find where comment="foxos:active"]
/container/stop [find where comment="foxos:mosdns"]
/container/stop [find where comment="foxos:mihomo"]
```

然后按已审核的恢复窗口还原 `before-foxos.backup`。RouterOS binary restore 会重启并覆盖设备配置，必须由设备操作者再次明确确认。

## 常见故障

| 现象 | 检查 |
|---|---|
| `bad command name container` | 同版本 x86 container package 是否安装并重启 |
| `not allowed by device-mode` | `container=yes` 是否完成物理确认 |
| preflight 架构失败 | x86_64 CPU 应返回 `architecture-name=x86` |
| 缺少 `disk1/...` | 是否把目录内容而不是外层目录上传到 disk1 根 |
| 镜像长期不为 stopped | checksum、文件大小、磁盘、package、container 日志 |
| Mihomo 未 running | Secret 写入、配置路径、`foxos-mihomo-runtime` mount |
| MosDNS 未 running | `MOSDNS_AUTO_INIT=0`、配置目录与 container 日志 |
| FoxOS ready 为 503 | SQLite/挂载、RouterOS REST、Mihomo 9090、MosDNS TCP 53 |
| 页面打不开 | `foxos-active` comment 的容器、veth-foxos、bridge-lan、8090 |

RouterOS REST 当前为管理 LAN 内 HTTP。不要把 `www`、8090、9090 或 7890 暴露到 WAN。
