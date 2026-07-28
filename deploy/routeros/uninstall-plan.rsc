# Exact read-only uninstall plan. Persistent files are retained by default.

:global FoxOSSiteManifestVersion
:global FoxOSSiteStorageRoot
:global FoxOSUninstallInspectVerbose true
:global FoxOSUninstallCurrentDigest
:global FoxOSUninstallApprovedDigest
:global FoxOSUninstallConfirmation
:global FoxOSUninstallContainerCount
:global FoxOSUninstallDNSCount
:global FoxOSUninstallRemainingCount
:if ($FoxOSSiteManifestVersion != 2) do={ :error "先导入不可变的 load-site-config.rsc" }
/import file-name=($FoxOSSiteStorageRoot . "/load-site-config.rsc")
/import file-name=($FoxOSSiteStorageRoot . "/foxos-uninstall-inspect.rsc")
:if ([:len $FoxOSUninstallCurrentDigest] != 128) do={ :error "只读卸载检查未生成有效摘要" }
:set FoxOSUninstallApprovedDigest $FoxOSUninstallCurrentDigest
:set FoxOSUninstallConfirmation ""
:put "=== FoxOS exact uninstall plan (read-only) ==="
:put ("REMAINING DELETE SET entries=" . $FoxOSUninstallRemainingCount . "; owned containers=" . $FoxOSUninstallContainerCount . ". Missing resources above are DONE and will be skipped.")
:put ("REMOVE optional owned DNS record count=" . $FoxOSUninstallDNSCount . "; unknown DNS, DHCP, routes, NAT, Mangle, firewall, FastTrack, bridges, addresses, disks, users, and certificates stay untouched.")
:put ("PRESERVE all files under " . $FoxOSSiteStorageRoot . ", including foxos-data, foxos-backups, configs, image archives, versioned root-dirs, site manifest, and RouterOS backups.")
:put "Running containers will be stopped and polled before removal. A failure stops closed; rerun this plan to bind the new remaining set before retrying."
:put ("APPROVED UNINSTALL SHA-512: " . $FoxOSUninstallApprovedDigest)
:put ("Confirm with :global FoxOSUninstallConfirmation \"" . $FoxOSUninstallApprovedDigest . "\" then import uninstall-apply.rsc without changing RouterOS state.")
