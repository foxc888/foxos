# FoxOS RouterOS Container 安装模板
# 前置：已上传 foxos-arm64.tar 或 foxos-amd64.tar，已导入 foxos-env，已有管理桥 bridge。
# 本脚本不修改 DNS、不创建默认路由、不接管 LAN 流量。

:local imageFile "foxos-arm64.tar"
:local managementBridge "bridge"
:local foxosAddress "10.0.0.4/24"
:local foxosGateway "10.0.0.1"

:if ([:len [/interface/bridge find where name=$managementBridge]] = 0) do={
  :error ("管理桥不存在: " . $managementBridge)
}
:if ([:len [/file find where name=$imageFile]] = 0) do={
  :error ("镜像文件不存在: " . $imageFile)
}
:if ([:len [/container/envs find where list="foxos-env"]] = 0) do={
  :error "foxos-env 尚未导入"
}
:if ([:len [/interface/veth find where name="veth-foxos"]] = 0) do={
  /interface/veth add name=veth-foxos address=$foxosAddress gateway=$foxosGateway comment="foxos:runtime"
  /interface/bridge/port add bridge=$managementBridge interface=veth-foxos comment="foxos:runtime"
}
:if ([:len [/container find where comment="foxos:active"]] > 0) do={
  :error "已存在 foxos:active 容器；升级请使用 upgrade.rsc"
}
/container/add file=$imageFile interface=veth-foxos root-dir=containers/foxos envlist=foxos-env logging=yes start-on-boot=yes comment="foxos:active"
:put "FoxOS 镜像导入已排队；等待 /container 显示 stopped 后手动 start，并访问 http://10.0.0.4:8090"
