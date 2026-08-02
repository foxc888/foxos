# FoxOS exact read-only install plan. It reruns preflight and the shared
# resource inspector, then binds operator confirmation to the current SHA-512.

:global FoxOSSiteManifestVersion
:global FoxOSSiteStorageRoot
:global FoxOSSiteManagementBridge
:global FoxOSSiteNetwork
:global FoxOSSiteRouterAddress
:global FoxOSSiteMihomoAddress
:global FoxOSSiteMosDNSAddress
:global FoxOSSiteFoxOSAddress
:global FoxOSSitePublicHostname
:global FoxOSSiteSubscriptionPrivateCIDRs
:global FoxOSInstallInspectVerbose true
:global FoxOSInstallCurrentDigest
:global FoxOSInstallApprovedDigest
:global FoxOSInstallConfirmation
:if ($FoxOSSiteManifestVersion != 2) do={ :error "先导入不可变的 load-site-config.rsc" }
/import file-name=($FoxOSSiteStorageRoot . "/load-site-config.rsc")

/import file-name=($FoxOSSiteStorageRoot . "/preflight.rsc")
/import file-name=($FoxOSSiteStorageRoot . "/foxos-install-inspect.rsc")
:if ([:len $FoxOSInstallCurrentDigest] != 128) do={ :error "只读检查未生成有效计划摘要" }

:set FoxOSInstallApprovedDigest $FoxOSInstallCurrentDigest
:set FoxOSInstallConfirmation ""
:local storageMode "disk"
:if ($FoxOSSiteStorageRoot = "foxos") do={ :set storageMode "internal" }
:put "=== FoxOS exact install plan ==="
:put ("Prerequisites: RouterOS 7.21+, architecture-name=x86 or x86_64 normalized to amd64, matching container package, container=yes, scheduler=yes, bridge=" . $FoxOSSiteManagementBridge . ", storage-mode=" . $storageMode . ", storage=" . $FoxOSSiteStorageRoot . ".")
:put ("Site " . $FoxOSSiteNetwork . ": RouterOS=" . $FoxOSSiteRouterAddress . ", Mihomo=" . $FoxOSSiteMihomoAddress . ", MosDNS=" . $FoxOSSiteMosDNSAddress . ", FoxOS=https://" . $FoxOSSitePublicHostname . " (" . $FoxOSSiteFoxOSAddress . ").")
:put "CREATE and REUSE decisions above are the complete RouterOS resource pre-state for this plan. Any FAIL stops here."
:put "The write phase manages only foxos-rest, foxos-service, FoxOS env/mount/veth/bridge-port owners, containers foxos-mihomo, foxos-mosdns, initial admin slot foxos-initial, the owned sequential-start scheduler/script, and Mihomo secret lines."
:put "DNS, DHCP/DHCP DNS, default routes, NAT, Mangle, firewall, FastTrack, and unknown resources remain untouched."
:put "Before confirmation, create an encrypted RouterOS v7 binary backup in WinBox with AES-SHA256 and a unique offline password. Also run /export hide-sensitive file=before-foxos."
:put ("Rollback: stop only exact FoxOS-owned containers, preserve " . $FoxOSSiteStorageRoot . "/foxos-data and images, then restore the reviewed binary backup if required.")
:put ("APPROVED PLAN SHA-512: " . $FoxOSInstallApprovedDigest)
:put ("To confirm this exact current state, run :global FoxOSInstallConfirmation \"" . $FoxOSInstallApprovedDigest . "\" then import foxos-full-install.rsc without re-importing or editing site-config.rsc.")
