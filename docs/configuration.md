# FoxOS 运行配置

FoxOS 的非敏感配置从进程环境读取。`development` 可从对应 `FOXOS_*` 环境变量读取四项运行秘密；`production` 只读取固定的 `/run/secrets/foxos/*` 文件，并在发现任一敏感环境变量（包括空值）时拒绝启动。真实 Token、密码、节点链接、证书私钥和 RouterOS 导出不得进入仓库、镜像、bundle、日志或截图。

## 运行秘密

| 用途 | 生产文件 | 开发环境变量 |
|---|---|---|
| API Token | `/run/secrets/foxos/api-token` | `FOXOS_API_TOKEN` |
| 确认密钥 | `/run/secrets/foxos/confirmation-key` | `FOXOS_CONFIRMATION_KEY` |
| RouterOS 服务密码 | `/run/secrets/foxos/routeros-password` | `FOXOS_ROUTEROS_PASSWORD` |
| Mihomo Controller Secret | `/run/secrets/foxos/mihomo-secret` | `FOXOS_MIHOMO_SECRET` |

四个值都必须是 `32..4096` 字节、无空白的可打印 ASCII，且彼此不同。生产 secret 目录只能包含表中四个固定文件；任一文件缺失、重复、无效或出现额外文件时都失败关闭。API Token 用于非浏览器 Bearer 认证，并由 Web UI 在同源登录时一次性换取浏览器会话；确认密钥用于签署高风险计划，并通过带领域前缀的 HMAC-SHA256 保护对外可见的 Mihomo 配置/快照 digest，避免节点密码或 UUID 被裸摘要用于离线猜测。服务还从确认密钥派生独立的 Mihomo apply 完整性密钥：配置内容 HMAC 与 journal envelope HMAC 使用不同领域，后者覆盖所有恢复决策字段，包括操作身份、目标 digest、期望 snapshot label、备份路径、阶段和创建时间。任一必填秘密缺失或不合格时服务拒绝启动。不要在 Mihomo 任务运行中或 pending journal 存在时轮换确认密钥；轮换会使旧确认令牌、运行中任务 digest、旧发布快照和未完成恢复 envelope 无法验证，必须先完成/回滚 pending 操作，再在维护窗口重新预览并发布配置生成新快照。

`FOXOS_ENV` 只接受 `development` 或 `production`。未设置时仅允许 HTTP 绑定 loopback；正式包固定为 `production`，并强制启用 HTTPS、使用持久绝对 backup/TLS 路径和非临时 SQLite 文件；不满足时启动失败。

## 站点清单

RouterOS 全量包提供不可变 `site-config.example.rsc`；操作者复制为唯一 `site-config.rsc`，编辑后用 `seal-site-config.sh` 生成独立 SHA-512。可编辑清单不得直接 import；固定的 `load-site-config.rsc` 会先校验摘要与 11 项赋值白名单，preflight 再核对当前内容与 loader 证明，然后由安装器生成以下非敏感环境。八项 `FOXOS_SITE_*` 必须全设或全不设；全不设只用于本地开发并采用默认样例。

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
| `FOXOS_ROUTEROS_PASSWORD` | `/run/secrets/foxos/routeros-password`；仅开发模式使用同名 env |

URL 为空时 RouterOS 显示 `configured=false, online=false`，所有 RouterOS 写入不可用。配置 URL 后用户名和密码都必填。

## Mihomo

| 变量 | 全量包值 | 说明 |
|---|---|---|
| `FOXOS_MIHOMO_URL` | `http://<site-mihomo-address>:9090` | Controller |
| `FOXOS_MIHOMO_PROXY_URL` | `http://<site-mihomo-address>:7890` | 当前策略 mixed-port 出口探测 |
| `FOXOS_MIHOMO_SECRET` | `/run/secrets/foxos/mihomo-secret`；仅开发模式使用同名 env | 32 到 4096 字节 |
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

本地开发未设置 `FOXOS_HTTPS_ENABLED` 时默认只监听 `127.0.0.1:8090`。如需显式开发环境，设置 `FOXOS_ENV=development`；即使如此也应保持 `-listen 127.0.0.1:8090`，不得用普通 HTTP 在 LAN 传输 Bearer Token。

## 端点安全

RouterOS、Mihomo、Mihomo proxy 和 MosDNS URL：

- 只允许 `http` 或 `https`。
- host 必须是 private、loopback 或 link-local IP 字面量。
- 禁止 hostname、`.local`、公网 IP 和 URL userinfo。
- 客户端禁用重定向并设置超时。
- HTTPS 使用系统信任链，不跳过证书验证。

订阅抓取默认只允许 HTTPS 443 公共目标。`FOXOS_SUBSCRIPTION_PRIVATE_CIDRS` 可设置最多 32 个逗号分隔、无空格、canonical 的 RFC1918 或 IPv6 ULA 前缀；它只开放明确网段，不能开放 loopback、link-local、multicast、unspecified 或 metadata 类地址。连接与每次重定向都会重新解析并重新检查，以拒绝 DNS rebinding。

全量 RouterOS 安装器维护精确 `23/24` 键非敏感 `foxos-env` 基线：站点清单未配置私网订阅 allowlist 时为 23 键，配置非空 allowlist 时为 24 键。production 中四个敏感环境变量必须为零绑定；已有 marker 的安装可补齐非敏感缺项，但未知额外键、敏感键或固定值不一致会失败关闭，不自动覆盖。

## 浏览器 Token

设置页把 API Token 发送到同源 session 端点后立即从页面内存清除。服务端签发 8 小时随机会话：生产使用 Secure、HttpOnly、SameSite=Strict、`__Host-` Cookie，CSRF 值只在页面内存；刷新会用 Cookie 恢复，服务重启、过期或主动退出后失效。管理 Token、会话值和 CSRF 都不写入 Web Storage。当前版本没有 JWT、多用户、角色、持久会话或跨实例共享。
