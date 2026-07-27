# FoxOS container rollback. It switches only FoxOS-owned container slots.
# SQLite compatibility must be checked against the pre-upgrade backup.

:local active [/container find where comment="foxos:active"]
:local rollback [/container find where comment="foxos:rollback"]
:if ([:len $active] != 1) do={ :error "找不到唯一 foxos:active 容器" }
:if ([:len $rollback] != 1) do={ :error "找不到唯一 foxos:rollback 容器" }
:if ([/container get $rollback status] != "stopped") do={ :error "rollback 槽位必须处于 stopped，拒绝切换" }

:put "回滚影响：停止当前 FoxOS，启动保留槽位；不修改 Mihomo、MosDNS、DNS、DHCP、路由、NAT、Mangle 或防火墙。"
/container/stop $active
:delay 3s
:if ([/container get $active status] != "stopped") do={
  /container/start $active
  :error "active 容器未正常停止，已请求恢复启动；未切换所有权标记"
}
/container/set $active comment="foxos:failed" start-on-boot=no
/container/set $rollback comment="foxos:active" start-on-boot=yes
/container/start $rollback
:delay 5s
:if ([/container get $rollback status] != "running") do={
  /container/stop $rollback
  /container/set $rollback comment="foxos:rollback" start-on-boot=no
  /container/set $active comment="foxos:active" start-on-boot=yes
  /container/start $active
  :error "回滚槽位未进入 running，已请求恢复原 active 容器"
}
:put "回滚槽位已进入 running；请验证 http://10.0.0.4:8090/api/v1/health/ready、SQLite 版本和审计日志。"
