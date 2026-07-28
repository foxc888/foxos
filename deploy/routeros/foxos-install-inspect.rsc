# Shared read-only install-state inspection. This script never creates, changes,
# starts, stops, or removes RouterOS resources. preflight.rsc must pass first.

:global FoxOSSiteManifestVersion
:global FoxOSSiteManagementBridge
:global FoxOSSiteStorageRoot
:global FoxOSSiteNetwork
:global FoxOSSitePrefixLength
:global FoxOSSiteRouterAddress
:global FoxOSSiteMihomoAddress
:global FoxOSSiteMosDNSAddress
:global FoxOSSiteFoxOSAddress
:global FoxOSSitePublicHostname
:global FoxOSSiteSubscriptionPrivateCIDRs
:global FoxOSInstallInspectVerbose
:global FoxOSInstallCurrentDigest
:if ($FoxOSSiteManifestVersion != 2) do={ :error "site-config.rsc manifest version 2 is required" }

:local managementBridge $FoxOSSiteManagementBridge
:local storageRoot $FoxOSSiteStorageRoot
:local releaseID "__FOXOS_RELEASE_ID__"
:if ($releaseID ~ "^__.*__$" || [:len $releaseID] < 1 || [:len $releaseID] > 40 || $releaseID !~ "^[A-Za-z0-9._-]+$") do={ :error "foxos-install-inspect.rsc 未绑定有效 release ID；只能使用发布包内脚本" }
:local foxosImagePath ($storageRoot . "/foxos-upgrade-" . $releaseID . "/foxos-amd64.tar")
:local prefixLength $FoxOSSitePrefixLength
:local routerAddress $FoxOSSiteRouterAddress
:local mihomoIP $FoxOSSiteMihomoAddress
:local mosdnsIP $FoxOSSiteMosDNSAddress
:local foxosIP $FoxOSSiteFoxOSAddress
:local subscriptionPrivateCIDRs $FoxOSSiteSubscriptionPrivateCIDRs
:local failed false
:local material ("foxos-install-v3|" . $releaseID . "|" . $managementBridge . "|" . $storageRoot . "|" . $FoxOSSiteNetwork . "|" . $prefixLength . "|" . $routerAddress . "|" . $mihomoIP . "|" . $mosdnsIP . "|" . $foxosIP . "|" . $FoxOSSitePublicHostname . "|" . $subscriptionPrivateCIDRs)
:set FoxOSInstallCurrentDigest ""

:local envState "CREATE"
:local envItems [/container/envs find where list="foxos-env"]
:local persistedDataRoot [/file find where name=($storageRoot . "/foxos-data")]
:local persistedBackupRoot [/file find where name=($storageRoot . "/foxos-backups")]
:if ([:len $envItems] = 0 && ([:len $persistedDataRoot] > 0 || [:len $persistedBackupRoot] > 0)) do={
  :set envState "FAIL"
  :set failed true
  :if ($FoxOSInstallInspectVerbose) do={ :put "FAIL retained foxos-data or foxos-backups exists without foxos-env; restore the original confirmation key or archive the retained state before a fresh install" }
}
:set material ($material . "|retained-roots=" . [:len $persistedDataRoot] . ":" . [:len $persistedBackupRoot])
:local envMarkers [/container/envs find where list="foxos-env" key="FOXOS_INSTALL_MARKER"]
:local existingInstall false
:local completeInstall false
:local foxosRouterPassword ""
:local foxosMihomoSecret ""
:local foxosApiToken ""
:local foxosConfirmationKey ""
:local mihomoSecretPresent false
:if ([:len $envItems] > 0) do={
  :if ([:len $envMarkers] != 1) do={
    :set envState "FAIL"
    :set failed true
  } else={
    :local installMarkerValue [/container/envs get $envMarkers value]
    :if ($installMarkerValue = "foxos:applying") do={
      :set existingInstall true
      :set envState "RESUME"
    } else={
      :if ($installMarkerValue = "foxos:complete") do={
        :set existingInstall true
        :set completeInstall true
        :set envState "REUSE"
      } else={
        :set envState "FAIL"
        :set failed true
      }
    }
  }
}

:if ($existingInstall) do={
  :local allowedEnvKeys "|FOXOS_INSTALL_MARKER|FOXOS_ENV|FOXOS_ROUTEROS_URL|FOXOS_ROUTEROS_USERNAME|FOXOS_ROUTEROS_PASSWORD|FOXOS_MIHOMO_URL|FOXOS_MIHOMO_PROXY_URL|FOXOS_MIHOMO_SECRET|FOXOS_MIHOMO_BASE_CONFIG|FOXOS_MIHOMO_LOCAL_CONFIG|FOXOS_MIHOMO_RUNTIME_CONFIG|FOXOS_MIHOMO_BACKUP_DIR|FOXOS_MIHOMO_VALIDATOR_BINARY|FOXOS_MOSDNS_URL|FOXOS_API_TOKEN|FOXOS_CONFIRMATION_KEY|FOXOS_BACKUP_DIR|FOXOS_UPGRADE_STATE_PATH|FOXOS_SITE_MANAGEMENT_BRIDGE|FOXOS_SITE_STORAGE_ROOT|FOXOS_SITE_NETWORK|FOXOS_SITE_ROUTER_ADDRESS|FOXOS_SITE_MIHOMO_ADDRESS|FOXOS_SITE_MOSDNS_ADDRESS|FOXOS_SITE_FOXOS_ADDRESS|FOXOS_SITE_PUBLIC_HOSTNAME|FOXOS_HTTPS_ENABLED|FOXOS_SUBSCRIPTION_PRIVATE_CIDRS|"
  :foreach envID in=$envItems do={
    :local currentKey [/container/envs get $envID key]
    :if ([:typeof [:find $allowedEnvKeys ("|" . $currentKey . "|")]] = "nil") do={
      :set envState "FAIL"
      :set failed true
    }
    :set material ($material . "|env-item:" . $currentKey . "=" . [/container/envs get $envID .id])
  }
  :local fixedEnvDefinitions {"FOXOS_ENV|production";("FOXOS_ROUTEROS_URL|http://" . $routerAddress);"FOXOS_ROUTEROS_USERNAME|foxos-service";("FOXOS_MIHOMO_URL|http://" . $mihomoIP . ":9090");("FOXOS_MIHOMO_PROXY_URL|http://" . $mihomoIP . ":7890");"FOXOS_MIHOMO_BASE_CONFIG|/data/mihomo/base.yaml";"FOXOS_MIHOMO_LOCAL_CONFIG|/data/mihomo/config.yaml";"FOXOS_MIHOMO_RUNTIME_CONFIG|/root/.config/mihomo/config.yaml";"FOXOS_MIHOMO_BACKUP_DIR|/backups/mihomo";"FOXOS_MIHOMO_VALIDATOR_BINARY|/usr/local/bin/mihomo";("FOXOS_MOSDNS_URL|http://" . $mosdnsIP . ":53");"FOXOS_BACKUP_DIR|/backups/foxos";"FOXOS_UPGRADE_STATE_PATH|/data/upgrade-checkpoint.json";("FOXOS_SITE_MANAGEMENT_BRIDGE|" . $managementBridge);("FOXOS_SITE_STORAGE_ROOT|" . $storageRoot);("FOXOS_SITE_NETWORK|" . $FoxOSSiteNetwork);("FOXOS_SITE_ROUTER_ADDRESS|" . $routerAddress);("FOXOS_SITE_MIHOMO_ADDRESS|" . $mihomoIP);("FOXOS_SITE_MOSDNS_ADDRESS|" . $mosdnsIP);("FOXOS_SITE_FOXOS_ADDRESS|" . $foxosIP);("FOXOS_SITE_PUBLIC_HOSTNAME|" . $FoxOSSitePublicHostname);"FOXOS_HTTPS_ENABLED|true"}
  :foreach definition in=$fixedEnvDefinitions do={
    :local separator [:find $definition "|"]
    :local envKey [:pick $definition 0 $separator]
    :local expectedValue [:pick $definition ($separator + 1) [:len $definition]]
    :local envID [/container/envs find where list="foxos-env" key=$envKey]
    :if ([:len $envID] > 1) do={
      :set envState "FAIL"
      :set failed true
    } else={
      :if ([:len $envID] = 0) do={
        :set material ($material . "|env:" . $envKey . "=MISSING")
        :if ($completeInstall) do={ :set envState "FAIL"; :set failed true }
      } else={
        :if ([/container/envs get $envID value] != $expectedValue) do={ :set envState "FAIL"; :set failed true }
        :set material ($material . "|env:" . $envKey . "=" . [/container/envs get $envID .id])
      }
    }
  }
  :foreach secretKey in={"FOXOS_ROUTEROS_PASSWORD";"FOXOS_MIHOMO_SECRET";"FOXOS_API_TOKEN";"FOXOS_CONFIRMATION_KEY"} do={
    :local secretID [/container/envs find where list="foxos-env" key=$secretKey]
    :if ([:len $secretID] > 1) do={
      :set envState "FAIL"
      :set failed true
    } else={
      :if ([:len $secretID] = 0) do={
        :set material ($material . "|secret:" . $secretKey . "=MISSING")
        :if ($completeInstall) do={ :set envState "FAIL"; :set failed true }
      } else={
        :local secretValue [/container/envs get $secretID value]
        :if ([:len $secretValue] < 32) do={ :set envState "FAIL"; :set failed true }
        :if ($secretKey = "FOXOS_ROUTEROS_PASSWORD") do={ :set foxosRouterPassword $secretValue }
        :if ($secretKey = "FOXOS_MIHOMO_SECRET") do={ :set foxosMihomoSecret $secretValue; :set mihomoSecretPresent true }
        :if ($secretKey = "FOXOS_API_TOKEN") do={ :set foxosApiToken $secretValue }
        :if ($secretKey = "FOXOS_CONFIRMATION_KEY") do={ :set foxosConfirmationKey $secretValue }
        :local secretDigest [:convert $secretValue transform=sha512 to=hex]
        :set material ($material . "|secret:" . $secretKey . "=" . [/container/envs get $secretID .id] . ":" . $secretDigest)
      }
    }
  }
  :if (([:len $foxosRouterPassword] > 0 && $foxosRouterPassword = $foxosMihomoSecret) || ([:len $foxosRouterPassword] > 0 && $foxosRouterPassword = $foxosApiToken) || ([:len $foxosRouterPassword] > 0 && $foxosRouterPassword = $foxosConfirmationKey) || ([:len $foxosMihomoSecret] > 0 && $foxosMihomoSecret = $foxosApiToken) || ([:len $foxosMihomoSecret] > 0 && $foxosMihomoSecret = $foxosConfirmationKey) || ([:len $foxosApiToken] > 0 && $foxosApiToken = $foxosConfirmationKey)) do={
    :set envState "FAIL"
    :set failed true
  }
  :local privateCIDRID [/container/envs find where list="foxos-env" key="FOXOS_SUBSCRIPTION_PRIVATE_CIDRS"]
  :local expectedEnvCount 27
  :if ([:len $subscriptionPrivateCIDRs] > 0) do={
    :set expectedEnvCount 28
    :if ([:len $privateCIDRID] > 1) do={ :set envState "FAIL"; :set failed true }
    :if ([:len $privateCIDRID] = 0 && $completeInstall) do={ :set envState "FAIL"; :set failed true }
    :if ([:len $privateCIDRID] = 1 && [/container/envs get $privateCIDRID value] != $subscriptionPrivateCIDRs) do={ :set envState "FAIL"; :set failed true }
  } else={
    :if ([:len $privateCIDRID] > 0) do={ :set envState "FAIL"; :set failed true }
  }
  :if ([:len $envItems] > $expectedEnvCount || ($completeInstall && [:len $envItems] != $expectedEnvCount)) do={ :set envState "FAIL"; :set failed true }
}
:set material ($material . "|foxos-env=" . $envState . ":" . [:len $envItems])
:if ($FoxOSInstallInspectVerbose) do={ :put ("RESOURCE env/foxos-env " . $envState) }

:local mosdnsEnvState "CREATE"
:local mosdnsEnvItems [/container/envs find where list="foxos-mosdns-env"]
:if ([:len $mosdnsEnvItems] > 0) do={
  :local allowedMosDNSEnvKeys "|FOXOS_INSTALL_MARKER|MOSDNS_AUTO_INIT|"
  :foreach mosdnsEnvID in=$mosdnsEnvItems do={
    :local mosdnsEnvKey [/container/envs get $mosdnsEnvID key]
    :if ([:typeof [:find $allowedMosDNSEnvKeys ("|" . $mosdnsEnvKey . "|")]] = "nil") do={
      :set mosdnsEnvState "FAIL"
      :set failed true
    }
    :set material ($material . "|mosdns-env-item:" . $mosdnsEnvKey . "=" . [/container/envs get $mosdnsEnvID .id])
  }
  :local mosdnsMarkers [/container/envs find where list="foxos-mosdns-env" key="FOXOS_INSTALL_MARKER"]
  :local mosdnsAutoInit [/container/envs find where list="foxos-mosdns-env" key="MOSDNS_AUTO_INIT"]
  :if ([:len $mosdnsMarkers] != 1 || [:len $mosdnsAutoInit] > 1 || [:len $mosdnsEnvItems] > 2) do={
    :set mosdnsEnvState "FAIL"
    :set failed true
  } else={
    :local mosdnsMarkerValue [/container/envs get $mosdnsMarkers value]
    :if ([:len $mosdnsAutoInit] = 1 && [/container/envs get $mosdnsAutoInit value] != "0") do={ :set mosdnsEnvState "FAIL"; :set failed true }
    :if ($mosdnsMarkerValue = "foxos:mosdns:applying") do={
      :set mosdnsEnvState "RESUME"
    } else={
      :if ($mosdnsMarkerValue = "foxos:mosdns:complete" && [:len $mosdnsEnvItems] = 2 && [:len $mosdnsAutoInit] = 1) do={
        :set mosdnsEnvState "REUSE"
      } else={
        :set mosdnsEnvState "FAIL"
        :set failed true
      }
    }
  }
}
:set material ($material . "|mosdns-env=" . $mosdnsEnvState . ":" . [:len $mosdnsEnvItems])
:if ($FoxOSInstallInspectVerbose) do={ :put ("RESOURCE env/foxos-mosdns-env " . $mosdnsEnvState) }

:local groupState "CREATE"
:local serviceGroup [/user/group find where name="foxos-rest"]
:if ([:len $serviceGroup] > 0) do={
  :if ([:len $serviceGroup] = 1 && $existingInstall && [/user/group get $serviceGroup policy] = "read,write,rest-api") do={
    :set groupState "REUSE"
  } else={
    :set groupState "FAIL"
    :set failed true
  }
}
:set material ($material . "|group=" . $groupState . ":" . [:len $serviceGroup])
:if ($FoxOSInstallInspectVerbose) do={ :put ("RESOURCE user-group/foxos-rest " . $groupState) }

:local userState "CREATE"
:local serviceUser [/user find where name="foxos-service"]
:if ([:len $serviceUser] > 0) do={
  :if ([:len $serviceUser] = 1 && $existingInstall && [/user get $serviceUser comment] = "foxos:service" && [/user get $serviceUser group] = "foxos-rest" && [/user get $serviceUser address] = ($foxosIP . "/32")) do={
    :set userState "REUSE"
  } else={
    :set userState "FAIL"
    :set failed true
  }
}
:set material ($material . "|user=" . $userState . ":" . [:len $serviceUser])
:if ($FoxOSInstallInspectVerbose) do={ :put ("RESOURCE user/foxos-service " . $userState) }

:local mountDefinitions {"foxos-mihomo-runtime|mihomo-config|/root/.config/mihomo";"foxos-mihomo-config|mihomo-config|/data/mihomo";"foxos-mosdns-runtime|mosdns-config|/cus/mosdns";"foxos-data|foxos-data|/data";"foxos-backups|foxos-backups|/backups"}
:foreach definition in=$mountDefinitions do={
  :local firstSeparator [:find $definition "|"]
  :local secondSeparator [:find $definition "|" ($firstSeparator + 1)]
  :local mountName [:pick $definition 0 $firstSeparator]
  :local sourceName [:pick $definition ($firstSeparator + 1) $secondSeparator]
  :local destination [:pick $definition ($secondSeparator + 1) [:len $definition]]
  :local sourcePath ($storageRoot . "/" . $sourceName)
  :local mountID [/container/mounts find where list=$mountName]
  :local mountState "CREATE"
  :local mountEvidence ""
  :if ([:len $mountID] > 0) do={
    :if ([:len $mountID] = 1 && [/container/mounts get $mountID src] = $sourcePath && [/container/mounts get $mountID dst] = $destination && ([/container/mounts get $mountID read-only] = false || [/container/mounts get $mountID read-only] = "no")) do={
      :set mountState "REUSE"
    } else={
      :set mountState "FAIL"
      :set failed true
    }
  }
  :if ([:len $mountID] = 1) do={ :set mountEvidence (":" . [/container/mounts get $mountID .id] . ":" . [/container/mounts get $mountID src] . ":" . [/container/mounts get $mountID dst] . ":" . [/container/mounts get $mountID read-only]) }
  :set material ($material . "|mount:" . $mountName . "=" . $mountState . ":" . [:len $mountID] . $mountEvidence)
  :if ($FoxOSInstallInspectVerbose) do={ :put ("RESOURCE mount/" . $mountName . " " . $mountState) }
}

:local vethDefinitions {("veth-mihomo|" . $mihomoIP . "/" . $prefixLength . "|foxos:mihomo");("veth-mosdns|" . $mosdnsIP . "/" . $prefixLength . "|foxos:mosdns");("veth-foxos|" . $foxosIP . "/" . $prefixLength . "|foxos:admin")}
:foreach definition in=$vethDefinitions do={
  :local firstSeparator [:find $definition "|"]
  :local secondSeparator [:find $definition "|" ($firstSeparator + 1)]
  :local vethName [:pick $definition 0 $firstSeparator]
  :local vethAddress [:pick $definition ($firstSeparator + 1) $secondSeparator]
  :local owner [:pick $definition ($secondSeparator + 1) [:len $definition]]
  :local addressOnly [:pick $vethAddress 0 [:find $vethAddress "/"]]
  :local vethID [/interface/veth find where name=$vethName]
  :local addressVeth [/interface/veth find where address=$vethAddress]
  :local vethState "CREATE"
  :if ([:len $vethID] = 0) do={
    :if ([:len $addressVeth] > 0 || [:len [/ip/address find where address~($addressOnly . "/")]] > 0 || [:len [/ip/dhcp-server/lease find where address=$addressOnly]] > 0 || [:len [/ip/arp find where address=$addressOnly]] > 0 || [/ping address=$addressOnly count=2 interval=200ms] > 0) do={
      :set vethState "FAIL"
      :set failed true
    }
  } else={
    :if ([:len $vethID] = 1 && [:len $addressVeth] = 1 && [/interface/veth get $addressVeth name] = $vethName && [/interface/veth get $vethID comment] = $owner && [/interface/veth get $vethID address] = $vethAddress && [/interface/veth get $vethID gateway] = $routerAddress) do={
      :set vethState "REUSE"
    } else={
      :set vethState "FAIL"
      :set failed true
    }
  }
  :set material ($material . "|veth:" . $vethName . "=" . $vethState . ":" . [:len $vethID])
  :if ($FoxOSInstallInspectVerbose) do={ :put ("RESOURCE veth/" . $vethName . " " . $vethState) }

  :local portID [/interface/bridge/port find where interface=$vethName]
  :local portState "CREATE"
  :if ([:len $portID] > 0) do={
    :if ([:len $portID] = 1 && [/interface/bridge/port get $portID bridge] = $managementBridge && [/interface/bridge/port get $portID comment] = $owner) do={
      :set portState "REUSE"
    } else={
      :set portState "FAIL"
      :set failed true
    }
  }
  :if ($vethState = "FAIL") do={ :set portState "FAIL" }
  :set material ($material . "|port:" . $vethName . "=" . $portState . ":" . [:len $portID])
  :if ($FoxOSInstallInspectVerbose) do={ :put ("RESOURCE bridge-port/" . $vethName . " " . $portState) }
}

:local containerDefinitions {"foxos-mihomo|foxos:mihomo|veth-mihomo||foxos-mihomo-runtime|containers/mihomo";"foxos-mosdns|foxos:mosdns|veth-mosdns|foxos-mosdns-env|foxos-mosdns-runtime|containers/mosdns"}
:foreach definition in=$containerDefinitions do={
  :local p1 [:find $definition "|"]
  :local p2 [:find $definition "|" ($p1 + 1)]
  :local p3 [:find $definition "|" ($p2 + 1)]
  :local p4 [:find $definition "|" ($p3 + 1)]
  :local p5 [:find $definition "|" ($p4 + 1)]
  :local containerName [:pick $definition 0 $p1]
  :local owner [:pick $definition ($p1 + 1) $p2]
  :local interfaceName [:pick $definition ($p2 + 1) $p3]
  :local envListName [:pick $definition ($p3 + 1) $p4]
  :local mountListName [:pick $definition ($p4 + 1) $p5]
  :local rootDirectory ($storageRoot . "/" . [:pick $definition ($p5 + 1) [:len $definition]])
  :local ownedContainer [/container find where comment=$owner]
  :local namedContainer [/container find where name=$containerName]
  :local containerState "CREATE"
  :if ([:len $ownedContainer] > 0 || [:len $namedContainer] > 0) do={
    :if ([:len $ownedContainer] = 1 && [:len $namedContainer] = 1 && [/container get $ownedContainer .id] = [/container get $namedContainer .id] && [/container get $ownedContainer interface] = $interfaceName && [/container get $ownedContainer envlists] = $envListName && [/container get $ownedContainer mountlists] = $mountListName && [/container get $ownedContainer root-dir] = $rootDirectory && ([/container get $ownedContainer logging] = true || [/container get $ownedContainer logging] = "yes")) do={
      :local currentStatus [/container get $ownedContainer status]
      :if (($currentStatus = "running" || $currentStatus = "stopped") && [/container get $ownedContainer start-on-boot] = false) do={
        :set containerState "REUSE"
        :set material ($material . "|container-status:" . $containerName . "=" . $currentStatus . ":" . [/container get $ownedContainer start-on-boot])
      } else={
        :set containerState "FAIL"
        :set failed true
      }
    } else={
      :set containerState "FAIL"
      :set failed true
    }
  }
  :set material ($material . "|container:" . $containerName . "=" . $containerState . ":" . [:len $ownedContainer] . ":" . [:len $namedContainer])
  :if ($FoxOSInstallInspectVerbose) do={ :put ("RESOURCE container/" . $containerName . " " . $containerState) }
}

:local activeState "CREATE"
:local activeContainer [/container find where comment="foxos:active"]
:local initialByName [/container find where name="foxos-initial"]
:if ([:len $activeContainer] > 0 || [:len $initialByName] > 0) do={
  :local activeName ""
  :if ([:len $activeContainer] = 1) do={ :set activeName [/container get $activeContainer name] }
  :if ([:len $activeContainer] = 1 && $activeName ~ "^foxos-[A-Za-z0-9._-]+$" && [/container get $activeContainer interface] = "veth-foxos" && [/container get $activeContainer envlists] = "foxos-env" && [/container get $activeContainer mountlists] = "foxos-mihomo-config,foxos-data,foxos-backups" && [/container get $activeContainer root-dir] = ($storageRoot . "/containers/" . $activeName) && ([/container get $activeContainer logging] = true || [/container get $activeContainer logging] = "yes")) do={
    :local activeStatus [/container get $activeContainer status]
    :if (($activeStatus = "running" || $activeStatus = "stopped") && [/container get $activeContainer start-on-boot] = false) do={
      :set activeState "REUSE"
      :set material ($material . "|active-id=" . [/container get $activeContainer .id] . ":" . [/container get $activeContainer name] . ":" . [/container get $activeContainer root-dir] . ":" . $activeStatus . ":" . [/container get $activeContainer start-on-boot])
    } else={
      :set activeState "FAIL"
      :set failed true
    }
  } else={
    :set activeState "FAIL"
    :set failed true
  }
  :if ([:len $initialByName] > 0 && ([:len $activeContainer] != 1 || [/container get $initialByName .id] != [/container get $activeContainer .id])) do={ :set activeState "FAIL"; :set failed true }
}
:local inactiveAdminSlots ([:len [/container find where comment="foxos:pending"]] + [:len [/container find where comment="foxos:rollback"]] + [:len [/container find where comment="foxos:retained"]] + [:len [/container find where comment="foxos:failed"]])
:if ($activeState = "CREATE" && $inactiveAdminSlots > 0) do={
  :set activeState "FAIL"
  :set failed true
}
:set material ($material . "|container:active=" . $activeState . ":" . [:len $activeContainer])
:if ($FoxOSInstallInspectVerbose) do={ :put ("RESOURCE container/foxos-active-slot " . $activeState) }

:local expectedStartSource (":delay 20s; /import file-name=" . $storageRoot . "/load-site-config.rsc; /import file-name=" . $storageRoot . "/foxos-start-all.rsc")
:local startScriptState "CREATE"
:local startScriptByName [/system/script find where name="foxos-start-sequence"]
:local startScriptByOwner [/system/script find where comment="foxos:start-sequence"]
:if ([:len $startScriptByName] > 0 || [:len $startScriptByOwner] > 0) do={
  :if ([:len $startScriptByName] = 1 && [:len $startScriptByOwner] = 1 && [/system/script get $startScriptByName .id] = [/system/script get $startScriptByOwner .id] && $existingInstall && [/system/script get $startScriptByName source] = $expectedStartSource && [/system/script get $startScriptByName policy] = "read,write,test") do={
    :set startScriptState "REUSE"
  } else={
    :set startScriptState "FAIL"
    :set failed true
  }
}
:set material ($material . "|start-script=" . $startScriptState . ":" . [:len $startScriptByName] . ":" . [:len $startScriptByOwner])
:if ($FoxOSInstallInspectVerbose) do={ :put ("RESOURCE system-script/foxos-start-sequence " . $startScriptState) }

:local schedulerState "CREATE"
:local schedulerByName [/system/scheduler find where name="foxos-start-sequence"]
:local schedulerByOwner [/system/scheduler find where comment="foxos:start-sequence"]
:if ([:len $schedulerByName] > 0 || [:len $schedulerByOwner] > 0) do={
  :if ([:len $schedulerByName] = 1 && [:len $schedulerByOwner] = 1 && [/system/scheduler get $schedulerByName .id] = [/system/scheduler get $schedulerByOwner .id] && $existingInstall && [/system/scheduler get $schedulerByName on-event] = "foxos-start-sequence" && [/system/scheduler get $schedulerByName start-time] = "startup" && [/system/scheduler get $schedulerByName interval] = "0s" && [/system/scheduler get $schedulerByName policy] = "read,write,test") do={
    :set schedulerState "REUSE"
    :set material ($material . "|scheduler-disabled=" . [/system/scheduler get $schedulerByName disabled] . ":interval=" . [/system/scheduler get $schedulerByName interval] . ":policy=" . [/system/scheduler get $schedulerByName policy])
  } else={
    :set schedulerState "FAIL"
    :set failed true
  }
}
:set material ($material . "|scheduler=" . $schedulerState . ":" . [:len $schedulerByName] . ":" . [:len $schedulerByOwner])
:if ($FoxOSInstallInspectVerbose) do={ :put ("RESOURCE scheduler/foxos-start-sequence " . $schedulerState) }

:local imageState "REUSE"
:foreach imageDefinition in={($foxosImagePath . "|1048576");($storageRoot . "/mihomo_amd64.tar|20971520");($storageRoot . "/mosdns-amd64.tar|5242880")} do={
  :local separator [:find $imageDefinition "|"]
  :local imagePath [:pick $imageDefinition 0 $separator]
  :local minimumSize [:tonum [:pick $imageDefinition ($separator + 1) [:len $imageDefinition]]]
  :local imageID [/file find where name=$imagePath]
  :if ([:len $imageID] != 1 || [/file get $imageID size] < $minimumSize) do={
    :set imageState "FAIL"
    :set failed true
  } else={
    :set material ($material . "|image=" . [/file get $imageID .id] . ":" . [/file get $imageID name] . ":" . [/file get $imageID size])
  }
}
:if ($FoxOSInstallInspectVerbose) do={ :put ("RESOURCE file/container-images " . $imageState) }

:local configState "REUSE"
:if ($existingInstall && $completeInstall = false) do={ :set configState "RESUME" }
:foreach configName in={"config.yaml";"base.yaml"} do={
  :local configFile [/file find where name=($storageRoot . "/mihomo-config/" . $configName)]
  :if ([:len $configFile] != 1) do={
    :set configState "FAIL"
    :set failed true
  } else={
    :local configContents [/file get $configFile contents]
    :local secretStart [:find $configContents "\nsecret:"]
    :if ([:typeof $secretStart] = "nil") do={
      :if ([:find $configContents "secret:"] = 0) do={ :set secretStart 0 } else={ :set configState "FAIL"; :set failed true }
    } else={ :set secretStart ($secretStart + 1) }
    :if ($configState != "FAIL") do={
      :local secretEnd [:find $configContents "\n" $secretStart]
      :if ([:typeof $secretEnd] = "nil") do={ :set secretEnd [:len $configContents] }
      :local secretLine [:pick $configContents $secretStart $secretEnd]
      :if ($mihomoSecretPresent) do={
        :local expectedSecretLine ("secret: \"" . $foxosMihomoSecret . "\"")
        :if ($completeInstall && $secretLine != $expectedSecretLine) do={ :set configState "FAIL"; :set failed true }
        :if ($completeInstall = false && $secretLine != $expectedSecretLine && $secretLine != "secret: \"\"") do={ :set configState "FAIL"; :set failed true }
      } else={
        :if ($secretLine != "secret: \"\"") do={ :set configState "FAIL"; :set failed true }
      }
      :local configSecretState "empty"
      :if ($secretLine != "secret: \"\"") do={ :set configSecretState "managed" }
      :local configDigest [:convert $configContents transform=sha512 to=hex]
      :set material ($material . "|config:" . $configName . "=" . [/file get $configFile size] . ":" . $configSecretState . ":" . $configDigest)
    }
  }
}
:if ($FoxOSInstallInspectVerbose) do={ :put ("RESOURCE file/mihomo-secret-lines " . $configState) }

:if ($failed) do={
  :set FoxOSInstallCurrentDigest ""
  :error "install inspection found FAIL resources; no RouterOS resource was changed"
}
:local currentDigest [:convert $material transform=sha512 to=hex]
:if ([:len $currentDigest] != 128) do={ :error "RouterOS SHA-512 digest generation failed" }
:set FoxOSInstallCurrentDigest $currentDigest
:if ($FoxOSInstallInspectVerbose) do={ :put ("PLAN DIGEST SHA-512 " . $FoxOSInstallCurrentDigest) }
