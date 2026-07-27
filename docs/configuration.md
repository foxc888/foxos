# FoxOS 运行配置

FoxOS 只从进程环境读取凭据、站点和依赖端点。真实 Token、密码、节点链接、证书私钥和 RouterOS 导出不得进入仓库、镜像、日志或截图。

## 必填密钥

| 变量 | 约束 |
|---|---|
| `FOXOS_API_TOKEN` | 至少 32 字符，无首尾空白 |
| `FOXOS_CONFIRMATION_KEY` | 至少 32 字符，无首尾空白，必须与 API Token 不同 |

API Token 用于 Bearer 认证；确认密钥用于签署高风险计划，并通过带领域前缀的 HMAC-SHA256 保护对外可见的 Mihomo 配置/快照 digest，避免节点密码或 UUID 被裸摘要用于离线猜测。任一缺失或不合格时服务拒绝启动。不要在 Mihomo 任务运行中轮换确认密钥；轮换会使旧确认令牌、运行中任务 digest 和旧发布快照无法验证，必须在维护窗口重新预览并发布配置生成新快照。

## 站点清单

RouterOS 全量包由 `site-config.rsc` 生成以下字段。进程环境必须八项全设或全不设；全不设只用于本地开发并采用默认样例。

| 变量 | 默认样例 |
|---|---|
| `FOXOS_SITE_MANAGEMENT_BRIDGE` | `bridge-lan` |
| `FOXOS_SITE_STORAGE_ROOT` | `disk1` |
| `FOXOS_SITE_NETWORK` | `10.0.0.0/24` |
| `FOXOS_SITE_ROUTER_ADDRESS` | `10.0.0.1` |
| `FOXOS_SITE_MIHOMO_ADDRESS` | `10.0.0.2` |
| `FOXOS_SITE_MOSDNS_ADDRESS` | `10.0.0.3` |
| `FOXOS_SITE_FOXOS_ADDRESS` | `10.0.0.4` |
| `FOXOS_SITE_PUBLIC_HOSTNAME` | `foxos.home.arpa` |

网段必须是 canonical IPv4 CIDR；四个地址必须唯一、位于网段可用主机范围；hostname 必须是小写 `home.arpa` 子域。RouterOS/Mihomo/MosDNS 端点必须与清单地址及固定服务端口一致，否则服务拒绝启动。公开 `GET /api/v1/site` 返回实际清单、受保护地址、服务 URL、HTTPS 状态和 CA 指纹。

## RouterOS

| 变量 | 全量包值 |
|---|---|
| `FOXOS_ROUTEROS_URL` | `http://<site-router-address>` |
| `FOXOS_ROUTEROS_USERNAME` | `foxos-service` |
| `FOXOS_ROUTEROS_PASSWORD` | 首次安装随机生成 |

URL 为空时 RouterOS 显示 `configured=false, online=false`，所有 RouterOS 写入不可用。配置 URL 后用户名和密码都必填。

## Mihomo

| 变量 | 全量包值 | 说明 |
|---|---|---|
| `FOXOS_MIHOMO_URL` | `http://<site-mihomo-address>:9090` | Controller |
| `FOXOS_MIHOMO_PROXY_URL` | `http://<site-mihomo-address>:7890` | 当前策略 mixed-port 出口探测 |
| `FOXOS_MIHOMO_SECRET` | 首次安装随机生成 | 32 到 4096 字符 |
| `FOXOS_MIHOMO_BASE_CONFIG` | `/data/mihomo/base.yaml` | 可信运行基线 |
| `FOXOS_MIHOMO_LOCAL_CONFIG` | `/data/mihomo/config.yaml` | FoxOS 可写路径 |
| `FOXOS_MIHOMO_RUNTIME_CONFIG` | `/root/.config/mihomo/config.yaml` | Controller reload 路径 |
| `FOXOS_MIHOMO_BACKUP_DIR` | `/backups/mihomo` | 配置快照 |
| `FOXOS_MIHOMO_VALIDATOR_BINARY` | `/usr/local/bin/mihomo` | 真实语义校验 |

Controller 一旦配置，上述 secret、base/runtime/local、validator 和 backup 必须完整。base 是每次生成的可信结构化合并基线；FoxOS 管理的节点、组、设备规则和草稿字段不能删除 `external-controller`、`secret`、`bind-address`、`external-ui`、TUN、DNS、日志等运行基础。mixed port 出口探测只代表当前策略的显式代理请求，不证明 RouterOS 客户端透明代理可用。

## MosDNS 与备份

| 变量 | 全量包值 |
|---|---|
| `FOXOS_MOSDNS_URL` | `http://<site-mosdns-address>:53` |
| `FOXOS_BACKUP_DIR` | `/backups/foxos` |
| `FOXOS_UPGRADE_STATE_PATH` | 默认位于数据库同目录 | 升级检查点状态 |

MosDNS 客户端只使用 URL host/port 做 TCP 连接检查，不调用写入接口。MosDNS 配置把未认证的 9099 API 固定到 `127.0.0.1`，仅供容器内规则插件使用；发布包不包含未使用的第三方管理 UI。备份目录未设置时本地开发默认 `backups`。

## HTTPS

RouterOS 全量包设置 `FOXOS_HTTPS_ENABLED=true`。可选覆盖仅用于受控部署：

| 变量 | HTTPS 默认值 | 约束 |
|---|---|---|
| `FOXOS_TLS_DIR` | `/data/tls` | 持久 CA/叶证书目录，不接受 partial/symlink material |
| `FOXOS_INTERNAL_LISTEN` | `127.0.0.1:8090` | 必须是 loopback 8090 |
| `FOXOS_HTTPS_LISTEN` | `:443` | 必须使用 443 |
| `FOXOS_HTTP_REDIRECT_LISTEN` | `:80` | 必须使用 80 |

首次启动生成 10 年本地 CA 和 397 天叶证书；叶证书剩余不足 30 天时由同一 CA 续签。CA 不会自动轮换或自动分发信任。LAN HTTP 只跳转，Bearer API 要求真实 HTTPS；内部反向代理使用每进程随机凭据，客户端转发头不能代替它。

本地开发未设置 `FOXOS_HTTPS_ENABLED` 时可使用 `-listen :8090` 普通 HTTP，但不得把这种开发模式用于承载 LAN Bearer Token。

## 端点安全

RouterOS、Mihomo、Mihomo proxy 和 MosDNS URL：

- 只允许 `http` 或 `https`。
- host 必须是 private、loopback 或 link-local IP 字面量。
- 禁止 hostname、`.local`、公网 IP 和 URL userinfo。
- 客户端禁用重定向并设置超时。
- HTTPS 使用系统信任链，不跳过证书验证。

全量 RouterOS 安装器维护精确 25 键 `foxos-env` allowlist。已有 marker 的安装可补齐缺项，但未知额外键或固定值不一致会失败关闭，不自动覆盖。

## 浏览器 Token

设置页只把 API Token 保存在当前页面内存，刷新或关闭后清除。不要依赖 Web Storage 持久登录；当前版本没有多用户、角色或服务端会话。
