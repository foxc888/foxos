# RouterOS 原生 L2TP

FoxOS 使用 RouterOS 原生 `interface/l2tp-client`，不在 FoxOS 容器内运行另一套 L2TP 客户端。

## 当前只读能力

接口：

`GET /api/v1/routeros/l2tp`

返回：

- RouterOS ID
- 名称
- 服务器地址
- 用户名
- 运行/禁用状态
- 是否添加默认路由
- 默认路由距离
- 是否使用对端 DNS
- PPP Profile
- 是否由 FoxOS 管理

## 凭据边界

RouterOS REST 响应中的 password 字段不会进入 FoxOS L2TP 模型，也不会通过 API 或日志返回。后续编辑接口中密码将保持只写。

## 所有权

FoxOS 管理的 L2TP 使用：

`foxos:l2tp:<stable-id>`

没有该 comment 的现有 L2TP 连接只读展示，默认不能修改或删除。

## 暂未开放的 L2TP CRUD

当前没有新增、编辑或删除 L2TP client 的 API。未来如开放，必须遵循：

1. 读取现有 L2TP。
2. 生成新增或修改计划。
3. 显示服务器、路由和 DNS 影响。
4. HMAC 确认。
5. 受控写入。
6. 回读运行状态。
7. 失败时恢复原配置。

DNS 第一阶段不允许自动开启 `use-peer-dns`。
