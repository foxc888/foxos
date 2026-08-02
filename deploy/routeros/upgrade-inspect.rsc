# Read-only phase-1 upgrade inspection. The bundle builder binds this file to
# one release ID. It never creates, changes, starts, stops, or removes resources.

:global FoxOSSiteManifestVersion
:global FoxOSSiteStorageRoot
:global FoxOSSitePrefixLength
:global FoxOSSiteRouterAddress
:global FoxOSSiteFoxOSAddress
:global FoxOSSiteLoadedDigest
:global FoxOSSiteLoadedConfigPath
:global FoxOSSiteLoaderVersion
:global FoxOSContainerCompatVersion
:global FoxOSContainerState
:global FoxOSContainerRoot
:global FoxOSContainerMountLists
:global FoxOSMountCompatVersion
:global FoxOSWritableMountMode
:global FoxOSMountSource
:global FoxOSMountMode
:global FoxOSSecretContractVersion
:global FoxOSSecretHostDirectory
:global FoxOSSecretContainerDirectory
:global FoxOSSecretMountName
:global FoxOSReadonlyMountMode
:global FoxOSAdminMountLists
:global FoxOSSecretFileNames
:global FoxOSSecretEvidence
:global FoxOSUpgradeInspectVerbose
:global FoxOSUpgradeCurrentDigest
:global FoxOSUpgradeActiveID
:global FoxOSUpgradeImageID
:local releaseID "__FOXOS_RELEASE_ID__"
:if ($releaseID ~ "^__.*__\$" || [:len $releaseID] < 1 || [:len $releaseID] > 40 || !($releaseID ~ "^[A-Za-z0-9._-]+\$")) do={ :error "upgrade-inspect.rsc 未绑定有效 release ID" }
:if ($FoxOSSiteManifestVersion != 2 || $FoxOSSiteLoaderVersion != 2 || $FoxOSContainerCompatVersion != 2 || [:len $FoxOSSiteLoadedDigest] != 128 || $FoxOSSiteLoadedConfigPath != ($FoxOSSiteStorageRoot . "/site-config.rsc")) do={
  :error "必须先使用根目录不可变 loader 验证 manifest v2"
}
:if ($FoxOSMountCompatVersion != 2 || $FoxOSWritableMountMode != "rw") do={ :error "mount compatibility contract is unavailable" }
:if ($FoxOSSecretContractVersion != 1 || $FoxOSSecretHostDirectory != ($FoxOSSiteStorageRoot . "/foxos-secrets") || $FoxOSSecretContainerDirectory != "/run/secrets/foxos" || $FoxOSSecretMountName != "foxos-secrets" || $FoxOSReadonlyMountMode != "ro") do={ :error "secret-file compatibility contract is unavailable" }

:local storageRoot $FoxOSSiteStorageRoot
:local pendingName ("foxos-" . $releaseID)
:local pendingRoot ($storageRoot . "/containers/" . $pendingName)
:local payloadRoot ($storageRoot . "/foxos-upgrade-" . $releaseID)
:local imagePath ($payloadRoot . "/foxos-amd64.tar")
:set FoxOSUpgradeCurrentDigest ""
:set FoxOSUpgradeActiveID ""
:set FoxOSUpgradeImageID ""

:local architecture [/system/resource get architecture-name]
:local imageArchitecture ""
:if ($architecture = "x86" || $architecture = "x86_64") do={ :set imageArchitecture "amd64" }
:if ($imageArchitecture != "amd64") do={ :error ("此升级包只支持 RouterOS x86 或 x86_64，当前为 " . $architecture) }
:if ($architecture = "x86_64") do={ :put "WARNING architecture-name=x86_64 是非标准 RouterOS 环境；升级兼容仍需目标机验收" }
:local routerVersion [/system/resource get version]
:local routerVersionBase $routerVersion
:local routerVersionSpace [:find $routerVersionBase " "]
:if ([:typeof $routerVersionSpace] != "nil") do={ :set routerVersionBase [:pick $routerVersionBase 0 $routerVersionSpace] }
:local packageID [/system/package find where name="container"]
:if ([:len $packageID] != 1 || [/system/package get $packageID disabled] = true || [/system/package get $packageID version] != $routerVersionBase) do={
  :error "container package 必须唯一、启用且与 RouterOS 完全同版本"
}
:local active [/container find where comment="foxos:active"]
:local rollback [/container find where comment="foxos:rollback"]
:local rollbackComplete [/container find where comment="foxos:rollback-complete"]
:local pending [/container find where comment="foxos:pending"]
:local promoteTransition [/container find where comment="foxos:transition:promote"]
:local rollbackTransition [/container find where comment="foxos:transition:rollback"]
:local rollbackPrevious [/container find where comment="foxos:transition:rollback:previous"]
:if ([:len $active] != 1) do={ :error "必须且只能存在一个 foxos:active 容器" }
:if ([:len $rollback] > 0 || [:len $rollbackComplete] > 0) do={ :error "已有即时 rollback 或 rollback-complete；先执行确认式 cleanup" }
:if ([:len $pending] > 0) do={ :error "已有 foxos:pending；拒绝覆盖未完成升级" }
:if ([:len $promoteTransition] > 0 || [:len $rollbackTransition] > 0 || [:len $rollbackPrevious] > 0) do={ :error "存在未收敛生命周期过渡标记；先人工审核并收敛" }
:if ([:len [/container find where name=$pendingName]] > 0) do={ :error ("版本化槽位已存在，拒绝覆盖: " . $pendingName) }
:if ([:len [/file find where name=$pendingRoot]] > 0) do={ :error ("版本化 root-dir 已存在，拒绝复用: " . $pendingRoot) }
:local imageFile [/file find where name=$imagePath]
:if ([:len $imageFile] != 1 || [/file get $imageFile size] < 1048576) do={ :error ("版本化 FoxOS 镜像缺失、不唯一或过小: " . $imagePath) }

:local activeName [/container get $active name]
:if (!($activeName ~ "^foxos-[A-Za-z0-9._-]+\$") || [/container get $active interface] != "veth-foxos" || [/container get $active envlists] != "foxos-env" || [$FoxOSContainerMountLists $active] != $FoxOSAdminMountLists || [$FoxOSContainerRoot $active] != ($storageRoot . "/containers/" . $activeName) || ([/container get $active start-on-boot] != false && [/container get $active start-on-boot] != "no") || ([/container get $active logging] != false && [/container get $active logging] != "no") || [$FoxOSContainerState $active] != "running") do={
  :error "active 容器完整身份契约不匹配或未运行"
}
:local installMarkers [/container/envs find where list="foxos-env" key="FOXOS_INSTALL_MARKER"]
:if ([:len $installMarkers] != 1) do={ :error "FOXOS_INSTALL_MARKER 必须且只能存在一个" }
:if ([/container/envs get $installMarkers value] != "foxos:complete") do={ :error "foxos-env 未处于 foxos:complete" }
:local foxosVeth [/interface/veth find where name="veth-foxos"]
:local expectedFoxOSCIDR ($FoxOSSiteFoxOSAddress . "/" . $FoxOSSitePrefixLength)
:if ([:len $foxosVeth] != 1 || [/interface/veth get $foxosVeth comment] != "foxos:admin" || [/interface/veth get $foxosVeth address] != $expectedFoxOSCIDR || [/interface/veth get $foxosVeth gateway] != $FoxOSSiteRouterAddress) do={
  :error "veth-foxos 身份、地址或网关不匹配"
}

:local expectedStartSource (":delay 20s; /import file-name=" . $storageRoot . "/load-site-config.rsc; /import file-name=" . $storageRoot . "/foxos-start-all.rsc")
:local startScriptByName [/system/script find where name="foxos-start-sequence"]
:local startScriptByOwner [/system/script find where comment="foxos:start-sequence"]
:if ([:len $startScriptByName] != 1 || [:len $startScriptByOwner] != 1 || $startScriptByName != $startScriptByOwner || [/system/script get $startScriptByName source] != $expectedStartSource || [/system/script get $startScriptByName policy] != {"read";"write";"test"}) do={
  :error "冷启动协调脚本的名称、owner、内容或 policy 不匹配"
}
:local startSchedulerByName [/system/scheduler find where name="foxos-start-sequence"]
:local startSchedulerByOwner [/system/scheduler find where comment="foxos:start-sequence"]
:local startSchedulerDisabled ""
:if ([:len $startSchedulerByName] = 1) do={ :set startSchedulerDisabled [/system/scheduler get $startSchedulerByName disabled] }
:if ([:len $startSchedulerByName] != 1 || [:len $startSchedulerByOwner] != 1 || $startSchedulerByName != $startSchedulerByOwner || [/system/scheduler get $startSchedulerByName on-event] != "foxos-start-sequence" || [/system/scheduler get $startSchedulerByName start-time] != "startup" || [/system/scheduler get $startSchedulerByName interval] != 0s || [/system/scheduler get $startSchedulerByName policy] != {"read";"write";"test"} || ($startSchedulerDisabled != false && $startSchedulerDisabled != "no")) do={
  :error "冷启动 scheduler 的名称、owner、事件、时序、policy 或启用状态不匹配"
}

:local material ("foxos-upgrade-create-v3|release=" . $releaseID . "|site=" . $FoxOSSiteLoadedDigest . "|active=" . [:pick $active 0] . ":" . $activeName . ":" . [$FoxOSContainerRoot $active] . ":" . [$FoxOSContainerState $active] . ":" . [/container get $active start-on-boot] . "|image=" . [:pick $imageFile 0] . ":" . $imagePath . ":" . [/file get $imageFile size] . "|pending=" . $pendingName . ":" . $pendingRoot . "|marker=" . [:pick $installMarkers 0] . ":" . [/container/envs get $installMarkers value] . "|veth=" . [:pick $foxosVeth 0] . ":" . [/interface/veth get $foxosVeth address] . ":" . [/interface/veth get $foxosVeth gateway] . "|start-script=" . [:pick $startScriptByName 0] . ":" . [/system/script get $startScriptByName comment] . ":" . [/system/script get $startScriptByName source] . ":" . [:tostr [/system/script get $startScriptByName policy]] . "|start-scheduler=" . [:pick $startSchedulerByName 0] . ":" . [/system/scheduler get $startSchedulerByName comment] . ":" . [/system/scheduler get $startSchedulerByName on-event] . ":" . [/system/scheduler get $startSchedulerByName start-time] . ":" . [/system/scheduler get $startSchedulerByName interval] . ":" . [:tostr [/system/scheduler get $startSchedulerByName policy]] . ":" . $startSchedulerDisabled)
:foreach secretName in=$FoxOSSecretFileNames do={
  :set material ($material . "|secret=" . [$FoxOSSecretEvidence $secretName])
}
:local upgradeMountDefinitions {"foxos-mihomo-config|mihomo-config|/data/mihomo";"foxos-data|foxos-data|/data";"foxos-backups|foxos-backups|/backups"}
:local verifiedMounts 0
:foreach definition in=$upgradeMountDefinitions do={
  :local p1 [:find $definition "|"]
  :local p2 [:find $definition "|" ($p1 + 1)]
  :local mountName [:pick $definition 0 $p1]
  :local expectedSource ($storageRoot . "/" . [:pick $definition ($p1 + 1) $p2])
  :local expectedDestination [:pick $definition ($p2 + 1) [:len $definition]]
  :local mountID [/container/mounts find where list=$mountName]
  :if ([:len $mountID] != 1 || [$FoxOSMountSource $mountID] != $expectedSource || [/container/mounts get $mountID dst] != $expectedDestination || [$FoxOSMountMode $mountID] != $FoxOSWritableMountMode) do={
    :error ("升级所需挂载身份或读写属性不匹配: " . $mountName)
  }
  :set material ($material . "|mount=" . $mountName . ":" . [:pick $mountID 0] . ":" . [$FoxOSMountSource $mountID] . ":" . [/container/mounts get $mountID dst] . ":" . [$FoxOSMountMode $mountID])
  :set verifiedMounts ($verifiedMounts + 1)
}
:if ($verifiedMounts != 3) do={ :error "三个升级共享挂载未全部验证" }
:local secretMount [/container/mounts find where list=$FoxOSSecretMountName]
:if ([:len $secretMount] != 1 || [$FoxOSMountSource $secretMount] != $FoxOSSecretHostDirectory || [/container/mounts get $secretMount dst] != $FoxOSSecretContainerDirectory || [$FoxOSMountMode $secretMount] != $FoxOSReadonlyMountMode) do={ :error "upgrade secret mount identity or read-only mode does not match" }
:set material ($material . "|mount=" . $FoxOSSecretMountName . ":" . [:pick $secretMount 0] . ":" . [$FoxOSMountSource $secretMount] . ":" . [/container/mounts get $secretMount dst] . ":" . [$FoxOSMountMode $secretMount])
:local digest [:convert $material transform=sha512 to=hex]
:if ([:len $digest] != 128) do={ :error "无法生成升级阶段 1 摘要" }
:set FoxOSUpgradeActiveID $active
:set FoxOSUpgradeImageID $imageFile
:set FoxOSUpgradeCurrentDigest $digest
:if ($FoxOSUpgradeInspectVerbose) do={
  :put ("KEEP active=" . $activeName . " id=" . [:pick $active 0] . " status=running root-dir=" . [$FoxOSContainerRoot $active])
  :put ("CREATE pending=" . $pendingName . " root-dir=" . $pendingRoot . " from=" . $imagePath)
  :put "PRESERVE site manifest, Mihomo/MosDNS config, data, backups, existing image files, containers, and root-dirs."
}
