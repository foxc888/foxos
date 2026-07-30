# Read-only inspection for rolling back the active release represented by this
# versioned payload. No container or persistent resource is changed here.

:global FoxOSSiteManifestVersion
:global FoxOSSiteFoxOSAddress
:global FoxOSSiteStorageRoot
:global FoxOSSitePrefixLength
:global FoxOSSiteRouterAddress
:global FoxOSContainerCompatVersion
:global FoxOSContainerState
:global FoxOSContainerRoot
:global FoxOSMountCompatVersion
:global FoxOSWritableMountMode
:global FoxOSMountMode
:global FoxOSRollbackInspectVerbose
:global FoxOSRollbackCurrentDigest
:global FoxOSRollbackActiveID
:global FoxOSRollbackSlotID
:if ($FoxOSSiteManifestVersion != 2) do={ :error "先导入不可变的 load-site-config.rsc" }
/import file-name=($FoxOSSiteStorageRoot . "/load-site-config.rsc")
:if ($FoxOSContainerCompatVersion != 1) do={ :error "container compatibility contract is unavailable" }
:if ($FoxOSMountCompatVersion != 1 || $FoxOSWritableMountMode != "rw") do={ :error "mount compatibility contract is unavailable" }

:local releaseID "__FOXOS_RELEASE_ID__"
:if ($releaseID ~ "^__.*__\$" || [:len $releaseID] < 1 || [:len $releaseID] > 40 || !($releaseID ~ "^[A-Za-z0-9._-]+\$")) do={ :error "rollback-inspect.rsc 未绑定有效 release ID" }
:local activeNameExpected ("foxos-" . $releaseID)
:local activeRootExpected ($FoxOSSiteStorageRoot . "/containers/" . $activeNameExpected)
:local material ("foxos-rollback-v2|" . $releaseID . "|" . $FoxOSSiteStorageRoot . "|" . $FoxOSSiteFoxOSAddress)
:set FoxOSRollbackCurrentDigest ""
:set FoxOSRollbackActiveID ""
:set FoxOSRollbackSlotID ""

:local active [/container find where comment="foxos:active"]
:local rollback [/container find where comment="foxos:rollback"]
:local pending [/container find where comment="foxos:pending"]
:local rollbackComplete [/container find where comment="foxos:rollback-complete"]
:local promoteTransition [/container find where comment="foxos:transition:promote"]
:local rollbackTransition [/container find where comment="foxos:transition:rollback"]
:local rollbackPrevious [/container find where comment="foxos:transition:rollback:previous"]
:if ([:len $active] != 1 || [:len $rollback] != 1) do={ :error "回滚必须且只能存在一个 active 和一个 rollback 槽位" }
:if ([:len $pending] > 0 || [:len $rollbackComplete] > 0) do={ :error "存在 pending 或 rollback-complete，拒绝开始新的回滚" }
:if ([:len $promoteTransition] > 0 || [:len $rollbackTransition] > 0 || [:len $rollbackPrevious] > 0) do={ :error "存在未收敛的升级过渡标记；先运行并审核 foxos-start-all.rsc" }
:if ($active = $rollback) do={ :error "active 与 rollback 槽位 ID 冲突" }

:local activeName [/container get $active name]
:local rollbackName [/container get $rollback name]
:local activeStatus [$FoxOSContainerState $active]
:local activeBoot [/container get $active start-on-boot]
:local rollbackBoot [/container get $rollback start-on-boot]
:local activeLogging [/container get $active logging]
:local rollbackLogging [/container get $rollback logging]
:if ($activeName != $activeNameExpected || [$FoxOSContainerRoot $active] != $activeRootExpected || [/container get $active interface] != "veth-foxos" || [/container get $active envlists] != "foxos-env" || [/container get $active mountlists] != "foxos-mihomo-config,foxos-data,foxos-backups" || ($activeBoot != false && $activeBoot != "no") || ($activeLogging != false && $activeLogging != "no") || ($activeStatus != "running" && $activeStatus != "stopped")) do={ :error "active 槽位不是此版本化 payload 对应的完整 release 身份" }
:if (!($rollbackName ~ "^foxos-[A-Za-z0-9._-]+\$") || [$FoxOSContainerRoot $rollback] != ($FoxOSSiteStorageRoot . "/containers/" . $rollbackName) || [/container get $rollback interface] != "veth-foxos" || [/container get $rollback envlists] != "foxos-env" || [/container get $rollback mountlists] != "foxos-mihomo-config,foxos-data,foxos-backups" || ($rollbackBoot != false && $rollbackBoot != "no") || ($rollbackLogging != false && $rollbackLogging != "no") || [$FoxOSContainerState $rollback] != "stopped") do={ :error "rollback 槽位完整身份或停止状态不匹配" }
:set material ($material . "|active=" . [:pick $active 0] . ":" . $activeName . ":" . [$FoxOSContainerRoot $active] . ":" . $activeStatus . ":" . [/container get $active interface] . ":" . [/container get $active envlists] . ":" . [/container get $active mountlists] . ":" . $activeBoot . ":" . $activeLogging)
:set material ($material . "|rollback=" . [:pick $rollback 0] . ":" . $rollbackName . ":" . [$FoxOSContainerRoot $rollback] . ":" . [$FoxOSContainerState $rollback] . ":" . [/container get $rollback interface] . ":" . [/container get $rollback envlists] . ":" . [/container get $rollback mountlists] . ":" . $rollbackBoot . ":" . $rollbackLogging)

:local installMarkers [/container/envs find where list="foxos-env" key="FOXOS_INSTALL_MARKER"]
:if ([:len $installMarkers] != 1 || [/container/envs get $installMarkers value] != "foxos:complete") do={ :error "foxos-env 必须处于唯一 foxos:complete 状态" }
:set material ($material . "|marker=" . [/container/envs get $installMarkers .id] . ":" . [/container/envs get $installMarkers value])
:local tokenID [/container/envs find where list="foxos-env" key="FOXOS_API_TOKEN"]
:if ([:len $tokenID] != 1) do={ :error "FOXOS_API_TOKEN 缺失或不唯一" }
:local apiToken [/container/envs get $tokenID value]
:if ([:len $apiToken] < 32) do={ :error "FOXOS_API_TOKEN 不符合安全基线" }
:local tokenDigest [:convert $apiToken transform=sha512 to=hex]
:set material ($material . "|token=" . [/container/envs get $tokenID .id] . ":" . $tokenDigest)

:local foxosVeth [/interface/veth find where name="veth-foxos"]
:local expectedFoxOSCIDR ($FoxOSSiteFoxOSAddress . "/" . $FoxOSSitePrefixLength)
:if ([:len $foxosVeth] != 1 || [/interface/veth get $foxosVeth comment] != "foxos:admin" || [/interface/veth get $foxosVeth address] != $expectedFoxOSCIDR || [/interface/veth get $foxosVeth gateway] != $FoxOSSiteRouterAddress) do={ :error "veth-foxos 身份、地址或网关不匹配" }
:set material ($material . "|veth=" . [/interface/veth get $foxosVeth .id] . ":" . [/interface/veth get $foxosVeth address] . ":" . [/interface/veth get $foxosVeth gateway])

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
:set material ($material . "|start-script=" . [/system/script get $startScriptByName .id] . ":" . [/system/script get $startScriptByName comment] . ":" . [/system/script get $startScriptByName source] . ":" . [/system/script get $startScriptByName policy])
:set material ($material . "|start-scheduler=" . [/system/scheduler get $startSchedulerByName .id] . ":" . [/system/scheduler get $startSchedulerByName comment] . ":" . [/system/scheduler get $startSchedulerByName on-event] . ":" . [/system/scheduler get $startSchedulerByName start-time] . ":" . [/system/scheduler get $startSchedulerByName interval] . ":" . [/system/scheduler get $startSchedulerByName policy] . ":" . $startSchedulerDisabled)

:local sharedMountDefinitions {"foxos-mihomo-config|mihomo-config|/data/mihomo";"foxos-data|foxos-data|/data";"foxos-backups|foxos-backups|/backups"}
:local verifiedSharedMounts 0
:foreach definition in=$sharedMountDefinitions do={
  :local p1 [:find $definition "|"]
  :local p2 [:find $definition "|" ($p1 + 1)]
  :local mountName [:pick $definition 0 $p1]
  :local expectedSource ($FoxOSSiteStorageRoot . "/" . [:pick $definition ($p1 + 1) $p2])
  :local expectedDestination [:pick $definition ($p2 + 1) [:len $definition]]
  :local mountID [/container/mounts find where list=$mountName]
  :if ([:len $mountID] != 1 || [/container/mounts get $mountID src] != $expectedSource || [/container/mounts get $mountID dst] != $expectedDestination || [$FoxOSMountMode $mountID] != $FoxOSWritableMountMode) do={ :error ("rollback 所需共享挂载身份或读写属性不匹配: " . $mountName) }
  :set verifiedSharedMounts ($verifiedSharedMounts + 1)
  :set material ($material . "|mount=" . [:pick $mountID 0] . ":" . $mountName . ":" . [/container/mounts get $mountID src] . ":" . [/container/mounts get $mountID dst] . ":" . [$FoxOSMountMode $mountID])
}
:if ($verifiedSharedMounts != 3) do={ :error "三个共享挂载未全部通过身份与可写检查" }

:local digest [:convert $material transform=sha512 to=hex]
:if ([:len $digest] != 128) do={ :error "无法生成 rollback 摘要" }
:set FoxOSRollbackActiveID $active
:set FoxOSRollbackSlotID $rollback
:set FoxOSRollbackCurrentDigest $digest
:if ($FoxOSRollbackInspectVerbose) do={
  :put ("STOP active id=" . [:pick $active 0] . " name=" . $activeName . " root-dir=" . [$FoxOSContainerRoot $active] . " status=" . $activeStatus)
  :put ("START rollback id=" . [:pick $rollback 0] . " name=" . $rollbackName . " root-dir=" . [$FoxOSContainerRoot $rollback] . " status=stopped")
  :put ("PLAN DIGEST SHA-512 " . $digest)
}
