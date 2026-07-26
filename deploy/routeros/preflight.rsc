# 只读预检：不会修改 RouterOS。
:put "=== FoxOS preflight ==="
:put ("RouterOS: " . [/system/resource get version])
:put ("Architecture: " . [/system/resource get architecture-name])
:put ("Free disk: " . [/system/resource get free-hdd-space])
:put ("Container entries: " . [:len [/container find]])
:put ("veth-foxos: " . [:len [/interface/veth find where name="veth-foxos"]])
:put ("foxos-env values: " . [:len [/container/envs find where list="foxos-env"]])
:put ("foxos-service account: " . [:len [/user find where name="foxos-service"]])
:put ("10.0.0.4 configured addresses: " . [:len [/ip/address find where address~"10.0.0.4/"]])
:put ("foxos-data mount: " . [:len [/container/mounts find where name="foxos-data"]])
:put ("foxos-backups mount: " . [:len [/container/mounts find where name="foxos-backups"]])
:put "=== Bridges ==="
:foreach item in=[/interface/bridge find] do={
  :put ([/interface/bridge get $item name])
}
:put "=== IPv4 addresses ==="
:foreach item in=[/ip/address find] do={
  :put (([/ip/address get $item interface]) . " -> " . ([/ip/address get $item address]))
}
:put "=== Uploaded FoxOS images ==="
:foreach item in=[/file find where name~"foxos-.*\\.tar"] do={
  :put (([/file get $item name]) . " size=" . ([/file get $item size]))
}
:put "确认 container device-mode、磁盘空间、CPU 架构、管理桥和服务账号后再安装。"
