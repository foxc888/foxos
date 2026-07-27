# FoxOS two-phase upgrade, phase 1: import a pending image without stopping active.
# This script does not change DNS, DHCP, routes, NAT, Mangle, or firewall rules.

:local architecture [/system/resource get architecture-name]
:global FoxOSSiteManifestVersion
:global FoxOSSiteStorageRoot
:if ($FoxOSSiteManifestVersion != 1) do={ :error "先导入已审核的 site-config.rsc" }
:local storageRoot $FoxOSSiteStorageRoot
:local imageFile ""
:if ($architecture = "x86") do={ :set imageFile "foxos-amd64.tar" }
:if ($architecture = "arm64") do={ :set imageFile "foxos-arm64.tar" }
:if ($imageFile = "") do={ :error ("不支持的架构: " . $architecture) }

:local imagePath ($storageRoot . "/" . $imageFile)
:local active [/container find where comment="foxos:active"]
:local rollback [/container find where comment="foxos:rollback"]
:local pending [/container find where comment="foxos:pending"]
:if ([:len $active] != 1) do={ :error "必须且只能存在一个 foxos:active 容器" }
:if ([:len $rollback] > 0) do={ :error "已有 foxos:rollback；完成清理或回滚后才能再次升级" }
:if ([:len $pending] > 0) do={ :error "已有 foxos:pending；拒绝覆盖未完成升级" }
:if ([:len [/container find where name="foxos-next"]] > 0) do={ :error "同名 foxos-next 容器已存在，拒绝覆盖" }
:if ([:len [/file find where name=$imagePath]] != 1) do={ :error ("镜像文件不存在或不唯一: " . $imagePath) }
:if ([:len [/container/envs find where list="foxos-env" key="FOXOS_INSTALL_MARKER" value="foxos"]] != 1) do={ :error "foxos-env 所有权标记缺失" }
:local foxosVeth [/interface/veth find where name="veth-foxos"]
:if ([:len $foxosVeth] != 1) do={ :error "veth-foxos 缺失或不唯一" }
:if ([/interface/veth get $foxosVeth comment] != "foxos:admin") do={ :error "veth-foxos 所有权标记缺失" }
:foreach mountName in={"foxos-mihomo-config";"foxos-data";"foxos-backups"} do={
  :if ([:len [/container/mounts find where name=$mountName]] != 1) do={ :error ("升级所需挂载缺失: " . $mountName) }
}

:put ("升级阶段 1：active 容器保持运行，仅导入 " . $storageRoot . " 中的新镜像到 foxos:pending。")
:put ("新 root-dir: " . $storageRoot . "/containers/foxos-next；回滚仍使用当前 active root-dir。")
/container/add name=foxos-next file=$imagePath interface=veth-foxos root-dir=($storageRoot . "/containers/foxos-next") envlist=foxos-env mountlists=foxos-mihomo-config,foxos-data,foxos-backups logging=yes start-on-boot=no comment="foxos:pending"
:put ("镜像导入已排队，当前 FoxOS 未停止。等待 foxos:pending status=stopped 后执行 " . $storageRoot . "/upgrade-promote.rsc。")
