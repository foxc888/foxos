# Apply the exact rollback retirement plan after shared inspection and a
# digest-bound one-time confirmation. Only the frozen rollback object ID is
# changed; containers and persistent resources remain in place.

:global FoxOSSiteManifestVersion
:global FoxOSSiteStorageRoot
:global FoxOSContainerCompatVersion
:global FoxOSContainerState
:global FoxOSContainerRoot
:global FoxOSUpgradeCleanupInspectVerbose false
:global FoxOSUpgradeCleanupCurrentDigest
:global FoxOSUpgradeCleanupApprovedDigest
:global FoxOSUpgradeCleanupConfirmation
:global FoxOSUpgradeCleanupActiveID
:global FoxOSUpgradeCleanupRollbackID
:global FoxOSUpgradeCleanupRollbackMarker
:if ($FoxOSSiteManifestVersion != 2) do={ :error "先导入不可变的 load-site-config.rsc" }
/import file-name=($FoxOSSiteStorageRoot . "/load-site-config.rsc")
:if ($FoxOSContainerCompatVersion != 2) do={ :error "container compatibility contract is unavailable" }
:local releaseID "__FOXOS_RELEASE_ID__"
:if ($releaseID ~ "^__.*__\$" || [:len $releaseID] < 1 || [:len $releaseID] > 40 || !($releaseID ~ "^[A-Za-z0-9._-]+\$")) do={ :error "upgrade-cleanup-apply.rsc 未绑定有效 release ID" }
:local payloadRoot ($FoxOSSiteStorageRoot . "/foxos-upgrade-" . $releaseID)
:local approved $FoxOSUpgradeCleanupApprovedDigest
:local confirmation $FoxOSUpgradeCleanupConfirmation

/import file-name=($payloadRoot . "/upgrade-cleanup-inspect.rsc")
:if ([:len $approved] != 128 || $approved != $FoxOSUpgradeCleanupCurrentDigest) do={
  :error "cleanup 前态在计划后变化；重新运行版本化 upgrade-cleanup-plan.rsc"
}
:if ($confirmation != $approved) do={
  :error "未确认当前 APPROVED CLEANUP SHA-512；未归档 rollback 槽位"
}
:local activeSnapshot $FoxOSUpgradeCleanupActiveID
:local rollbackSnapshot $FoxOSUpgradeCleanupRollbackID
:local markerSnapshot $FoxOSUpgradeCleanupRollbackMarker

# Re-read the sealed site manifest and repeat the complete inspector after the
# confirmation. The second digest also covers mounts, veth, and cold-start IDs.
/import file-name=($FoxOSSiteStorageRoot . "/load-site-config.rsc")
/import file-name=($payloadRoot . "/upgrade-cleanup-inspect.rsc")
:if ($FoxOSUpgradeCleanupCurrentDigest != $approved || $FoxOSUpgradeCleanupActiveID != $activeSnapshot || $FoxOSUpgradeCleanupRollbackID != $rollbackSnapshot || $FoxOSUpgradeCleanupRollbackMarker != $markerSnapshot) do={
  :error "cleanup 完整对象 snapshot 在确认后变化；重新运行 upgrade-cleanup-plan.rsc"
}
:set FoxOSUpgradeCleanupConfirmation ""
:set FoxOSUpgradeCleanupApprovedDigest ""

:local active [/container find where comment="foxos:active"]
:local retirement [/container find where comment=$markerSnapshot]
:if ($active != $activeSnapshot || $retirement != $rollbackSnapshot) do={ :error "cleanup 写入前 active 或 rollback 对象 ID 变化" }
/container/set $rollbackSnapshot comment="foxos:retained"
:if ([/container get $rollbackSnapshot comment] != "foxos:retained" || [$FoxOSContainerState $rollbackSnapshot] != "stopped" || ([/container get $rollbackSnapshot start-on-boot] != false && [/container get $rollbackSnapshot start-on-boot] != "no")) do={
  :error "rollback 槽位归档回读失败"
}
:if ([/container get $activeSnapshot comment] != "foxos:active" || [$FoxOSContainerState $activeSnapshot] != "running") do={ :error "cleanup 后 active 身份或运行状态异常" }
:put "rollback 槽位已归档为 foxos:retained；active、容器、root-dir、镜像、数据、备份和检查点均保留。"
