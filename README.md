# FoxOS

FoxOS 是面向 RouterOS + Mihomo + MosDNS 的一体化网络管理后台。

## 目标

- FoxOS、Mihomo、MosDNS 均运行在 RouterOS Container 中
- 统一管理 RouterOS 设备、路由、L2TP 与 Mihomo 节点
- MosDNS 第一阶段保持只读，不修改 DNS
- 所有修改遵循：备份 → 生成 → 验证 → 应用 → 检查 → 失败回滚

## 计划功能

- RouterOS 系统、接口、路由和 DHCP 状态
- Mihomo 节点新增、删除、订阅导入和延迟检测
- select、url-test、fallback、load-balance 策略组
- 链式代理
- RouterOS 原生 L2TP
- 设备静态 IP 绑定
- 设备直连、单节点、代理链和 L2TP 出口策略
- 操作任务、日志、备份和恢复
- amd64/arm64 RouterOS Container 安装包

## 项目状态

当前处于核心后端重构阶段。Web UI 已合并，真实设备控制接口正在开发。未接通的功能不会伪装成功。

## 架构

详见 [docs/architecture.md](docs/architecture.md)。

## RouterOS 部署

详见 [docs/routeros-install.md](docs/routeros-install.md)。

## 上游项目说明

Mihomo 配置管理部分借鉴 ClashManager 的成熟设计。详见 [docs/clashmanager-origin.md](docs/clashmanager-origin.md)。

## 安全原则

- 凭据不提交到 GitHub
- FoxOS 只修改带有 `foxos:` 标识的 RouterOS 规则
- Mihomo 配置修改前自动创建快照
- 应用后执行健康检查，失败自动恢复
- DNS 第一阶段只读
