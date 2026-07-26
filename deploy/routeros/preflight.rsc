# 只读预检：不会修改 RouterOS。
:put "=== FoxOS preflight ==="
:put ("RouterOS: " . [/system/resource get version])
:put ("Architecture: " . [/system/resource get architecture-name])
:put ("Free disk: " . [/system/resource get free-hdd-space])
:put ("Container entries: " . [:len [/container find]])
:put ("veth-foxos: " . [:len [/interface/veth find where name="veth-foxos"]])
:put ("foxos-env values: " . [:len [/container/envs find where list="foxos-env"]])
:put ("foxos-service account: " . [:len [/user find where name="foxos-service"]])
:put "确认 container device-mode、磁盘空间、CPU 架构、管理桥和服务账号后再安装。"
