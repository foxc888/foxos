# FoxOS two-phase upgrade, phase 2: promote a fully imported pending image.
# Run only after phase 1 shows foxos:pending status=stopped.

:local active [/container find where comment="foxos:active"]
:local pending [/container find where comment="foxos:pending"]
:local rollback [/container find where comment="foxos:rollback"]
:if ([:len $active] != 1) do={ :error "必须且只能存在一个 foxos:active 容器" }
:if ([:len $pending] != 1) do={ :error "必须且只能存在一个 foxos:pending 容器" }
:if ([:len $rollback] > 0) do={ :error "已有 foxos:rollback，拒绝覆盖" }
:if ([/container get $pending status] != "stopped") do={
  /container/print
  :error "pending 镜像尚未完成导入；等待 status=stopped 后重试"
}

:put "升级阶段 2 影响：停止当前 FoxOS，保留为 rollback，启动 pending；共享 SQLite、Mihomo 配置和备份目录。"
:put "若新容器未进入 running，脚本会恢复旧容器；应用健康异常时立即执行 disk1/rollback.rsc。"
/container/stop $active
:delay 3s
:if ([/container get $active status] != "stopped") do={
  /container/start $active
  :error "active 容器未正常停止，已请求恢复启动；未切换所有权标记"
}
/container/set $active comment="foxos:rollback" start-on-boot=no
/container/set $pending comment="foxos:active" start-on-boot=yes
/container/start $pending
:delay 5s
:if ([/container get $pending status] != "running") do={
  /container/stop $pending
  /container/set $pending comment="foxos:failed" start-on-boot=no
  /container/set $active comment="foxos:active" start-on-boot=yes
  /container/start $active
  :error "新容器未进入 running，已自动恢复旧容器"
}
:put "新 FoxOS 容器已进入 running。现在验证 health/ready、管理页面和只读依赖；验证失败执行 disk1/rollback.rsc。"
