# 发布、RouterOS 安装、升级与回滚

本文区分自动化资产验证和真实设备验收。当前仓库可构建、可组包；没有 RouterOS 连接信息与上传通道时，只能完成静态检查和模拟验收。

## CI 与资产

`FoxOS Core CI` 在 `main`、`agent/foxos-core` push/PR 上执行 Go、race、lint、安全、Web、Vitest、Playwright、RouterOS 静态检查和 amd64 全量组包。成功后提供：

```text
foxos-full-amd64-<commit>.tar.gz
foxos-full-amd64-<commit>.tar.gz.sha256
```

`Release artifacts` 仅在手动触发或 `v*` tag 运行，先复用完整质量门禁，再生成：

- `foxos-linux-amd64`、`foxos-linux-arm64`。
- `foxos-amd64.tar`、`foxos-arm64.tar` 单容器镜像。
- `foxos-full-amd64-<tag>.tar.gz` 全栈包。

只有 tag workflow 会创建/更新 GitHub Release。arm64 当前只有 FoxOS 单容器资产；包含 Mihomo/MosDNS 的完整包只支持 amd64。

## 全量包内容

组包器把三张输入镜像转换成指定 amd64 的单层、未压缩 Docker v1 tar，并复制：

- `mihomo-config/` 和 `mosdns-config/`。
- `preflight.rsc`、`foxos-plan.rsc`、全量安装和启动脚本。
- 单容器安装、pending/promote 升级和 rollback 脚本。
- `QUICK-INSTALL.md`、`RELEASE-MANIFEST.txt`、`SHA256SUMS`。

组包会拒绝填充的 Mihomo Secret、私钥、节点链接、凭据 URL 和常见敏感字段。外层另生成 `.sha256`。

## 首次安装

完整步骤见 [QUICK-INSTALL](../deploy/routeros/QUICK-INSTALL.md)。不可跳过的控制点：

1. RouterOS 7.21+、`architecture-name=x86`、同版本 container package、`container=yes`。
2. `bridge-lan`、`disk1`、`10.0.0.1/24` 和受限 REST 已存在。
3. 上传完整目录内容，工作站先验证 checksum。
4. 保存 RouterOS export 与 binary backup。
5. 只读 preflight 与只读 plan 都通过。
6. 操作者明确确认后才运行 full-install。
7. 镜像异步导入完成后，第二次 import 按 Mihomo、MosDNS、FoxOS 启动。
8. 保存随机生成的 RouterOS password、Mihomo Secret、API Token 和 confirmation key。

安装器维护 14 项精确 `foxos-env`：

```text
FOXOS_INSTALL_MARKER
FOXOS_API_TOKEN
FOXOS_CONFIRMATION_KEY
FOXOS_ROUTEROS_URL
FOXOS_ROUTEROS_USERNAME
FOXOS_ROUTEROS_PASSWORD
FOXOS_MIHOMO_URL
FOXOS_MIHOMO_PROXY_URL
FOXOS_MIHOMO_SECRET
FOXOS_MIHOMO_LOCAL_CONFIG
FOXOS_MIHOMO_RUNTIME_CONFIG
FOXOS_MIHOMO_BACKUP_DIR
FOXOS_MOSDNS_URL
FOXOS_BACKUP_DIR
```

重复执行复用有效凭据和匹配 owner 的资源；固定值不一致、额外 env 或同名非 FoxOS 资源会拒绝继续。

## 上线验收

- 三个容器的名称/comment 唯一且状态 running。
- `health/live` 为 200；`health/ready` 的 SQLite、路径和已配置依赖均为 ok。
- Web 中 RouterOS、Mihomo、MosDNS 分别显示实时来源，不把单项失败扩散到整页。
- RouterOS 资源、接口、路由、DHCP、容器和设备可读。
- Mihomo Controller、活动连接/流量、选择器和节点 HTTP 可读；出口探测单独显示。
- MosDNS 只有 TCP 状态，没有写入入口。
- RouterOS DNS、DHCP DNS、默认路由、NAT、Mangle、Filter 与安装前一致。
- `10.0.0.1` 至 `.4` 管理地址不能成为设备策略。
- 一台可恢复测试设备完成 Lease plan/confirm/execute/readback/audit。
- 出口策略只在单独预置并审核 FoxOS anchor/表/网关后测试。

自动测试或容器 `running` 都不是完整验收；必须保留设备输出、API 回读和网络连通性证据。

## 两阶段升级

升级前：

1. 保存 RouterOS export/binary backup。
2. 从 FoxOS 创建 SQLite/Mihomo 备份并验证 manifest。
3. 保留当前 root-dir、旧镜像和 rollback 空间。
4. 上传新 `foxos-amd64.tar` 到 `disk1/` 并验证来源。
5. 确认没有现存 `foxos:pending` 或 `foxos:rollback`。

阶段 1：

```routeros
/import file-name=disk1/upgrade.rsc
```

当前 active 保持运行，脚本只导入 `foxos-next` 为 `foxos:pending`。等待 pending 为 stopped。

阶段 2：

```routeros
/import file-name=disk1/upgrade-promote.rsc
```

脚本停止旧 active，将其标记 rollback，把 pending 标记 active 并启动。新容器未进入 running 时自动请求恢复旧容器。进入 running 后仍要人工验证 live/ready、Web、依赖、审计和测试设备。

不要在验收完成前清理 rollback。

## 应用回滚

若新容器 running 但应用验收失败：

```routeros
/import file-name=disk1/rollback.rsc
```

脚本只切换 FoxOS 自有 active/rollback 容器，不修改 Mihomo、MosDNS、DNS、DHCP、路由、NAT、Mangle 或防火墙。rollback 容器必须为 stopped；启动失败时脚本请求恢复原 active。

共享 SQLite 可能已被新版本迁移。回滚前核对数据版本，需要时用升级前 FoxOS 备份恢复。不要让旧二进制直接打开未经验证的新 schema。

## 首次安装回滚

首次安装没有容器 rollback 槽。失败时停止三个 FoxOS 所有权容器，保留 `disk1/foxos-data` 与镜像，然后在维护窗口恢复安装前 RouterOS binary backup。binary restore 会重启并覆盖设备配置，是破坏性步骤，必须再次明确确认。

## 故障排查

| 现象 | 检查 |
|---|---|
| Web 打开但全部不可用 | 当前页面 API Token、401、live/ready |
| RouterOS unavailable | REST 地址/范围、专用账号、veth/bridge |
| Mihomo unavailable | 9090、Secret、shared config path、Controller log |
| MosDNS unavailable | `FOXOS_MOSDNS_URL=http://10.0.0.3:53`、TCP listener |
| current-policy exit unavailable | `FOXOS_MIHOMO_PROXY_URL`、7890、外网 HTTPS |
| 出口计划 prerequisite 失败 | anchor、FastTrack、FIB table、Mihomo route/L2TP |
| 发布后 ROLLED_BACK | job `errorClass`、snapshot、Controller reload |
| 容器导入失败 | 单层 tar、checksum、架构、磁盘、container log |
| 深链接刷新异常 | 通过 FoxOS 8090 访问；前端使用 Hash 路由 |

提交诊断前删除 Token、密码、节点链接、env values 和 RouterOS export 中的敏感信息。

## 实机状态

截至当前代码状态，只完成本地自动化、浏览器验收和模拟组包。没有真实 RouterOS 连接信息、上传通道和变更确认，因此实机安装、真实出口切换和物理回滚仍待完成。
