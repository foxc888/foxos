# FoxOS

FoxOS 是面向 RouterOS Container 的网络管理后台，用一个中文 Web 界面统一查看和管理 RouterOS、Mihomo 与 MosDNS。项目借鉴 [qianfree/ClashManager](https://github.com/qianfree/ClashManager) 的节点解析、策略组、配置生成和单文件部署思路，并增加 RouterOS 设备清单、安全确认、审计、部署与回滚能力。

> 当前状态：可构建的发布候选版。Go/Web 自动化验证和 RouterOS 部署资产已接入；真实 RouterOS 设备联调仍必须在隔离环境完成。未实现的写入能力不会显示为成功。

## 组件与边界

| 组件 | 默认示例地址 | 职责 |
|---|---|---|
| RouterOS | `10.0.0.1` | 宿主、DHCP、路由、原生 L2TP、防火墙 |
| Mihomo | `10.0.0.2` | 代理节点、策略组与流量出口 |
| MosDNS | `10.0.0.3:53` | DNS；当前阶段只读 |
| FoxOS | `10.0.0.4:8090` | Web、API、SQLite、计划确认和审计 |

硬边界：

- 不修改 RouterOS DNS、DHCP 下发 DNS、Mihomo DNS 或 MosDNS 配置。
- 不自动增加默认路由、NAT、Mangle 或全 LAN 代理。
- 只有用户明确操作的设备才允许进入变更计划。
- RouterOS L2TP 当前只读，密码永不进入 API 输出。
- FoxOS 写入只允许带 `foxos:` 所有权标识的资源。
- `10.0.0.1` 至 `10.0.0.4` 为管理面地址，策略设计必须旁路保护。

## 已实现

### Web

- 总览、RouterOS、MosDNS、代理节点、设备管理、网络拓扑、日志、设置。
- 明确显示连接中、实时数据或演示数据。
- API Token 仅保存在当前浏览器。
- RouterOS DHCP/ARP 设备、Mihomo 状态、节点、L2TP、审计实时读取。
- 节点手动新增、删除、多行分享链接原子导入。
- 单节点及批量 TCP 可达性探测。
- 固定 IP：计划预览、警告、二次确认、执行、回读验证。
- MosDNS 保持只读。

### 后端

- Go HTTP 服务、Bearer Token、SQLite、健康与就绪接口。
- 节点和策略组 CRUD。
- SS、VMess、VLESS、Trojan、Hysteria2、SOCKS5、HTTP 分享链接解析。
- 原子批量导入：任一链接或数据库写入失败时整批拒绝。
- `select`、`url-test`、`fallback`、`load-balance` 策略组。
- Mihomo YAML 生成、校验、快照、原子替换、热重载、健康检查和失败回滚基础。
- RouterOS 资源、接口、DHCP Lease、ARP、L2TP 读取。
- DHCP 与 ARP 合并为设备清单。
- 静态租约 IP/MAC 冲突检测。
- HMAC-SHA256 确认令牌绑定完整计划，五分钟过期、不可篡改、仅用一次。
- RouterOS Writer 方法/路径/所有权双层允许列表。
- 写后重新读取 Lease 并验证 MAC、IP、静态状态和 comment。
- STARTED/SUCCEEDED/FAILED 审计。

### 发布

- Go 与 Web CI。
- 多阶段、非 root 容器镜像。
- amd64/arm64 二进制和 RouterOS image tar 工作流。
- RouterOS 只读预检、安装、升级、回滚模板。
- 运行变量模板不包含真实密钥。

## 暂未开放

- RouterOS 原生 L2TP 新增、编辑、删除。
- RouterOS 设备出口策略路由执行器。
- 链式代理从 UI 到 Mihomo 配置应用的完整闭环。
- MosDNS 写入和 DNS 接管。
- 真实 RouterOS、Mihomo、MosDNS 实机验收。

UI 中这些区域可用于展示或编排，但不能把未执行的状态标为“已生效”。

## 快速开发

要求 Go 1.24+、Node.js 22+。

```bash
go mod download
go test ./...

cd web
npm ci
npm run typecheck
npm run build
```

运行：

```bash
export FOXOS_API_TOKEN="$(openssl rand -hex 32)"
export FOXOS_CONFIRMATION_KEY="$(openssl rand -hex 32)"
go run ./cmd/server -listen :8090 -static web/dist -database data/foxos.db
```

打开 `http://127.0.0.1:8090`，在设置页填入同一个 `FOXOS_API_TOKEN`。

## 运行配置

必填：

```text
FOXOS_API_TOKEN=至少32字符
FOXOS_CONFIRMATION_KEY=另一个至少32字符密钥
```

RouterOS：

```text
FOXOS_ROUTEROS_URL=https://10.0.0.1
FOXOS_ROUTEROS_USERNAME=foxos-service
FOXOS_ROUTEROS_PASSWORD=仅运行时提供
```

Mihomo：

```text
FOXOS_MIHOMO_URL=http://10.0.0.2:9090
FOXOS_MIHOMO_SECRET=Controller密钥
FOXOS_MIHOMO_LOCAL_CONFIG=/mihomo/config/config.yaml
FOXOS_MIHOMO_RUNTIME_CONFIG=/mihomo/config/config.yaml
FOXOS_MIHOMO_BACKUP_DIR=/backups/mihomo
```

不要提交实际 Token、密码、Controller secret、节点分享链接或带敏感字段的 RouterOS 导出。此前在聊天中暴露过的 GitHub PAT 也应撤销并重新生成。

## API

健康接口无需认证：

- `GET /api/v1/health/live`
- `GET /api/v1/health/ready`

业务接口使用：

```http
Authorization: Bearer <FOXOS_API_TOKEN>
```

主要接口：

- `GET/POST /api/v1/nodes`
- `GET/PUT/DELETE /api/v1/nodes/{id}`
- `POST /api/v1/nodes/import`
- `POST /api/v1/nodes/{id}/probe`
- `GET/POST /api/v1/proxy-groups`
- `PUT/DELETE /api/v1/proxy-groups/{id}`
- `GET/POST /api/v1/device-policies`
- `GET/PUT/DELETE /api/v1/device-policies/{id}`
- `GET /api/v1/routeros/overview`
- `GET /api/v1/mihomo/overview`
- `GET /api/v1/routeros/l2tp`
- `POST /api/v1/routeros/plans/device-binding`
- `POST /api/v1/routeros/plans/device-binding/execute`
- `GET /api/v1/audit-events?limit=100`

读取节点不会返回密码、UUID 或完整凭据。详见 [API 速查](docs/api-reference.md)。

## RouterOS 部署

1. 在 Actions 运行 `Release artifacts`，下载匹配架构的 `foxos-amd64.tar` 或 `foxos-arm64.tar`。
2. 在 RouterOS 确认 Container device-mode、架构、磁盘和管理桥。
3. 在 Git 管理之外填写 `deploy/routeros/foxos-env.example.rsc`。
4. 上传镜像，执行 `preflight.rsc`。
5. 核对并执行 `install.rsc`。
6. 启动容器，验证 live/ready 和只读状态。
7. 升级用双槽 `upgrade.rsc`；验证前保留旧槽位，故障用 `rollback.rsc`。

脚本不会修改 DNS、默认路由、NAT、Mangle 或防火墙。完整步骤、服务账号、验收和故障排查见 [发布与 RouterOS 安装](docs/release-and-routeros.md)。

## 安全执行闭环

Mihomo：

```text
SQLite期望状态 → 临时YAML → 校验 → 快照
→ 原子替换 → 热重载 → 健康检查 → 失败回滚
```

RouterOS：

```text
读取实际状态 → 生成计划 → 用户确认 → 单次令牌
→ 允许列表写入 → 回读验证 → 审计
```

FoxOS 不接管没有 `foxos:` 标识的用户资源。

## 项目结构

```text
cmd/server/                 服务入口
internal/api/               HTTP API与认证
internal/config/            运行配置
internal/domain/            节点、策略组、设备策略
internal/mihomo/            解析、配置生成、Controller与回滚
internal/routeros/          REST读取、计划、Writer与验证
internal/store/sqlite/      持久化
web/                        React Web UI
deploy/routeros/            RouterOS脚本与env模板
docs/                       中文说明
.github/workflows/          CI与发布构建
```

## 文档

- [总体架构](docs/architecture.md)
- [运行配置](docs/configuration.md)
- [RouterOS 连接](docs/routeros-setup.md)
- [发布与 RouterOS 安装](docs/release-and-routeros.md)
- [API 速查](docs/api-reference.md)
- [节点管理](docs/node-management.md)
- [设备管理](docs/device-management.md)
- [RouterOS 原生 L2TP](docs/l2tp.md)
- [备份与恢复](docs/backup-restore.md)
- [操作确认](docs/operation-confirmation.md)
- [操作审计](docs/logs.md)
- [ClashManager 来源说明](docs/clashmanager-origin.md)
- [Web 与 API 接线](docs/web-api-integration.md)

## 验证说明

PR 合并前必须通过：

- `go mod tidy` / `go mod verify`
- `gofmt -l cmd internal`
- `go test ./...`
- Web `npm ci`、TypeScript 类型检查、Vite 生产构建
- Docker amd64/arm64 构建

自动化通过不等于生产 RouterOS 已验证。首次部署应在可恢复的隔离环境中完成，并保留 RouterOS 导出、SQLite 备份和旧容器槽位。
