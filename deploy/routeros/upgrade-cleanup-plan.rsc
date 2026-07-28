# Read-only retirement plan for the single immediate rollback evidence slot.
# The shared inspector binds the complete active, rollback, network, mount,
# site-manifest, and ordered cold-start state into one SHA-512 digest.

:global FoxOSSiteManifestVersion
:global FoxOSSiteStorageRoot
:global FoxOSUpgradeCleanupInspectVerbose true
:global FoxOSUpgradeCleanupCurrentDigest
:global FoxOSUpgradeCleanupApprovedDigest
:global FoxOSUpgradeCleanupConfirmation
:global FoxOSUpgradeCleanupActiveID
:global FoxOSUpgradeCleanupRollbackID
:global FoxOSUpgradeCleanupRollbackMarker
:if ($FoxOSSiteManifestVersion != 2) do={ :error "先导入不可变的 load-site-config.rsc" }
/import file-name=($FoxOSSiteStorageRoot . "/load-site-config.rsc")
:local releaseID "__FOXOS_RELEASE_ID__"
:if ($releaseID ~ "^__.*__$" || [:len $releaseID] < 1 || [:len $releaseID] > 40 || $releaseID !~ "^[A-Za-z0-9._-]+$") do={ :error "upgrade-cleanup-plan.rsc 未绑定有效 release ID" }
:local payloadRoot ($FoxOSSiteStorageRoot . "/foxos-upgrade-" . $releaseID)
/import file-name=($payloadRoot . "/upgrade-cleanup-inspect.rsc")

:local digest $FoxOSUpgradeCleanupCurrentDigest
:local retirement $FoxOSUpgradeCleanupRollbackID
:local sourceMarker $FoxOSUpgradeCleanupRollbackMarker
:if ([:len $digest] != 128 || [:len $FoxOSUpgradeCleanupActiveID] != 1 || [:len $retirement] != 1) do={ :error "cleanup inspector 未返回完整摘要或对象 snapshot" }
:set FoxOSUpgradeCleanupApprovedDigest $digest
:set FoxOSUpgradeCleanupConfirmation ""
:put "=== FoxOS rollback retirement plan (read-only) ==="
:put ("CHANGE only comment " . $sourceMarker . " -> foxos:retained on id=" . [/container get $retirement .id] . ".")
:put "KEEP the unique running active, shared mounts, veth, sealed site manifest, and enabled ordered cold-start coordinator exactly as inspected."
:put "This expires immediate scripted rollback. It does not delete the container, root-dir, image, data, backups, or SQLite checkpoint."
:if ($sourceMarker = "foxos:rollback-complete") do={ :put "The next upgrade must use a new release ID; the completed release name and root-dir remain retained." }
:put ("APPROVED CLEANUP SHA-512: " . $digest)
:put ("Confirm with :global FoxOSUpgradeCleanupConfirmation \"" . $digest . "\" then import " . $payloadRoot . "/upgrade-cleanup-apply.rsc without changing RouterOS state.")
