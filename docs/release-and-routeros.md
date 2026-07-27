# 发布、RouterOS 安装、升级与回滚

本文把自动验证、模拟验证和实体 RouterOS 验收分开记录。当前仓库可以构建和组包，Linux namespace 会验证隔离管理数据平面；尚无实体 RouterOS 连接、上传通道和变更确认，因此首装、真实 DHCP 写入、设备出口和物理回滚仍待验收。

## CI 与资产

`FoxOS Core CI` 在 `main`、`agent/foxos-core` push/PR 上执行：

- Go module、gofmt、vet、全量测试、race、lint、gosec、govulncheck。
- TypeScript、Vitest、npm audit、生产构建和三视口 Playwright。
- actionlint、Shell、RouterOS 静态检查和敏感材料检查。
- Linux namespace 中的非 root 80/443、CA/HTTPS、跳转、管理路径、回程和 fail-closed egress readiness。
- Linux amd64 FoxOS/Mihomo/MosDNS 三镜像构建、真实 Mihomo 有效/无效配置语义、MosDNS 挂载启动契约、逐镜像 Trivy 和完整 RouterOS bundle。

同一 push/PR 还会触发独立的 `FoxOS CodeQL` workflow，分别分析 Go 与 JavaScript/TypeScript；不能用 Core CI 绿色替代 CodeQL 结论。

最终 FoxOS 镜像中的 `/usr/local/bin/mihomo` 是配置发布校验器，独立 `mihomo-runtime` 使用同一个二进制。构建固定官方 `v1.19.29` 源码归档的 SHA-256，使用 Go 1.26.5，并将上游仍固定在已知 High 版本的 `golang.org/x/crypto`、`golang.org/x/net`、`golang.org/x/oauth2` 最小提升到已修复版本，版本标识为 `v1.19.29-foxos1`。CI 会在 FoxOS 镜像和独立运行时中分别执行语义契约，并扫描两张最终镜像；这仍不等同于 RouterOS 客户端透明数据平面验收。

`mosdns-runtime` 从 SHA-256 固定的 `jasonxtt/mosdns` 提交 `2ac30e867a7b40ee0ef70ef85b7dcf7ce56d48d0` 重建 `v0.6.4-foxos1`，把 `golang.org/x/crypto` 与 `golang.org/x/net` 提升到已修复版本。CI 验证版本、入口、`MOSDNS_AUTO_INIT=0`、不存在外部初始化 URL，并用包内配置启动后回读进程仍在运行。Mihomo 与 MosDNS 运行时都是 scratch，只复制静态二进制、CA、时区数据和 mode 1777 的空 `/tmp`；预制第三方 tar 不再跟踪或作为组包回退输入。

成功后提供：

```text
foxos-full-amd64-<commit>.tar.gz
foxos-full-amd64-<commit>.tar.gz.sha256
```

`Release artifacts` 只在手动触发或 `v*` tag 运行。arm64 当前只有 FoxOS 单容器资产；包含 Mihomo/MosDNS 的完整包只支持 amd64。

自动化级别只证明代码、镜像和资产契约：

| 级别 | 已证明 | 未证明 |
|---|---|---|
| 自动测试 | 结构化配置、回滚、恢复、DHCP 计算、UI、静态脚本、三镜像构建/契约/扫描 | RouterOS 命令运行和物理设备行为 |
| Linux namespace | FoxOS 非 root HTTPS 管理面、CA/SAN、路由回程、管理 LAN 路径 | RouterOS Container、FastTrack、Mihomo 透明入口 |
| 实体待验收 | 无 | 首装/升级、Lease 写入、真实客户端出口、binary restore |

## 全量包内容与站点清单

组包器把三张输入镜像转换成指定 amd64 的单层、未压缩 Docker v1 tar，并复制：

- `mihomo-config/`、`mosdns-config/`。
- `provenance/` 中两份组件来源锁，记录源码 SHA-256、固定提交、构建器和依赖提升。
- 唯一拓扑来源 `site-config.rsc`。
- 只读 preflight/plan、两阶段安装、HTTPS verify、受控 DNS plan/apply。
- pending/promote 升级和 rollback 脚本。
- `QUICK-INSTALL.md`、`RELEASE-MANIFEST.txt`、`SHA256SUMS`。

组包拒绝填充的 Mihomo Secret、私钥、节点链接、凭据 URL 和常见敏感字段。MosDNS 9099 API 必须绑定容器 loopback，未使用的第三方管理 UI 不允许进入包。外层另生成 `.sha256`。

组包脚本要求调用方显式提供 `FOXOS_IMAGE`、`MIHOMO_IMAGE`、`MOSDNS_IMAGE`，不会读取仓库中的历史二进制。Core CI 与 Release workflow 只在三张输入镜像完成各自 Trivy 门禁后调用组包器。

`site-config.rsc` 定义管理桥、存储、网段、四个服务地址和 `home.arpa` hostname。默认值只是示例。所有 RouterOS 脚本要求先 import 同一份清单；安装器把完整 `FOXOS_SITE_*` 环境交给后端，前端再从 `GET /api/v1/site` 读取，不维护第二份拓扑。

## 首次安装控制点

完整命令见 [QUICK-INSTALL](../deploy/routeros/QUICK-INSTALL.md)。不可跳过：

1. RouterOS 7.21+、`architecture-name=x86`、同版本 container package、`container=yes`。
2. 清单指定的管理桥、存储、RouterOS 地址和受限 REST 已存在。
3. 工作站验证外层与包内 checksum，审核并上传站点清单。
4. 保存 RouterOS export 与 binary backup。
5. 每次先 import 清单；只读 preflight 与 plan 都通过。
6. 操作者确认精确计划后才运行 full-install。
7. 等异步镜像导入完成，再运行 start 脚本；`running` 不是 ready。
8. 保存随机凭据，导入并核对 `foxos-local-ca.pem`，设为 trusted。
9. `foxos-verify.rsc` 自动验证 live、ready、站点、页面和带认证只读 API。
10. 需要 hostname 时单独执行 DNS plan/精确确认/apply；脚本不启用或接管 DNS/DHCP。

FoxOS 完整 env allowlist 是 25 键：安装 marker、四个随机/凭据字段、RouterOS/Mihomo/MosDNS 端点和路径、八个 `FOXOS_SITE_*` 字段、`FOXOS_HTTPS_ENABLED=true`。重复安装可补齐缺项，但未知额外键或固定值不一致会失败关闭。

## HTTPS 与 CA

FoxOS 容器保持 UID 10001，仅 `/app/foxos` 有 `cap_net_bind_service`。外部监听：

- TCP 443：本地 CA 签发的 TLS，叶证书含 public hostname 和 FoxOS IP SAN。
- TCP 80：只返回到 public hostname 的 308 跳转。
- TCP 8090：仅容器 loopback，作为带随机进程内凭据的反向代理后端。

启用 HTTPS 后，Bearer API 只接受真实 TLS，或来自 loopback 且带正确内部凭据的反向代理请求；伪造 `X-Forwarded-Proto` 不生效。CA 文件持久保存在 `foxos-data/tls`。浏览器和 RouterOS 必须先核对指纹并显式信任，服务不会跳过证书验证。

## 实体上线验收

- 三个容器 name/comment 唯一；运行状态和 start-on-boot 分别回读。
- `health/live`、`health/ready`、站点清单、Web 和带认证只读 API 全部通过 HTTPS。
- RouterOS、Mihomo、MosDNS 状态来源独立，不把单项失败扩散或伪装为在线。
- 安装前后 DNS、DHCP DNS、默认路由、NAT、Mangle、Filter、FastTrack diff 符合计划。
- 站点清单四个管理地址不能成为设备策略。
- 一台可恢复测试设备完成动态 Lease 采用、确认、make-static、回读和审计。
- DHCP 范围首尾计数和范围内保留扣除与 UI/API 一致。
- `blocked`、`l2tp` 仅在 capability 可用时测试。
- `mihomo-node`、`proxy-chain` 当前必须为不可用；没有透明入口、回程、管理旁路、FastTrack 和真实出口 IP 证据时不得放开。

容器 `running`、Controller 在线、路由 marker 或 mixed-port 出口检查都不是完整设备流量验收。

## 两阶段升级

升级前：

1. 保存 RouterOS export/binary backup。
2. 从 FoxOS 创建 SQLite/Mihomo 备份并验证 manifest。
3. 保留当前 root-dir、旧镜像、足够 rollback 空间和 CA。
4. 上传新镜像并验证来源，确认没有现存 pending/rollback 槽。
5. 重新 import 当前站点清单。

阶段 1：

```routeros
/import file-name=disk1/site-config.rsc
/import file-name=disk1/upgrade.rsc
```

active 保持运行，脚本只异步导入 `foxos-next` 为 `foxos:pending`。等待 pending 为 stopped。

阶段 2：

```routeros
/import file-name=disk1/site-config.rsc
/import file-name=disk1/upgrade-promote.rsc
```

实际顺序：

1. 当前 active 通过 HTTPS API 创建绑定 pending operation ID 的 SQLite 兼容检查点；失败时不停止 active。
2. 停止 active；失败则请求恢复 active，不启动 pending。
3. pending 保持 `foxos:pending` 和 `start-on-boot=no` 启动。
4. 最多约 90 秒自动检查 container running、live、ready、页面和带认证只读 API。
5. 全部通过后才把旧槽改为 rollback、新槽改为 active/start-on-boot=yes。
6. 新 active 记录 promoted 状态和审计；记录失败也请求恢复旧 active。

任一验收失败会停止并标记 pending 为 failed，请求启动旧 active，并验证旧版本 ready。若旧版本检测到共享 SQLite schema 不兼容，会在打开数据库前校验并原子恢复检查点。若新旧两槽都未恢复 ready，脚本失败关闭并保留槽位/检查点，必须人工处置。

不要在实体流量验收和一次维护窗口回滚演练前清理 rollback。

## 应用回滚

```routeros
/import file-name=disk1/site-config.rsc
/import file-name=disk1/rollback.rsc
```

脚本只操作唯一的 FoxOS active/rollback 槽。旧槽在保留 rollback owner 时启动，旧二进制先按升级检查点恢复兼容 SQLite；只有 live、ready、页面和带认证只读 API 都通过后才切换 owner/start-on-boot。旧槽失败会请求恢复原 active；两者都失败时保留证据并要求人工恢复。

首次安装没有容器 rollback 槽。失败时停止三个精确 FoxOS owner 的容器，保留数据和镜像，并在明确维护窗口恢复安装前 RouterOS binary backup。

## 故障排查

| 现象 | 检查 |
|---|---|
| 脚本提示未加载站点清单 | 当前会话是否先 import 实际存储根下的 `site-config.rsc` |
| Web/API 不可用 | CA 信任、443、live/ready、页面内存 Token |
| RouterOS unavailable | 清单地址、REST 范围、专用账号、veth/bridge |
| Mihomo unavailable | 清单地址 `:9090`、Secret、共享 base/runtime 配置、Controller log |
| MosDNS unavailable | 清单地址 TCP 53、listener |
| 设备出口不可选 | `GET /api/v1/egress/capabilities` 的 `missing`；Mihomo 两种模式当前预期不可用 |
| pending 自动恢复 | upgrade checkpoint、两个槽位、容器日志、ready 和升级审计 |
| 深链接刷新异常 | 通过 HTTPS 同源访问；前端使用 Hash 路由 |

提交诊断前删除 Token、密码、节点链接、env values、证书私钥和 RouterOS export 中的敏感信息。
