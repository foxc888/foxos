# FoxOS 全栈安装脚本（RouterOS x86/x86_64 -> Linux amd64）
# 目标拓扑只从不可变 loader 已验证并加载的 site-config.rsc 读取。
# 只管理带 foxos: 所有权标识的资源，不修改 DNS、DHCP、默认路由、NAT、Mangle 或现有防火墙。
# 执行前必须先运行 preflight.rsc 和 foxos-plan.rsc，完成 RouterOS export/backup，并由操作者明确确认计划。
# 目标 RouterOS/container package 版本为 7.21+；官方 x86 通常对应 Linux amd64。

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
:global FoxOSInstallInspectVerbose false
:global FoxOSInstallCurrentDigest
:global FoxOSInstallApprovedDigest
:global FoxOSInstallConfirmation
:if ($FoxOSSiteManifestVersion != 2) do={ :error "先导入不可变的 load-site-config.rsc" }
/import file-name=($FoxOSSiteStorageRoot . "/load-site-config.rsc")
:if ($FoxOSContainerCompatVersion != 2) do={ :error "container compatibility contract is unavailable" }
:if ($FoxOSMountCompatVersion != 2 || $FoxOSWritableMountMode != "rw") do={ :error "mount compatibility contract is unavailable" }
:if ($FoxOSServiceAccessContractVersion != 1 || [:typeof $FoxOSServiceGroupPolicy] != "str" || [:typeof $FoxOSServiceGroupPolicyMatches] != "array") do={ :error "RouterOS service access contract is unavailable" }
:if ($FoxOSSecretContractVersion != 1 || $FoxOSReadonlyMountMode != "ro" || $FoxOSSecretHostDirectory != ($FoxOSSiteStorageRoot . "/foxos-secrets")) do={ :error "secret-file compatibility contract is unavailable" }
:local managementBridge $FoxOSSiteManagementBridge
:local storageRoot $FoxOSSiteStorageRoot
:local storageMode "disk"
:if ($storageRoot = "foxos") do={ :set storageMode "internal" }
:local releaseID "__FOXOS_RELEASE_ID__"
:if ($releaseID ~ "^__.*__\$" || [:len $releaseID] < 1 || [:len $releaseID] > 40 || !($releaseID ~ "^[A-Za-z0-9._-]+\$")) do={ :error "foxos-full-install.rsc 未绑定有效 release ID；只能使用发布包内脚本" }
:local foxosImagePath ($storageRoot . "/foxos-upgrade-" . $releaseID . "/foxos-amd64.tar")
:local siteNetwork $FoxOSSiteNetwork
:local prefixLength $FoxOSSitePrefixLength
:local routerAddress $FoxOSSiteRouterAddress
:local routerCIDR ($routerAddress . "/" . $prefixLength)
:local mihomoIP $FoxOSSiteMihomoAddress
:local mihomoAddress ($mihomoIP . "/" . $prefixLength)
:local mosdnsIP $FoxOSSiteMosDNSAddress
:local mosdnsAddress ($mosdnsIP . "/" . $prefixLength)
:local foxosIP $FoxOSSiteFoxOSAddress
:local foxosAddress ($foxosIP . "/" . $prefixLength)
:local publicHostname $FoxOSSitePublicHostname
:local subscriptionPrivateCIDRs $FoxOSSiteSubscriptionPrivateCIDRs
:local architecture [/system/resource get architecture-name]
:local imageArchitecture ""
:if ($architecture = "x86" || $architecture = "x86_64") do={ :set imageArchitecture "amd64" }
:local routerVersion [/system/resource get version]
:local routerVersionBase $routerVersion
:local routerVersionSpace [:find $routerVersionBase " "]
:if ([:typeof $routerVersionSpace] != "nil") do={ :set routerVersionBase [:pick $routerVersionBase 0 $routerVersionSpace] }

:local approvedDigest $FoxOSInstallApprovedDigest
:local confirmationDigest $FoxOSInstallConfirmation
/import file-name=($storageRoot . "/preflight.rsc")
/import file-name=($storageRoot . "/foxos-install-inspect.rsc")
:if ([:len $approvedDigest] != 128 || $approvedDigest != $FoxOSInstallCurrentDigest) do={
  :error "RouterOS 或站点前态在计划后变化；重新运行 foxos-plan.rsc"
}
:if ($confirmationDigest != $approvedDigest) do={
  :error "未确认当前 APPROVED PLAN SHA-512；未执行任何资源写入"
}
:set FoxOSInstallConfirmation ""

:put "=== FoxOS exact change plan (write phase) ==="
:put "Create or reuse only: foxos-rest, foxos-service, non-sensitive foxos-env, four secret files, foxos-* mounts, foxos:* veth/bridge ports/containers."
:put ("Create or reuse addresses: " . $mihomoAddress . ", " . $mosdnsAddress . ", " . $foxosAddress . " on " . $managementBridge . ".")
:put "No DNS, DHCP, default route, NAT, Mangle, or existing firewall changes."
:put ("Storage: mode=" . $storageMode . " root=" . $storageRoot . ".")
:put ("Rollback: stop FoxOS containers, restore RouterOS backup, and keep " . $storageRoot . "/foxos-data plus old image tar files.")

:if ($imageArchitecture != "amd64") do={
  :error ("此 amd64 安装包仅支持 RouterOS x86 或 x86_64，当前为 " . $architecture)
}
:if ($architecture = "x86_64") do={ :put "WARNING architecture-name=x86_64 是非标准 RouterOS 环境；本次兼容仅代表允许进入目标机验收" }
:local firstVersionDot [:find $routerVersionBase "."]
:if ([:typeof $firstVersionDot] = "nil") do={ :error ("无法解析 RouterOS 版本: " . $routerVersion) }
:local versionMajor [:tonum [:pick $routerVersionBase 0 $firstVersionDot]]
:local versionTail [:pick $routerVersionBase ($firstVersionDot + 1) [:len $routerVersionBase]]
:local versionMinorEnd [:find ($versionTail . ".") "."]
:local versionMinor [:tonum [:pick $versionTail 0 $versionMinorEnd]]
:if ($versionMajor < 7 || ($versionMajor = 7 && $versionMinor < 21)) do={
  :error "RouterOS 7.21 或更高版本才支持本安装器使用的 envlists/mountlists"
}
:if ([:len [/interface/bridge find where name=$managementBridge]] != 1) do={
  :error ("未找到唯一管理桥: " . $managementBridge)
}
:local storageFree 0
:if ($storageMode = "internal") do={
  :set storageFree [/system/resource get free-hdd-space]
} else={
  :local diskID [/disk find where slot=$storageRoot]
  :if ([:len $diskID] != 1) do={
    :error ("未找到唯一持久化磁盘: " . $storageRoot)
  }
  :set storageFree [/disk get $diskID free]
}
:if ($storageFree < 536870912) do={
  :error ($storageRoot . " 可用空间不足 512 MiB: " . $storageFree)
}
:local packageID [/system/package find where name="container"]
:if ([:len $packageID] != 1) do={
  :error "container package 未安装"
}
:if ([/system/package get $packageID disabled] = true) do={
  :error "container package 已禁用"
}
:if ([/system/package get $packageID version] != $routerVersionBase) do={
  :error "container package 与 RouterOS 版本不一致"
}
:local containerDeviceMode [/system/device-mode get container]
:local schedulerDeviceMode [/system/device-mode get scheduler]
:if ($containerDeviceMode != true && $containerDeviceMode != "yes") do={
  :error "device-mode container=yes 未启用"
}
:if ($schedulerDeviceMode != true && $schedulerDeviceMode != "yes") do={
  :error "device-mode scheduler=yes 未启用；禁止创建无法冷启动的安装"
}
:local routerManagementIP [/ip/address find where address~($routerAddress . "/")]
:if ([:len $routerManagementIP] != 1) do={
  :error ("未找到唯一 RouterOS 管理地址 " . $routerCIDR)
}
:if ([/ip/address get $routerManagementIP address] != $routerCIDR || [/ip/address get $routerManagementIP interface] != $managementBridge) do={
  :error ("RouterOS 管理地址必须是 " . $managementBridge . " 上的 " . $routerCIDR)
}
:local wwwService [/ip/service find where name="www" && dynamic=no]
:if ([:len $wwwService] != 1) do={ :error "未找到唯一 RouterOS www/REST 服务" }
:local wwwAddresses [/ip/service get $wwwService address]
:if ([/ip/service get $wwwService disabled] = true || [/ip/service get $wwwService port] != 80) do={
  :error "FoxOS 需要已启用且端口为 80 的 RouterOS www/REST 服务"
}
:local wwwCursor 0
:local wwwCount 0
:local wwwSeen ","
:local wwwAllowed true
:local foxosRESTAddress ($foxosIP . "/32")
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
:if ($wwwAllowed = false || $wwwCount < 1) do={
  :error ("www/REST address list may contain only exact entries " . $siteNetwork . " and/or " . $foxosRESTAddress)
}

:local upgradeDirectory ("foxos-upgrade-" . $releaseID)
:local requiredFiles {"mihomo_amd64.tar";"mosdns-amd64.tar";($upgradeDirectory . "/foxos-amd64.tar");($upgradeDirectory . "/upgrade-inspect.rsc");($upgradeDirectory . "/upgrade-plan.rsc");($upgradeDirectory . "/upgrade.rsc");($upgradeDirectory . "/upgrade-promote-inspect.rsc");($upgradeDirectory . "/upgrade-promote-plan.rsc");($upgradeDirectory . "/upgrade-promote.rsc");($upgradeDirectory . "/rollback-inspect.rsc");($upgradeDirectory . "/rollback-plan.rsc");($upgradeDirectory . "/rollback.rsc");($upgradeDirectory . "/upgrade-cleanup-inspect.rsc");($upgradeDirectory . "/upgrade-cleanup-plan.rsc");($upgradeDirectory . "/upgrade-cleanup-apply.rsc");($upgradeDirectory . "/UPGRADE-MANIFEST.txt");($upgradeDirectory . "/SHA256SUMS");"site-config.rsc";"site-config.rsc.sha512";"load-site-config.rsc";"preflight.rsc";"foxos-install-inspect.rsc";"foxos-plan.rsc";"foxos-start-all.rsc";"foxos-trust-ca.rsc";"foxos-verify.rsc";"foxos-uninstall-inspect.rsc";"uninstall-plan.rsc";"uninstall-apply.rsc";"SHA256SUMS"}
:foreach fileName in=$requiredFiles do={
  :local fileID [/file find where name=($storageRoot . "/" . $fileName)]
  :if ([:len $fileID] != 1) do={
    :error ("缺少或不唯一文件: " . $storageRoot . "/" . $fileName)
  }
}
:foreach configFile in={"mihomo-config/config.yaml";"mosdns-config/config_custom.yaml"} do={
  :local configID [/file find where name=($storageRoot . "/" . $configFile)]
  :if ([:len $configID] != 1) do={
    :error ("缺少或不唯一配置: " . $storageRoot . "/" . $configFile)
  }
}

:local randomCharacters "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
:local envMarker [/container/envs find where list="foxos-env" key="FOXOS_INSTALL_MARKER"]
:local existingInstall false
:local installMarkerValue ""
:local foxosRouterPassword ""
:local foxosMihomoSecret ""
:local foxosApiToken ""
:local foxosConfirmationKey ""
:foreach sensitiveKey in=$FoxOSSensitiveEnvKeys do={
  :if ([:len [/container/envs find where list="foxos-env" key=$sensitiveKey]] > 0) do={
    :error ("legacy sensitive env is forbidden; rotate credentials and use secret files: " . $sensitiveKey)
  }
}
:if ([:len $envMarker] > 1) do={ :error "FOXOS_INSTALL_MARKER 不唯一，拒绝继续" }
:if ([:len $envMarker] = 0 && [:len [/user/group find where name="foxos-rest"]] > 0) do={
  :error "同名 foxos-rest 用户组已存在且没有 FoxOS 安装标记，拒绝在写入前继续"
}
:if ([:len $envMarker] = 1) do={
  :set installMarkerValue [/container/envs get $envMarker value]
  :if ($installMarkerValue != "foxos:applying" && $installMarkerValue != "foxos:complete") do={ :error "FOXOS_INSTALL_MARKER 状态无效" }
  :set existingInstall true
} else={
  :if ([:len [/container/envs find where list="foxos-env"]] > 0) do={
    :error "同名 foxos-env 已存在但没有 FoxOS applying/complete 标记，拒绝覆盖"
  }
  /container/envs add list=foxos-env key=FOXOS_INSTALL_MARKER value="foxos:applying"
  :set installMarkerValue "foxos:applying"
}

:local secretDirectory [/file find where name=$FoxOSSecretHostDirectory]
:if ([:len $secretDirectory] = 0) do={
  :if ($installMarkerValue = "foxos:complete") do={ :error "complete install is missing the secret directory; refuse implicit credential rotation" }
  /file add name=$FoxOSSecretHostDirectory type=directory
}
:set secretDirectory [/file find where name=$FoxOSSecretHostDirectory]
:if ([:len $secretDirectory] != 1 || [/file get $secretDirectory type] != "directory") do={ :error "secret directory creation or readback failed" }

:local secretDefinitions {"routeros-password|32|32";"mihomo-secret|32|48";"api-token|32|64";"confirmation-key|32|64"}
:foreach definition in=$secretDefinitions do={
  :local firstSeparator [:find $definition "|"]
  :local secondSeparator [:find $definition "|" ($firstSeparator + 1)]
  :local secretName [:pick $definition 0 $firstSeparator]
  :local minimumLength [:tonum [:pick $definition ($firstSeparator + 1) $secondSeparator]]
  :local generatedLength [:tonum [:pick $definition ($secondSeparator + 1) [:len $definition]]]
  :local secretPath ($FoxOSSecretHostDirectory . "/" . $secretName)
  :local secretFile [/file find where name=$secretPath]
  :if ([:len $secretFile] = 0) do={
    :if ($installMarkerValue = "foxos:complete") do={ :error ("complete install is missing secret file: " . $secretName) }
    /file add name=$secretPath type=file
  }
  :set secretFile [/file find where name=$secretPath]
  :if ([:len $secretFile] != 1 || [/file get $secretFile type] != "file") do={ :error ("secret file creation or identity readback failed: " . $secretName) }
  :if ([/file get $secretFile size] = 0) do={
    :if ($installMarkerValue = "foxos:complete") do={ :error ("complete install has an empty secret file: " . $secretName) }
    :local generatedValue [:rndstr from=$randomCharacters length=$generatedLength]
    /file set $secretFile contents=$generatedValue
  }
}
:foreach definition in=$secretDefinitions do={
  :local firstSeparator [:find $definition "|"]
  :local secondSeparator [:find $definition "|" ($firstSeparator + 1)]
  :local secretName [:pick $definition 0 $firstSeparator]
  :local minimumLength [:tonum [:pick $definition ($firstSeparator + 1) $secondSeparator]]
  :local secretValue [$FoxOSSecretRead $secretName]
  :if ([:len $secretValue] < $minimumLength) do={ :error ("secret file does not meet the minimum length: " . $secretName) }
  :local secretEvidence [$FoxOSSecretEvidence $secretName]
  :if ([:len $secretEvidence] < 1) do={ :error ("secret file evidence generation failed: " . $secretName) }
  :if ($secretName = "routeros-password") do={ :set foxosRouterPassword $secretValue }
  :if ($secretName = "mihomo-secret") do={ :set foxosMihomoSecret $secretValue }
  :if ($secretName = "api-token") do={ :set foxosApiToken $secretValue }
  :if ($secretName = "confirmation-key") do={ :set foxosConfirmationKey $secretValue }
}
:if ($foxosRouterPassword = $foxosMihomoSecret || $foxosRouterPassword = $foxosApiToken || $foxosRouterPassword = $foxosConfirmationKey || $foxosMihomoSecret = $foxosApiToken || $foxosMihomoSecret = $foxosConfirmationKey || $foxosApiToken = $foxosConfirmationKey) do={
  :error "generated secret files must contain four distinct values"
}

:local fixedEnvDefinitions {"FOXOS_ENV|production";("FOXOS_ROUTEROS_URL|http://" . $routerAddress);"FOXOS_ROUTEROS_USERNAME|foxos-service";("FOXOS_MIHOMO_URL|http://" . $mihomoIP . ":9090");("FOXOS_MIHOMO_PROXY_URL|http://" . $mihomoIP . ":7890");"FOXOS_MIHOMO_BASE_CONFIG|/data/mihomo/base.yaml";"FOXOS_MIHOMO_LOCAL_CONFIG|/data/mihomo/config.yaml";"FOXOS_MIHOMO_RUNTIME_CONFIG|/root/.config/mihomo/config.yaml";"FOXOS_MIHOMO_BACKUP_DIR|/backups/mihomo";"FOXOS_MIHOMO_VALIDATOR_BINARY|/usr/local/bin/mihomo";("FOXOS_MOSDNS_URL|http://" . $mosdnsIP . ":53");"FOXOS_BACKUP_DIR|/backups/foxos";"FOXOS_UPGRADE_STATE_PATH|/data/upgrade-checkpoint.json";("FOXOS_SITE_MANAGEMENT_BRIDGE|" . $managementBridge);("FOXOS_SITE_STORAGE_ROOT|" . $storageRoot);("FOXOS_SITE_NETWORK|" . $siteNetwork);("FOXOS_SITE_ROUTER_ADDRESS|" . $routerAddress);("FOXOS_SITE_MIHOMO_ADDRESS|" . $mihomoIP);("FOXOS_SITE_MOSDNS_ADDRESS|" . $mosdnsIP);("FOXOS_SITE_FOXOS_ADDRESS|" . $foxosIP);("FOXOS_SITE_PUBLIC_HOSTNAME|" . $publicHostname);"FOXOS_HTTPS_ENABLED|true"}
:foreach definition in=$fixedEnvDefinitions do={
  :local separator [:find $definition "|"]
  :local envKey [:pick $definition 0 $separator]
  :local expectedValue [:pick $definition ($separator + 1) [:len $definition]]
  :local envID [/container/envs find where list="foxos-env" key=$envKey]
  :if ([:len $envID] > 1) do={ :error ("FoxOS env 键不唯一: " . $envKey) }
  :if ([:len $envID] = 0) do={
    /container/envs add list=foxos-env key=$envKey value=$expectedValue
  } else={
    :if ([/container/envs get $envID value] != $expectedValue) do={
      :error ("现有 FoxOS env 值与固定部署计划不一致: " . $envKey)
    }
  }
}
:local expectedEnvCount 23
:local privateCIDRID [/container/envs find where list="foxos-env" key="FOXOS_SUBSCRIPTION_PRIVATE_CIDRS"]
:if ([:len $privateCIDRID] > 1) do={ :error "FOXOS_SUBSCRIPTION_PRIVATE_CIDRS 不唯一" }
:if ([:len $subscriptionPrivateCIDRs] > 0) do={
  :set expectedEnvCount 24
  :if ([:len $privateCIDRID] = 0) do={
    /container/envs add list=foxos-env key=FOXOS_SUBSCRIPTION_PRIVATE_CIDRS value=$subscriptionPrivateCIDRs
  } else={
    :if ([/container/envs get $privateCIDRID value] != $subscriptionPrivateCIDRs) do={
      :error "现有订阅私网 allowlist 与站点清单不一致，拒绝自动覆盖"
    }
  }
} else={
  :if ([:len $privateCIDRID] > 0) do={
    :error "foxos-env 已配置订阅私网 allowlist，但站点清单为空；拒绝隐式保留或删除"
  }
}
:if ([:len [/container/envs find where list="foxos-env"]] != $expectedEnvCount) do={
  :error "foxos-env 包含安全基线之外的键，拒绝继续"
}

:local currentMosDNSEnvItems [/container/envs find where list="foxos-mosdns-env"]
:local allowedMosDNSEnvKeys "|FOXOS_INSTALL_MARKER|MOSDNS_AUTO_INIT|"
:foreach currentMosDNSEnvID in=$currentMosDNSEnvItems do={
  :local currentMosDNSEnvKey [/container/envs get $currentMosDNSEnvID key]
  :if ([:typeof [:find $allowedMosDNSEnvKeys ("|" . $currentMosDNSEnvKey . "|")]] = "nil") do={
    :error ("foxos-mosdns-env 出现未知键，拒绝在补写前继续: " . $currentMosDNSEnvKey)
  }
}
:local mosdnsEnvMarker [/container/envs find where list="foxos-mosdns-env" key="FOXOS_INSTALL_MARKER"]
:if ([:len $mosdnsEnvMarker] > 1) do={ :error "foxos-mosdns-env 所有权标记不唯一" }
:if ([:len $mosdnsEnvMarker] = 0) do={
  :if ([:len [/container/envs find where list="foxos-mosdns-env"]] > 0) do={ :error "foxos-mosdns-env 已存在但没有 FoxOS 所有权标记" }
  /container/envs add list=foxos-mosdns-env key=FOXOS_INSTALL_MARKER value="foxos:mosdns:applying"
} else={
  :local mosdnsMarkerValue [/container/envs get $mosdnsEnvMarker value]
  :if ($mosdnsMarkerValue != "foxos:mosdns:applying" && $mosdnsMarkerValue != "foxos:mosdns:complete") do={ :error "foxos-mosdns-env 状态无效" }
}
:local mosdnsAutoInit [/container/envs find where list="foxos-mosdns-env" key="MOSDNS_AUTO_INIT"]
:if ([:len $mosdnsAutoInit] > 1) do={ :error "MOSDNS_AUTO_INIT 不唯一" }
:if ([:len $mosdnsAutoInit] = 1 && [/container/envs get $mosdnsAutoInit value] != "0") do={ :error "MOSDNS_AUTO_INIT 必须为 0，拒绝覆盖" }
:if ([:len $mosdnsAutoInit] = 0) do={
  /container/envs add list=foxos-mosdns-env key=MOSDNS_AUTO_INIT value="0"
}
:if ([:len [/container/envs find where list="foxos-mosdns-env"]] != 2 || [:len [/container/envs find where list="foxos-mosdns-env" key=MOSDNS_AUTO_INIT value="0"]] != 1) do={
  :error "foxos-mosdns-env 必须且只能包含所有权标记与 MOSDNS_AUTO_INIT=0"
}
:if ($existingInstall) do={
  :put "复用或补齐现有 FoxOS 凭据，不在重复执行时轮换有效密钥。"
} else={
  :put "已生成新的随机 FoxOS 凭据。"
}

:foreach mihomoConfigName in={"config.yaml";"base.yaml"} do={
  :local mihomoConfigFile [/file find where name=($storageRoot . "/mihomo-config/" . $mihomoConfigName)]
  :if ([:len $mihomoConfigFile] != 1) do={ :error ("Mihomo 配置文件缺失或不唯一: " . $mihomoConfigName) }
  :local mihomoConfig [/file get $mihomoConfigFile contents]
  :local secretStart [:find $mihomoConfig "\nsecret:"]
  :if ([:typeof $secretStart] = "nil") do={
    :if ([:find $mihomoConfig "secret:"] = 0) do={ :set secretStart 0 } else={ :error ("Mihomo 配置缺少顶层 secret 字段: " . $mihomoConfigName) }
  } else={ :set secretStart ($secretStart + 1) }
  :local secretEnd [:find $mihomoConfig "\n" $secretStart]
  :if ([:typeof $secretEnd] = "nil") do={ :set secretEnd [:len $mihomoConfig] }
  :local updatedMihomoConfig (([:pick $mihomoConfig 0 $secretStart]) . "secret: \"" . $foxosMihomoSecret . "\"" . ([:pick $mihomoConfig $secretEnd [:len $mihomoConfig]]))
  /file set $mihomoConfigFile contents=$updatedMihomoConfig
  :if ([:typeof [:find [/file get $mihomoConfigFile contents] ("secret: \"" . $foxosMihomoSecret . "\"")]] = "nil") do={
    :error ("Mihomo Secret 写入后验证失败: " . $mihomoConfigName)
  }
}

:local mountDefinitions {($FoxOSSecretMountName . "|" . $FoxOSSecretHostDirectory . "|" . $FoxOSSecretContainerDirectory . "|" . $FoxOSReadonlyMountMode);("foxos-mihomo-runtime|" . $storageRoot . "/mihomo-config|/root/.config/mihomo|" . $FoxOSWritableMountMode);("foxos-mihomo-config|" . $storageRoot . "/mihomo-config|/data/mihomo|" . $FoxOSWritableMountMode);("foxos-mosdns-runtime|" . $storageRoot . "/mosdns-config|/cus/mosdns|" . $FoxOSWritableMountMode);("foxos-data|" . $storageRoot . "/foxos-data|/data|" . $FoxOSWritableMountMode);("foxos-backups|" . $storageRoot . "/foxos-backups|/backups|" . $FoxOSWritableMountMode)}
:foreach definition in=$mountDefinitions do={
  :local first [:find $definition "|"]
  :local second [:find $definition "|" ($first + 1)]
  :local third [:find $definition "|" ($second + 1)]
  :local mountName [:pick $definition 0 $first]
  :local sourcePath [:pick $definition ($first + 1) $second]
  :local destination [:pick $definition ($second + 1) $third]
  :local expectedMode [:pick $definition ($third + 1) [:len $definition]]
  :local mountID [/container/mounts find where list=$mountName]
  :if ([:len $mountID] = 0) do={
    /container/mounts add list=$mountName src=$sourcePath dst=$destination mode=$expectedMode
  } else={
    :if ([:len $mountID] != 1) do={ :error ("挂载名不唯一: " . $mountName) }
  }
  :set mountID [/container/mounts find where list=$mountName]
  :if ([:len $mountID] != 1 || [$FoxOSMountSource $mountID] != $sourcePath || [/container/mounts get $mountID dst] != $destination || [$FoxOSMountMode $mountID] != $expectedMode) do={
    :error ("挂载内容或读写属性不匹配，拒绝继续: " . $mountName)
  }
}

:local serviceGroup [/user/group find where name="foxos-rest"]
:if ([:len $serviceGroup] = 0) do={
  /user/group add name=foxos-rest policy=$FoxOSServiceGroupPolicy
} else={
  :if ([:len $serviceGroup] != 1) do={ :error "foxos-rest 用户组不唯一" }
  :if ($existingInstall = false) do={ :error "同名 foxos-rest 用户组已存在且没有 FoxOS 安装标记，拒绝复用" }
}
:set serviceGroup [/user/group find where name="foxos-rest"]
:if ([:len $serviceGroup] != 1 || [$FoxOSServiceGroupPolicyMatches $serviceGroup] = false) do={
  :error "foxos-rest 权限创建或复用回读与安全基线不一致"
}
:local serviceUser [/user find where name="foxos-service"]
:if ([:len $serviceUser] = 0) do={
  /user add name=foxos-service group=foxos-rest address=($foxosIP . "/32") password=$foxosRouterPassword comment="foxos:service"
} else={
  :if ([:len $serviceUser] != 1) do={ :error "foxos-service 用户不唯一" }
  :if ([/user get $serviceUser comment] != "foxos:service") do={ :error "同名 RouterOS 用户不是 FoxOS 创建，拒绝覆盖" }
  /user set $serviceUser group=foxos-rest address=($foxosIP . "/32") password=$foxosRouterPassword
}

:local vethDefinitions {"veth-mihomo|" . $mihomoAddress . "|foxos:mihomo";"veth-mosdns|" . $mosdnsAddress . "|foxos:mosdns";"veth-foxos|" . $foxosAddress . "|foxos:admin"}
:foreach definition in=$vethDefinitions do={
  :local first [:find $definition "|"]
  :local second [:find $definition "|" ($first + 1)]
  :local vethName [:pick $definition 0 $first]
  :local vethAddress [:pick $definition ($first + 1) $second]
  :local owner [:pick $definition ($second + 1) [:len $definition]]
  :local addressOnly [:pick $vethAddress 0 [:find $vethAddress "/"]]
  :local vethID [/interface/veth find where name=$vethName]
  :local addressVeth [/interface/veth find where address=$vethAddress]
  :if ([:len [/ip/address find where address~($addressOnly . "/")]] > 0) do={ :error ("保留地址已配置在 RouterOS: " . $addressOnly) }
  :if ([:len [/ip/dhcp-server/lease find where address=$addressOnly]] > 0) do={ :error ("保留地址已被 DHCP Lease 使用: " . $addressOnly) }
  :if ([:len $addressVeth] > 0 && [:len $vethID] = 0) do={ :error ("保留地址已被其他 veth 使用: " . $vethAddress) }
  :if ([:len $vethID] = 0) do={
    :if ([:len [/ip/arp find where address=$addressOnly && status!="failed"]] > 0 || [/ping address=$addressOnly count=2 interval=200ms] > 0) do={
      :error ("保留地址已出现在 ARP 或可达，拒绝创建: " . $addressOnly)
    }
    /interface/veth add name=$vethName address=$vethAddress gateway=$routerAddress comment=$owner
  } else={
    :if ([:len $vethID] != 1) do={ :error ("veth 名不唯一: " . $vethName) }
    :if ([/interface/veth get $vethID comment] != $owner) do={ :error ("同名 veth 不属于 FoxOS: " . $vethName) }
    :if ([/interface/veth get $vethID address] != $vethAddress || [/interface/veth get $vethID gateway] != $routerAddress) do={
      :error ("已有 FoxOS veth 地址或网关不匹配: " . $vethName)
    }
    :if ([:len $addressVeth] != 1 || [/interface/veth get $addressVeth name] != $vethName) do={
      :error ("保留地址同时被其他 veth 使用: " . $vethAddress)
    }
  }
  :local portID [/interface/bridge/port find where interface=$vethName]
  :if ([:len $portID] = 0) do={
    /interface/bridge/port add bridge=$managementBridge interface=$vethName comment=$owner
  } else={
    :foreach port in=$portID do={
      :if ([/interface/bridge/port get $port bridge] != $managementBridge) do={ :error ("veth 已加入其他 bridge: " . $vethName) }
      :if ([/interface/bridge/port get $port comment] != $owner) do={ :error ("bridge port 没有匹配的 FoxOS 所有权标记: " . $vethName) }
    }
  }
}

:local mihomoContainer [/container find where comment="foxos:mihomo"]
:local mihomoByName [/container find where name="foxos-mihomo"]
:if ([:len $mihomoContainer] = 0) do={
  :if ([:len $mihomoByName] > 0) do={ :error "foxos-mihomo 同名容器没有 FoxOS 所有权标记" }
  /container/add name=foxos-mihomo file=($storageRoot . "/mihomo_amd64.tar") interface=veth-mihomo root-dir=($storageRoot . "/containers/mihomo") mountlists=foxos-mihomo-runtime logging=yes start-on-boot=no comment="foxos:mihomo"
}
:set mihomoContainer [/container find where comment="foxos:mihomo"]
:set mihomoByName [/container find where name="foxos-mihomo"]
:if ([:len $mihomoContainer] != 1 || [:len $mihomoByName] != 1 || $mihomoContainer != $mihomoByName || [/container get $mihomoContainer interface] != "veth-mihomo" || [/container get $mihomoContainer envlists] != "" || [$FoxOSContainerMountLists $mihomoContainer] != "foxos-mihomo-runtime" || [$FoxOSContainerRoot $mihomoContainer] != ($storageRoot . "/containers/mihomo") || ([/container get $mihomoContainer start-on-boot] != false && [/container get $mihomoContainer start-on-boot] != "no") || ([/container get $mihomoContainer logging] != true && [/container get $mihomoContainer logging] != "yes")) do={
  :error "Mihomo 容器身份契约不匹配"
}

:local mosdnsContainer [/container find where comment="foxos:mosdns"]
:local mosdnsByName [/container find where name="foxos-mosdns"]
:if ([:len $mosdnsContainer] = 0) do={
  :if ([:len $mosdnsByName] > 0) do={ :error "foxos-mosdns 同名容器没有 FoxOS 所有权标记" }
  /container/add name=foxos-mosdns file=($storageRoot . "/mosdns-amd64.tar") interface=veth-mosdns root-dir=($storageRoot . "/containers/mosdns") envlists=foxos-mosdns-env mountlists=foxos-mosdns-runtime logging=yes start-on-boot=no comment="foxos:mosdns"
}
:set mosdnsContainer [/container find where comment="foxos:mosdns"]
:set mosdnsByName [/container find where name="foxos-mosdns"]
:if ([:len $mosdnsContainer] != 1 || [:len $mosdnsByName] != 1 || $mosdnsContainer != $mosdnsByName || [/container get $mosdnsContainer interface] != "veth-mosdns" || [/container get $mosdnsContainer envlists] != "foxos-mosdns-env" || [$FoxOSContainerMountLists $mosdnsContainer] != "foxos-mosdns-runtime" || [$FoxOSContainerRoot $mosdnsContainer] != ($storageRoot . "/containers/mosdns") || ([/container get $mosdnsContainer start-on-boot] != false && [/container get $mosdnsContainer start-on-boot] != "no") || ([/container get $mosdnsContainer logging] != true && [/container get $mosdnsContainer logging] != "yes")) do={
  :error "MosDNS 容器身份契约不匹配"
}

:local foxosContainer [/container find where comment="foxos:active"]
:local foxosByName [/container find where name="foxos-initial"]
:if ([:len $foxosContainer] = 0) do={
  :if ([:len $foxosByName] > 0) do={ :error "foxos-initial 同名容器没有 FoxOS 所有权标记" }
  /container/add name=foxos-initial file=$foxosImagePath interface=veth-foxos root-dir=($storageRoot . "/containers/foxos-initial") envlists=foxos-env mountlists=$FoxOSAdminMountLists logging=no start-on-boot=no comment="foxos:active"
}
:set foxosContainer [/container find where comment="foxos:active"]
:local activeName ""
:if ([:len $foxosContainer] = 1) do={ :set activeName [/container get $foxosContainer name] }
:if ([:len $foxosContainer] != 1 || !($activeName ~ "^foxos-[A-Za-z0-9._-]+\$") || [/container get $foxosContainer interface] != "veth-foxos" || [/container get $foxosContainer envlists] != "foxos-env" || [$FoxOSContainerMountLists $foxosContainer] != $FoxOSAdminMountLists || [$FoxOSContainerRoot $foxosContainer] != ($storageRoot . "/containers/" . $activeName) || ([/container get $foxosContainer start-on-boot] != false && [/container get $foxosContainer start-on-boot] != "no") || ([/container get $foxosContainer logging] != false && [/container get $foxosContainer logging] != "no")) do={
  :error "FoxOS active 容器身份契约不匹配"
}

:local expectedStartSource (":delay 20s; /import file-name=" . $storageRoot . "/load-site-config.rsc; /import file-name=" . $storageRoot . "/foxos-start-all.rsc")
:local startScript [/system/script find where name="foxos-start-sequence"]
:if ([:len $startScript] = 0) do={
  /system/script add name=foxos-start-sequence source=$expectedStartSource policy=read,write,test comment="foxos:start-sequence"
} else={
  :if ([:len $startScript] != 1 || [/system/script get $startScript comment] != "foxos:start-sequence" || [/system/script get $startScript source] != $expectedStartSource || [/system/script get $startScript policy] != {"read";"write";"test"}) do={
    :error "冷启动协调脚本不符合 FoxOS 精确所有权或内容契约"
  }
}
:local startScheduler [/system/scheduler find where name="foxos-start-sequence"]
:if ([:len $startScheduler] = 0) do={
  /system/scheduler add name=foxos-start-sequence start-time=startup interval=0s on-event=foxos-start-sequence policy=read,write,test disabled=yes comment="foxos:start-sequence"
} else={
  :if ([:len $startScheduler] != 1 || [/system/scheduler get $startScheduler comment] != "foxos:start-sequence" || [/system/scheduler get $startScheduler on-event] != "foxos-start-sequence" || [/system/scheduler get $startScheduler start-time] != "startup" || [/system/scheduler get $startScheduler interval] != 0s || [/system/scheduler get $startScheduler policy] != {"read";"write";"test"}) do={
    :error "冷启动 scheduler 不符合 FoxOS 精确所有权或顺序契约"
  }
}

:put "FoxOS 全栈资源已创建或复用，镜像导入为异步操作。"
:put ("等待 Mihomo、MosDNS、FoxOS 三个容器均为 status=stopped，再执行 /import file-name=" . $storageRoot . "/foxos-start-all.rsc。")
:put "安装完成后四项凭据仅保存在只读 secret files；不输出内容，也不写入 foxos-env。"
:local finalMosDNSMarker [/container/envs find where list="foxos-mosdns-env" key="FOXOS_INSTALL_MARKER"]
:if ([:len $finalMosDNSMarker] != 1) do={ :error "MosDNS 安装状态标记回读失败" }
/container/envs set $finalMosDNSMarker value="foxos:mosdns:complete"
:local finalInstallMarker [/container/envs find where list="foxos-env" key="FOXOS_INSTALL_MARKER"]
:if ([:len $finalInstallMarker] != 1) do={ :error "FoxOS 安装状态标记回读失败" }
/container/envs set $finalInstallMarker value="foxos:complete"
:if ([/container/envs get $finalMosDNSMarker value] != "foxos:mosdns:complete" || [/container/envs get $finalInstallMarker value] != "foxos:complete") do={
  :error "安装资源已创建，但 complete 状态回读失败；重新运行计划后可继续收敛"
}
:set FoxOSInstallApprovedDigest ""
