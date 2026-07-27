# Web UI 与 FoxOS API 状态语义

Web UI 只显示 API 返回或浏览器实际完成的检查，不内置会被当作实时状态的演示设备、节点或日志。

## 独立数据源

首轮先读取公开站点清单，再并行请求：

- RouterOS overview、routes、DHCP servers、containers。
- Mihomo overview、MosDNS overview。
- 节点、L2TP、设备库存、设备策略、代理组、审计。
- 出口 capability、DHCP 地址规划、任务、告警和订阅。

每项维护独立状态：

| 状态 | 含义 |
|---|---|
| `loading` | 正在请求，不推断在线 |
| `live` | 该接口本次成功返回；服务是否在线仍取决于响应中的 `online` |
| `stale` | 曾有数据，但刷新失败或超过 120 秒 |
| `unavailable` | 尚无可用数据且请求失败 |

单个接口失败只影响对应区域。旧数据可以在 `stale` 状态下供排障参考，但不能显示为实时或在线。每个资源区域显示 API 来源、最后成功时间和错误。

## Token 生命周期

设置页要求至少 32 字符的 `FOXOS_API_TOKEN`。Token：

- 只保存在当前 JavaScript 页面内存。
- 不写入 URL、SQLite、`localStorage` 或 `sessionStorage`。
- 刷新或关闭标签页后清除，需要重新输入。
- 旧版本遗留在 `sessionStorage` 的值只迁移一次，并同时删除两个 Web Storage 中的旧键。

生产部署中 Web 与 API 通过 `https://<site-hostname>` 同源，业务请求使用相对路径 `/api/v1/...`；LAN HTTP 只跳转，内部 8090 仅 loopback。Vite 开发服务器只把 `/api` 转发到本地开发后端 `127.0.0.1:8090`。

## 导航与可访问性

页面使用 Hash 路由，例如：

```text
/#/overview
/#/devices
/#/proxies
/#/operations
```

直接打开深链接、浏览器前进和后退都会同步视图。切换视图后主标题获得程序化焦点，移动菜单关闭后焦点回到菜单按钮。

交互约束：

- 全站统一 `:focus-visible`。
- Dialog 使用 `role=dialog`、`aria-modal`、标题关联、Tab 焦点锁定、Escape 关闭和焦点恢复。
- 状态和操作结果通过 `aria-live` 发布。
- 设备与节点表格支持方向键、Home、End、Enter 和 Space。
- 表单使用可见 label；图标按钮有可访问名称。
- 390px、平板和桌面布局不产生页面级水平滚动。

## 写操作

前端不会在本地假定成功：

- 节点、组、设备资料保存后等待对应 API 成功。
- 保存节点或链式组只改变 SQLite，通知明确说明 Mihomo 运行配置尚未发布。
- 固定 IP、DHCP 扩容、出口策略、Mihomo 发布/恢复、订阅更新/删除和备份恢复都先显示后端计划、影响和警告。
- 高风险 Dialog 要求影响确认；发布类操作轮询持久任务，以 `SUCCEEDED`、`FAILED` 或 `ROLLED_BACK` 为最终结果。
- API 失败、任务失败或回滚不会显示成功 toast。

## 探测语义

| 显示项 | 证明范围 |
|---|---|
| TCP 可达 | FoxOS 到节点地址端口完成 TCP 建连 |
| Mihomo 节点 HTTP | Controller 对指定节点完成 HTTP delay check |
| 当前策略出口 | 通过 Mihomo mixed port 请求外部出口检查，作用域是当前策略 |
| L2TP 会话 | RouterOS 报告 client running 且未禁用 |
| MosDNS 在线 | FoxOS 到 MosDNS TCP 53 建连成功 |

这些结果不会被合并成一个笼统的“节点在线”。mixed-port 出口不证明设备透明流量，Mihomo 节点/链 readiness 当前保持不可用。当前版本不声称提供 UDP 丢包、抖动或完整 DNS 查询质量测试。

## 自动化覆盖

Vitest 覆盖站点清单、API 并行降级、Token 生命周期、状态转换、错误引用、策略状态与 Dialog。Playwright 在 1440x900、834x1112、390x844 项目中逐元素检查裁切、桌面/移动交互、键盘、焦点锁定、危险确认、Mihomo 发布成功和回滚。
