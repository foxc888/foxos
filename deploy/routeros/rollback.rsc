# 回滚模板：停止新容器并恢复上一个保留槽位。
:local active [/container find where comment="foxos:active"]
:local rollback [/container find where comment="foxos:rollback"]
:if ([:len $active] != 1) do={ :error "找不到唯一 foxos:active 容器" }
:if ([:len $rollback] != 1) do={ :error "找不到唯一 foxos:rollback 容器" }
/container/stop $active
:delay 3s
/container/set $active comment="foxos:failed"
/container/set $rollback comment="foxos:active" start-on-boot=yes
/container/start $rollback
:put "已启动回滚槽位；请检查 http://10.0.0.4:8090/api/v1/health/ready 和审计日志。"
