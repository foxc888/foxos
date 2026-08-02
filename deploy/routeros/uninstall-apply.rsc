# Remove only the remaining FoxOS-owned RouterOS resources confirmed by
# uninstall-plan.rsc. Missing resources are skipped. Persistent files,
# image archives, configuration, backups, and certificates are retained.

:global FoxOSSiteManifestVersion
:global FoxOSSiteManagementBridge
:global FoxOSSiteStorageRoot
:global FoxOSSitePrefixLength
:global FoxOSSiteRouterAddress
:global FoxOSSiteMihomoAddress
:global FoxOSSiteMosDNSAddress
:global FoxOSSiteFoxOSAddress
:global FoxOSSitePublicHostname
:global FoxOSContainerCompatVersion
:global FoxOSContainerState
:global FoxOSContainerRoot
:global FoxOSContainerMountLists
:global FoxOSMountCompatVersion
:global FoxOSWritableMountMode
:global FoxOSMountSource
:global FoxOSMountMode
:global FoxOSServiceAccessContractVersion
:global FoxOSServiceGroupPolicy
:global FoxOSServiceGroupPolicyMatches
:global FoxOSSecretContractVersion
:global FoxOSSecretHostDirectory
:global FoxOSSecretContainerDirectory
:global FoxOSSecretMountName
:global FoxOSReadonlyMountMode
:global FoxOSAdminMountLists
:global FoxOSSensitiveEnvKeys
:global FoxOSSecretFileNames
:global FoxOSSecretRead
:global FoxOSSecretEvidence
:global FoxOSUninstallInspectVerbose false
:global FoxOSUninstallCurrentDigest
:global FoxOSUninstallApprovedDigest
:global FoxOSUninstallConfirmation
:global FoxOSUninstallContainerCount
:global FoxOSUninstallRemainingCount
:if ($FoxOSSiteManifestVersion != 2) do={ :error "先导入不可变的 load-site-config.rsc" }
/import file-name=($FoxOSSiteStorageRoot . "/load-site-config.rsc")
:if ($FoxOSContainerCompatVersion != 2) do={ :error "container compatibility contract is unavailable" }
:if ($FoxOSMountCompatVersion != 2 || $FoxOSWritableMountMode != "rw") do={ :error "mount compatibility contract is unavailable" }
:if ($FoxOSServiceAccessContractVersion != 1 || [:typeof $FoxOSServiceGroupPolicy] != "str" || [:typeof $FoxOSServiceGroupPolicyMatches] != "array") do={ :error "RouterOS service access contract is unavailable" }
:if ($FoxOSSecretContractVersion != 1 || $FoxOSSecretHostDirectory != ($FoxOSSiteStorageRoot . "/foxos-secrets") || $FoxOSSecretContainerDirectory != "/run/secrets/foxos" || $FoxOSSecretMountName != "foxos-secrets" || $FoxOSReadonlyMountMode != "ro") do={ :error "secret-file compatibility contract is unavailable" }
:local approved $FoxOSUninstallApprovedDigest
:local confirmation $FoxOSUninstallConfirmation
/import file-name=($FoxOSSiteStorageRoot . "/foxos-uninstall-inspect.rsc")
:if ([:len $approved] != 128 || $approved != $FoxOSUninstallCurrentDigest || $confirmation != $approved) do={
  :error "RouterOS 剩余删除集在卸载计划后变化或摘要未确认；重新运行 uninstall-plan.rsc"
}
:set FoxOSUninstallConfirmation ""
:set FoxOSUninstallApprovedDigest ""
:local ownedContainers [/container find where comment~"^foxos:"]
:local envItems [/container/envs find where list="foxos-env"]
:local mosdnsEnvItems [/container/envs find where list="foxos-mosdns-env"]
:local schedulerSnapshot [/system/scheduler find where name="foxos-start-sequence"]
:local startScriptSnapshot [/system/script find where name="foxos-start-sequence"]
:local dnsRecordSnapshot [/ip/dns/static find where comment="foxos:dns:admin"]
:local serviceUserSnapshot [/user find where name="foxos-service"]
:local serviceGroupSnapshot [/user/group find where name="foxos-rest"]
:local mihomoPortSnapshot [/interface/bridge/port find where interface="veth-mihomo"]
:local mosdnsPortSnapshot [/interface/bridge/port find where interface="veth-mosdns"]
:local foxosPortSnapshot [/interface/bridge/port find where interface="veth-foxos"]
:local mihomoVethSnapshot [/interface/veth find where name="veth-mihomo"]
:local mosdnsVethSnapshot [/interface/veth find where name="veth-mosdns"]
:local foxosVethSnapshot [/interface/veth find where name="veth-foxos"]
:local mihomoRuntimeMountSnapshot [/container/mounts find where list="foxos-mihomo-runtime"]
:local mihomoConfigMountSnapshot [/container/mounts find where list="foxos-mihomo-config"]
:local mosdnsRuntimeMountSnapshot [/container/mounts find where list="foxos-mosdns-runtime"]
:local dataMountSnapshot [/container/mounts find where list="foxos-data"]
:local backupsMountSnapshot [/container/mounts find where list="foxos-backups"]
:local secretMountSnapshot [/container/mounts find where list=$FoxOSSecretMountName]
:local secretDirectorySnapshot [/file find where name=$FoxOSSecretHostDirectory]
:local apiTokenFileSnapshot [/file find where name=($FoxOSSecretHostDirectory . "/api-token")]
:local confirmationKeyFileSnapshot [/file find where name=($FoxOSSecretHostDirectory . "/confirmation-key")]
:local routerPasswordFileSnapshot [/file find where name=($FoxOSSecretHostDirectory . "/routeros-password")]
:local mihomoSecretFileSnapshot [/file find where name=($FoxOSSecretHostDirectory . "/mihomo-secret")]
:local apiTokenEvidenceSnapshot ""
:local confirmationKeyEvidenceSnapshot ""
:local routerPasswordEvidenceSnapshot ""
:local mihomoSecretEvidenceSnapshot ""
:if ([:len $secretDirectorySnapshot] = 1) do={
  :set apiTokenEvidenceSnapshot [$FoxOSSecretEvidence "api-token"]
  :set confirmationKeyEvidenceSnapshot [$FoxOSSecretEvidence "confirmation-key"]
  :set routerPasswordEvidenceSnapshot [$FoxOSSecretEvidence "routeros-password"]
  :set mihomoSecretEvidenceSnapshot [$FoxOSSecretEvidence "mihomo-secret"]
}
/import file-name=($FoxOSSiteStorageRoot . "/foxos-uninstall-inspect.rsc")
:if ($FoxOSUninstallCurrentDigest != $approved) do={
  :error "RouterOS 删除集在批准摘要与对象 ID snapshot 之间变化；重新运行 uninstall-plan.rsc"
}
:if ([:len $ownedContainers] != $FoxOSUninstallContainerCount) do={ :error "容器删除集在摘要回读后变化；停止卸载" }

:foreach containerID in=$ownedContainers do={
  :local owner [/container get $containerID comment]
  :local containerName [/container get $containerID name]
  :local accepted false
  :if ($owner = "foxos:mihomo" && $containerName = "foxos-mihomo" && [/container get $containerID interface] = "veth-mihomo" && [/container get $containerID envlists] = "" && [$FoxOSContainerMountLists $containerID] = "foxos-mihomo-runtime" && [$FoxOSContainerRoot $containerID] = ($FoxOSSiteStorageRoot . "/containers/mihomo")) do={ :set accepted true }
  :if ($owner = "foxos:mosdns" && $containerName = "foxos-mosdns" && [/container get $containerID interface] = "veth-mosdns" && [/container get $containerID envlists] = "foxos-mosdns-env" && [$FoxOSContainerMountLists $containerID] = "foxos-mosdns-runtime" && [$FoxOSContainerRoot $containerID] = ($FoxOSSiteStorageRoot . "/containers/mosdns")) do={ :set accepted true }
  :if (($owner = "foxos:active" || $owner = "foxos:pending" || $owner = "foxos:rollback" || $owner = "foxos:rollback-complete" || $owner = "foxos:transition:promote" || $owner = "foxos:transition:rollback" || $owner = "foxos:transition:rollback:previous" || $owner = "foxos:retained" || $owner = "foxos:failed") && $containerName ~ "^foxos-[A-Za-z0-9._-]+\$" && [/container get $containerID interface] = "veth-foxos" && [/container get $containerID envlists] = "foxos-env" && [$FoxOSContainerMountLists $containerID] = $FoxOSAdminMountLists && [$FoxOSContainerRoot $containerID] = ($FoxOSSiteStorageRoot . "/containers/" . $containerName)) do={ :set accepted true }
  :local currentStatus [$FoxOSContainerState $containerID]
  :if ($currentStatus != "running" && $currentStatus != "stopped") do={ :set accepted false }
  :local containerLogging [/container get $containerID logging]
  :local loggingMatches false
  :if ($owner = "foxos:mihomo" || $owner = "foxos:mosdns") do={
    :if ($containerLogging = true || $containerLogging = "yes") do={ :set loggingMatches true }
  } else={
    :if ($containerLogging = false || $containerLogging = "no") do={ :set loggingMatches true }
  }
  :if (([/container get $containerID start-on-boot] != false && [/container get $containerID start-on-boot] != "no") || $loggingMatches = false) do={ :set accepted false }
  :if ($accepted = false) do={ :error "容器完整身份在摘要回读后变化；停止卸载" }
  /container/set $containerID start-on-boot=no
  :if ($currentStatus = "running") do={ /container/stop $containerID }
}
:local allStopped false
:for attempt from=1 to=12 do={
  :delay 5s
  :set allStopped true
  :foreach containerID in=$ownedContainers do={
    :if ([$FoxOSContainerState $containerID] != "stopped") do={ :set allStopped false }
  }
  :if ($allStopped) do={ :break }
}
:if ($allStopped = false) do={ :error "至少一个 FoxOS 容器在 60 秒内未停止；未删除任何容器或网络资源" }
:if ([:len [/container find where comment~"^foxos:"]] != [:len $ownedContainers]) do={ :error "容器删除集在停止等待期间变化；未删除任何容器" }
:local pendingMihomoApplyJournalAfterStop [/file find where name=($FoxOSSiteStorageRoot . "/foxos-backups/mihomo/.foxos-mihomo-apply.json")]
:if ([:len $pendingMihomoApplyJournalAfterStop] > 0) do={
  :error "停止期间出现 pending Mihomo apply journal；容器保持 stopped，scheduler、env 与 confirmation key 均保留，必须先恢复并清除 journal"
}

:local scheduler $schedulerSnapshot
:local currentScheduler [/system/scheduler find where name="foxos-start-sequence"]
:if ([:len $currentScheduler] != [:len $scheduler] || ([:len $scheduler] = 1 && $currentScheduler != $scheduler)) do={ :error "冷启动 scheduler ID 在摘要确认后变化" }
:if ([:len $scheduler] > 0) do={
  :if ([:len $scheduler] != 1 || [/system/scheduler get $scheduler comment] != "foxos:start-sequence" || [/system/scheduler get $scheduler on-event] != "foxos-start-sequence" || [/system/scheduler get $scheduler start-time] != "startup" || [/system/scheduler get $scheduler interval] != 0s || [/system/scheduler get $scheduler policy] != {"read";"write";"test"}) do={ :error "冷启动 scheduler 回读冲突" }
  /system/scheduler set $scheduler disabled=yes
  /system/scheduler remove $scheduler
}

:foreach containerID in=$ownedContainers do={
  :local owner [/container get $containerID comment]
  :local containerName [/container get $containerID name]
  :local accepted false
  :if ($owner = "foxos:mihomo" && $containerName = "foxos-mihomo" && [/container get $containerID interface] = "veth-mihomo" && [/container get $containerID envlists] = "" && [$FoxOSContainerMountLists $containerID] = "foxos-mihomo-runtime" && [$FoxOSContainerRoot $containerID] = ($FoxOSSiteStorageRoot . "/containers/mihomo")) do={ :set accepted true }
  :if ($owner = "foxos:mosdns" && $containerName = "foxos-mosdns" && [/container get $containerID interface] = "veth-mosdns" && [/container get $containerID envlists] = "foxos-mosdns-env" && [$FoxOSContainerMountLists $containerID] = "foxos-mosdns-runtime" && [$FoxOSContainerRoot $containerID] = ($FoxOSSiteStorageRoot . "/containers/mosdns")) do={ :set accepted true }
  :if (($owner = "foxos:active" || $owner = "foxos:pending" || $owner = "foxos:rollback" || $owner = "foxos:rollback-complete" || $owner = "foxos:transition:promote" || $owner = "foxos:transition:rollback" || $owner = "foxos:transition:rollback:previous" || $owner = "foxos:retained" || $owner = "foxos:failed") && $containerName ~ "^foxos-[A-Za-z0-9._-]+\$" && [/container get $containerID interface] = "veth-foxos" && [/container get $containerID envlists] = "foxos-env" && [$FoxOSContainerMountLists $containerID] = $FoxOSAdminMountLists && [$FoxOSContainerRoot $containerID] = ($FoxOSSiteStorageRoot . "/containers/" . $containerName)) do={ :set accepted true }
  :local containerLogging [/container get $containerID logging]
  :local loggingMatches false
  :if ($owner = "foxos:mihomo" || $owner = "foxos:mosdns") do={
    :if ($containerLogging = true || $containerLogging = "yes") do={ :set loggingMatches true }
  } else={
    :if ($containerLogging = false || $containerLogging = "no") do={ :set loggingMatches true }
  }
  :if ([$FoxOSContainerState $containerID] != "stopped" || ([/container get $containerID start-on-boot] != false && [/container get $containerID start-on-boot] != "no") || $loggingMatches = false) do={ :set accepted false }
  :if ($accepted = false) do={ :error "容器完整身份在删除前变化；停止卸载" }
  /container/remove $containerID
}

:local dnsRecord $dnsRecordSnapshot
:local currentDNSRecord [/ip/dns/static find where comment="foxos:dns:admin"]
:if ([:len $currentDNSRecord] != [:len $dnsRecord] || ([:len $dnsRecord] = 1 && $currentDNSRecord != $dnsRecord)) do={ :error "DNS 删除对象 ID 在摘要确认后变化" }
:if ([:len $dnsRecord] > 0) do={
  :if ([:len $dnsRecord] != 1 || [/ip/dns/static get $dnsRecord name] != $FoxOSSitePublicHostname || [/ip/dns/static get $dnsRecord type] != "A" || [/ip/dns/static get $dnsRecord address] != $FoxOSSiteFoxOSAddress) do={ :error "DNS 所有权内容变化；停止卸载" }
  /ip/dns/static/remove $dnsRecord
}

:local vethDefinitions {("veth-mihomo|foxos:mihomo|" . $FoxOSSiteMihomoAddress . "/" . $FoxOSSitePrefixLength);("veth-mosdns|foxos:mosdns|" . $FoxOSSiteMosDNSAddress . "/" . $FoxOSSitePrefixLength);("veth-foxos|foxos:admin|" . $FoxOSSiteFoxOSAddress . "/" . $FoxOSSitePrefixLength)}
:foreach definition in=$vethDefinitions do={
  :local p1 [:find $definition "|"]
  :local p2 [:find $definition "|" ($p1 + 1)]
  :local vethName [:pick $definition 0 $p1]
  :local owner [:pick $definition ($p1 + 1) $p2]
  :local expectedAddress [:pick $definition ($p2 + 1) [:len $definition]]
  :local portID ""
  :local vethID ""
  :if ($vethName = "veth-mihomo") do={ :set portID $mihomoPortSnapshot; :set vethID $mihomoVethSnapshot }
  :if ($vethName = "veth-mosdns") do={ :set portID $mosdnsPortSnapshot; :set vethID $mosdnsVethSnapshot }
  :if ($vethName = "veth-foxos") do={ :set portID $foxosPortSnapshot; :set vethID $foxosVethSnapshot }
  :local currentPortID [/interface/bridge/port find where interface=$vethName]
  :if ([:len $currentPortID] != [:len $portID] || ([:len $portID] = 1 && $currentPortID != $portID)) do={ :error ("bridge port ID 在摘要确认后变化: " . $vethName) }
  :if ([:len $portID] > 0) do={
    :if ([:len $portID] != 1 || [/interface/bridge/port get $portID bridge] != $FoxOSSiteManagementBridge || [/interface/bridge/port get $portID comment] != $owner) do={ :error ("bridge port 回读冲突: " . $vethName) }
    /interface/bridge/port/remove $portID
  }
  :local currentVethID [/interface/veth find where name=$vethName]
  :if ([:len $currentVethID] != [:len $vethID] || ([:len $vethID] = 1 && $currentVethID != $vethID)) do={ :error ("veth ID 在摘要确认后变化: " . $vethName) }
  :if ([:len $vethID] > 0) do={
    :if ([:len $vethID] != 1 || [/interface/veth get $vethID comment] != $owner || [/interface/veth get $vethID address] != $expectedAddress || [/interface/veth get $vethID gateway] != $FoxOSSiteRouterAddress) do={ :error ("veth 回读冲突: " . $vethName) }
    /interface/veth/remove $vethID
  }
}

:local serviceUser $serviceUserSnapshot
:local currentServiceUser [/user find where name="foxos-service"]
:if ([:len $currentServiceUser] != [:len $serviceUser] || ([:len $serviceUser] = 1 && $currentServiceUser != $serviceUser)) do={ :error "foxos-service ID 在摘要确认后变化" }
:if ([:len $serviceUser] > 0) do={
  :if ([:len $serviceUser] != 1 || [/user get $serviceUser comment] != "foxos:service" || [/user get $serviceUser group] != "foxos-rest" || [/user get $serviceUser address] != ($FoxOSSiteFoxOSAddress . "/32")) do={ :error "foxos-service 回读冲突" }
  /user/remove $serviceUser
}
:local serviceGroup $serviceGroupSnapshot
:local currentServiceGroup [/user/group find where name="foxos-rest"]
:if ([:len $currentServiceGroup] != [:len $serviceGroup] || ([:len $serviceGroup] = 1 && $currentServiceGroup != $serviceGroup)) do={ :error "foxos-rest ID 在摘要确认后变化" }
:if ([:len $serviceGroup] > 0) do={
  :if ([:len $serviceGroup] != 1 || [$FoxOSServiceGroupPolicyMatches $serviceGroup] = false) do={ :error "foxos-rest 回读冲突" }
  /user/group/remove $serviceGroup
}

:local allowedEnvKeys "|FOXOS_INSTALL_MARKER|FOXOS_ENV|FOXOS_ROUTEROS_URL|FOXOS_ROUTEROS_USERNAME|FOXOS_MIHOMO_URL|FOXOS_MIHOMO_PROXY_URL|FOXOS_MIHOMO_BASE_CONFIG|FOXOS_MIHOMO_LOCAL_CONFIG|FOXOS_MIHOMO_RUNTIME_CONFIG|FOXOS_MIHOMO_BACKUP_DIR|FOXOS_MIHOMO_VALIDATOR_BINARY|FOXOS_MOSDNS_URL|FOXOS_BACKUP_DIR|FOXOS_UPGRADE_STATE_PATH|FOXOS_SITE_MANAGEMENT_BRIDGE|FOXOS_SITE_STORAGE_ROOT|FOXOS_SITE_NETWORK|FOXOS_SITE_ROUTER_ADDRESS|FOXOS_SITE_MIHOMO_ADDRESS|FOXOS_SITE_MOSDNS_ADDRESS|FOXOS_SITE_FOXOS_ADDRESS|FOXOS_SITE_PUBLIC_HOSTNAME|FOXOS_HTTPS_ENABLED|FOXOS_SUBSCRIPTION_PRIVATE_CIDRS|"
:if ([:len [/container/envs find where list="foxos-env"]] != [:len $envItems]) do={ :error "foxos-env 删除集在摘要确认后变化" }
:foreach sensitiveKey in=$FoxOSSensitiveEnvKeys do={
  :if ([:len [/container/envs find where list="foxos-env" key=$sensitiveKey]] > 0) do={ :error ("legacy sensitive env is forbidden during uninstall: " . $sensitiveKey) }
}
:if ([:len $envItems] > 0) do={
  :local envMarker ""
  :foreach envID in=$envItems do={
    :local envKey [/container/envs get $envID key]
    :if ([:typeof [:find $allowedEnvKeys ("|" . $envKey . "|")]] = "nil") do={ :error ("foxos-env 出现未知键: " . $envKey) }
    :if ($envKey = "FOXOS_INSTALL_MARKER") do={
      :if ([:len $envMarker] > 0) do={ :error "foxos-env 所有权标记不唯一" }
      :set envMarker $envID
    }
  }
  :if ([:len $envMarker] = 0 || [/container/envs get $envMarker key] != "FOXOS_INSTALL_MARKER" || ([/container/envs get $envMarker value] != "foxos:applying" && [/container/envs get $envMarker value] != "foxos:complete")) do={ :error "foxos-env 标记在删除前变化" }
  :foreach envID in=$envItems do={
    :if ($envID != $envMarker) do={ /container/envs/remove $envID }
  }
  /container/envs/remove $envMarker
}

:if ([:len [/container/envs find where list="foxos-mosdns-env"]] != [:len $mosdnsEnvItems]) do={ :error "foxos-mosdns-env 删除集在摘要确认后变化" }
:if ([:len $mosdnsEnvItems] > 0) do={
  :local mosdnsMarker ""
  :local autoInit ""
  :foreach envID in=$mosdnsEnvItems do={
    :local envKey [/container/envs get $envID key]
    :if ($envKey = "FOXOS_INSTALL_MARKER") do={
      :if ([:len $mosdnsMarker] > 0) do={ :error "foxos-mosdns-env 所有权标记不唯一" }
      :set mosdnsMarker $envID
    } else={
      :if ($envKey = "MOSDNS_AUTO_INIT") do={
        :if ([:len $autoInit] > 0) do={ :error "MOSDNS_AUTO_INIT 不唯一" }
        :set autoInit $envID
      } else={ :error ("foxos-mosdns-env 出现未知键: " . $envKey) }
    }
  }
  :if ([:len $mosdnsMarker] = 0 || ([/container/envs get $mosdnsMarker value] != "foxos:mosdns:applying" && [/container/envs get $mosdnsMarker value] != "foxos:mosdns:complete") || [:len $mosdnsEnvItems] > 2) do={ :error "foxos-mosdns-env 所有权状态回读冲突" }
  :if ([:len $autoInit] > 0) do={
    :if ([/container/envs get $autoInit value] != "0") do={ :error "MOSDNS_AUTO_INIT 回读冲突" }
    /container/envs/remove $autoInit
  }
  :if ([:len $mosdnsMarker] = 0 || [/container/envs get $mosdnsMarker key] != "FOXOS_INSTALL_MARKER") do={ :error "MosDNS env 标记在删除前变化" }
  /container/envs/remove $mosdnsMarker
}

:local mountDefinitions {($FoxOSSecretMountName . "|" . $FoxOSSecretHostDirectory . "|" . $FoxOSSecretContainerDirectory . "|" . $FoxOSReadonlyMountMode);("foxos-mihomo-runtime|" . $FoxOSSiteStorageRoot . "/mihomo-config|/root/.config/mihomo|" . $FoxOSWritableMountMode);("foxos-mihomo-config|" . $FoxOSSiteStorageRoot . "/mihomo-config|/data/mihomo|" . $FoxOSWritableMountMode);("foxos-mosdns-runtime|" . $FoxOSSiteStorageRoot . "/mosdns-config|/cus/mosdns|" . $FoxOSWritableMountMode);("foxos-data|" . $FoxOSSiteStorageRoot . "/foxos-data|/data|" . $FoxOSWritableMountMode);("foxos-backups|" . $FoxOSSiteStorageRoot . "/foxos-backups|/backups|" . $FoxOSWritableMountMode)}
:foreach definition in=$mountDefinitions do={
  :local p1 [:find $definition "|"]
  :local p2 [:find $definition "|" ($p1 + 1)]
  :local p3 [:find $definition "|" ($p2 + 1)]
  :local mountName [:pick $definition 0 $p1]
  :local expectedSource [:pick $definition ($p1 + 1) $p2]
  :local expectedDestination [:pick $definition ($p2 + 1) $p3]
  :local expectedMode [:pick $definition ($p3 + 1) [:len $definition]]
  :local mountID ""
  :if ($mountName = $FoxOSSecretMountName) do={ :set mountID $secretMountSnapshot }
  :if ($mountName = "foxos-mihomo-runtime") do={ :set mountID $mihomoRuntimeMountSnapshot }
  :if ($mountName = "foxos-mihomo-config") do={ :set mountID $mihomoConfigMountSnapshot }
  :if ($mountName = "foxos-mosdns-runtime") do={ :set mountID $mosdnsRuntimeMountSnapshot }
  :if ($mountName = "foxos-data") do={ :set mountID $dataMountSnapshot }
  :if ($mountName = "foxos-backups") do={ :set mountID $backupsMountSnapshot }
  :local currentMountID [/container/mounts find where list=$mountName]
  :if ([:len $currentMountID] != [:len $mountID] || ([:len $mountID] = 1 && $currentMountID != $mountID)) do={ :error ("mount ID 在摘要确认后变化: " . $mountName) }
  :if ([:len $mountID] > 0) do={
    :if ([:len $mountID] != 1 || [$FoxOSMountSource $mountID] != $expectedSource || [/container/mounts get $mountID dst] != $expectedDestination || [$FoxOSMountMode $mountID] != $expectedMode) do={ :error ("mount 回读冲突: " . $mountName) }
    /container/mounts/remove $mountID
  }
}

:if ([:len $secretDirectorySnapshot] > 0) do={
  :local currentSecretDirectory [/file find where name=$FoxOSSecretHostDirectory]
  :if ([:len $secretDirectorySnapshot] != 1 || $currentSecretDirectory != $secretDirectorySnapshot || [/file get $secretDirectorySnapshot type] != "directory") do={ :error "secret directory identity changed after uninstall confirmation" }

  # FoxOSSecretEvidence validates the complete four-file set. Validate every
  # snapshot before removing any member so later checks do not see a partial set.
  :foreach secretName in=$FoxOSSecretFileNames do={
    :local secretFileSnapshot ""
    :local secretEvidenceSnapshot ""
    :if ($secretName = "api-token") do={ :set secretFileSnapshot $apiTokenFileSnapshot; :set secretEvidenceSnapshot $apiTokenEvidenceSnapshot }
    :if ($secretName = "confirmation-key") do={ :set secretFileSnapshot $confirmationKeyFileSnapshot; :set secretEvidenceSnapshot $confirmationKeyEvidenceSnapshot }
    :if ($secretName = "routeros-password") do={ :set secretFileSnapshot $routerPasswordFileSnapshot; :set secretEvidenceSnapshot $routerPasswordEvidenceSnapshot }
    :if ($secretName = "mihomo-secret") do={ :set secretFileSnapshot $mihomoSecretFileSnapshot; :set secretEvidenceSnapshot $mihomoSecretEvidenceSnapshot }
    :local currentSecretFile [/file find where name=($FoxOSSecretHostDirectory . "/" . $secretName)]
    :if ([:len $secretFileSnapshot] != 1 || $currentSecretFile != $secretFileSnapshot || [$FoxOSSecretEvidence $secretName] != $secretEvidenceSnapshot) do={ :error ("secret file identity or digest changed after uninstall confirmation: " . $secretName) }
  }

  :foreach secretName in=$FoxOSSecretFileNames do={
    :local secretFileSnapshot ""
    :if ($secretName = "api-token") do={ :set secretFileSnapshot $apiTokenFileSnapshot }
    :if ($secretName = "confirmation-key") do={ :set secretFileSnapshot $confirmationKeyFileSnapshot }
    :if ($secretName = "routeros-password") do={ :set secretFileSnapshot $routerPasswordFileSnapshot }
    :if ($secretName = "mihomo-secret") do={ :set secretFileSnapshot $mihomoSecretFileSnapshot }
    /file/remove $secretFileSnapshot
    :if ([:len [/file find where name=($FoxOSSecretHostDirectory . "/" . $secretName)]] != 0) do={ :error ("secret file removal readback failed: " . $secretName) }
  }
  :if ([:len [/file find where name~("^" . $FoxOSSecretHostDirectory . "/")]] != 0) do={ :error "secret directory contains unapproved residual files; refusing directory removal" }
  /file/remove $secretDirectorySnapshot
  :if ([:len [/file find where name=$FoxOSSecretHostDirectory]] != 0) do={ :error "secret directory removal readback failed" }
}

:local expectedStartSource (":delay 20s; /import file-name=" . $FoxOSSiteStorageRoot . "/load-site-config.rsc; /import file-name=" . $FoxOSSiteStorageRoot . "/foxos-start-all.rsc")
:local startScript $startScriptSnapshot
:local currentStartScript [/system/script find where name="foxos-start-sequence"]
:if ([:len $currentStartScript] != [:len $startScript] || ([:len $startScript] = 1 && $currentStartScript != $startScript)) do={ :error "冷启动协调脚本 ID 在摘要确认后变化" }
:if ([:len $startScript] > 0) do={
  :if ([:len $startScript] != 1 || [/system/script get $startScript comment] != "foxos:start-sequence" || [/system/script get $startScript source] != $expectedStartSource || [/system/script get $startScript policy] != {"read";"write";"test"}) do={ :error "冷启动协调脚本回读冲突" }
  /system/script remove $startScript
}

:set FoxOSUninstallInspectVerbose false
/import file-name=($FoxOSSiteStorageRoot . "/foxos-uninstall-inspect.rsc")
:if ($FoxOSUninstallRemainingCount != 0) do={ :error "卸载已执行但仍有 owned 资源；重新运行只读 plan 后继续收敛" }
:put "FoxOS RouterOS resources converged to DONE. The four production secret files and their directory were removed; data, images, configs, versioned root-dirs, backups, site manifest, and local CA remain only for audited recovery or disposal."
