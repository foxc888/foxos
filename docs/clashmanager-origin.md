# ClashManager 借鉴与来源说明

FoxOS 的 Mihomo 配置管理设计参考：

- 项目：https://github.com/qianfree/ClashManager
- 上游许可证：MIT

## 借鉴范围

- Go Web 后端分层
- SQLite 数据模型与迁移
- 节点、规则和策略组管理
- 分享链接批量导入
- Mihomo 配置生成和验证
- 管理员认证
- 前端静态资源嵌入单个可执行文件

## FoxOS 新增范围

- RouterOS REST API 适配
- DHCP 静态绑定
- 设备出口策略
- RouterOS 原生 L2TP
- Mihomo Controller 运行时状态
- 链式代理编排
- 事务、任务、备份、验证与回滚
- RouterOS Container 安装和多架构发布
- FoxOS 独立 UI

## 不纳入第一阶段

- DNS 编辑
- 对外订阅分发
- Sing-box 管理

后续若直接复用上游代码文件，将在对应文件保留版权声明，并在发布包中附带上游 MIT 许可证。
