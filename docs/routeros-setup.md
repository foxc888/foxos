# RouterOS 连接配置

FoxOS 第一阶段通过 RouterOS REST API 读取状态，不使用 SSH 拼接命令。

## RouterOS 准备

建议为 FoxOS 创建独立的 RouterOS 用户和最小权限组。开发阶段先启用只读权限，确认资源、接口、DHCP 和 ARP 均可读取后，再单独授权设备绑定、路由和 L2TP 写入能力。

REST API 地址示例：

- HTTP：`http://10.0.0.1`
- HTTPS：`https://10.0.0.1`

生产环境推荐 HTTPS。

## 当前只读端点

- `/rest/system/resource`
- `/rest/interface`
- `/rest/ip/dhcp-server/lease`
- `/rest/ip/arp`

代码内使用固定允许列表，不能把任意 URL 路径转发给 RouterOS。

## 设备识别

FoxOS 使用 MAC 地址合并 DHCP Lease 与 ARP：

- DHCP 提供主机名、租约状态和地址
- ARP 提供当前接口和在线邻居状态
- MAC 地址统一转为大写格式
- 静态绑定和出口策略后续均以 MAC + 稳定设备 ID 关联

## 凭据

RouterOS 用户名和密码只从 FoxOS Secret 配置读取，不提交到 GitHub，也不通过状态 API 返回。
