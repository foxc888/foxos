# RouterOS Container 安装说明

> 当前文档描述最终部署结构。正式镜像发布后会补充可直接复制的命令和版本校验值。

## 前置条件

- RouterOS v7
- 已安装并启用 Container 功能
- 支持的 CPU：amd64 或 arm64
- 建议使用独立存储保存容器和数据
- RouterOS 能访问 Mihomo、MosDNS 与 FoxOS 容器地址

## 推荐地址

| 组件 | 示例地址 | 用途 |
|---|---|---|
| RouterOS | 10.0.0.1 | 宿主和 REST API |
| Mihomo | 10.0.0.2 | 代理与 Controller |
| MosDNS | 10.0.0.3 | DNS，只读接入 |
| FoxOS | 10.0.0.4 | Web 管理后台 |

地址仅为示例，正式安装脚本会先检测冲突。

## 持久化目录

- `foxos/config`：运行配置
- `foxos/data`：SQLite 数据库
- `foxos/backups`：Mihomo 与 RouterOS 快照
- `foxos/logs`：操作与诊断日志

升级时只替换容器 root-dir，不删除这些挂载目录。

## 安装流程

1. 检查 RouterOS 版本、架构、Container 和存储空间。
2. 上传对应架构的 `foxos_*.tar`。
3. 创建 veth 并加入容器 bridge。
4. 配置 FoxOS 固定地址。
5. 创建持久化 mounts。
6. 导入并启动容器。
7. 访问 FoxOS 初始化页面。
8. 配置 RouterOS 和 Mihomo 连接。
9. 执行只读检测。
10. 用户确认后才启用写入能力。

## DNS 边界

安装脚本不会修改：

- RouterOS DNS
- DHCP 下发 DNS
- Mihomo DNS
- MosDNS 配置

DNS 第一阶段只显示运行状态。

## 卸载原则

停止并删除 FoxOS Container 后，保留数据目录和备份。FoxOS 创建的 RouterOS 资源均带 `foxos:` 标识，可通过卸载向导单独清理，不影响用户原有规则。
