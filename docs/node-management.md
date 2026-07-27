# 节点、策略组与订阅

## 手动与批量导入

支持 SS、VMess、VLESS、Trojan、Hysteria2/Hy2、SOCKS5、HTTP(S) 分享链接。批量导入流程：

1. 在内存中解析全部非空行。
2. 验证协议、服务器、端口和凭据字段。
3. 为整批生成稳定数据库输入。
4. 使用 SQLite 事务保存；任一解析或写入失败时整批不变。
5. API 只返回脱敏节点字段和 `hasCredential`。

分享链接是敏感材料，不进入日志、审计或发布包。TCP probe 会拒绝 private、loopback、link-local、multicast 目标，避免把节点探测变成内网扫描器。

## 策略组

支持 `select`、`url-test`、`fallback`、`load-balance` 和 `chain`。组成员使用 node/group ID，生成时验证引用和组环。

chain 只允许至少两个有序节点且不能包含子组。UI 顺序是 RouterOS -> 第 1 跳 -> 第 2 跳 -> ... -> Internet。生成器复制 hop；从第 2 跳开始，每一跳的 `dialer-proxy` 指向前一跳，最终选择组只暴露最后一跳，因此最后一个节点是真实出口。两跳和三跳用例会校验生成 YAML 与 UI 顺序一致。

## 发布边界

保存、导入或删除节点/组只改变 SQLite，不会暗中热重载 Mihomo。运行配置必须在运维页完成 preview、脱敏 Diff、确认和 apply。仍被其他组、设备策略或订阅引用的节点/组不能删除。

## 订阅

订阅创建后先 preview，再提交确认 update。抓取失败、内容 digest 在确认后变化或数据库更新失败时保留上一份节点集。启用的订阅由调度器按秒级 interval 检查到期，并用持久任务更新；长时间未成功会产生过期告警。

订阅安全限制见 [API 参考](api-reference.md#订阅)。
