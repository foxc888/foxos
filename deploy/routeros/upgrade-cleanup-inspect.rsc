# Shared read-only inspection for retiring the immediate rollback evidence slot.
# The bundle builder binds this file to one release ID. It never changes a
# container, mount, interface, system script, scheduler, or persistent file.

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
:global FoxOSUpgradeCleanupInspectVerbose
:global FoxOSUpgradeCleanupCurrentDigest
:global FoxOSUpgradeCleanupActiveID
:global FoxOSUpgradeCleanupRollbackID
:global FoxOSUpgradeCleanupRollbackMarker
:local releaseID "__FOXOS_RELEASE_ID__"
:if ($releaseID ~ "^__.*__\$" || [:len $releaseID] < 1 || [:len $releaseID] > 40 || !($releaseID ~ "^[A-Za-z0-9._-]+\$")) do={ :error "upgrade-cleanup-inspect.rsc 未绑定有效 release ID" }
:if ($FoxOSSiteManifestVersion != 2 || $FoxOSSiteLoaderVersion != 2 || $FoxOSContainerCompatVersion != 2 || [:len $FoxOSSiteLoadedDigest] != 128 || $FoxOSSiteLoadedConfigPath != ($FoxOSSiteStorageRoot . "/site-config.rsc")) do={
  :error "必须先使用根目录不可变 loader 验证 manifest v2"
}
:if ($FoxOSMountCompatVersion != 2 || $FoxOSWritableMountMode != "rw") do={ :error "mount compatibility contract is unavailable" }
:if ($FoxOSSecretContractVersion != 1 || $FoxOSSecretHostDirectory != ($FoxOSSiteStorageRoot . "/foxos-secrets") || $FoxOSSecretContainerDirectory != "/run/secrets/foxos" || $FoxOSSecretMountName != "foxos-secrets" || $FoxOSReadonlyMountMode != "ro") do={ :error "secret-file compatibility contract is unavailable" }

:set FoxOSUpgradeCleanupCurrentDigest ""
:set FoxOSUpgradeCleanupActiveID ""
:set FoxOSUpgradeCleanupRollbackID ""
:set FoxOSUpgradeCleanupRollbackMarker ""

:local active [/container find where comment="foxos:active"]
:local rollback [/container find where comment="foxos:rollback"]
:local rollbackComplete [/container find where comment="foxos:rollback-complete"]
:local pending [/container find where comment="foxos:pending"]
:local transitions [/container find where comment~"^foxos:transition:"]
:if ([:len $active] != 1) do={ :error "cleanup 必须且只能存在一个 foxos:active 容器" }
:if (([:len $rollback] + [:len $rollbackComplete]) != 1) do={ :error "cleanup 必须且只能存在一个 foxos:rollback 或 foxos:rollback-complete 槽位" }
:if ([:len $pending] > 0) do={ :error "存在 foxos:pending；拒绝归档即时回滚证据" }
:if ([:len $transitions] > 0) do={ :error "存在任意未收敛 foxos:transition:* 槽位；拒绝 cleanup" }

:local retirement $rollback
:local sourceMarker "foxos:rollback"
:if ([:len $rollbackComplete] = 1) do={
  :set retirement $rollbackComplete
  :set sourceMarker "foxos:rollback-complete"
}
:if ($active = $retirement) do={ :error "active 与待归档 rollback 槽位 ID 冲突" }

:local activeName [/container get $active name]
:local retirementName [/container get $retirement name]
:local activeBoot [/container get $active start-on-boot]
:local retirementBoot [/container get $retirement start-on-boot]
:local activeLogging [/container get $active logging]
:local retirementLogging [/container get $retirement logging]
:if (!($activeName ~ "^foxos-[A-Za-z0-9._-]+\$") || [$FoxOSContainerRoot $active] != ($FoxOSSiteStorageRoot . "/containers/" . $activeName) || [/container get $active interface] != "veth-foxos" || [/container get $active envlists] != "foxos-env" || [$FoxOSContainerMountLists $active] != $FoxOSAdminMountLists || ($activeBoot != false && $activeBoot != "no") || ($activeLogging != false && $activeLogging != "no") || [$FoxOSContainerState $active] != "running") do={
  :error "cleanup active 槽位完整身份契约不匹配或未运行"
}
:if (!($retirementName ~ "^foxos-[A-Za-z0-9._-]+\$") || [$FoxOSContainerRoot $retirement] != ($FoxOSSiteStorageRoot . "/containers/" . $retirementName) || [/container get $retirement interface] != "veth-foxos" || [/container get $retirement envlists] != "foxos-env" || [$FoxOSContainerMountLists $retirement] != $FoxOSAdminMountLists || ($retirementBoot != false && $retirementBoot != "no") || ($retirementLogging != false && $retirementLogging != "no") || [$FoxOSContainerState $retirement] != "stopped") do={
  :error "待归档 rollback 槽位完整身份契约或 stopped 状态不匹配"
}
:local releaseName ("foxos-" . $releaseID)
:if ($sourceMarker = "foxos:rollback" && $activeName != $releaseName) do={ :error "当前 payload 只能归档其 promote 后的 rollback 槽位" }
:if ($sourceMarker = "foxos:rollback-complete" && $retirementName != $releaseName) do={ :error "当前 payload 只能归档其 rollback-complete 槽位" }

:local knownAdminSlots [/container find where comment~"^foxos:(active|pending|rollback|rollback-complete|transition:promote|transition:rollback|transition:rollback:previous|retained|failed)\$"]
:local interfaceAdminSlots [/container find where interface="veth-foxos"]
:if ([:len $knownAdminSlots] != [:len $interfaceAdminSlots]) do={ :error "veth-foxos 上存在未绑定或错绑的管理容器，拒绝 cleanup" }
:local runningAdminCount 0
:local runningAdminID ""
:local adminSlotMaterial ""
:foreach adminSlot in=$knownAdminSlots do={
  :local adminName [/container get $adminSlot name]
  :local adminStatus [$FoxOSContainerState $adminSlot]
  :local adminBoot [/container get $adminSlot start-on-boot]
  :local adminLogging [/container get $adminSlot logging]
  :if (!($adminName ~ "^foxos-[A-Za-z0-9._-]+\$") || [/container get $adminSlot interface] != "veth-foxos" || [/container get $adminSlot envlists] != "foxos-env" || [$FoxOSContainerMountLists $adminSlot] != $FoxOSAdminMountLists || [$FoxOSContainerRoot $adminSlot] != ($FoxOSSiteStorageRoot . "/containers/" . $adminName) || ($adminStatus != "running" && $adminStatus != "stopped") || ($adminBoot != false && $adminBoot != "no") || ($adminLogging != false && $adminLogging != "no")) do={
    :error ("cleanup 管理槽位完整身份契约不匹配: " . $adminName)
  }
  :if ($adminStatus = "running") do={
    :set runningAdminCount ($runningAdminCount + 1)
    :set runningAdminID $adminSlot
  }
  :set adminSlotMaterial ($adminSlotMaterial . "|admin-slot=" . $adminSlot . ":" . $adminName . ":" . [/container get $adminSlot comment] . ":" . $adminStatus . ":" . [$FoxOSContainerRoot $adminSlot] . ":" . [/container get $adminSlot interface] . ":" . [/container get $adminSlot envlists] . ":" . [$FoxOSContainerMountLists $adminSlot] . ":" . $adminBoot . ":" . $adminLogging)
}
:if ($runningAdminCount != 1 || $runningAdminID != $active) do={ :error "cleanup 要求 committed active 是唯一 running 管理槽位" }

:local material ("foxos-upgrade-cleanup-v4|release=" . $releaseID . "|site=" . $FoxOSSiteLoadedDigest)
:foreach secretName in=$FoxOSSecretFileNames do={
  :set material ($material . "|secret=" . [$FoxOSSecretEvidence $secretName])
}
:set material ($material . "|active=" . [:pick $active 0] . ":" . $activeName . ":" . [$FoxOSContainerRoot $active] . ":" . [$FoxOSContainerState $active] . ":" . [/container get $active comment] . ":" . [/container get $active interface] . ":" . [/container get $active envlists] . ":" . [$FoxOSContainerMountLists $active] . ":" . $activeBoot . ":" . $activeLogging)
:set material ($material . "|rollback=" . [:pick $retirement 0] . ":" . $retirementName . ":" . [$FoxOSContainerRoot $retirement] . ":" . [$FoxOSContainerState $retirement] . ":" . $sourceMarker . ":" . [/container get $retirement interface] . ":" . [/container get $retirement envlists] . ":" . [$FoxOSContainerMountLists $retirement] . ":" . $retirementBoot . ":" . $retirementLogging)
:set material ($material . $adminSlotMaterial)

:local installMarkers [/container/envs find where list="foxos-env" key="FOXOS_INSTALL_MARKER"]
:if ([:len $installMarkers] != 1 || [/container/envs get $installMarkers value] != "foxos:complete") do={ :error "foxos-env 必须处于唯一 foxos:complete 状态" }
:set material ($material . "|marker=" . [:pick $installMarkers 0] . ":" . [/container/envs get $installMarkers value])

:local foxosVeth [/interface/veth find where name="veth-foxos"]
:local expectedFoxOSCIDR ($FoxOSSiteFoxOSAddress . "/" . $FoxOSSitePrefixLength)
:if ([:len $foxosVeth] != 1 || [/interface/veth get $foxosVeth comment] != "foxos:admin" || [/interface/veth get $foxosVeth address] != $expectedFoxOSCIDR || [/interface/veth get $foxosVeth gateway] != $FoxOSSiteRouterAddress) do={
  :error "veth-foxos 身份、地址或网关不匹配"
}
:set material ($material . "|veth=" . [:pick $foxosVeth 0] . ":" . [/interface/veth get $foxosVeth comment] . ":" . [/interface/veth get $foxosVeth address] . ":" . [/interface/veth get $foxosVeth gateway])

:local sharedMountDefinitions {"foxos-mihomo-config|mihomo-config|/data/mihomo";"foxos-data|foxos-data|/data";"foxos-backups|foxos-backups|/backups"}
:local verifiedSharedMounts 0
:foreach definition in=$sharedMountDefinitions do={
  :local p1 [:find $definition "|"]
  :local p2 [:find $definition "|" ($p1 + 1)]
  :local mountName [:pick $definition 0 $p1]
  :local expectedSource ($FoxOSSiteStorageRoot . "/" . [:pick $definition ($p1 + 1) $p2])
  :local expectedDestination [:pick $definition ($p2 + 1) [:len $definition]]
  :local mountID [/container/mounts find where list=$mountName]
  :if ([:len $mountID] != 1 || [$FoxOSMountSource $mountID] != $expectedSource || [/container/mounts get $mountID dst] != $expectedDestination || [$FoxOSMountMode $mountID] != $FoxOSWritableMountMode) do={
    :error ("cleanup 所需共享挂载身份或读写属性不匹配: " . $mountName)
  }
  :set material ($material . "|mount=" . [:pick $mountID 0] . ":" . $mountName . ":" . [$FoxOSMountSource $mountID] . ":" . [/container/mounts get $mountID dst] . ":" . [$FoxOSMountMode $mountID])
  :set verifiedSharedMounts ($verifiedSharedMounts + 1)
}
:if ($verifiedSharedMounts != 3) do={ :error "三个共享挂载未全部通过身份与可写检查" }
:local secretMount [/container/mounts find where list=$FoxOSSecretMountName]
:if ([:len $secretMount] != 1 || [$FoxOSMountSource $secretMount] != $FoxOSSecretHostDirectory || [/container/mounts get $secretMount dst] != $FoxOSSecretContainerDirectory || [$FoxOSMountMode $secretMount] != $FoxOSReadonlyMountMode) do={ :error "cleanup secret mount identity or read-only mode does not match" }
:set material ($material . "|mount=" . $FoxOSSecretMountName . ":" . [:pick $secretMount 0] . ":" . [$FoxOSMountSource $secretMount] . ":" . [/container/mounts get $secretMount dst] . ":" . [$FoxOSMountMode $secretMount])

:local expectedStartSource (":delay 20s; /import file-name=" . $FoxOSSiteStorageRoot . "/load-site-config.rsc; /import file-name=" . $FoxOSSiteStorageRoot . "/foxos-start-all.rsc")
:local startScriptByName [/system/script find where name="foxos-start-sequence"]
:local startScriptByOwner [/system/script find where comment="foxos:start-sequence"]
:if ([:len $startScriptByName] != 1 || [:len $startScriptByOwner] != 1 || $startScriptByName != $startScriptByOwner || [/system/script get $startScriptByName source] != $expectedStartSource || [/system/script get $startScriptByName policy] != {"read";"write";"test"}) do={
  :error "冷启动 system script 的唯一性、所有权或内容不匹配"
}
:set material ($material . "|start-script=" . [:pick $startScriptByName 0] . ":" . [/system/script get $startScriptByName name] . ":" . [/system/script get $startScriptByName comment] . ":" . [/system/script get $startScriptByName source] . ":" . [:tostr [/system/script get $startScriptByName policy]])

:local schedulerByName [/system/scheduler find where name="foxos-start-sequence"]
:local schedulerByOwner [/system/scheduler find where comment="foxos:start-sequence"]
:if ([:len $schedulerByName] != 1 || [:len $schedulerByOwner] != 1 || $schedulerByName != $schedulerByOwner || [/system/scheduler get $schedulerByName on-event] != "foxos-start-sequence" || [/system/scheduler get $schedulerByName start-time] != "startup" || [/system/scheduler get $schedulerByName interval] != 0s || [/system/scheduler get $schedulerByName policy] != {"read";"write";"test"} || ([/system/scheduler get $schedulerByName disabled] != false && [/system/scheduler get $schedulerByName disabled] != "no")) do={
  :error "冷启动 scheduler 的唯一性、所有权、内容或 enabled 状态不匹配"
}
:set material ($material . "|scheduler=" . [:pick $schedulerByName 0] . ":" . [/system/scheduler get $schedulerByName name] . ":" . [/system/scheduler get $schedulerByName comment] . ":" . [/system/scheduler get $schedulerByName on-event] . ":" . [/system/scheduler get $schedulerByName start-time] . ":" . [/system/scheduler get $schedulerByName interval] . ":" . [:tostr [/system/scheduler get $schedulerByName policy]] . ":" . [/system/scheduler get $schedulerByName disabled])

:local digest [:convert $material transform=sha512 to=hex]
:if ([:len $digest] != 128) do={ :error "无法生成 rollback 归档摘要" }
:set FoxOSUpgradeCleanupActiveID $active
:set FoxOSUpgradeCleanupRollbackID $retirement
:set FoxOSUpgradeCleanupRollbackMarker $sourceMarker
:set FoxOSUpgradeCleanupCurrentDigest $digest
:if ($FoxOSUpgradeCleanupInspectVerbose) do={
  :put ("KEEP active id=" . [:pick $active 0] . " name=" . $activeName . " status=running root-dir=" . [$FoxOSContainerRoot $active])
  :put ("RETAIN rollback id=" . [:pick $retirement 0] . " name=" . $retirementName . " marker=" . $sourceMarker . " status=stopped root-dir=" . [$FoxOSContainerRoot $retirement])
  :put ("KEEP enabled scheduler id=" . [:pick $schedulerByName 0] . " and system script id=" . [:pick $startScriptByName 0])
  :put ("PLAN DIGEST SHA-512 " . $digest)
}
