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
FOXOS_ROUTEROS_URL=http://10.0.0.1
FOXOS_ROUTEROS_USERNAME=foxos-service
FOXOS_ROUTEROS_PASSWORD=仅运行时提供
```

Mihomo：

```text
FOXOS_MIHOMO_URL=http://10.0.0.2:9090
FOXOS_MIHOMO_SECRET=Controller密钥
FOXOS_MIHOMO_LOCAL_CONFIG=/data/mihomo/config.yaml
FOXOS_MIHOMO_RUNTIME_CONFIG=/root/.config/mihomo/config.yaml
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

你的现有设备为 RouterOS x86_64，网络规划为：

| 组件 | 地址 |
|---|---|
| RouterOS | `10.0.0.1` |
| Mihomo Controller | `10.0.0.2:9090` |
| MosDNS | `10.0.0.3:53` |
| FoxOS | `10.0.0.4:8090` |

管理桥固定为 `bridge-lan`。因此应下载 `foxos-full-amd64-<commit>`，不要使用 arm64 镜像。全量包包含 FoxOS、Mihomo、MosDNS 三个镜像、手工填写的 RouterOS 安装脚本和配置下载工具。两套配置只下载到你的电脑，不会被二次发布到 Actions Artifact。

### 推荐：全栈快速安装

完整操作见 [`deploy/routeros/QUICK-INSTALL.md`](deploy/routeros/QUICK-INSTALL.md)。最短流程是：

1. 下载并解压最新绿色 Core CI 的 `foxos-full-amd64-<commit>`。
2. 确认 RouterOS 已安装同版本 x86 `container` package、`container=yes`，并有至少 512 MiB 可用空间。
3. 双击 `SETUP.cmd`，在自动打开的 `foxos-full-install.rsc` 顶部填写四个值并保存。
4. 用 WinBox 上传文档列出的七项文件/目录。
5. 执行 `/import file-name=foxos-full-install.rsc`。
6. 等三个容器全部 `status=stopped` 后执行 `/import file-name=foxos-start-all.rsc`。
7. 打开 `http://10.0.0.4:8090`，使用你填写的 FoxOS Token。

必须分成两次 import：RouterOS 会异步解压 `/container/add file=...` 导入的镜像，而且首次不会自动启动；固定等待时间不能保证三张镜像都已完成。

快速安装不会修改 RouterOS DNS、DHCP DNS、Mihomo DNS、MosDNS、NAT、默认路由、Mangle 或现有防火墙。

以下内容保留为“仅安装 FoxOS 单容器”的手动高级流程。

### 第 1 步：下载 FoxOS 镜像

在 GitHub 仓库打开：

```text
Actions → FoxOS Core CI → 最新一次绿色运行
→ 页面底部 Artifacts
→ foxos-full-amd64-<commit>
```

下载的文件是 ZIP，先在电脑解压，得到 `foxos-amd64.tar`。不要把 ZIP 直接导入 RouterOS。

如果分支尚未产生 Artifact，可以先合并 PR #2，再手动运行 `Release artifacts`，下载同名 amd64 tar。

### 第 2 步：上传文件

使用 WinBox 打开 `Files`，上传：

```text
foxos-amd64.tar
deploy/routeros/preflight.rsc
deploy/routeros/install.rsc
```

脚本上传后在 RouterOS 根目录中的文件名通常是 `preflight.rsc` 和 `install.rsc`。

### 第 3 步：只读预检

在 WinBox Terminal 执行：

```routeros
/import file-name=preflight.rsc
```

必须检查：

- Architecture 是 `x86_64`。
- RouterOS Container 已允许。
- `10.0.0.4` 没有被占用。
- 输出中存在你的 LAN 管理桥；默认安装脚本使用 `bridge-lan`。
- `foxos-amd64.tar` 已上传且大小正常。
- 磁盘空间足够。

如果管理桥不是 `bridge-lan`，先用文本编辑器修改 `install.rsc` 中：

```routeros
:local managementBridge "你的实际桥名称"
```

不要为了匹配脚本而改动现有 LAN 或 DHCP。

### 第 4 步：准备 RouterOS REST 服务账号

不要让 FoxOS 使用 `admin`。以下命令创建最小化专用账号；把密码替换成你本地生成的强密码：

```routeros
/user/group/add name=foxos-rest policy=read,write,rest-api
/user/add name=foxos-service group=foxos-rest password="替换为强密码" disabled=no
```

FoxOS 通过 RouterOS REST API 工作。当前 env 模板使用内网 HTTP：

```routeros
/ip/service/enable www
/ip/service/set www port=80 address=10.0.0.0/24
```

这只把 RouterOS Web/REST 限制在管理网段，不应暴露到 WAN。若你的 RouterOS 已使用 `www-ssl` 和可信证书，可改用 HTTPS；不要直接把自签名 HTTPS 地址填入 FoxOS，因为当前客户端不会跳过证书校验。

### 第 5 步：生成 FoxOS 密钥

在 Windows PowerShell 执行两次，每次保存不同结果：

```powershell
[Convert]::ToHexString(
  [Security.Cryptography.RandomNumberGenerator]::GetBytes(32)
).ToLower()
```

分别用于：

- `FOXOS_API_TOKEN`
- `FOXOS_CONFIRMATION_KEY`

两者至少 32 字符且不能相同。

### 第 6 步：填写并导入 env

下载 `deploy/routeros/foxos-env.example.rsc` 到电脑，复制为 `foxos-env.local.rsc`，只在本地副本中替换：

```text
FOXOS_API_TOKEN
FOXOS_CONFIRMATION_KEY
FOXOS_ROUTEROS_PASSWORD
FOXOS_MIHOMO_SECRET
```

确认这些非秘密配置保持为：

```text
FOXOS_ROUTEROS_URL=http://10.0.0.1
FOXOS_ROUTEROS_USERNAME=foxos-service
FOXOS_MIHOMO_URL=http://10.0.0.2:9090
FOXOS_MIHOMO_RUNTIME_CONFIG=/root/.config/mihomo/config.yaml
```

上传本地副本后执行：

```routeros
/import file-name=foxos-env.local.rsc
/container/envs/print where list="foxos-env"
```

确认 key 全部存在即可，不要截图或复制 value。随后删除含明文的导入文件：

```routeros
/file/remove [find where name="foxos-env.local.rsc"]
```

### 第 7 步：安装 FoxOS

再次确认 `install.rsc` 的管理桥名称，然后执行：

```routeros
/import file-name=install.rsc
/container/print
```

脚本会：

- 按 CPU 架构自动选择 `foxos-amd64.tar`。
- 创建 `veth-foxos = 10.0.0.4/24`。
- 把 veth 加入指定管理桥。
- 创建持久化 `foxos-data` 和 `foxos-backups` mounts。
- 导入 `foxos:active` 容器。

脚本不会修改 DNS、默认路由、NAT、Mangle、防火墙或 DHCP DNS。

镜像导入完成并显示 stopped 后启动：

```routeros
/container/start [find where comment="foxos:active"]
/container/print
/log/print where topics~"container"
```

### 第 8 步：打开和验证

电脑浏览器打开：

```text
http://10.0.0.4:8090
```

在 FoxOS 设置页输入 `FOXOS_API_TOKEN`。然后依次确认：

1. 总览显示“实时数据”，不是演示数据。
2. RouterOS 显示在线。
3. Mihomo 显示在线。
4. MosDNS 只显示状态，不出现写入操作。
5. 节点列表可读取并可进行 TCP 探测。
6. 日志页面能看到审计记录。

健康接口：

```text
http://10.0.0.4:8090/api/v1/health/live
http://10.0.0.4:8090/api/v1/health/ready
```

### 第 9 步：第一次写入测试

不要先操作主要设备。选择一台可随时重新联网的测试设备：

1. 在设备管理中选择设备。
2. 保持当前 IP，不修改出口策略。
3. 点击固定 IP。
4. 阅读 RouterOS 变更计划和警告。
5. 确认执行。
6. 在 RouterOS 检查该 Lease 为静态，并带有 `foxos:device:` comment。
7. 确认设备能续租、访问网关和互联网。

当前版本只执行静态 DHCP Lease；设备出口路由、链式代理写入和 L2TP 写入尚未开放。

### 第 10 步：失败时停止与清理

仅停止 FoxOS：

```routeros
/container/stop [find where comment="foxos:active"]
```

停止 FoxOS 不会停止 Mihomo 或 MosDNS，也不会恢复或修改 DNS，因为安装过程从未修改 DNS。

完整升级、双槽回滚、备份、验收和故障排查见 [发布与 RouterOS 安装](docs/release-and-routeros.md)。

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
