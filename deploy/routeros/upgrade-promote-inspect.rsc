# Read-only phase-2 inspection for either a normal pending promotion or a
# post-switch audit resume. It never mutates RouterOS or application state.

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
:global FoxOSMountCompatVersion
:global FoxOSWritableMountMode
:global FoxOSMountMode
:global FoxOSUpgradePromoteInspectVerbose
:global FoxOSUpgradePromoteCurrentDigest
:global FoxOSUpgradePromoteState
:global FoxOSUpgradePromoteActiveID
:global FoxOSUpgradePromotePendingID
:global FoxOSUpgradePromoteRollbackID
:local releaseID "__FOXOS_RELEASE_ID__"
:if ($releaseID ~ "^__.*__\$" || [:len $releaseID] < 1 || [:len $releaseID] > 40 || !($releaseID ~ "^[A-Za-z0-9._-]+\$")) do={ :error "upgrade-promote-inspect.rsc 未绑定有效 release ID" }
:if ($FoxOSSiteManifestVersion != 2 || $FoxOSSiteLoaderVersion != 1 || $FoxOSContainerCompatVersion != 1 || [:len $FoxOSSiteLoadedDigest] != 128 || $FoxOSSiteLoadedConfigPath != ($FoxOSSiteStorageRoot . "/site-config.rsc")) do={
  :error "必须先使用本升级包的不可变 loader 验证根目录 manifest v2"
}
:if ($FoxOSMountCompatVersion != 1 || $FoxOSWritableMountMode != "rw") do={ :error "mount compatibility contract is unavailable" }

:local pendingName ("foxos-" . $releaseID)
:local pendingRoot ($FoxOSSiteStorageRoot . "/containers/" . $pendingName)
:local activeOwner [/container find where comment="foxos:active"]
:local pendingOwner [/container find where comment="foxos:pending"]
:local rollbackOwner [/container find where comment="foxos:rollback"]
:local rollbackComplete [/container find where comment="foxos:rollback-complete"]
:local promoteTransition [/container find where comment="foxos:transition:promote"]
:local rollbackTransition [/container find where comment="foxos:transition:rollback"]
:local rollbackPrevious [/container find where comment="foxos:transition:rollback:previous"]
:if ([:len $promoteTransition] > 0 || [:len $rollbackTransition] > 0 || [:len $rollbackPrevious] > 0) do={ :error "存在未收敛生命周期过渡标记；先人工审核并收敛" }
:if ([:len $rollbackComplete] > 0) do={ :error "已有 rollback-complete；当前 release 不可 promote" }

:local state ""
:local oldSlot ""
:local newSlot ""
:if ([:len $activeOwner] = 1 && [:len $pendingOwner] = 1 && [:len $rollbackOwner] = 0) do={
  :set state "pending"
  :set oldSlot $activeOwner
  :set newSlot $pendingOwner
}
:if ([:len $activeOwner] = 1 && [:len $pendingOwner] = 0 && [:len $rollbackOwner] = 1 && [/container get $activeOwner name] = $pendingName && [$FoxOSContainerRoot $activeOwner] = $pendingRoot) do={
  :set state "switched"
  :set oldSlot $rollbackOwner
  :set newSlot $activeOwner
}
:if ($state = "") do={ :error "需要唯一 active+pending，或已切换的唯一 active+rollback 状态" }
:if ($oldSlot = $newSlot) do={ :error "旧槽位与新槽位 ID 冲突" }
:local oldName [/container get $oldSlot name]
:if (!($oldName ~ "^foxos-[A-Za-z0-9._-]+\$") || [$FoxOSContainerRoot $oldSlot] != ($FoxOSSiteStorageRoot . "/containers/" . $oldName) || [/container get $oldSlot interface] != "veth-foxos" || [/container get $oldSlot envlists] != "foxos-env" || [/container get $oldSlot mountlists] != "foxos-mihomo-config,foxos-data,foxos-backups" || ([/container get $oldSlot start-on-boot] != false && [/container get $oldSlot start-on-boot] != "no") || ([/container get $oldSlot logging] != false && [/container get $oldSlot logging] != "no")) do={
  :error "旧槽位完整身份契约不匹配"
}
:if ([/container get $newSlot name] != $pendingName || [$FoxOSContainerRoot $newSlot] != $pendingRoot || [/container get $newSlot interface] != "veth-foxos" || [/container get $newSlot envlists] != "foxos-env" || [/container get $newSlot mountlists] != "foxos-mihomo-config,foxos-data,foxos-backups" || ([/container get $newSlot start-on-boot] != false && [/container get $newSlot start-on-boot] != "no") || ([/container get $newSlot logging] != false && [/container get $newSlot logging] != "no")) do={
  :error "新槽位与当前升级包 release ID 或完整身份契约不匹配"
}
:if ($state = "pending" && ([$FoxOSContainerState $oldSlot] != "running" || [$FoxOSContainerState $newSlot] != "stopped")) do={ :error "promote 前必须是旧 active running、新 pending stopped" }
:if ($state = "switched" && ([$FoxOSContainerState $oldSlot] != "stopped" || [$FoxOSContainerState $newSlot] != "running")) do={ :error "audit resume 必须是旧 rollback stopped、新 active running" }

:local tokenID [/container/envs find where list="foxos-env" key="FOXOS_API_TOKEN"]
:if ([:len $tokenID] != 1) do={ :error "FOXOS_API_TOKEN 缺失或不唯一" }
:local apiToken [/container/envs get $tokenID value]
:if ([:len $apiToken] < 32) do={ :error "FOXOS_API_TOKEN 不符合安全基线" }
:local tokenDigest [:convert $apiToken transform=sha512 to=hex]
:local installMarkers [/container/envs find where list="foxos-env" key="FOXOS_INSTALL_MARKER"]
:if ([:len $installMarkers] != 1 || [/container/envs get $installMarkers value] != "foxos:complete") do={ :error "foxos-env 必须处于唯一 foxos:complete 状态" }
:local foxosVeth [/interface/veth find where name="veth-foxos"]
:local expectedFoxOSCIDR ($FoxOSSiteFoxOSAddress . "/" . $FoxOSSitePrefixLength)
:if ([:len $foxosVeth] != 1 || [/interface/veth get $foxosVeth comment] != "foxos:admin" || [/interface/veth get $foxosVeth address] != $expectedFoxOSCIDR || [/interface/veth get $foxosVeth gateway] != $FoxOSSiteRouterAddress) do={ :error "veth-foxos 身份、地址或网关不匹配" }
:local expectedStartSource (":delay 20s; /import file-name=" . $FoxOSSiteStorageRoot . "/load-site-config.rsc; /import file-name=" . $FoxOSSiteStorageRoot . "/foxos-start-all.rsc")
:local startScriptByName [/system/script find where name="foxos-start-sequence"]
:local startScriptByOwner [/system/script find where comment="foxos:start-sequence"]
:if ([:len $startScriptByName] != 1 || [:len $startScriptByOwner] != 1 || [/system/script get $startScriptByName .id] != [/system/script get $startScriptByOwner .id] || [/system/script get $startScriptByName source] != $expectedStartSource || [/system/script get $startScriptByName policy] != "read,write,test") do={
  :error "冷启动协调脚本的名称、owner、内容或 policy 不匹配"
}
:local startSchedulerByName [/system/scheduler find where name="foxos-start-sequence"]
:local startSchedulerByOwner [/system/scheduler find where comment="foxos:start-sequence"]
:local startSchedulerDisabled ""
:if ([:len $startSchedulerByName] = 1) do={ :set startSchedulerDisabled [/system/scheduler get $startSchedulerByName disabled] }
:if ([:len $startSchedulerByName] != 1 || [:len $startSchedulerByOwner] != 1 || [/system/scheduler get $startSchedulerByName .id] != [/system/scheduler get $startSchedulerByOwner .id] || [/system/scheduler get $startSchedulerByName on-event] != "foxos-start-sequence" || [/system/scheduler get $startSchedulerByName start-time] != "startup" || [/system/scheduler get $startSchedulerByName interval] != "0s" || [/system/scheduler get $startSchedulerByName policy] != "read,write,test" || ($startSchedulerDisabled != false && $startSchedulerDisabled != "no")) do={
  :error "冷启动 scheduler 的名称、owner、事件、时序、policy 或启用状态不匹配"
}

:local material ("foxos-upgrade-promote-v2|release=" . $releaseID . "|site=" . $FoxOSSiteLoadedDigest . "|state=" . $state . "|old=" . [:pick $oldSlot 0] . ":" . $oldName . ":" . [$FoxOSContainerRoot $oldSlot] . ":" . [$FoxOSContainerState $oldSlot] . ":" . [/container get $oldSlot comment] . "|new=" . [:pick $newSlot 0] . ":" . [/container get $newSlot name] . ":" . [$FoxOSContainerRoot $newSlot] . ":" . [$FoxOSContainerState $newSlot] . ":" . [/container get $newSlot comment] . "|token=" . [/container/envs get $tokenID .id] . ":" . $tokenDigest . "|marker=" . [/container/envs get $installMarkers .id] . ":" . [/container/envs get $installMarkers value] . "|veth=" . [/interface/veth get $foxosVeth .id] . ":" . [/interface/veth get $foxosVeth address] . ":" . [/interface/veth get $foxosVeth gateway] . "|start-script=" . [/system/script get $startScriptByName .id] . ":" . [/system/script get $startScriptByName comment] . ":" . [/system/script get $startScriptByName source] . ":" . [/system/script get $startScriptByName policy] . "|start-scheduler=" . [/system/scheduler get $startSchedulerByName .id] . ":" . [/system/scheduler get $startSchedulerByName comment] . ":" . [/system/scheduler get $startSchedulerByName on-event] . ":" . [/system/scheduler get $startSchedulerByName start-time] . ":" . [/system/scheduler get $startSchedulerByName interval] . ":" . [/system/scheduler get $startSchedulerByName policy] . ":" . $startSchedulerDisabled)
:local sharedMountDefinitions {"foxos-mihomo-config|mihomo-config|/data/mihomo";"foxos-data|foxos-data|/data";"foxos-backups|foxos-backups|/backups"}
:local verifiedSharedMounts 0
:foreach definition in=$sharedMountDefinitions do={
  :local p1 [:find $definition "|"]
  :local p2 [:find $definition "|" ($p1 + 1)]
  :local mountName [:pick $definition 0 $p1]
  :local expectedSource ($FoxOSSiteStorageRoot . "/" . [:pick $definition ($p1 + 1) $p2])
  :local expectedDestination [:pick $definition ($p2 + 1) [:len $definition]]
  :local mountID [/container/mounts find where list=$mountName]
  :if ([:len $mountID] != 1 || [/container/mounts get $mountID src] != $expectedSource || [/container/mounts get $mountID dst] != $expectedDestination || [$FoxOSMountMode $mountID] != $FoxOSWritableMountMode) do={
    :error ("promote 所需共享挂载身份或读写属性不匹配: " . $mountName)
  }
  :set material ($material . "|mount=" . $mountName . ":" . [:pick $mountID 0] . ":" . [/container/mounts get $mountID src] . ":" . [/container/mounts get $mountID dst] . ":" . [$FoxOSMountMode $mountID])
  :set verifiedSharedMounts ($verifiedSharedMounts + 1)
}
:if ($verifiedSharedMounts != 3) do={ :error "三个共享挂载未全部通过身份与可写检查" }
:local digest [:convert $material transform=sha512 to=hex]
:if ([:len $digest] != 128) do={ :error "无法生成 promote 摘要" }
:set FoxOSUpgradePromoteState $state
:set FoxOSUpgradePromoteActiveID $activeOwner
:set FoxOSUpgradePromotePendingID $pendingOwner
:set FoxOSUpgradePromoteRollbackID $rollbackOwner
:set FoxOSUpgradePromoteCurrentDigest $digest
:if ($FoxOSUpgradePromoteInspectVerbose) do={
  :put ("STATE=" . $state . " old=" . $oldName . " id=" . [:pick $oldSlot 0] . " status=" . [$FoxOSContainerState $oldSlot])
  :put ("NEW release=" . $releaseID . " container=" . $pendingName . " id=" . [:pick $newSlot 0] . " status=" . [$FoxOSContainerState $newSlot])
}
