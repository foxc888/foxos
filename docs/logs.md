# 操作审计

FoxOS 对 RouterOS 写操作保存持久化审计记录。

## 状态

- `STARTED`：已完成认证、计划校验和重放检查，即将写入
- `SUCCEEDED`：RouterOS 写入及回读验证均成功
- `FAILED`：写入或回读验证失败

同一次执行使用相同审计 ID 更新状态，不重复创建无关记录。

## 保存内容

- 操作类型
- 目标设备策略 ID
- 操作数量
- 结果
- 错误分类
- 创建和更新时间

## 不保存

- 确认令牌
- FoxOS API Token
- RouterOS 用户名和密码
- Mihomo Secret
- 节点密码或 UUID
- 完整认证请求头

## 查询接口

`GET /api/v1/audit-events?limit=100`

要求 Bearer Token。limit 范围为 1–500。
