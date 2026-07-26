# FoxOS

FoxOS 是运行在 RouterOS Container 环境中的网络管理后台，用一个 Web 界面管理 RouterOS、Mihomo 和 MosDNS。

> 当前分支：核心后端开发中。Web UI 已存在；RouterOS 和 Mihomo 的真实只读连接、节点数据、设备策略、配置生成及回滚基础已经实现。尚未完成的写入功能不会显示为成功。

## 运行关系

| 组件 | 示例地址 | 职责 |
|---|---|---|
| RouterOS | 10.0.0.1 | 宿主、DHCP、路由、L2TP、防火墙 |
| Mihomo | 10.0.0.2 | 代理节点、策略组和流量出口 |
| MosDNS | 10.0.0.3 | DNS；第一阶段只读 |
| FoxOS | 10.0.0.4 | 统一 Web UI、API、SQLite 和任务控制 |

FoxOS、Mihomo、MosDNS 最终都作为 RouterOS Container 运行。示例地址可以修改，安装脚本必须先检测冲突。

## 设计来源

Mihomo 配置管理借鉴 [qianfree/ClashManager](https://github.com/qianfree/ClashManager) 的成熟逻辑，包括节点模型、策略组、分享链接导入、配置生成和单文件部署思路。FoxOS 保留自己的 UI，并新增 RouterOS、设备绑定、L2TP、链式代理、任务、快照和回滚能力。

详见 [ClashManager 来源说明](docs/clashmanager-origin.md)。

## 当前已经实现

### 核心服务

- Go HTTP 服务入口
- 健康与就绪接口
- SQLite 自动建表
- 数据目录与数据库权限收紧
- Bearer Token API 认证
- GitHub Actions 格式与测试检查

### Mihomo

- 节点模型及协议字段校验
- SS、VMess、VLESS、Trojan、Hysteria2、SOCKS5、HTTP 分享链接解析
- 批量解析失败时整批拒绝
- select、url-test、fallback、load-balance 策略组
- 节点与策略组稳定 ID 引用
- Mihomo YAML 生成
- 临时配置校验
- 正式配置快照
- 原子替换、热重载、健康检查和失败回滚
- Controller Bearer Token 与重定向保护

### RouterOS

- REST API Basic Auth
- 固定只读路径允许列表
- 系统资源、接口、DHCP Lease 和 ARP 读取
- DHCP 与 ARP 合并成设备清单
- 静态绑定计划预览
- MAC/IP 校验和 IP 冲突检测
- 已正确绑定时返回空计划
- FoxOS 资源使用 `foxos:` 标识
- RouterOS 原生 L2TP 客户端只读列表
- L2TP 密码字段不进入模型或 API
- `foxos:l2tp:` 所有权识别

### 设备策略

- SQLite 持久化
- 静态 IP
- 直连、Mihomo 节点、代理链、L2TP 和阻断出口模型
- 受认证的增删查改 API
- RouterOS 写入前预览计划
- HMAC 计划确认令牌（绑定完整计划和过期时间）
- 篡改、过期、越权路径和非 FoxOS 资源写入拒绝
- 静态绑定受控执行器及模拟 Writer 安全测试
- 真实 RouterOS DHCP Lease Writer
- 写入后二次读取租约并核对 MAC、IP、静态状态和所有权 comment
- Writer 与执行器双层路径允许列表

## 尚未完成

- 管理员首次初始化和浏览器会话
- 前端与真实 API 全面接线
- 失败补偿与恢复记录
- 设备出口路由执行器
- RouterOS 原生 L2TP 新增、编辑、删除和回读验证
- 链式代理可视化编排和应用
- 任务队列和审计页面
- MosDNS 状态适配
- RouterOS Container 最终镜像
- amd64/arm64 GitHub Releases
- 实际 RouterOS 集成测试

## 安全闭环

Mihomo 修改遵循：

```text
SQLite期望状态 → 生成临时YAML → 校验 → 快照
→ 原子替换 → 热重载 → 健康检查 → 失败回滚
```

RouterOS 修改遵循：

```text
读取实际状态 → 生成计划 → 用户确认 → 备份
→ 只修改foxos:资源 → 验证 → 失败补偿
```

FoxOS 不接管没有 `foxos:` 标识的用户规则。

RouterOS 计划确认令牌使用 HMAC-SHA256，完整绑定方法、路径、请求体、所有权标识和过期时间。计划发生任何变化后必须重新预览和确认。当前静态绑定执行器只允许 DHCP Lease 的 PUT/PATCH 路径。真实 Writer 写入后必须重新读取 Lease，并精确匹配 MAC、IP、dynamic=false 和 `foxos:device:` comment；不满足即报告验证失败。执行 API 已接通，但只有在 RouterOS 已配置、API 已认证、计划签名有效、令牌未使用且回读验证通过时才返回成功。确认令牌只能使用一次。

## API

所有业务接口均需要：

```http
Authorization: Bearer <FOXOS_API_TOKEN>
```

无需认证：

- `GET /api/v1/health/live`
- `GET /api/v1/health/ready`

当前业务接口：

- `GET/POST /api/v1/nodes`
- `GET/PUT/DELETE /api/v1/nodes/{id}`
- `GET/POST /api/v1/device-policies`
- `GET/PUT/DELETE /api/v1/device-policies/{id}`
- `GET /api/v1/routeros/overview`
- `GET /api/v1/mihomo/overview`
- `GET /api/v1/routeros/l2tp`
- `POST /api/v1/routeros/plans/device-binding`
- `POST /api/v1/routeros/plans/device-binding/execute`
- `GET /api/v1/audit-events?limit=100`

节点查询不会返回密码、UUID 或完整凭据，只返回 `hasCredential`。

## 运行配置

必填：

```text
FOXOS_API_TOKEN=至少32个字符
FOXOS_CONFIRMATION_KEY=独立的至少32字符签名密钥
```

RouterOS：

```text
FOXOS_ROUTEROS_URL=https://10.0.0.1
FOXOS_ROUTEROS_USERNAME=foxos
FOXOS_ROUTEROS_PASSWORD=运行时密钥
```

Mihomo：

```text
FOXOS_MIHOMO_URL=http://10.0.0.2:9090
FOXOS_MIHOMO_SECRET=Controller密钥
FOXOS_MIHOMO_LOCAL_CONFIG=/mnt/mihomo/config.yaml
FOXOS_MIHOMO_RUNTIME_CONFIG=/root/.config/mihomo/config.yaml
FOXOS_MIHOMO_BACKUP_DIR=/data/backups/mihomo
```

完整说明见 [运行配置](docs/configuration.md)。

## 本地开发

环境要求：

- Go 1.24+
- Node.js 18+

后端：

```bash
go mod download
go test ./...
FOXOS_API_TOKEN=01234567890123456789012345678901 \
  go run ./cmd/server --database ./data/foxos.db
```

前端：

```bash
cd web
npm install
npm run dev
```

生产构建最终会将 `web/dist` 与 Go 后端打包进 RouterOS Container，RouterOS 上不需要 Node.js 或 Go。

## 验证状态

当前分支已完成以下静态验证：

- 全部 Go 文件通过 `gofmt` 解析与统一格式化
- 清除接口拼接遗留的字面量 `\\n` / `\\t`
- RouterOS DHCP 写入后强制执行回读验证
- 覆盖计划篡改、无所有权、缺少验证器和回读失败测试
- L2TP API 模型不包含密码字段，并有防泄漏测试

GitHub Actions 会运行 `go mod tidy`、`go mod verify`、`gofmt -l cmd internal` 和 `go test ./...`。FoxOS Core CI 第 141 次运行已经通过全部格式和单元测试。浏览器端到端测试与真实 RouterOS 测试环境验证尚未完成，因此本分支仍属于开发版本，不建议直接接管生产网络。

## 项目结构

```text
cmd/server/                 FoxOS 服务入口
internal/api/               HTTP API和认证
internal/config/            运行时配置
internal/domain/            节点、策略组和设备策略
internal/mihomo/            分享链接、配置生成、Controller和回滚
internal/routeros/          REST读取和操作计划
internal/store/sqlite/      SQLite持久化与迁移
web/                        FoxOS Web UI
docs/                       中文设计、安装和使用说明
.github/workflows/          自动测试和后续多架构构建
```

## RouterOS 部署

最终发布：

- `foxos_amd64.tar`
- `foxos_arm64.tar`
- RouterOS `.rsc` 安装与检查脚本
- 示例配置和中文说明

配置、数据库和备份通过 mounts 独立持久化，升级只替换容器镜像。

详见 [RouterOS Container 安装设计](docs/routeros-install.md)。

## 文档

- [总体架构](docs/architecture.md)
- [运行配置](docs/configuration.md)
- [RouterOS 连接](docs/routeros-setup.md)
- [节点管理](docs/node-management.md)
- [设备管理](docs/device-management.md)
- [RouterOS 原生 L2TP](docs/l2tp.md)
- [备份与恢复](docs/backup-restore.md)
- [操作确认与执行安全](docs/operation-confirmation.md)
- [操作审计](docs/logs.md)
- [ClashManager 来源说明](docs/clashmanager-origin.md)

## DNS 边界

当前不会修改 RouterOS DNS、DHCP 下发 DNS、Mihomo DNS 或 MosDNS 配置。MosDNS 第一阶段只读取状态。
