# FoxOS

FoxOS 是面向 RouterOS Container 的网络运维后台，用一个高密度中文控制台管理 RouterOS、Mihomo 与 MosDNS。后端使用 Go 1.24、SQLite，前端使用 React、TypeScript、Vite。

> 发布状态：代码和发布脚本已形成 release candidate，Go/Web/安全/E2E/RouterOS 静态门禁均已建立。尚未连接真实 RouterOS 设备，因此不能把自动化通过等同于生产部署验收。

![FoxOS 总览，数据源独立降级状态](docs/screenshots/overview-desktop.jpg)

## 固定部署拓扑

| 组件 | 管理地址 | 当前职责 |
|---|---|---|
| RouterOS | `10.0.0.1` | 宿主、DHCP、路由、L2TP、Container |
| Mihomo | `10.0.0.2:9090` | Controller、节点、策略组、运行流量 |
| MosDNS | `10.0.0.3:53` | DNS，只读 TCP 状态检查 |
| FoxOS | `10.0.0.4:8090` | Web、API、SQLite、任务、审计与备份 |

RouterOS x86_64 CPU 在 `/system/resource` 中报告的 `architecture-name` 是 `x86`；发布包使用 Linux `amd64` 镜像。

## 能力状态

| 状态 | 能力 |
|---|---|
| 已实现并自动化验证 | 独立数据源状态、Hash 深链接、响应式与键盘访问；节点/组/设备资料；Mihomo 草稿、生成、脱敏 Diff、校验、快照、发布和失败回滚；订阅、告警、备份、审计、持久任务 |
| 已实现但要求 RouterOS 前置资源 | 静态 DHCP Lease；设备出口 `direct`、`mihomo-node`、`proxy-chain`、`l2tp`、`blocked`；写入前必须具备 FoxOS anchor、路由表和网关等安全前置条件 |
| 只读 | RouterOS 资源、接口、WAN/路由、DHCP、容器、L2TP；Mihomo 活动连接、流量、选择器、延迟；MosDNS TCP 可达状态 |
| 暂未开放 | MosDNS 配置写入、DNS 接管、RouterOS 原生 L2TP CRUD、诊断包导出、多用户/RBAC |
| 尚未验收 | 真实 RouterOS/Mihomo/MosDNS 设备部署、真实流量切换和物理回滚 |

### 可信状态

页面不会用样例数据冒充在线：

- 每个 RouterOS、Mihomo、MosDNS、设备、策略、节点和审计请求独立加载、成功或失败。
- 单个接口失败只降级对应区域，不会让整页切换到静态数据。
- 每项状态显示来源、最后更新时间，以及加载中、不可用或过期状态。
- 只有对应实时接口明确返回在线，页面才显示在线。
- TCP 建连、Mihomo 节点 HTTP 检查、当前策略出口 IP、RouterOS L2TP 会话分别显示，不互相替代。

### 安全写入闭环

Mihomo：

```text
SQLite 草稿 -> 生成 YAML -> 脱敏 Diff -> 校验 -> 快照
-> 原子替换 -> 热重载 -> Controller 健康验证 -> 失败回滚
```

RouterOS：

```text
读取实际状态 -> 生成精确计划 -> 用户确认 -> 单次签名令牌
-> 允许列表写入 -> 回读验证 -> 审计 -> 失败补偿
```

确认令牌绑定完整计划，五分钟过期且只能使用一次。节点和策略组删除前检查引用，拒绝产生悬空策略。长操作持久化为 `QUEUED`、`RUNNING`、`VERIFYING`、`SUCCEEDED`、`FAILED` 或 `ROLLED_BACK`，支持幂等提交、重试和重启恢复。

## 快速开发

要求 Go 1.24.x、Node.js 22.x。

```bash
go mod download
go test ./...

cd web
npm ci
npm run typecheck
npm run test:unit
npm run build
```

启动后端：

```bash
export FOXOS_API_TOKEN="$(openssl rand -hex 32)"
export FOXOS_CONFIRMATION_KEY="$(openssl rand -hex 32)"
go run ./cmd/server -listen :8090 -static web/dist -database data/foxos.db
```

打开 `http://127.0.0.1:8090`，在设置页输入同一个 `FOXOS_API_TOKEN`。Token 仅保存在当前页面内存，刷新或关闭标签页后清除；旧版本遗留在 Web Storage 的 Token 会被迁移一次并立即删除。

## 运行配置

`FOXOS_API_TOKEN` 与 `FOXOS_CONFIRMATION_KEY` 必填、至少 32 字符、不得相同。依赖端点必须是私网或 loopback IP 字面量，禁止 URL 用户信息和开放公网目标。

| 变量 | 全量包默认值 | 说明 |
|---|---|---|
| `FOXOS_API_TOKEN` | 首次安装随机生成 | Web/API Bearer Token |
| `FOXOS_CONFIRMATION_KEY` | 首次安装随机生成 | 高风险计划签名密钥 |
| `FOXOS_ROUTEROS_URL` | `http://10.0.0.1` | RouterOS REST 根地址 |
| `FOXOS_ROUTEROS_USERNAME` | `foxos-service` | 专用服务用户 |
| `FOXOS_ROUTEROS_PASSWORD` | 首次安装随机生成 | RouterOS 服务密码 |
| `FOXOS_MIHOMO_URL` | `http://10.0.0.2:9090` | Mihomo Controller |
| `FOXOS_MIHOMO_PROXY_URL` | `http://10.0.0.2:7890` | 当前策略出口探测代理 |
| `FOXOS_MIHOMO_SECRET` | 首次安装随机生成 | Controller Secret |
| `FOXOS_MIHOMO_LOCAL_CONFIG` | `/data/mihomo/config.yaml` | FoxOS 可写挂载路径 |
| `FOXOS_MIHOMO_RUNTIME_CONFIG` | `/root/.config/mihomo/config.yaml` | Mihomo 进程内配置路径 |
| `FOXOS_MIHOMO_BACKUP_DIR` | `/backups/mihomo` | 发布快照目录 |
| `FOXOS_MOSDNS_URL` | `http://10.0.0.3:53` | 仅用于 TCP 53 状态检查 |
| `FOXOS_BACKUP_DIR` | `/backups/foxos` | SQLite/Mihomo 管理备份 |

完整约束见 [运行配置](docs/configuration.md)。

## API

`GET /api/v1/health/live` 和 `GET /api/v1/health/ready` 无需认证。live 只表示进程存活；ready 会真实检查 SQLite、关键挂载以及已配置依赖。

其余接口使用：

```http
Authorization: Bearer <FOXOS_API_TOKEN>
```

主要接口组：

- 节点与策略组：`/api/v1/nodes`、`/api/v1/proxy-groups`
- 设备与在线历史：`/api/v1/devices`、`/api/v1/device-policies`
- RouterOS 状态：`/api/v1/routeros/overview`、`routes`、`dhcp-servers`、`containers`、`l2tp`
- 受控写入：`/api/v1/routeros/plans/device-binding`、`/api/v1/routeros/plans/egress/{id}`
- Mihomo：`/api/v1/mihomo/draft`、`config/preview`、`config/apply`、`snapshots`、`probes/{id}`
- 运维：`/api/v1/jobs/{id}`、`subscriptions`、`alerts`、`backups`、`audit-events`
- MosDNS：`GET /api/v1/mosdns/overview`，无写入接口

请求体有固定大小上限，错误响应不返回内部密钥。节点读取不会返回密码、UUID 或完整凭据。完整方法和确认流程见 [API 参考](docs/api-reference.md)。

## RouterOS 全量部署包

Core CI 为每个提交生成：

```text
foxos-full-amd64-<commit>.tar.gz
foxos-full-amd64-<commit>.tar.gz.sha256
```

发布包包含：

- FoxOS、Mihomo、MosDNS 三个单层、未压缩 Docker v1 tar，供 RouterOS 本地 `file=` 导入。
- `mihomo-config/`、`mosdns-config/`、安装/启动/升级/回滚脚本。
- `preflight.rsc`、`foxos-plan.rsc`、`QUICK-INSTALL.md`、`RELEASE-MANIFEST.txt`、`SHA256SUMS`。
- 首次安装随机凭据；仓库和包内不预置真实 Token、密码或节点链接。

目标要求 RouterOS 7.21+、同版本 x86 `container` package、`container=yes`、现有 `bridge-lan`、`disk1`，上传完成后仍至少有 512 MiB 可用空间。

安全顺序：

1. 在工作站验证外层 `.sha256` 和包内 `SHA256SUMS`。
2. 把解压后的完整目录内容上传到 RouterOS `disk1/`。
3. 保存 RouterOS export 与 binary backup。
4. 运行只读 `/import file-name=disk1/preflight.rsc`。
5. 运行只读 `/import file-name=disk1/foxos-plan.rsc`。
6. 审核精确影响和回滚路径，并取得操作者明确确认。
7. 才运行 `/import file-name=disk1/foxos-full-install.rsc`。
8. 等三个容器均为 `status=stopped`，再运行 `/import file-name=disk1/foxos-start-all.rsc`。

安装器幂等复用匹配所有权的资源，遇到同名用户资源会失败关闭。容器名称是 `foxos-mihomo`、`foxos-mosdns`、`foxos-active`；对应 comment 是 `foxos:mihomo`、`foxos:mosdns`、`foxos:active`。

完整步骤见 [QUICK-INSTALL](deploy/routeros/QUICK-INSTALL.md) 和 [发布、升级与回滚](docs/release-and-routeros.md)。

## 安全边界

- 永久保护 `10.0.0.1` 至 `10.0.0.4`，禁止为管理地址创建设备策略。
- 不修改 RouterOS DNS、DHCP 下发 DNS、默认路由、NAT、Mangle 或用户防火墙。
- 不接管没有 `foxos:` 所有权标识的 RouterOS 资源。
- 出口执行器要求预置且回读验证 FoxOS anchor/路由表/网关；活动 FastTrack 会导致计划失败。
- 订阅仅允许 HTTPS 443、无 URL 凭据、公共目标；逐次重解析防 DNS rebinding，最多三次重定向、2 MiB、15 秒。
- RouterOS/Mihomo/MosDNS 配置端点只允许私网或 loopback IP 字面量，并禁用重定向。
- CSP、安全响应头和 API `no-store` 已启用；容器以非 root 用户运行。
- MosDNS 保持只读，L2TP 密码不会进入 API、日志或数据库输出。
- 当前是单一 Bearer Token 模型，没有多用户/RBAC；不要把 8090 或 RouterOS REST 暴露到 WAN。

不要提交密钥、节点链接、密码、Token、RouterOS 导出或真实公网信息。

## 备份、恢复与升级

FoxOS 备份包含 SQLite 和已配置的 Mihomo 配置，记录 SHA-256 清单，默认保留最近 20 份。恢复必须先获取预览与一次性确认令牌；恢复后校验 SQLite 与 Mihomo，失败时补偿，并保留任务和审计结果。

RouterOS 升级使用 pending/active/rollback 槽位。升级前保存 RouterOS backup、FoxOS 备份与旧镜像；新容器通过 live/ready 和只读状态验证后才执行 promote。恢复与升级细节见 [备份恢复](docs/backup-restore.md) 和 [发布文档](docs/release-and-routeros.md)。

## 故障排查

| 现象 | 先检查 |
|---|---|
| 页面所有数据源不可用 | 当前页面内存中的 API Token、401、FoxOS live/ready |
| 只有 RouterOS 不可用 | REST URL、专用账号权限、www/www-ssl 访问范围、bridge 连通 |
| 只有 Mihomo 不可用 | Controller 9090、Secret、配置挂载、Controller 日志 |
| MosDNS 不可用 | TCP 53 监听、`FOXOS_MOSDNS_URL`、容器状态 |
| 出口计划被拒绝 | 静态 IP、管理面保护、FoxOS anchor、FIB 表、Mihomo/L2TP 网关、FastTrack |
| 发布后回滚 | 对应持久任务、Mihomo Controller、快照目录和审计错误分类 |
| 容器无法启动 | 架构、同版本 container package、`container=yes`、磁盘、`/log/print where topics~"container"` |

故障材料必须先脱敏。不要上传 Authorization header、env list values、分享链接或 RouterOS export。

## 质量门禁

Core CI 执行：

- Go 1.24：module verify、gofmt、vet、全量测试、覆盖率和 race。
- Go 1.25 工具链：golangci-lint、gosec、govulncheck。
- Web：npm clean install、TypeScript、Vitest、npm audit、production build。
- Playwright：桌面、平板、390px 移动端，覆盖深链接、浏览器前进后退、键盘、焦点锁定、失败降级、危险确认、发布与回滚。
- CodeQL、Trivy、RouterOS 脚本静态检查、敏感材料和生成物检查。
- amd64 FoxOS 镜像与全量 RouterOS 包构建、校验和 Artifact 上传。

本地完整命令：

```bash
go test -race -shuffle=on -count=1 ./...
cd web
npm run typecheck
npm run test:unit
npm run build
npm run test:e2e
cd ..
scripts/check-routeros-scripts.sh
scripts/check-sensitive-material.sh
```

## 项目与文档

```text
cmd/server/                 HTTP 服务入口
cmd/routeros-image/         RouterOS 单层镜像转换
internal/api/               API、认证和确认边界
internal/mihomo/            生成、发布、探测和回滚
internal/routeros/          读取、计划、Writer、验证和补偿
internal/subscription/      安全抓取、预览、更新和调度
internal/task/              持久任务与恢复
internal/backup/            备份与恢复
internal/store/sqlite/      SQLite 持久化
web/                        React UI、Vitest、Playwright
deploy/routeros/            RouterOS 部署脚本
scripts/                    质量与组包脚本
```

- [总体架构](docs/architecture.md)
- [API 参考](docs/api-reference.md)
- [运行配置](docs/configuration.md)
- [设备策略](docs/device-management.md)
- [操作确认](docs/operation-confirmation.md)
- [备份恢复](docs/backup-restore.md)
- [Web/API 状态语义](docs/web-api-integration.md)
- [RouterOS 连接](docs/routeros-setup.md)
- [RouterOS 全量安装](deploy/routeros/QUICK-INSTALL.md)
- [发布、升级与回滚](docs/release-and-routeros.md)

自动化和模拟组包只能证明代码与资产一致。真实 RouterOS 写入前仍必须展示设备上的精确计划、影响范围、备份和回滚路径，并由操作者明确确认。
