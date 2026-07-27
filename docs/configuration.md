# FoxOS 运行配置

FoxOS 只从进程环境读取凭据与依赖端点。真实 Token、密码、节点链接和 RouterOS 导出不得进入仓库、镜像、日志或截图。

## 必填密钥

| 变量 | 约束 |
|---|---|
| `FOXOS_API_TOKEN` | 至少 32 字符，无首尾空白 |
| `FOXOS_CONFIRMATION_KEY` | 至少 32 字符，无首尾空白，必须与 API Token 不同 |

API Token 用于 Bearer 认证；确认密钥仅用于签署高风险计划。任一缺失或不合格时服务拒绝启动。

## RouterOS

| 变量 | 全量包值 |
|---|---|
| `FOXOS_ROUTEROS_URL` | `http://10.0.0.1` |
| `FOXOS_ROUTEROS_USERNAME` | `foxos-service` |
| `FOXOS_ROUTEROS_PASSWORD` | 首次安装随机生成 |

URL 为空时 RouterOS 显示 `configured=false, online=false`，写入 API 不可用。配置 URL 后用户名和密码都必填。

## Mihomo

| 变量 | 全量包值 | 说明 |
|---|---|---|
| `FOXOS_MIHOMO_URL` | `http://10.0.0.2:9090` | Controller |
| `FOXOS_MIHOMO_PROXY_URL` | `http://10.0.0.2:7890` | 当前策略出口探测 |
| `FOXOS_MIHOMO_SECRET` | 首次安装随机生成 | 32 到 4096 字符 |
| `FOXOS_MIHOMO_LOCAL_CONFIG` | `/data/mihomo/config.yaml` | FoxOS 挂载内路径 |
| `FOXOS_MIHOMO_RUNTIME_CONFIG` | `/root/.config/mihomo/config.yaml` | Controller reload 路径 |
| `FOXOS_MIHOMO_BACKUP_DIR` | `/backups/mihomo` | 配置快照 |

Controller 一旦配置，Secret、两个配置路径和备份目录必须完整。local 与 runtime path 可以不同，但必须由挂载指向同一个实际文件。

## MosDNS 与管理备份

| 变量 | 全量包值 |
|---|---|
| `FOXOS_MOSDNS_URL` | `http://10.0.0.3:53` |
| `FOXOS_BACKUP_DIR` | `/backups/foxos` |

MosDNS 客户端只使用 URL 的 host/port 做 TCP 连接检查，不调用写入接口。备份目录未设置时本地开发默认使用 `backups`。

## 端点安全

RouterOS、Mihomo、Mihomo proxy 和 MosDNS URL：

- 只允许 `http` 或 `https`。
- host 必须是 private、loopback 或 link-local IP 字面量。
- 禁止 hostname、`.local`、公网 IP 和 URL userinfo。
- 客户端禁用重定向并设置 8 到 10 秒超时。
- HTTPS 使用系统信任链，不跳过证书验证。

全量 RouterOS 安装器维护精确 14 项 `foxos-env` 允许列表。已有 marker 的安装可补齐缺项，但遇到未知额外键或固定值不一致会失败关闭，不自动覆盖。

## 浏览器 Token

设置页仅把 API Token 保存在当前页面内存，刷新或关闭后清除。不要依赖 Web Storage 持久登录；当前版本也没有多用户、角色或服务端会话。
