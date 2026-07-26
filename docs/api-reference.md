# FoxOS API 速查

除健康接口外，请求均需 `Authorization: Bearer <FOXOS_API_TOKEN>`。

## 节点

| 方法 | 路径 | 说明 |
|---|---|---|
| GET/POST | `/api/v1/nodes` | 列表、手动新增 |
| GET/PUT/DELETE | `/api/v1/nodes/{id}` | 读取、替换、删除 |
| POST | `/api/v1/nodes/import` | 多行分享链接原子导入 |
| POST | `/api/v1/nodes/{id}/probe` | 从 FoxOS 容器执行 TCP 可达性探测 |

导入支持 SS、VMess、VLESS、Trojan、Hysteria2、SOCKS5、HTTP(S) 分享链接。任意一条解析或保存失败，整批不写入。读取接口不会返回密码、UUID 或完整凭据。

探测只验证目标主机端口的 TCP 建连，不等同于完整代理握手、带宽测试或出口 IP 验证。

## 策略组

| 方法 | 路径 | 说明 |
|---|---|---|
| GET/POST | `/api/v1/proxy-groups` | 列表、新增 |
| PUT/DELETE | `/api/v1/proxy-groups/{id}` | 替换、删除 |

支持 `select`、`url-test`、`fallback`、`load-balance`。成员以稳定 node/group ID 引用。

## 状态和只读资源

- `GET /api/v1/routeros/overview`
- `GET /api/v1/mihomo/overview`
- `GET /api/v1/routeros/l2tp`
- `GET /api/v1/audit-events?limit=100`

L2TP 输出不会包含密码。MosDNS 在当前阶段不提供写入 API。

## 设备与确认执行

- `GET/POST /api/v1/device-policies`
- `GET/PUT/DELETE /api/v1/device-policies/{id}`
- `POST /api/v1/routeros/plans/device-binding`
- `POST /api/v1/routeros/plans/device-binding/execute`

前端先请求 plan，展示 operations 与 warnings，用户确认后原样提交 plan 和 confirmationToken。任何字段发生变化都必须重新生成计划。令牌五分钟过期、仅能使用一次。

示例：

```json
{
  "id": "device-phone",
  "name": "Phone",
  "macAddress": "AA:BB:CC:DD:EE:FF",
  "staticIp": "192.168.88.21",
  "dhcpServer": "bridge-lan",
  "egress": "direct"
}
```

当前执行器只应用静态 DHCP Lease。出口字段进入期望状态模型，但 RouterOS 策略路由执行器尚未开放，前端不得把仅保存模型显示为“路由已生效”。
