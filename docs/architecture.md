# FoxOS 架构

## 运行拓扑

- RouterOS 宿主：示例 `10.0.0.1`
- Mihomo Container：示例 `10.0.0.2`
- MosDNS Container：示例 `10.0.0.3`
- FoxOS Container：示例 `10.0.0.4`

FoxOS 后端通过 RouterOS REST API、Mihomo Controller API 和挂载配置文件完成管理。MosDNS 第一阶段仅采集状态。

## 核心分层

1. Web UI：统一操作界面。
2. API：认证、参数校验和任务状态。
3. Domain：节点、策略组、设备、出口、L2TP 和期望状态。
4. Reconciler：比较期望状态与实际状态。
5. Adapters：RouterOS、Mihomo、MosDNS。
6. Storage：SQLite、配置快照和审计日志。

## Mihomo 配置闭环

1. 将现有配置导入 SQLite。
2. 用户修改数据库中的期望配置。
3. 生成临时 YAML。
4. 执行结构和协议字段校验。
5. 调用 Mihomo 校验能力。
6. 备份正式配置。
7. 原子替换并热重载。
8. 通过 Controller API 验证。
9. 失败时恢复快照。

## RouterOS 配置闭环

FoxOS 只管理带 `foxos:` 注释或标识的资源，不接管用户已有规则。

1. 读取当前 RouterOS 状态。
2. 生成操作计划并展示影响。
3. 创建 RouterOS 备份。
4. 逐步应用操作。
5. 验证 API、默认路由和容器连通性。
6. 失败时执行补偿操作或恢复备份。

## 后台任务

延迟检测、订阅更新、配置重载、设备策略同步和全链路检测均通过任务执行器运行。API 返回任务 ID，前端显示进度、错误和回滚结果。

## 数据边界

- SQLite：期望配置、设备策略、任务和审计。
- Mihomo YAML：由生成器输出，不作为主要编辑入口。
- RouterOS：保存真实网络状态。
- Secret：凭据独立存储，不进入日志和数据库导出。
