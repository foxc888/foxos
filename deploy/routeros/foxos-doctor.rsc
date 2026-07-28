# FoxOS RouterOS strictly read-only host doctor.
# Output format: STATUS|check|key=value. STATUS is PASS, NEEDS-ACTION, or
# CONFLICT. This script reads metadata only and never prints secret values.

:global FoxOSSiteManifestVersion
:global FoxOSSiteManagementBridge
:global FoxOSSiteStorageRoot
:global FoxOSSiteNetwork
:global FoxOSSitePrefixLength
:global FoxOSSiteRouterAddress
:global FoxOSSiteFoxOSAddress
:global FoxOSSitePublicHostname
:global FoxOSSiteLoadedDigest
:global FoxOSSiteLoadedConfigPath
:global FoxOSSiteLoaderVersion

:local requiredArchitecture "x86"
:local minimumFreeBytes 536870912
:local needsActionCount 0
:local conflictCount 0

:put "PASS|doctor|name=foxos-doctor|mode=read-only|scope=host-prerequisites"

# RouterOS identity and version floor.
:local routerVersion [/system/resource get version]
:local architecture [/system/resource get architecture-name]
:local versionBase $routerVersion
:local versionSpace [:find $versionBase " "]
:if ([:typeof $versionSpace] != "nil") do={ :set versionBase [:pick $versionBase 0 $versionSpace] }
:local versionReady true
:local firstVersionDot [:find $versionBase "."]
:if ([:typeof $firstVersionDot] = "nil") do={
  :set versionReady false
} else={
  :local versionMajor [:tonum [:pick $versionBase 0 $firstVersionDot]]
  :local versionTail [:pick $versionBase ($firstVersionDot + 1) [:len $versionBase]]
  :local versionMinorEnd [:find ($versionTail . ".") "."]
  :local versionMinor [:tonum [:pick $versionTail 0 $versionMinorEnd]]
  :if ($versionMajor < 7 || ($versionMajor = 7 && $versionMinor < 21)) do={ :set versionReady false }
}
:if ($versionReady) do={
  :put ("PASS|routeros-version|value=" . $routerVersion . "|minimum=7.21")
} else={
  :put ("NEEDS-ACTION|routeros-version|value=" . $routerVersion . "|minimum=7.21")
  :set needsActionCount ($needsActionCount + 1)
}
:if ($architecture = $requiredArchitecture) do={
  :put ("PASS|architecture|value=" . $architecture . "|required=" . $requiredArchitecture)
} else={
  :put ("NEEDS-ACTION|architecture|value=" . $architecture . "|required=" . $requiredArchitecture)
  :set needsActionCount ($needsActionCount + 1)
}

# Use only topology loaded by the immutable loader. The digest itself is never
# printed. preflight.rsc remains the authoritative full manifest verifier.
:local manifestReady true
:local managementBridge ""
:local storageRoot ""
:local siteNetwork ""
:local prefixLength 0
:local routerAddress ""
:local foxosAddress ""
:local publicHostname ""
:if ($FoxOSSiteManifestVersion != 2 || $FoxOSSiteLoaderVersion != 1) do={ :set manifestReady false }
:if ($manifestReady) do={
  :set managementBridge $FoxOSSiteManagementBridge
  :set storageRoot $FoxOSSiteStorageRoot
  :set siteNetwork $FoxOSSiteNetwork
  :set prefixLength $FoxOSSitePrefixLength
  :set routerAddress $FoxOSSiteRouterAddress
  :set foxosAddress $FoxOSSiteFoxOSAddress
  :set publicHostname $FoxOSSitePublicHostname
  :if ([:len $managementBridge] < 1 || [:len $storageRoot] < 1 || [:len $siteNetwork] < 1 || [:len $routerAddress] < 1 || [:len $foxosAddress] < 1 || [:len $publicHostname] < 1) do={ :set manifestReady false }
  :if ($FoxOSSiteLoadedConfigPath != ($storageRoot . "/site-config.rsc") || [:len $FoxOSSiteLoadedDigest] != 128) do={ :set manifestReady false }
}
:if ($manifestReady) do={
  :put ("PASS|site-manifest|version=2|loader-version=1|bridge=" . $managementBridge . "|storage=" . $storageRoot . "|network=" . $siteNetwork . "|router-address=" . $routerAddress . "|foxos-address=" . $foxosAddress)
} else={
  :put "NEEDS-ACTION|site-manifest|reason=load-sealed-site-config-first"
  :set needsActionCount ($needsActionCount + 1)
}

# The release bundle requires one enabled, exact-version container package.
:local containerPackage [/system/package find where name="container"]
:if ([:len $containerPackage] != 1) do={
  :put ("NEEDS-ACTION|container-package|count=" . [:len $containerPackage] . "|required-count=1")
  :set needsActionCount ($needsActionCount + 1)
} else={
  :local packageVersion [/system/package get $containerPackage version]
  :local packageDisabled [/system/package get $containerPackage disabled]
  :if (($packageDisabled = true || $packageDisabled = "yes") || $packageVersion != $versionBase) do={
    :put ("NEEDS-ACTION|container-package|count=1|version=" . $packageVersion . "|routeros-version=" . $versionBase . "|disabled=" . $packageDisabled)
    :set needsActionCount ($needsActionCount + 1)
  } else={
    :put ("PASS|container-package|count=1|version=" . $packageVersion . "|disabled=" . $packageDisabled)
  }
}

# Device-mode reads are guarded so an unavailable property still produces a
# complete doctor result instead of aborting the script.
:local deviceModeReadable false
:local containerDeviceMode "unavailable"
:local schedulerDeviceMode "unavailable"
:onerror deviceModeReadError in={
  :set containerDeviceMode [/system/device-mode get container]
  :set schedulerDeviceMode [/system/device-mode get scheduler]
  :set deviceModeReadable true
} do={}
:if ($deviceModeReadable && ($containerDeviceMode = true || $containerDeviceMode = "yes")) do={
  :put ("PASS|device-mode-container|value=" . $containerDeviceMode)
} else={
  :put ("NEEDS-ACTION|device-mode-container|value=" . $containerDeviceMode . "|required=yes")
  :set needsActionCount ($needsActionCount + 1)
}
:if ($deviceModeReadable && ($schedulerDeviceMode = true || $schedulerDeviceMode = "yes")) do={
  :put ("PASS|device-mode-scheduler|value=" . $schedulerDeviceMode)
} else={
  :put ("NEEDS-ACTION|device-mode-scheduler|value=" . $schedulerDeviceMode . "|required=yes")
  :set needsActionCount ($needsActionCount + 1)
}

# Configured bridge, RouterOS address, storage, and REST posture.
:if ($manifestReady) do={
  :local bridgeID [/interface/bridge find where name=$managementBridge]
  :if ([:len $bridgeID] = 1) do={
    :put ("PASS|management-bridge|name=" . $managementBridge . "|count=1")
  } else={
    :put ("NEEDS-ACTION|management-bridge|name=" . $managementBridge . "|count=" . [:len $bridgeID] . "|required-count=1")
    :set needsActionCount ($needsActionCount + 1)
  }

  :local routerCIDR ($routerAddress . "/" . $prefixLength)
  :local routerIP [/ip/address find where address=$routerCIDR]
  :if ([:len $routerIP] = 1 && [/ip/address get $routerIP interface] = $managementBridge) do={
    :put ("PASS|management-address|address=" . $routerCIDR . "|interface=" . $managementBridge . "|count=1")
  } else={
    :local actualInterface "unavailable"
    :if ([:len $routerIP] = 1) do={ :set actualInterface [/ip/address get $routerIP interface] }
    :put ("NEEDS-ACTION|management-address|address=" . $routerCIDR . "|required-interface=" . $managementBridge . "|actual-interface=" . $actualInterface . "|count=" . [:len $routerIP])
    :set needsActionCount ($needsActionCount + 1)
  }

  :local diskID [/disk find where slot=$storageRoot]
  :if ([:len $diskID] != 1) do={
    :put ("NEEDS-ACTION|storage|slot=" . $storageRoot . "|count=" . [:len $diskID] . "|required-count=1")
    :set needsActionCount ($needsActionCount + 1)
  } else={
    :local diskFree [/disk get $diskID free]
    :if ($diskFree < $minimumFreeBytes) do={
      :put ("NEEDS-ACTION|storage|slot=" . $storageRoot . "|free-bytes=" . $diskFree . "|minimum-free-bytes=" . $minimumFreeBytes)
      :set needsActionCount ($needsActionCount + 1)
    } else={
      :put ("PASS|storage|slot=" . $storageRoot . "|free-bytes=" . $diskFree . "|minimum-free-bytes=" . $minimumFreeBytes)
    }
  }

  :local wwwService [/ip/service find where name="www"]
  :if ([:len $wwwService] != 1) do={
    :put ("NEEDS-ACTION|rest-www|count=" . [:len $wwwService] . "|required-count=1")
    :set needsActionCount ($needsActionCount + 1)
  } else={
    :local wwwDisabled [/ip/service get $wwwService disabled]
    :local wwwPort [/ip/service get $wwwService port]
    :local wwwAddresses [/ip/service get $wwwService address]
    :local wwwCursor 0
    :local wwwCount 0
    :local wwwSeen ","
    :local wwwAllowed true
    :local foxosRESTAddress ($foxosAddress . "/32")
    :if ([:len $wwwAddresses] = 0 || [:pick $wwwAddresses 0 1] = "," || [:pick $wwwAddresses ([:len $wwwAddresses] - 1) [:len $wwwAddresses]] = "," || [:typeof [:find $wwwAddresses ",,"]] != "nil") do={ :set wwwAllowed false }
    :while ($wwwAllowed && $wwwCursor < [:len $wwwAddresses]) do={
      :local wwwEnd [:find $wwwAddresses "," $wwwCursor]
      :if ([:typeof $wwwEnd] = "nil") do={ :set wwwEnd [:len $wwwAddresses] }
      :local wwwAddress [:pick $wwwAddresses $wwwCursor $wwwEnd]
      :set wwwCursor ($wwwEnd + 1)
      :set wwwCount ($wwwCount + 1)
      :if (($wwwAddress != $siteNetwork && $wwwAddress != $foxosRESTAddress) || [:typeof [:find $wwwSeen ("," . $wwwAddress . ",")]] != "nil" || $wwwCount > 2) do={ :set wwwAllowed false }
      :set wwwSeen ($wwwSeen . $wwwAddress . ",")
    }
    :if (($wwwDisabled = true || $wwwDisabled = "yes") || $wwwPort != 80 || $wwwAllowed = false || $wwwCount < 1) do={
      :put ("NEEDS-ACTION|rest-www|disabled=" . $wwwDisabled . "|port=" . $wwwPort . "|address=" . $wwwAddresses . "|allowed=" . $wwwAllowed)
      :set needsActionCount ($needsActionCount + 1)
    } else={
      :put ("PASS|rest-www|disabled=" . $wwwDisabled . "|port=" . $wwwPort . "|address=" . $wwwAddresses . "|allowed=true")
    }
  }
} else={
  :put "NEEDS-ACTION|management-bridge|inspection=skipped|reason=manifest-not-loaded"
  :put "NEEDS-ACTION|management-address|inspection=skipped|reason=manifest-not-loaded"
  :put "NEEDS-ACTION|storage|inspection=skipped|reason=manifest-not-loaded"
  :put "NEEDS-ACTION|rest-www|inspection=skipped|reason=manifest-not-loaded"
  :set needsActionCount ($needsActionCount + 4)
}

# Container metadata only. No env value, command, or container log is read.
:local containerMetadataReadable false
:local ownedContainerCount 0
:local namedContainerCount 0
:local foxosEnvCount 0
:local mosdnsEnvCount 0
:local reservedEnvCount 0
:local reservedMountCount 0
:onerror containerMetadataReadError in={
  :set ownedContainerCount [:len [/container find where comment~"^foxos:"]]
  :set namedContainerCount [:len [/container find where name~"^foxos-"]]
  :set foxosEnvCount [:len [/container/envs find where list="foxos-env"]]
  :set mosdnsEnvCount [:len [/container/envs find where list="foxos-mosdns-env"]]
  :set reservedEnvCount [:len [/container/envs find where list~"^foxos-"]]
  :set reservedMountCount [:len [/container/mounts find where list~"^foxos-"]]
  :set containerMetadataReadable true
} do={}
:if ($containerMetadataReadable = false) do={
  :put "NEEDS-ACTION|footprint-container-metadata|inspection=unavailable"
  :set needsActionCount ($needsActionCount + 1)
} else={
  :if ($ownedContainerCount > 0 || $namedContainerCount > 0) do={
    :put ("CONFLICT|footprint-containers|owned-count=" . $ownedContainerCount . "|reserved-name-count=" . $namedContainerCount)
    :set conflictCount ($conflictCount + 1)
  } else={
    :put "PASS|footprint-containers|owned-count=0|reserved-name-count=0"
  }
  :if ($reservedEnvCount > 0) do={
    :put ("CONFLICT|footprint-env-lists|reserved-entry-count=" . $reservedEnvCount . "|foxos-env-count=" . $foxosEnvCount . "|foxos-mosdns-env-count=" . $mosdnsEnvCount)
    :set conflictCount ($conflictCount + 1)
  } else={
    :put "PASS|footprint-env-lists|reserved-entry-count=0|foxos-env-count=0|foxos-mosdns-env-count=0"
  }
  :if ($reservedMountCount > 0) do={
    :put ("CONFLICT|footprint-mounts|reserved-count=" . $reservedMountCount)
    :set conflictCount ($conflictCount + 1)
  } else={
    :put "PASS|footprint-mounts|reserved-count=0"
  }
}

# Reserved VETH and bridge-port names plus FoxOS ownership comments.
:local vethMetadataReadable false
:local namedVethCount 0
:local ownedVethCount 0
:local namedPortCount 0
:local ownedPortCount 0
:onerror vethMetadataReadError in={
  :set namedVethCount ([:len [/interface/veth find where name="veth-mihomo"]] + [:len [/interface/veth find where name="veth-mosdns"]] + [:len [/interface/veth find where name="veth-foxos"]])
  :set ownedVethCount [:len [/interface/veth find where comment~"^foxos:"]]
  :set namedPortCount ([:len [/interface/bridge/port find where interface="veth-mihomo"]] + [:len [/interface/bridge/port find where interface="veth-mosdns"]] + [:len [/interface/bridge/port find where interface="veth-foxos"]])
  :set ownedPortCount [:len [/interface/bridge/port find where comment~"^foxos:"]]
  :set vethMetadataReadable true
} do={}
:if ($vethMetadataReadable = false) do={
  :put "NEEDS-ACTION|footprint-veth-ports|inspection=unavailable"
  :set needsActionCount ($needsActionCount + 1)
} else={
  :if ($namedVethCount > 0 || $ownedVethCount > 0 || $namedPortCount > 0 || $ownedPortCount > 0) do={
    :put ("CONFLICT|footprint-veth-ports|reserved-veth-count=" . $namedVethCount . "|owned-veth-count=" . $ownedVethCount . "|reserved-port-count=" . $namedPortCount . "|owned-port-count=" . $ownedPortCount)
    :set conflictCount ($conflictCount + 1)
  } else={
    :put "PASS|footprint-veth-ports|reserved-veth-count=0|owned-veth-count=0|reserved-port-count=0|owned-port-count=0"
  }
}

# Reserved RouterOS user, script, scheduler, and DNS identities.
:local serviceUserCount [:len [/user find where name="foxos-service"]]
:local ownedUserCount [:len [/user find where comment~"^foxos:"]]
:local serviceGroupCount [:len [/user/group find where name="foxos-rest"]]
:if ($serviceUserCount > 0 || $ownedUserCount > 0 || $serviceGroupCount > 0) do={
  :put ("CONFLICT|footprint-access|service-user-count=" . $serviceUserCount . "|owned-user-count=" . $ownedUserCount . "|service-group-count=" . $serviceGroupCount)
  :set conflictCount ($conflictCount + 1)
} else={
  :put "PASS|footprint-access|service-user-count=0|owned-user-count=0|service-group-count=0"
}

:local startScriptCount [:len [/system/script find where name="foxos-start-sequence"]]
:local ownedScriptCount [:len [/system/script find where comment="foxos:start-sequence"]]
:local startSchedulerCount [:len [/system/scheduler find where name="foxos-start-sequence"]]
:local ownedSchedulerCount [:len [/system/scheduler find where comment="foxos:start-sequence"]]
:if ($startScriptCount > 0 || $ownedScriptCount > 0 || $startSchedulerCount > 0 || $ownedSchedulerCount > 0) do={
  :put ("CONFLICT|footprint-start-sequence|script-name-count=" . $startScriptCount . "|owned-script-count=" . $ownedScriptCount . "|scheduler-name-count=" . $startSchedulerCount . "|owned-scheduler-count=" . $ownedSchedulerCount)
  :set conflictCount ($conflictCount + 1)
} else={
  :put "PASS|footprint-start-sequence|script-name-count=0|owned-script-count=0|scheduler-name-count=0|owned-scheduler-count=0"
}

:local ownedDNSCount [:len [/ip/dns/static find where comment~"^foxos:dns:"]]
:local hostnameDNSCount 0
:if ($manifestReady) do={ :set hostnameDNSCount [:len [/ip/dns/static find where name=$publicHostname]] }
:if ($ownedDNSCount > 0) do={
  :put ("CONFLICT|footprint-dns-static|owned-count=" . $ownedDNSCount . "|configured-hostname-count=" . $hostnameDNSCount . "|reason=retained-owned-record")
  :set conflictCount ($conflictCount + 1)
} else={
  :if ($hostnameDNSCount > 0) do={
    :put ("PASS|footprint-dns-static|owned-count=0|configured-hostname-count=" . $hostnameDNSCount . "|core-install=allowed|optional-dns-apply=blocked")
  } else={
    :put "PASS|footprint-dns-static|owned-count=0|configured-hostname-count=0"
  }
}

# Retained application state is a first-install conflict even after RouterOS
# objects were removed. Only names and counts are inspected.
:if ($manifestReady) do={
  :local dataRootCount [:len [/file find where name=($storageRoot . "/foxos-data")]]
  :local backupRootCount [:len [/file find where name=($storageRoot . "/foxos-backups")]]
  :local serviceRootCount ([:len [/file find where name=($storageRoot . "/containers/mihomo")]] + [:len [/file find where name=($storageRoot . "/containers/mosdns")]])
  :local adminRootCount 0
  :local adminRootPrefix ($storageRoot . "/containers/foxos-")
  :foreach fileID in=[/file find where name~"/containers/foxos-"] do={
    :local fileName [/file get $fileID name]
    :if ([:pick $fileName 0 [:len $adminRootPrefix]] = $adminRootPrefix) do={ :set adminRootCount ($adminRootCount + 1) }
  }
  :if ($dataRootCount > 0 || $backupRootCount > 0 || $serviceRootCount > 0 || $adminRootCount > 0) do={
    :put ("CONFLICT|footprint-files|data-root-count=" . $dataRootCount . "|backup-root-count=" . $backupRootCount . "|service-root-count=" . $serviceRootCount . "|admin-root-entry-count=" . $adminRootCount)
    :set conflictCount ($conflictCount + 1)
  } else={
    :put "PASS|footprint-files|data-root-count=0|backup-root-count=0|service-root-count=0|admin-root-entry-count=0"
  }
} else={
  :put "NEEDS-ACTION|footprint-files|inspection=skipped|reason=manifest-not-loaded"
  :set needsActionCount ($needsActionCount + 1)
}

:if ($conflictCount > 0) do={
  :put ("CONFLICT|summary|needs-action-count=" . $needsActionCount . "|conflict-count=" . $conflictCount . "|first-install=blocked")
} else={
  :if ($needsActionCount > 0) do={
    :put ("NEEDS-ACTION|summary|needs-action-count=" . $needsActionCount . "|conflict-count=0|first-install=not-ready")
  } else={
    :put "PASS|summary|needs-action-count=0|conflict-count=0|first-install=ready-for-plan"
  }
}
