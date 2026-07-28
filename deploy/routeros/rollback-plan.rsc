# Exact read-only rollback plan bound to the active versioned release payload.

:global FoxOSSiteManifestVersion
:global FoxOSSiteStorageRoot
:global FoxOSRollbackInspectVerbose true
:global FoxOSRollbackCurrentDigest
:global FoxOSRollbackApprovedDigest
:global FoxOSRollbackConfirmation
:if ($FoxOSSiteManifestVersion != 2) do={ :error "先导入不可变的 load-site-config.rsc" }
/import file-name=($FoxOSSiteStorageRoot . "/load-site-config.rsc")
:local releaseID "__FOXOS_RELEASE_ID__"
:if ($releaseID ~ "^__.*__$" || [:len $releaseID] < 1 || [:len $releaseID] > 40 || $releaseID !~ "^[A-Za-z0-9._-]+$") do={ :error "rollback-plan.rsc 未绑定有效 release ID" }
:local payloadRoot ($FoxOSSiteStorageRoot . "/foxos-upgrade-" . $releaseID)
/import file-name=($payloadRoot . "/rollback-inspect.rsc")
:if ([:len $FoxOSRollbackCurrentDigest] != 128) do={ :error "只读 rollback 检查未生成有效摘要" }
:set FoxOSRollbackApprovedDigest $FoxOSRollbackCurrentDigest
:set FoxOSRollbackConfirmation ""
:put "=== FoxOS rollback plan (read-only) ==="
:put ("RELEASE " . $releaseID . "; stop its exact active slot, start the exact stopped rollback slot, and verify it before switching ownership.")
:put "The old binary may atomically restore the SQLite upgrade checkpoint before opening the database. Failed rollback acceptance requests recovery of the current active slot."
:put "PRESERVE both containers, root-dirs, image payloads, persistent configuration, data, backups, CA, and checkpoint."
:put ("APPROVED ROLLBACK SHA-512: " . $FoxOSRollbackApprovedDigest)
:put ("Confirm with :global FoxOSRollbackConfirmation \"" . $FoxOSRollbackApprovedDigest . "\" then import " . $payloadRoot . "/rollback.rsc without changing RouterOS state.")
