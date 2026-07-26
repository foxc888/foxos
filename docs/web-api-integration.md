# Web UI 与 FoxOS API 接线

本页说明 FoxOS Web UI 如何读取真实后端，以及页面何时显示演示数据。

## 数据模式

顶部状态栏明确显示：

- **连接中**：浏览器正在请求 FoxOS API。
- **实时数据**：RouterOS、Mihomo、节点、L2TP 和审计接口已完成读取。
- **演示数据**：未配置 API Token、API 不可达、认证失败或任一必要接口失败。

演示模式不会被标记为真实在线状态。UI 保留演示内容是为了安装前预览布局，不代表 RouterOS 或代理链路已经工作。

## 首次连接

1. 启动 FoxOS 后端。
2. 打开 Web UI 的“设置”。
3. 在“FoxOS API”中填写至少 32 个字符的 `FOXOS_API_TOKEN`。
4. Token 只保存在当前浏览器的 Local Storage，不写入源码、GitHub 或 URL。
5. 返回总览，点击“运行全链路检测”。

生产部署时 Web UI 与 Go 后端同源，所有请求使用相对路径 `/api/v1/...`。本地开发时 Vite 只把 `/api` 转发到固定的 `127.0.0.1:8090`，不支持把 Token 转发到任意远端。

## 已接入接口

| 页面 | 接口 | 当前行为 |
|---|---|---|
| 总览 | RouterOS/Mihomo overview | 判断控制面是否在线 |
| 代理节点 | `GET /api/v1/nodes` | 读取 SQLite 节点 |
| 代理节点 | `DELETE /api/v1/nodes/{id}` | 删除普通 Mihomo 节点 |
| 代理节点 | `GET /api/v1/routeros/l2tp` | 合并显示 RouterOS 原生 L2TP，只读 |
| 设备管理 | RouterOS overview devices | 显示 DHCP 与 ARP 合并设备清单 |
| 日志 | `GET /api/v1/audit-events` | 显示持久化操作审计 |

L2TP 不通过 Mihomo 节点 API 删除。设备“固定 IP”和“应用出口”仍保持预览交互，下一阶段接入计划确认与执行 API。

## 尚未接入

- 节点新增/编辑时的完整协议字段提交
- 机场订阅和 Sub-Store
- 节点延迟、丢包、落地 IP 与地区检测
- 设备静态绑定计划确认界面
- 设备出口路由执行
- 链式代理保存与 Mihomo 应用
- MosDNS 状态适配器

## DNS 边界

此阶段不会修改 RouterOS DNS、DHCP 下发 DNS、Mihomo DNS 或 MosDNS 配置。MosDNS 页面仍然是只读展示。
