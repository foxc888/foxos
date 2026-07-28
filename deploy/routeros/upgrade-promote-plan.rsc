# Exact read-only plan for checkpointing, stopping active, validating pending,
# and switching ownership, or for revalidating a switched active before the
# post-switch audit write is resumed.

:global FoxOSSiteManifestVersion
:global FoxOSSiteStorageRoot
:global FoxOSUpgradePromoteInspectVerbose true
:global FoxOSUpgradePromoteCurrentDigest
:global FoxOSUpgradePromoteApprovedDigest
:global FoxOSUpgradePromoteConfirmation
:global FoxOSUpgradePromoteState
:local releaseID "__FOXOS_RELEASE_ID__"
:if ($releaseID ~ "^__.*__$" || [:len $releaseID] < 1 || [:len $releaseID] > 40 || $releaseID !~ "^[A-Za-z0-9._-]+$") do={ :error "upgrade-promote-plan.rsc 未绑定有效 release ID" }
:if ($FoxOSSiteManifestVersion != 2) do={ :error "先导入根目录不可变 load-site-config.rsc" }
:local payloadRoot ($FoxOSSiteStorageRoot . "/foxos-upgrade-" . $releaseID)
/import file-name=($FoxOSSiteStorageRoot . "/load-site-config.rsc")
/import file-name=($payloadRoot . "/upgrade-promote-inspect.rsc")
:if ([:len $FoxOSUpgradePromoteCurrentDigest] != 128) do={ :error "只读 promote 检查未生成有效摘要" }
:set FoxOSUpgradePromoteApprovedDigest $FoxOSUpgradePromoteCurrentDigest
:set FoxOSUpgradePromoteConfirmation ""
:put "=== FoxOS promote plan (read-only) ==="
:if ($FoxOSUpgradePromoteState = "pending") do={
  :put "Impact: create a release-bound SQLite checkpoint, stop old active, start and verify pending, then atomically mark old=rollback and new=active."
}
:if ($FoxOSUpgradePromoteState = "switched") do={
  :put "Impact: ownership is already switched; revalidate the running new active through live, ready, page, and authenticated read-only API checks, then retry the release-bound promoted audit write. If validation or the audit write cannot be confirmed, apply preserves the new active and stopped rollback and does not automatically abort or roll back."
}
:put "Failure recovery keeps both versioned root-dirs and the SQLite checkpoint; no config, image, data, backup, network, DNS, DHCP, route, NAT, Mangle, firewall, or FastTrack entry is deleted."
:put ("APPROVED PROMOTE SHA-512: " . $FoxOSUpgradePromoteApprovedDigest)
:put ("Confirm with :global FoxOSUpgradePromoteConfirmation \"" . $FoxOSUpgradePromoteApprovedDigest . "\" then import " . $payloadRoot . "/upgrade-promote.rsc without changing RouterOS state.")
