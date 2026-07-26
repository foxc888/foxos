# FoxOS 运行配置

FoxOS 使用容器环境变量接收连接信息。真实密码和 Token 不写入 GitHub。

## 必填

| 变量 | 说明 |
|---|---|
| `FOXOS_API_TOKEN` | FoxOS API 令牌，至少 32 个字符 |

## RouterOS

| 变量 | 示例 |
|---|---|
| `FOXOS_ROUTEROS_URL` | `https://10.0.0.1` |
| `FOXOS_ROUTEROS_USERNAME` | `foxos` |
| `FOXOS_ROUTEROS_PASSWORD` | 运行时密钥 |

如果不填写 RouterOS URL，状态 API返回 `configured: false`。

## Mihomo

| 变量 | 示例 |
|---|---|
| `FOXOS_MIHOMO_URL` | `http://10.0.0.2:9090` |
| `FOXOS_MIHOMO_SECRET` | Controller secret |
| `FOXOS_MIHOMO_LOCAL_CONFIG` | FoxOS 中挂载路径 |
| `FOXOS_MIHOMO_RUNTIME_CONFIG` | Mihomo 容器内配置路径 |
| `FOXOS_MIHOMO_BACKUP_DIR` | FoxOS 备份目录 |

本地挂载路径和 Mihomo 容器路径允许不同，但必须指向同一配置文件。

## 状态接口

- `GET /api/v1/routeros/overview`
- `GET /api/v1/mihomo/overview`

两个接口均要求：

```http
Authorization: Bearer <FOXOS_API_TOKEN>
```

响应不包含 RouterOS 密码、Mihomo Secret 或节点凭据。
