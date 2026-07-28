# Shared read-only uninstall-state inspection. Missing owned resources are DONE;
# existing resources must match the exact FoxOS contract before they enter the
# remaining deletion set bound to a SHA-512 digest.

:global FoxOSSiteManifestVersion
:global FoxOSSiteManagementBridge
:global FoxOSSiteStorageRoot
:global FoxOSSitePrefixLength
:global FoxOSSiteRouterAddress
:global FoxOSSiteMihomoAddress
:global FoxOSSiteMosDNSAddress
:global FoxOSSiteFoxOSAddress
:global FoxOSSitePublicHostname
:global FoxOSUninstallInspectVerbose
:global FoxOSUninstallCurrentDigest
:global FoxOSUninstallContainerCount
:global FoxOSUninstallDNSCount
:global FoxOSUninstallRemainingCount
:if ($FoxOSSiteManifestVersion != 2) do={ :error "先导入不可变的 load-site-config.rsc" }

:local failed false
:local remaining 0
:local material ("foxos-uninstall-v2|" . $FoxOSSiteManagementBridge . "|" . $FoxOSSiteStorageRoot . "|" . $FoxOSSiteFoxOSAddress . "|" . $FoxOSSitePublicHostname)
:local activeCount 0
:local mihomoCount 0
:local mosdnsCount 0
:local pendingCount 0
:local rollbackCount 0
:local rollbackCompleteCount 0
:local promoteTransitionCount 0
:local rollbackTransitionCount 0
:local rollbackPreviousCount 0
:local ownedContainers [/container find where comment~"^foxos:"]
:set FoxOSUninstallContainerCount [:len $ownedContainers]

:foreach containerID in=$ownedContainers do={
  :local owner [/container get $containerID comment]
  :local accepted false
  :if ($owner = "foxos:mihomo") do={
    :set mihomoCount ($mihomoCount + 1)
    :if ([/container get $containerID name] = "foxos-mihomo" && [/container get $containerID interface] = "veth-mihomo" && [/container get $containerID envlists] = "" && [/container get $containerID mountlists] = "foxos-mihomo-runtime" && [/container get $containerID root-dir] = ($FoxOSSiteStorageRoot . "/containers/mihomo")) do={ :set accepted true }
  }
  :if ($owner = "foxos:mosdns") do={
    :set mosdnsCount ($mosdnsCount + 1)
    :if ([/container get $containerID name] = "foxos-mosdns" && [/container get $containerID interface] = "veth-mosdns" && [/container get $containerID envlists] = "foxos-mosdns-env" && [/container get $containerID mountlists] = "foxos-mosdns-runtime" && [/container get $containerID root-dir] = ($FoxOSSiteStorageRoot . "/containers/mosdns")) do={ :set accepted true }
  }
  :if ($owner = "foxos:active" || $owner = "foxos:pending" || $owner = "foxos:rollback" || $owner = "foxos:rollback-complete" || $owner = "foxos:transition:promote" || $owner = "foxos:transition:rollback" || $owner = "foxos:transition:rollback:previous" || $owner = "foxos:retained" || $owner = "foxos:failed") do={
    :if ($owner = "foxos:active") do={ :set activeCount ($activeCount + 1) }
    :if ($owner = "foxos:pending") do={ :set pendingCount ($pendingCount + 1) }
    :if ($owner = "foxos:rollback") do={ :set rollbackCount ($rollbackCount + 1) }
    :if ($owner = "foxos:rollback-complete") do={ :set rollbackCompleteCount ($rollbackCompleteCount + 1) }
    :if ($owner = "foxos:transition:promote") do={ :set promoteTransitionCount ($promoteTransitionCount + 1) }
    :if ($owner = "foxos:transition:rollback") do={ :set rollbackTransitionCount ($rollbackTransitionCount + 1) }
    :if ($owner = "foxos:transition:rollback:previous") do={ :set rollbackPreviousCount ($rollbackPreviousCount + 1) }
    :local adminName [/container get $containerID name]
    :if ($adminName ~ "^foxos-[A-Za-z0-9._-]+$" && [/container get $containerID interface] = "veth-foxos" && [/container get $containerID envlists] = "foxos-env" && [/container get $containerID mountlists] = "foxos-mihomo-config,foxos-data,foxos-backups" && [/container get $containerID root-dir] = ($FoxOSSiteStorageRoot . "/containers/" . $adminName)) do={ :set accepted true }
  }
  :local status [/container get $containerID status]
  :if ($status != "running" && $status != "stopped") do={ :set accepted false }
  :if (([/container get $containerID start-on-boot] != false && [/container get $containerID start-on-boot] != "no") || ([/container get $containerID logging] != true && [/container get $containerID logging] != "yes")) do={ :set accepted false }
  :if ($accepted = false) do={
    :if ($FoxOSUninstallInspectVerbose) do={ :put ("FAIL container id=" . [/container get $containerID .id] . " owner=" . $owner) }
    :set failed true
  } else={
    :set remaining ($remaining + 1)
    :if ($FoxOSUninstallInspectVerbose) do={ :put ("REMOVE container " . [/container get $containerID name] . " owner=" . $owner . " status=" . $status) }
    :set material ($material . "|container:" . [/container get $containerID .id] . ":" . [/container get $containerID name] . ":" . $owner . ":" . [/container get $containerID root-dir] . ":" . [/container get $containerID interface] . ":" . [/container get $containerID envlists] . ":" . [/container get $containerID mountlists] . ":" . $status . ":" . [/container get $containerID start-on-boot] . ":" . [/container get $containerID logging])
  }
}
:if ($activeCount > 1 || $mihomoCount > 1 || $mosdnsCount > 1 || $pendingCount > 1 || $rollbackCount > 1 || $rollbackCompleteCount > 1 || $promoteTransitionCount > 1 || $rollbackTransitionCount > 1 || $rollbackPreviousCount > 1) do={ :set failed true }
:foreach serviceName in={"foxos-mihomo";"foxos-mosdns"} do={
  :local named [/container find where name=$serviceName]
  :if ([:len $named] > 1) do={ :set failed true }
  :if ([:len $named] = 1 && [/container get $named comment] !~ "^foxos:") do={ :set failed true }
}
:if ([:len $ownedContainers] = 0 && $FoxOSUninstallInspectVerbose) do={ :put "DONE containers" }

:local expectedStartSource (":delay 20s; /import file-name=" . $FoxOSSiteStorageRoot . "/load-site-config.rsc; /import file-name=" . $FoxOSSiteStorageRoot . "/foxos-start-all.rsc")
:local startScriptByName [/system/script find where name="foxos-start-sequence"]
:local startScriptByOwner [/system/script find where comment="foxos:start-sequence"]
:if ([:len $startScriptByName] = 0 && [:len $startScriptByOwner] = 0) do={
  :set material ($material . "|start-script:DONE")
  :if ($FoxOSUninstallInspectVerbose) do={ :put "DONE system-script/foxos-start-sequence" }
} else={
  :if ([:len $startScriptByName] != 1 || [:len $startScriptByOwner] != 1 || [/system/script get $startScriptByName .id] != [/system/script get $startScriptByOwner .id] || [/system/script get $startScriptByName source] != $expectedStartSource || [/system/script get $startScriptByName policy] != "read,write,test") do={
    :set failed true
  } else={
    :set remaining ($remaining + 1)
    :set material ($material . "|start-script:REMOVE:" . [/system/script get $startScriptByName .id])
    :if ($FoxOSUninstallInspectVerbose) do={ :put "REMOVE system-script/foxos-start-sequence" }
  }
}
:local schedulerByName [/system/scheduler find where name="foxos-start-sequence"]
:local schedulerByOwner [/system/scheduler find where comment="foxos:start-sequence"]
:if ([:len $schedulerByName] = 0 && [:len $schedulerByOwner] = 0) do={
  :set material ($material . "|scheduler:DONE")
  :if ($FoxOSUninstallInspectVerbose) do={ :put "DONE scheduler/foxos-start-sequence" }
} else={
  :if ([:len $schedulerByName] != 1 || [:len $schedulerByOwner] != 1 || [/system/scheduler get $schedulerByName .id] != [/system/scheduler get $schedulerByOwner .id] || [/system/scheduler get $schedulerByName on-event] != "foxos-start-sequence" || [/system/scheduler get $schedulerByName start-time] != "startup" || [/system/scheduler get $schedulerByName interval] != "0s" || [/system/scheduler get $schedulerByName policy] != "read,write,test") do={
    :set failed true
  } else={
    :set remaining ($remaining + 1)
    :set material ($material . "|scheduler:REMOVE:" . [/system/scheduler get $schedulerByName .id] . ":" . [/system/scheduler get $schedulerByName disabled] . ":" . [/system/scheduler get $schedulerByName interval] . ":" . [/system/scheduler get $schedulerByName policy])
    :if ($FoxOSUninstallInspectVerbose) do={ :put "REMOVE scheduler/foxos-start-sequence" }
  }
}

:local allowedEnvKeys "|FOXOS_INSTALL_MARKER|FOXOS_ENV|FOXOS_ROUTEROS_URL|FOXOS_ROUTEROS_USERNAME|FOXOS_ROUTEROS_PASSWORD|FOXOS_MIHOMO_URL|FOXOS_MIHOMO_PROXY_URL|FOXOS_MIHOMO_SECRET|FOXOS_MIHOMO_BASE_CONFIG|FOXOS_MIHOMO_LOCAL_CONFIG|FOXOS_MIHOMO_RUNTIME_CONFIG|FOXOS_MIHOMO_BACKUP_DIR|FOXOS_MIHOMO_VALIDATOR_BINARY|FOXOS_MOSDNS_URL|FOXOS_API_TOKEN|FOXOS_CONFIRMATION_KEY|FOXOS_BACKUP_DIR|FOXOS_UPGRADE_STATE_PATH|FOXOS_SITE_MANAGEMENT_BRIDGE|FOXOS_SITE_STORAGE_ROOT|FOXOS_SITE_NETWORK|FOXOS_SITE_ROUTER_ADDRESS|FOXOS_SITE_MIHOMO_ADDRESS|FOXOS_SITE_MOSDNS_ADDRESS|FOXOS_SITE_FOXOS_ADDRESS|FOXOS_SITE_PUBLIC_HOSTNAME|FOXOS_HTTPS_ENABLED|FOXOS_SUBSCRIPTION_PRIVATE_CIDRS|"
:local envItems [/container/envs find where list="foxos-env"]
:if ([:len $envItems] = 0) do={
  :set material ($material . "|env:DONE")
  :if ($FoxOSUninstallInspectVerbose) do={ :put "DONE env/foxos-env" }
} else={
  :local envMarker [/container/envs find where list="foxos-env" key="FOXOS_INSTALL_MARKER"]
  :if ([:len $envMarker] != 1 || ([/container/envs get $envMarker value] != "foxos:applying" && [/container/envs get $envMarker value] != "foxos:complete")) do={ :set failed true }
  :foreach envID in=$envItems do={
    :local envKey [/container/envs get $envID key]
    :if ([:typeof [:find $allowedEnvKeys ("|" . $envKey . "|")]] = "nil") do={ :set failed true }
    :local valueDigest [:convert [/container/envs get $envID value] transform=sha512 to=hex]
    :set material ($material . "|env:" . [/container/envs get $envID .id] . ":" . $envKey . ":" . $valueDigest)
    :set remaining ($remaining + 1)
  }
  :if ($FoxOSUninstallInspectVerbose) do={ :put ("REMOVE env/foxos-env entries=" . [:len $envItems]) }
}

:local mosdnsEnvItems [/container/envs find where list="foxos-mosdns-env"]
:if ([:len $mosdnsEnvItems] = 0) do={
  :set material ($material . "|mosdns-env:DONE")
  :if ($FoxOSUninstallInspectVerbose) do={ :put "DONE env/foxos-mosdns-env" }
} else={
  :local mosdnsMarker [/container/envs find where list="foxos-mosdns-env" key="FOXOS_INSTALL_MARKER"]
  :local autoInit [/container/envs find where list="foxos-mosdns-env" key="MOSDNS_AUTO_INIT"]
  :if ([:len $mosdnsMarker] != 1 || ([/container/envs get $mosdnsMarker value] != "foxos:mosdns:applying" && [/container/envs get $mosdnsMarker value] != "foxos:mosdns:complete") || [:len $autoInit] > 1 || [:len $mosdnsEnvItems] > 2) do={ :set failed true }
  :if ([:len $autoInit] = 1 && [/container/envs get $autoInit value] != "0") do={ :set failed true }
  :foreach envID in=$mosdnsEnvItems do={
    :local envKey [/container/envs get $envID key]
    :if ($envKey != "FOXOS_INSTALL_MARKER" && $envKey != "MOSDNS_AUTO_INIT") do={ :set failed true }
    :set material ($material . "|mosdns-env:" . [/container/envs get $envID .id] . ":" . $envKey)
    :set remaining ($remaining + 1)
  }
  :if ($FoxOSUninstallInspectVerbose) do={ :put ("REMOVE env/foxos-mosdns-env entries=" . [:len $mosdnsEnvItems]) }
}

:local groupID [/user/group find where name="foxos-rest"]
:local userID [/user find where name="foxos-service"]
:if ([:len $userID] > 1 || [:len $groupID] > 1) do={ :set failed true }
:if ([:len $userID] = 1) do={
  :if ([/user get $userID comment] != "foxos:service" || [/user get $userID group] != "foxos-rest" || [/user get $userID address] != ($FoxOSSiteFoxOSAddress . "/32")) do={ :set failed true }
  :set remaining ($remaining + 1)
  :set material ($material . "|user:REMOVE:" . [/user get $userID .id])
  :if ($FoxOSUninstallInspectVerbose) do={ :put "REMOVE user/foxos-service" }
} else={
  :set material ($material . "|user:DONE")
  :if ($FoxOSUninstallInspectVerbose) do={ :put "DONE user/foxos-service" }
}
:if ([:len $groupID] = 1) do={
  :if ([/user/group get $groupID policy] != "read,write,rest-api") do={ :set failed true }
  :set remaining ($remaining + 1)
  :set material ($material . "|group:REMOVE:" . [/user/group get $groupID .id])
  :if ($FoxOSUninstallInspectVerbose) do={ :put "REMOVE user-group/foxos-rest" }
} else={
  :if ([:len $userID] = 1) do={ :set failed true }
  :set material ($material . "|group:DONE")
  :if ($FoxOSUninstallInspectVerbose) do={ :put "DONE user-group/foxos-rest" }
}

:local mountDefinitions {"foxos-mihomo-runtime|mihomo-config|/root/.config/mihomo";"foxos-mihomo-config|mihomo-config|/data/mihomo";"foxos-mosdns-runtime|mosdns-config|/cus/mosdns";"foxos-data|foxos-data|/data";"foxos-backups|foxos-backups|/backups"}
:foreach definition in=$mountDefinitions do={
  :local p1 [:find $definition "|"]
  :local p2 [:find $definition "|" ($p1 + 1)]
  :local mountName [:pick $definition 0 $p1]
  :local sourcePath ($FoxOSSiteStorageRoot . "/" . [:pick $definition ($p1 + 1) $p2])
  :local destination [:pick $definition ($p2 + 1) [:len $definition]]
  :local mountID [/container/mounts find where list=$mountName]
  :if ([:len $mountID] = 0) do={
    :set material ($material . "|mount:" . $mountName . ":DONE")
    :if ($FoxOSUninstallInspectVerbose) do={ :put ("DONE mount/" . $mountName) }
  } else={
    :if ([:len $mountID] != 1 || [/container/mounts get $mountID src] != $sourcePath || [/container/mounts get $mountID dst] != $destination || ([/container/mounts get $mountID read-only] != false && [/container/mounts get $mountID read-only] != "no")) do={ :set failed true }
    :set remaining ($remaining + 1)
    :set material ($material . "|mount:" . $mountName . ":REMOVE:" . [/container/mounts get $mountID .id] . ":" . [/container/mounts get $mountID src] . ":" . [/container/mounts get $mountID dst] . ":" . [/container/mounts get $mountID read-only])
    :if ($FoxOSUninstallInspectVerbose) do={ :put ("REMOVE mount/" . $mountName) }
  }
}

:local vethDefinitions {("veth-mihomo|foxos:mihomo|" . $FoxOSSiteMihomoAddress . "/" . $FoxOSSitePrefixLength);("veth-mosdns|foxos:mosdns|" . $FoxOSSiteMosDNSAddress . "/" . $FoxOSSitePrefixLength);("veth-foxos|foxos:admin|" . $FoxOSSiteFoxOSAddress . "/" . $FoxOSSitePrefixLength)}
:foreach definition in=$vethDefinitions do={
  :local p1 [:find $definition "|"]
  :local p2 [:find $definition "|" ($p1 + 1)]
  :local vethName [:pick $definition 0 $p1]
  :local owner [:pick $definition ($p1 + 1) $p2]
  :local expectedAddress [:pick $definition ($p2 + 1) [:len $definition]]
  :local portID [/interface/bridge/port find where interface=$vethName]
  :if ([:len $portID] = 0) do={
    :set material ($material . "|port:" . $vethName . ":DONE")
    :if ($FoxOSUninstallInspectVerbose) do={ :put ("DONE bridge-port/" . $vethName) }
  } else={
    :if ([:len $portID] != 1 || [/interface/bridge/port get $portID bridge] != $FoxOSSiteManagementBridge || [/interface/bridge/port get $portID comment] != $owner) do={ :set failed true }
    :set remaining ($remaining + 1)
    :set material ($material . "|port:" . $vethName . ":REMOVE:" . [/interface/bridge/port get $portID .id])
    :if ($FoxOSUninstallInspectVerbose) do={ :put ("REMOVE bridge-port/" . $vethName) }
  }
  :local vethID [/interface/veth find where name=$vethName]
  :if ([:len $vethID] = 0) do={
    :set material ($material . "|veth:" . $vethName . ":DONE")
    :if ($FoxOSUninstallInspectVerbose) do={ :put ("DONE veth/" . $vethName) }
  } else={
    :if ([:len $vethID] != 1 || [/interface/veth get $vethID comment] != $owner || [/interface/veth get $vethID address] != $expectedAddress || [/interface/veth get $vethID gateway] != $FoxOSSiteRouterAddress) do={ :set failed true }
    :set remaining ($remaining + 1)
    :set material ($material . "|veth:" . $vethName . ":REMOVE:" . [/interface/veth get $vethID .id])
    :if ($FoxOSUninstallInspectVerbose) do={ :put ("REMOVE veth/" . $vethName) }
  }
}

:local dnsRecords [/ip/dns/static find where comment="foxos:dns:admin"]
:set FoxOSUninstallDNSCount [:len $dnsRecords]
:if ([:len $dnsRecords] > 1) do={ :set failed true }
:if ([:len $dnsRecords] = 1) do={
  :if ([/ip/dns/static get $dnsRecords name] != $FoxOSSitePublicHostname || [/ip/dns/static get $dnsRecords type] != "A" || [/ip/dns/static get $dnsRecords address] != $FoxOSSiteFoxOSAddress) do={ :set failed true }
  :set remaining ($remaining + 1)
  :set material ($material . "|dns:REMOVE:" . [/ip/dns/static get $dnsRecords .id] . ":" . [/ip/dns/static get $dnsRecords name] . ":" . [/ip/dns/static get $dnsRecords address])
  :if ($FoxOSUninstallInspectVerbose) do={ :put "REMOVE dns/foxos:dns:admin" }
} else={
  :set material ($material . "|dns:DONE")
  :if ($FoxOSUninstallInspectVerbose) do={ :put "DONE dns/foxos:dns:admin" }
}

:if ($failed) do={
  :set FoxOSUninstallCurrentDigest ""
  :set FoxOSUninstallRemainingCount 0
  :error "uninstall inspection found an ambiguous or non-owned conflict; nothing was changed"
}
:set FoxOSUninstallRemainingCount $remaining
:local digest [:convert $material transform=sha512 to=hex]
:if ([:len $digest] != 128) do={ :error "无法生成 uninstall SHA-512 摘要" }
:set FoxOSUninstallCurrentDigest $digest
:if ($FoxOSUninstallInspectVerbose) do={ :put ("UNINSTALL REMAINING " . $remaining . " DIGEST SHA-512 " . $digest) }
