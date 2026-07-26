# 操作确认与计划执行

RouterOS 写入采用“预览”和“执行”分离接口。

## 确认令牌

预览接口根据完整计划生成短期 HMAC 令牌。令牌绑定：

- 操作方法
- RouterOS REST 路径
- 请求内容
- FoxOS 所有权标识
- 过期时间

任何字段发生变化后，原令牌立即失效。令牌最长有效 15 分钟，正式 UI 默认使用 5 分钟。

## 执行器限制

静态绑定执行器仅允许：

- `PUT /rest/ip/dhcp-server/lease`
- `PATCH /rest/ip/dhcp-server/lease/*ID`

同时必须满足：

- comment 以 `foxos:device:` 开头
- 请求体 comment 与计划所有权一致
- 计划非空且明确要求确认
- 确认令牌未过期
- 确认令牌对应的计划没有变化

当前实现使用模拟 Writer 测试安全边界。真实 RouterOS Writer 接入后仍受同一验证器约束。
