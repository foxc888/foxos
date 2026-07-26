# FoxOS RouterOS Container 安装模板
# 前置：已上传匹配架构的 foxos-*.tar，已导入 foxos-env。
# 本脚本不修改 DNS、不创建默认路由、不接管 LAN 流量。

:local architecture [/system/resource get architecture-name]
:local imageFile ""
:local managementBridge "bridge-lan"
:local foxosAddress "10.0.0.4/24"
:local foxosGateway "10.0.0.1"

:if ($architecture = "x86_64") do={
  :set imageFile "foxos-amd64.tar"
} else={
  :if ($architecture = "arm64") do={
    :set imageFile "foxos-arm64.tar"
  } else={
    :error ("当前脚本不支持此架构: " . $architecture)
  }
}
:if ([:len [/interface/bridge find where name=$managementBridge]] = 0) do={
  :error ("管理桥不存在: " . $managementBridge . "；请先按 preflight 输出修改 managementBridge")
}
:if ([:len [/file find where name=$imageFile]] = 0) do={
  :error ("镜像文件不存在: " . $imageFile)
}
:if ([:len [/container/envs find where list="foxos-env"]] = 0) do={
  :error "foxos-env 尚未导入"
}
:if ([:len [/ip/address find where address~"10.0.0.4/"]] > 0) do={
  :error "10.0.0.4 已被 RouterOS 地址占用"
}
:if ([:len [/container/mounts find where list="foxos-data"]] = 0) do={
  /container/mounts add list=foxos-data src=foxos-data dst=/data
}
:if ([:len [/container/mounts find where list="foxos-backups"]] = 0) do={
  /container/mounts add list=foxos-backups src=foxos-backups dst=/backups
}
:if ([:len [/interface/veth find where name="veth-foxos"]] = 0) do={
  /interface/veth add name=veth-foxos address=$foxosAddress gateway=$foxosGateway comment="foxos:runtime"
  /interface/bridge/port add bridge=$managementBridge interface=veth-foxos comment="foxos:runtime"
}
:if ([:len [/container find where comment="foxos:active"]] > 0) do={
  :error "已存在 foxos:active 容器；升级请使用 upgrade.rsc"
}
/container/add file=$imageFile interface=veth-foxos root-dir=containers/foxos envlist=foxos-env mountlists=foxos-data,foxos-backups logging=yes start-on-boot=yes comment="foxos:active"
:put "FoxOS 镜像导入已排队；等待 /container 显示 stopped 后手动 start，并访问 http://10.0.0.4:8090"
