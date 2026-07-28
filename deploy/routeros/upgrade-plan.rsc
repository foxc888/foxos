# Exact read-only plan for creating one release-bound pending container.

:global FoxOSSiteManifestVersion
:global FoxOSSiteStorageRoot
:global FoxOSUpgradeInspectVerbose true
:global FoxOSUpgradeCurrentDigest
:global FoxOSUpgradeApprovedDigest
:global FoxOSUpgradeConfirmation
:local releaseID "__FOXOS_RELEASE_ID__"
:if ($releaseID ~ "^__.*__$" || [:len $releaseID] < 1 || [:len $releaseID] > 40 || $releaseID !~ "^[A-Za-z0-9._-]+$") do={ :error "upgrade-plan.rsc 未绑定有效 release ID" }
:if ($FoxOSSiteManifestVersion != 2) do={ :error "先导入根目录不可变 load-site-config.rsc" }
:local payloadRoot ($FoxOSSiteStorageRoot . "/foxos-upgrade-" . $releaseID)
/import file-name=($FoxOSSiteStorageRoot . "/load-site-config.rsc")
/import file-name=($payloadRoot . "/upgrade-inspect.rsc")
:if ([:len $FoxOSUpgradeCurrentDigest] != 128) do={ :error "只读升级检查未生成有效摘要" }
:set FoxOSUpgradeApprovedDigest $FoxOSUpgradeCurrentDigest
:set FoxOSUpgradeConfirmation ""
:put "=== FoxOS pending creation plan (read-only) ==="
:put "Impact: create exactly one stopped, start-on-boot=no pending container. The active container stays running."
:put "No config, data, image, root-dir, DNS, DHCP, route, NAT, Mangle, firewall, or FastTrack entry is overwritten or removed."
:put ("APPROVED UPGRADE SHA-512: " . $FoxOSUpgradeApprovedDigest)
:put ("Confirm with :global FoxOSUpgradeConfirmation \"" . $FoxOSUpgradeApprovedDigest . "\" then import " . $payloadRoot . "/upgrade.rsc without changing RouterOS state.")
