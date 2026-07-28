# 发布、RouterOS 安装、升级与回滚

本文把自动验证、模拟验证、CHR 和实体 RouterOS 验收分开记录。当前仓库可以构建和组包，Linux namespace 会验证隔离管理数据平面；尚未执行 CHR 或实体 RouterOS 验收，也没有实机上传通道和变更确认，因此首装、真实 DHCP 写入、设备出口和物理回滚仍待验收。

## CI 与资产

`FoxOS Core CI` 在 `main`、`agent/foxos-core` push/PR 上执行：

- Go module、gofmt、vet、全量测试、race、lint、gosec、govulncheck。
- 应用与 Playwright 配置独立 TypeScript 检查、Vitest、npm audit、生产构建和三视口 Playwright。
- actionlint、Shell、RouterOS 静态检查和敏感材料检查。
- Linux namespace 中的非 root 80/443、CA/HTTPS、跳转、管理路径、回程和 fail-closed egress readiness。
- Linux amd64 FoxOS/Mihomo/MosDNS 三镜像构建、真实 Mihomo 有效/无效配置语义、MosDNS 挂载启动契约、逐镜像 Trivy 和完整 RouterOS bundle。

同一 push/PR 还会触发独立的 `FoxOS CodeQL` workflow，分别分析 Go 与 JavaScript/TypeScript；不能用 Core CI 绿色替代 CodeQL 结论。

最终 FoxOS 镜像中的 `/usr/local/bin/mihomo` 是配置发布校验器，独立 `mihomo-runtime` 使用同一个二进制。构建固定官方 `v1.19.29` 源码归档的 SHA-256，使用 Go 1.26.5，并将上游仍固定在已知 High 版本的 `golang.org/x/crypto`、`golang.org/x/net`、`golang.org/x/oauth2` 最小提升到已修复版本，版本标识为 `v1.19.29-foxos1`。CI 会在 FoxOS 镜像和独立运行时中分别执行语义契约，并扫描两张最终镜像；这仍不等同于 RouterOS 客户端透明数据平面验收。

`mosdns-runtime` 从 SHA-256 固定的 `jasonxtt/mosdns` 提交 `2ac30e867a7b40ee0ef70ef85b7dcf7ce56d48d0` 重建 `v0.6.4-foxos1`，把 `golang.org/x/crypto` 与 `golang.org/x/net` 提升到已修复版本。CI 验证版本、入口、`MOSDNS_AUTO_INIT=0`、不存在外部初始化 URL，并用包内配置启动后回读进程仍在运行。Mihomo 与 MosDNS 运行时都是 scratch，只复制静态二进制、CA、时区数据和 mode 1777 的空 `/tmp`；预制第三方 tar 不再跟踪或作为组包回退输入。

Core CI 的提交资产名使用完整 commit SHA：

```text
foxos-full-amd64-<commit>.tar.gz
foxos-full-amd64-<commit>.tar.gz.sha256
```

`Release artifacts` 只在手动触发或 `v*` tag 运行，GitHub Release 中的全量包改用清理后的 ref/tag 作为 `<release-id>`：

```text
foxos-full-amd64-<release-id>.tar.gz
foxos-full-amd64-<release-id>.tar.gz.sha256
```

不要把 `<commit>` 与 `<release-id>` 混用。arm64 当前只有 FoxOS 单容器资产；包含 Mihomo/MosDNS 的完整包只支持 amd64。

该 workflow 为每个裸 Go 二进制和镜像 tar 分别生成 CycloneDX SBOM 与独立 `SHA256SUMS`，发布前重新校验全部摘要。全量 RouterOS bundle 仍使用自身包内 `SHA256SUMS` 和外层 `.sha256`；这些材料证明构建产物身份，不替代同一提交的 Core CI、CodeQL 或 RouterOS 验收。

自动化级别只证明代码、镜像和资产契约：

| 级别 | 已证明 | 未证明 |
|---|---|---|
| 自动测试 | 结构化配置、回滚、恢复、DHCP 计算、UI、静态脚本、三镜像构建/契约/扫描 | RouterOS 命令运行和物理设备行为 |
| Linux namespace | FoxOS 非 root HTTPS 管理面、CA/SAN、路由回程、管理 LAN 路径 | RouterOS Container、FastTrack、Mihomo 透明入口 |
| CHR 待验收 | 无 | RouterOS Container 首装/升级、脚本实际语义和回滚 |
| 实体待验收 | 无 | Lease 写入、真实客户端出口、binary restore 和物理恢复 |

## 全量包内容与站点清单

组包器把三张输入镜像转换成指定 amd64 的单层、未压缩 Docker v1 tar，并复制：

- `mihomo-config/`、`mosdns-config/`。
- `provenance/` 中两份组件来源锁，记录源码 SHA-256、固定提交、构建器和依赖提升。
- 不可变拓扑模板 `site-config.example.rsc` 和 `seal-site-config.sh`；包内没有可直接执行的站点清单。
- 只读 preflight/共享 inspector/plan、唯一 `foxos-full-install.rsc` 正式入口、HTTPS verify、受控 DNS plan/apply。
- `foxos-upgrade-<release-id>/`：唯一 FoxOS 镜像、独立 checksum、pending/promote/rollback 的 inspector、plan 和确认式 apply，以及确认式 rollback 归档。首次安装也从该版本化目录导入 FoxOS 镜像。
- 根目录中的确认式卸载脚本；运行态配置、loader、站点清单、数据和备份不进入版本化升级 payload。
- `QUICK-INSTALL.md`、`RELEASE-MANIFEST.txt`、`SHA256SUMS`。

组包拒绝填充的 Mihomo Secret、私钥、节点链接、凭据 URL 和常见敏感字段。MosDNS 9099 API 必须绑定容器 loopback，未使用的第三方管理 UI 不允许进入包。外层另生成 `.sha256`。

组包脚本要求调用方显式提供 `FOXOS_IMAGE`、`MIHOMO_IMAGE`、`MOSDNS_IMAGE`，不会读取仓库中的历史二进制。Core CI 与 Release workflow 只在三张输入镜像完成各自 Trivy 门禁后调用组包器。

操作者必须复制模板生成唯一 `site-config.rsc`，配置管理桥、存储、网段、四个服务地址、`home.arpa` hostname 和可选订阅私网 allowlist，再用封存工具生成独立 SHA-512。该可编辑清单及其摘要故意不进入发布包 `SHA256SUMS`，也不得直接 import；包内固定的 `load-site-config.rsc` 先把它作为数据验证 SHA-512 和 11 项精确赋值白名单。preflight 会重新计算当前内容摘要并核对 loader 证明。安装器把完整 `FOXOS_SITE_*` 环境交给后端，前端从 `GET /api/v1/site` 回读，不维护第二份拓扑。

## 首次安装控制点

完整命令见 [QUICK-INSTALL](../deploy/routeros/QUICK-INSTALL.md)。不可跳过：

1. RouterOS 7.21+、`architecture-name=x86`、同版本 container package、`container=yes` 与 `scheduler=yes`；两项 device-mode 更新都可能要求设备操作者按官方流程物理确认。
2. 清单指定的管理桥、存储、RouterOS 地址和受限 REST 已存在。
3. 工作站验证外层与包内 checksum，从模板生成并独立封存站点清单后上传。
4. 保存脱敏 RouterOS export 与带唯一离线密码、`aes-sha256` 的 binary backup。
5. import 不可变 `load-site-config.rsc` 后运行 `foxos-plan.rsc`；loader 验证可编辑清单，plan 再自动执行 preflight 和共享 inspector，逐项输出 `CREATE/REUSE/FAIL` 并绑定 SHA-512 前态摘要。
6. 操作者把该摘要原样设为确认值后才运行 `foxos-full-install.rsc`；执行器在第一次资源写入前重跑全部检查，前态变化即拒绝。
7. 等异步镜像导入完成，再运行可重入 start 脚本；后序启动失败会停止本次已启动的前序容器，`running` 仍不是 ready，autostart 保持关闭。
8. 导入并核对 `foxos-local-ca.pem`，设为 trusted；`foxos-verify.rsc` 自动验证 live、ready、站点、页面和带认证只读 API，全部通过后才启用 owned 顺序启动 scheduler。三个容器始终保持 `start-on-boot=no`。
9. 只从 `foxos-env` 读取一次随机 API Token，保存到离线密码库并登录 HTTPS 管理页。
10. 需要 hostname 时单独执行 DNS plan/精确确认/apply；脚本不启用或接管 DNS/DHCP。

FoxOS 完整 env allowlist 基线是 27 键：安装 marker、`FOXOS_ENV=production`、四个随机/凭据字段、RouterOS/Mihomo/MosDNS 端点和路径、升级检查点、八个 `FOXOS_SITE_*` 字段、`FOXOS_HTTPS_ENABLED=true`。站点清单显式配置私网订阅 allowlist 时增加 `FOXOS_SUBSCRIPTION_PRIVATE_CIDRS`，共 28 键。重复安装可补齐缺项，但未知额外键或固定值不一致会失败关闭。

当前生命周期脚本统一使用 `envlists=` 与 `mountlists=` 引用命名列表。FoxOS 的版本下限是 RouterOS 7.21，静态门禁会拒绝单数 `envlist` 或混用拼写。

MikroTik 当前 Container 官方页面在 2026-07-28 回读时仍把容器环境列表属性记录为单数 `envlist`，页面示例也使用单数；但是官方 `container-7.21.npk`、`container-7.21.3.npk`、`container-7.21.5.npk` 的 console/WebFig 命令元数据均暴露复数 `envlists`。三份审计输入的 SHA-256 分别为 `f51c93fe9331f2460171cbdc359339704ef1f763cb295190155dab4353961d16`、`c427ffd3a5a757116b4b5ed8ec6a5533f3a6eaa8afa6ad5adc2f22a124901f66`、`823c2386f6bd4657f7eae1b50f1f0ce167b9a945e0f95953e1182a9d73340e9b`。FoxOS 以目标版本包内的命令元数据为静态实现依据，但这仍不等于真实 RouterOS 已验收。

因此 CHR 发布门禁仍必须在目标 7.21.x 上保存 `/console/inspect request=completion input="/container/add "` 的原始输出，并在隔离、可丢弃的测试配置中完成最小 `/container/add` 与 `/container get` 回读，确认实际接受 `envlists`。该门禁未通过前，不能把 RouterOS 首装、升级、回滚或卸载标记为已验收，也不能在实体设备上尝试其他拼写。

## HTTPS 与 CA

FoxOS 容器保持 UID 10001，仅 `/app/foxos` 有 `cap_net_bind_service`。外部监听：

- TCP 443：本地 CA 签发的 TLS，叶证书含 public hostname 和 FoxOS IP SAN。
- TCP 80：只返回到 public hostname 的 308 跳转。
- TCP 8090：仅容器 loopback，作为带随机进程内凭据的反向代理后端。

启用 HTTPS 后，Bearer API 只接受真实 TLS，或来自 loopback 且带正确内部凭据的反向代理请求；伪造 `X-Forwarded-Proto` 不生效。CA 文件持久保存在 `foxos-data/tls`。浏览器和 RouterOS 必须先核对指纹并显式信任，服务不会跳过证书验证。

## 实体上线验收

- 三个容器 name/comment 唯一、运行状态符合计划、`start-on-boot=no`，且唯一 owned 顺序启动 scheduler 已启用。
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
4. 在工作站验证新全量包与其中 `foxos-upgrade-<release-id>/SHA256SUMS`，然后只上传整个 `foxos-upgrade-<release-id>/` 目录。禁止把新全量包覆盖到现有存储根；不得覆盖 `site-config*`、`load-site-config.rsc`、`mihomo-config/`、`mosdns-config/`、`foxos-data/`、`foxos-backups/`、`containers/` 或旧 payload。
5. 重新 import 不可变 `load-site-config.rsc`，让它校验并加载当前封存清单。

阶段 1：

```routeros
/import file-name=disk1/load-site-config.rsc
/import file-name=disk1/foxos-upgrade-<release-id>/upgrade-plan.rsc
:global FoxOSUpgradeConfirmation "<paste APPROVED UPGRADE SHA-512>"
/import file-name=disk1/foxos-upgrade-<release-id>/upgrade.rsc
```

plan 只读绑定 release ID、active/image 对象 ID、root-dir、状态、veth、mount、install marker 与当前站点摘要。apply 清空一次性确认，在首次写入前二次运行 inspector 并回读对象 snapshot；随后 active 保持运行，只异步导入 `foxos-<release-id>` 到同名版本化 root-dir 并标为 `foxos:pending`。它拒绝复用已有容器名或 root-dir；等待 pending 为 stopped。

阶段 2：

```routeros
/import file-name=disk1/load-site-config.rsc
/import file-name=disk1/foxos-upgrade-<release-id>/upgrade-promote-plan.rsc
:global FoxOSUpgradePromoteConfirmation "<paste APPROVED PROMOTE SHA-512>"
/import file-name=disk1/foxos-upgrade-<release-id>/upgrade-promote.rsc
```

实际顺序：

1. 当前 active 通过 HTTPS API 创建绑定 release ID 的 SQLite 兼容检查点；服务端先拒绝新 HTTP 写请求、排空完整任务 handler，再冻结全部 SQLite 写入。检查点请求按同一 operation 幂等重试；响应仍不确定时，只有槽位身份完全不变且 active 的 live 可达才尝试幂等 abort。
2. 停止 active；若 60 秒后它仍是同一 running 槽，脚本按 `live -> aborted/restored -> ready` 取消检查点并恢复写入，不启动 pending。任何槽位身份或状态不确定都失败关闭，不自动 abort。
3. pending 保持 `foxos:pending` 和 `start-on-boot=no` 启动。
4. 最多约 90 秒自动检查 container running、live、ready、页面和带认证只读 API。
5. 全部通过后才把旧槽改为 rollback、新槽改为 active；两个槽都保持 `start-on-boot=no`，冷启动继续由已验收的 owned scheduler 顺序协调。
6. 每次写入 promoted 前（包括 switched 重试）都重新检查新 active 的 running、live、ready、页面、认证只读 API 和 active/rollback 槽位身份；只有全部通过，才以同一 operation 幂等记录 promoted 状态和审计并解除写冻结。最终验收失败时不调用 promoted；响应不确定会重试六次。两种情况都保留新 active 和 stopped rollback，禁止自动 abort 或回滚，检查依赖与日志后重新运行 promote plan 查询并完成状态。

任一验收失败会停止并标记 pending 为 failed，请求启动旧 active。若旧版本检测到共享 SQLite schema 不兼容，会在打开数据库前校验并原子恢复检查点；旧版本 live 后，脚本只在 frozen 的对象 ID、owner、名称、root-dir 和状态仍完全匹配时请求 `aborted`，接受幂等返回的 `aborted` 或已恢复数据库的 `restored`，最后验证 ready。缺少 abort/restored 证据时即使 ready 可达也不能视为恢复写入；脚本失败关闭并保留槽位/检查点，必须人工处置。

不要在实体流量验收和一次维护窗口回滚演练前清理 rollback。验收完成后运行当前 payload 中的 `upgrade-cleanup-plan.rsc`，核对唯一 stopped `foxos:rollback` 或 `foxos:rollback-complete` 的容器名、root-dir 和摘要；把摘要原样设置为 `FoxOSUpgradeCleanupConfirmation` 后才运行 `upgrade-cleanup-apply.rsc`。apply 只把该证据槽改为 `foxos:retained`，不删除容器、root-dir、镜像、数据、备份或检查点。真实回滚完成后必须使用新的 release ID 前进，不能复用已 retained 的容器名或 root-dir。

## 应用回滚

```routeros
/import file-name=disk1/load-site-config.rsc
/import file-name=disk1/foxos-upgrade-<release-id>/rollback-plan.rsc
:global FoxOSRollbackConfirmation "<paste APPROVED ROLLBACK SHA-512>"
/import file-name=disk1/foxos-upgrade-<release-id>/rollback.rsc
```

plan 只读绑定当前 release 的 active、唯一 rollback、对象 ID、root-dir、状态、veth、mount、Token 摘要和站点摘要。apply 在首次 stop 前二次回读摘要与 snapshot，只操作这两个槽位。旧槽在保留 rollback owner 且 `start-on-boot=no` 时启动，旧二进制先按升级检查点恢复兼容 SQLite；只有 live、ready、页面和带认证只读 API 都通过后才切换 owner。旧槽失败会请求恢复原 active；两者都失败时保留证据并要求人工恢复。成功后新版本槽为 stopped `foxos:rollback-complete`，用同一 payload 的 cleanup 归档，再以新 release ID 前进。

首次安装没有容器 rollback 槽。失败时停止三个精确 FoxOS owner 的容器，保留数据和镜像，并在明确维护窗口恢复安装前 RouterOS binary backup。

## 确认式卸载

```routeros
/import file-name=disk1/load-site-config.rsc
/import file-name=disk1/uninstall-plan.rsc
:global FoxOSUninstallConfirmation "<paste APPROVED UNINSTALL SHA-512>"
/import file-name=disk1/uninstall-apply.rsc
```

plan 只读核对所有 FoxOS container comment、env marker、mount、veth/bridge port、服务用户/组和可选 owned DNS 记录。apply 会重新生成同一摘要，只有前态和确认都一致时才停止并删除这些精确 RouterOS 资源。它默认保留站点存储中的 `foxos-data`、`foxos-backups`、Mihomo/MosDNS 配置、三张镜像、所有版本化 root-dir、站点清单、RouterOS backups 和本地 CA；后续永久清除必须另行审核路径，不能把存储根或未知文件交给递归删除。

## 故障排查

| 现象 | 检查 |
|---|---|
| 脚本提示未加载站点清单 | 当前会话是否先 import 实际存储根下的不可变 `load-site-config.rsc`；不得直接 import 可编辑清单 |
| Web/API 不可用 | CA 信任、443、live/ready、浏览器会话是否过期、401/403 |
| RouterOS unavailable | 清单地址、REST 范围、专用账号、veth/bridge |
| Mihomo unavailable | 清单地址 `:9090`、Secret、共享 base/runtime 配置、Controller log |
| MosDNS unavailable | 清单地址 TCP 53、listener |
| 设备出口不可选 | `GET /api/v1/egress/capabilities` 的 `missing`；Mihomo 两种模式当前预期不可用 |
| pending 自动恢复 | upgrade checkpoint、两个槽位、容器日志、ready 和升级审计 |
| 深链接刷新异常 | 通过 HTTPS 同源访问；前端使用 Hash 路由 |

提交诊断前删除 Token、密码、节点链接、env values、证书私钥和 RouterOS export 中的敏感信息。
