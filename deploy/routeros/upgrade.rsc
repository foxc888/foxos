# 低风险升级模板：保留旧 root-dir 作为回滚槽位，不修改 DNS/路由。
:local imageFile "foxos-arm64.tar"
:local active [/container find where comment="foxos:active"]
:if ([:len $active] != 1) do={ :error "必须且只能存在一个 foxos:active 容器" }
:if ([:len [/file find where name=$imageFile]] = 0) do={ :error ("镜像文件不存在: " . $imageFile) }
/container/stop $active
:delay 3s
/container/set $active comment="foxos:rollback"
/container/add file=$imageFile interface=veth-foxos root-dir=containers/foxos-next envlist=foxos-env logging=yes start-on-boot=yes comment="foxos:active"
:put "新镜像导入完成后启动 foxos:active，验证 /api/v1/health/ready；确认正常后再删除旧回滚槽位。"
