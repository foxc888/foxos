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
:global FoxOSUninstallInspectVerbose false
:global FoxOSUninstallCurrentDigest
:global FoxOSUninstallApprovedDigest
:global FoxOSUninstallConfirmation
:global FoxOSUninstallContainerCount
:global FoxOSUninstallRemainingCount
:if ($FoxOSSiteManifestVersion != 2) do={ :error "先导入不可变的 load-site-config.rsc" }
/import file-name=($FoxOSSiteStorageRoot . "/load-site-config.rsc")
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
/import file-name=($FoxOSSiteStorageRoot . "/foxos-uninstall-inspect.rsc")
:if ($FoxOSUninstallCurrentDigest != $approved) do={
  :error "RouterOS 删除集在批准摘要与对象 ID snapshot 之间变化；重新运行 uninstall-plan.rsc"
}
:if ([:len $ownedContainers] != $FoxOSUninstallContainerCount) do={ :error "容器删除集在摘要回读后变化；停止卸载" }

:local scheduler $schedulerSnapshot
:local currentScheduler [/system/scheduler find where name="foxos-start-sequence"]
:if ([:len $currentScheduler] != [:len $scheduler] || ([:len $scheduler] = 1 && [/system/scheduler get $currentScheduler .id] != [/system/scheduler get $scheduler .id])) do={ :error "冷启动 scheduler ID 在摘要确认后变化" }
:if ([:len $scheduler] > 0) do={
  :if ([:len $scheduler] != 1 || [/system/scheduler get $scheduler comment] != "foxos:start-sequence" || [/system/scheduler get $scheduler on-event] != "foxos-start-sequence" || [/system/scheduler get $scheduler start-time] != "startup" || [/system/scheduler get $scheduler interval] != "0s" || [/system/scheduler get $scheduler policy] != "read,write,test") do={ :error "冷启动 scheduler 回读冲突" }
  /system/scheduler set $scheduler disabled=yes
  /system/scheduler remove $scheduler
}

:foreach containerID in=$ownedContainers do={
  :local owner [/container get $containerID comment]
  :local containerName [/container get $containerID name]
  :local accepted false
  :if ($owner = "foxos:mihomo" && $containerName = "foxos-mihomo" && [/container get $containerID interface] = "veth-mihomo" && [/container get $containerID envlists] = "" && [/container get $containerID mountlists] = "foxos-mihomo-runtime" && [/container get $containerID root-dir] = ($FoxOSSiteStorageRoot . "/containers/mihomo")) do={ :set accepted true }
  :if ($owner = "foxos:mosdns" && $containerName = "foxos-mosdns" && [/container get $containerID interface] = "veth-mosdns" && [/container get $containerID envlists] = "foxos-mosdns-env" && [/container get $containerID mountlists] = "foxos-mosdns-runtime" && [/container get $containerID root-dir] = ($FoxOSSiteStorageRoot . "/containers/mosdns")) do={ :set accepted true }
  :if (($owner = "foxos:active" || $owner = "foxos:pending" || $owner = "foxos:rollback" || $owner = "foxos:rollback-complete" || $owner = "foxos:transition:promote" || $owner = "foxos:transition:rollback" || $owner = "foxos:transition:rollback:previous" || $owner = "foxos:retained" || $owner = "foxos:failed") && $containerName ~ "^foxos-[A-Za-z0-9._-]+$" && [/container get $containerID interface] = "veth-foxos" && [/container get $containerID envlists] = "foxos-env" && [/container get $containerID mountlists] = "foxos-mihomo-config,foxos-data,foxos-backups" && [/container get $containerID root-dir] = ($FoxOSSiteStorageRoot . "/containers/" . $containerName)) do={ :set accepted true }
  :local currentStatus [/container get $containerID status]
  :if ($currentStatus != "running" && $currentStatus != "stopped") do={ :set accepted false }
  :if (([/container get $containerID start-on-boot] != false && [/container get $containerID start-on-boot] != "no") || ([/container get $containerID logging] != true && [/container get $containerID logging] != "yes")) do={ :set accepted false }
  :if ($accepted = false) do={ :error "容器完整身份在摘要回读后变化；停止卸载" }
  /container/set $containerID start-on-boot=no
  :if ($currentStatus = "running") do={ /container/stop $containerID }
}
:local allStopped false
:for attempt from=1 to=12 do={
  :delay 5s
  :set allStopped true
  :foreach containerID in=$ownedContainers do={
    :if ([/container get $containerID status] != "stopped") do={ :set allStopped false }
  }
  :if ($allStopped) do={ :break }
}
:if ($allStopped = false) do={ :error "至少一个 FoxOS 容器在 60 秒内未停止；未删除任何容器或网络资源" }
:if ([:len [/container find where comment~"^foxos:"]] != [:len $ownedContainers]) do={ :error "容器删除集在停止等待期间变化；未删除任何容器" }
:foreach containerID in=$ownedContainers do={
  :local owner [/container get $containerID comment]
  :local containerName [/container get $containerID name]
  :local accepted false
  :if ($owner = "foxos:mihomo" && $containerName = "foxos-mihomo" && [/container get $containerID interface] = "veth-mihomo" && [/container get $containerID envlists] = "" && [/container get $containerID mountlists] = "foxos-mihomo-runtime" && [/container get $containerID root-dir] = ($FoxOSSiteStorageRoot . "/containers/mihomo")) do={ :set accepted true }
  :if ($owner = "foxos:mosdns" && $containerName = "foxos-mosdns" && [/container get $containerID interface] = "veth-mosdns" && [/container get $containerID envlists] = "foxos-mosdns-env" && [/container get $containerID mountlists] = "foxos-mosdns-runtime" && [/container get $containerID root-dir] = ($FoxOSSiteStorageRoot . "/containers/mosdns")) do={ :set accepted true }
  :if (($owner = "foxos:active" || $owner = "foxos:pending" || $owner = "foxos:rollback" || $owner = "foxos:rollback-complete" || $owner = "foxos:transition:promote" || $owner = "foxos:transition:rollback" || $owner = "foxos:transition:rollback:previous" || $owner = "foxos:retained" || $owner = "foxos:failed") && $containerName ~ "^foxos-[A-Za-z0-9._-]+$" && [/container get $containerID interface] = "veth-foxos" && [/container get $containerID envlists] = "foxos-env" && [/container get $containerID mountlists] = "foxos-mihomo-config,foxos-data,foxos-backups" && [/container get $containerID root-dir] = ($FoxOSSiteStorageRoot . "/containers/" . $containerName)) do={ :set accepted true }
  :if ([/container get $containerID status] != "stopped" || ([/container get $containerID start-on-boot] != false && [/container get $containerID start-on-boot] != "no") || ([/container get $containerID logging] != true && [/container get $containerID logging] != "yes")) do={ :set accepted false }
  :if ($accepted = false) do={ :error "容器完整身份在删除前变化；停止卸载" }
  /container/remove $containerID
}

:local dnsRecord $dnsRecordSnapshot
:local currentDNSRecord [/ip/dns/static find where comment="foxos:dns:admin"]
:if ([:len $currentDNSRecord] != [:len $dnsRecord] || ([:len $dnsRecord] = 1 && [/ip/dns/static get $currentDNSRecord .id] != [/ip/dns/static get $dnsRecord .id])) do={ :error "DNS 删除对象 ID 在摘要确认后变化" }
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
  :if ([:len $currentPortID] != [:len $portID] || ([:len $portID] = 1 && [/interface/bridge/port get $currentPortID .id] != [/interface/bridge/port get $portID .id])) do={ :error ("bridge port ID 在摘要确认后变化: " . $vethName) }
  :if ([:len $portID] > 0) do={
    :if ([:len $portID] != 1 || [/interface/bridge/port get $portID bridge] != $FoxOSSiteManagementBridge || [/interface/bridge/port get $portID comment] != $owner) do={ :error ("bridge port 回读冲突: " . $vethName) }
    /interface/bridge/port/remove $portID
  }
  :local currentVethID [/interface/veth find where name=$vethName]
  :if ([:len $currentVethID] != [:len $vethID] || ([:len $vethID] = 1 && [/interface/veth get $currentVethID .id] != [/interface/veth get $vethID .id])) do={ :error ("veth ID 在摘要确认后变化: " . $vethName) }
  :if ([:len $vethID] > 0) do={
    :if ([:len $vethID] != 1 || [/interface/veth get $vethID comment] != $owner || [/interface/veth get $vethID address] != $expectedAddress || [/interface/veth get $vethID gateway] != $FoxOSSiteRouterAddress) do={ :error ("veth 回读冲突: " . $vethName) }
    /interface/veth/remove $vethID
  }
}

:local serviceUser $serviceUserSnapshot
:local currentServiceUser [/user find where name="foxos-service"]
:if ([:len $currentServiceUser] != [:len $serviceUser] || ([:len $serviceUser] = 1 && [/user get $currentServiceUser .id] != [/user get $serviceUser .id])) do={ :error "foxos-service ID 在摘要确认后变化" }
:if ([:len $serviceUser] > 0) do={
  :if ([:len $serviceUser] != 1 || [/user get $serviceUser comment] != "foxos:service" || [/user get $serviceUser group] != "foxos-rest" || [/user get $serviceUser address] != ($FoxOSSiteFoxOSAddress . "/32")) do={ :error "foxos-service 回读冲突" }
  /user/remove $serviceUser
}
:local serviceGroup $serviceGroupSnapshot
:local currentServiceGroup [/user/group find where name="foxos-rest"]
:if ([:len $currentServiceGroup] != [:len $serviceGroup] || ([:len $serviceGroup] = 1 && [/user/group get $currentServiceGroup .id] != [/user/group get $serviceGroup .id])) do={ :error "foxos-rest ID 在摘要确认后变化" }
:if ([:len $serviceGroup] > 0) do={
  :if ([:len $serviceGroup] != 1 || [/user/group get $serviceGroup policy] != "read,write,rest-api") do={ :error "foxos-rest 回读冲突" }
  /user/group/remove $serviceGroup
}

:local allowedEnvKeys "|FOXOS_INSTALL_MARKER|FOXOS_ENV|FOXOS_ROUTEROS_URL|FOXOS_ROUTEROS_USERNAME|FOXOS_ROUTEROS_PASSWORD|FOXOS_MIHOMO_URL|FOXOS_MIHOMO_PROXY_URL|FOXOS_MIHOMO_SECRET|FOXOS_MIHOMO_BASE_CONFIG|FOXOS_MIHOMO_LOCAL_CONFIG|FOXOS_MIHOMO_RUNTIME_CONFIG|FOXOS_MIHOMO_BACKUP_DIR|FOXOS_MIHOMO_VALIDATOR_BINARY|FOXOS_MOSDNS_URL|FOXOS_API_TOKEN|FOXOS_CONFIRMATION_KEY|FOXOS_BACKUP_DIR|FOXOS_UPGRADE_STATE_PATH|FOXOS_SITE_MANAGEMENT_BRIDGE|FOXOS_SITE_STORAGE_ROOT|FOXOS_SITE_NETWORK|FOXOS_SITE_ROUTER_ADDRESS|FOXOS_SITE_MIHOMO_ADDRESS|FOXOS_SITE_MOSDNS_ADDRESS|FOXOS_SITE_FOXOS_ADDRESS|FOXOS_SITE_PUBLIC_HOSTNAME|FOXOS_HTTPS_ENABLED|FOXOS_SUBSCRIPTION_PRIVATE_CIDRS|"
:if ([:len [/container/envs find where list="foxos-env"]] != [:len $envItems]) do={ :error "foxos-env 删除集在摘要确认后变化" }
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

:local mountDefinitions {"foxos-mihomo-runtime|mihomo-config|/root/.config/mihomo";"foxos-mihomo-config|mihomo-config|/data/mihomo";"foxos-mosdns-runtime|mosdns-config|/cus/mosdns";"foxos-data|foxos-data|/data";"foxos-backups|foxos-backups|/backups"}
:foreach definition in=$mountDefinitions do={
  :local p1 [:find $definition "|"]
  :local p2 [:find $definition "|" ($p1 + 1)]
  :local mountName [:pick $definition 0 $p1]
  :local expectedSource ($FoxOSSiteStorageRoot . "/" . [:pick $definition ($p1 + 1) $p2])
  :local expectedDestination [:pick $definition ($p2 + 1) [:len $definition]]
  :local mountID ""
  :if ($mountName = "foxos-mihomo-runtime") do={ :set mountID $mihomoRuntimeMountSnapshot }
  :if ($mountName = "foxos-mihomo-config") do={ :set mountID $mihomoConfigMountSnapshot }
  :if ($mountName = "foxos-mosdns-runtime") do={ :set mountID $mosdnsRuntimeMountSnapshot }
  :if ($mountName = "foxos-data") do={ :set mountID $dataMountSnapshot }
  :if ($mountName = "foxos-backups") do={ :set mountID $backupsMountSnapshot }
  :local currentMountID [/container/mounts find where list=$mountName]
  :if ([:len $currentMountID] != [:len $mountID] || ([:len $mountID] = 1 && [/container/mounts get $currentMountID .id] != [/container/mounts get $mountID .id])) do={ :error ("mount ID 在摘要确认后变化: " . $mountName) }
  :if ([:len $mountID] > 0) do={
    :if ([:len $mountID] != 1 || [/container/mounts get $mountID src] != $expectedSource || [/container/mounts get $mountID dst] != $expectedDestination || ([/container/mounts get $mountID read-only] != false && [/container/mounts get $mountID read-only] != "no")) do={ :error ("mount 回读冲突: " . $mountName) }
    /container/mounts/remove $mountID
  }
}

:local expectedStartSource (":delay 20s; /import file-name=" . $FoxOSSiteStorageRoot . "/load-site-config.rsc; /import file-name=" . $FoxOSSiteStorageRoot . "/foxos-start-all.rsc")
:local startScript $startScriptSnapshot
:local currentStartScript [/system/script find where name="foxos-start-sequence"]
:if ([:len $currentStartScript] != [:len $startScript] || ([:len $startScript] = 1 && [/system/script get $currentStartScript .id] != [/system/script get $startScript .id])) do={ :error "冷启动协调脚本 ID 在摘要确认后变化" }
:if ([:len $startScript] > 0) do={
  :if ([:len $startScript] != 1 || [/system/script get $startScript comment] != "foxos:start-sequence" || [/system/script get $startScript source] != $expectedStartSource || [/system/script get $startScript policy] != "read,write,test") do={ :error "冷启动协调脚本回读冲突" }
  /system/script remove $startScript
}

:set FoxOSUninstallInspectVerbose false
/import file-name=($FoxOSSiteStorageRoot . "/foxos-uninstall-inspect.rsc")
:if ($FoxOSUninstallRemainingCount != 0) do={ :error "卸载已执行但仍有 owned 资源；重新运行只读 plan 后继续收敛" }
:put "FoxOS RouterOS resources converged to DONE. Persistent files, images, configs, versioned root-dirs, backups, site manifest, and local CA remain for recovery or audited manual disposal."
