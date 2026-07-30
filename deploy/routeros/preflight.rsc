# FoxOS RouterOS full-bundle read-only preflight
# 本脚本只读取配置、文件和网络状态；不会创建、修改或删除 RouterOS 资源。
# 官方 RouterOS amd64 通常返回 x86；部分非标准 x86 环境返回 x86_64。
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
:global FoxOSSiteLoadedDigest
:global FoxOSSiteLoadedConfigPath
:global FoxOSSiteLoaderVersion
:if ($FoxOSSiteManifestVersion != 2 || $FoxOSSiteLoaderVersion != 1) do={ :error "先导入不可变的 load-site-config.rsc，禁止直接导入可编辑清单" }
:local managementBridge $FoxOSSiteManagementBridge
:local storageRoot $FoxOSSiteStorageRoot
:local storageMode "disk"
:if ($storageRoot = "foxos") do={ :set storageMode "internal" }
:local releaseID "__FOXOS_RELEASE_ID__"
:if ($releaseID ~ "^__.*__\$" || [:len $releaseID] < 1 || [:len $releaseID] > 40 || !($releaseID ~ "^[A-Za-z0-9._-]+\$")) do={ :error "preflight.rsc 未绑定有效 release ID；只能使用发布包内脚本" }
:local upgradeDirectory ("foxos-upgrade-" . $releaseID)
:local siteNetwork $FoxOSSiteNetwork
:local prefixLength $FoxOSSitePrefixLength
:local minimumFreeBytes 536870912
:local routerAddress $FoxOSSiteRouterAddress
:local routerCIDR ($routerAddress . "/" . $prefixLength)
:local mihomoAddress $FoxOSSiteMihomoAddress
:local mosdnsAddress $FoxOSSiteMosDNSAddress
:local foxosAddress $FoxOSSiteFoxOSAddress
:local publicHostname $FoxOSSitePublicHostname
:local subscriptionPrivateCIDRs $FoxOSSiteSubscriptionPrivateCIDRs
:local failed false

:local expectedConfigPath ($storageRoot . "/site-config.rsc")
:if ($FoxOSSiteLoadedConfigPath != $expectedConfigPath || [:len $FoxOSSiteLoadedDigest] != 128) do={
  :put "ERROR 当前会话没有 loader 对此存储根和清单的有效证明"
  :set failed true
}
:local loadedConfigFile [/file find where name=$expectedConfigPath]
:local loadedDigestFile [/file find where name=($expectedConfigPath . ".sha512")]
:if ([:len $loadedConfigFile] != 1 || [:len $loadedDigestFile] != 1) do={
  :put "ERROR loader 绑定的清单或独立摘要缺失或不唯一"
  :set failed true
} else={
  :local currentConfigContents [/file get $loadedConfigFile contents]
  :local currentDigestContents [/file get $loadedDigestFile contents]
  :local currentExpectedDigest [:pick $currentDigestContents 0 128]
  :local currentActualDigest [:convert $currentConfigContents transform=sha512 to=hex]
  :if ([:len $currentDigestContents] != 129 || [:pick $currentDigestContents 128 129] != "\n" || $currentExpectedDigest != $FoxOSSiteLoadedDigest || $currentActualDigest != $FoxOSSiteLoadedDigest) do={
    :put "ERROR site-config.rsc 在 loader 执行后发生变化或摘要不匹配"
    :set failed true
  }
}

:put "=== site manifest validation ==="
:if ([:len $managementBridge] < 1 || [:len $managementBridge] > 63 || !($managementBridge ~ "^[A-Za-z0-9][A-Za-z0-9._-]*\$")) do={
  :put "ERROR management bridge name is invalid"
  :set failed true
}
:if ([:len $storageRoot] < 1 || [:len $storageRoot] > 63 || !($storageRoot ~ "^[A-Za-z0-9][A-Za-z0-9._-]*\$")) do={
  :put "ERROR storage root name is invalid"
  :set failed true
}
:if ($storageMode = "internal" && $storageRoot != "foxos") do={
  :put "ERROR internal storage must use the reserved root foxos"
  :set failed true
}
:if ($prefixLength < 8 || $prefixLength > 30) do={
  :put "ERROR site prefix length must be between 8 and 30"
  :set failed true
}
:local prefixSeparator [:find $siteNetwork "/"]
:local networkAddressValue
:if ([:typeof $prefixSeparator] = "nil") do={
  :put "ERROR FoxOSSiteNetwork must be a valid IPv4 CIDR"
  :set failed true
} else={
  :set networkAddressValue [:toip [:pick $siteNetwork 0 $prefixSeparator]]
  :if ([:typeof $networkAddressValue] != "ip" || $siteNetwork != ($networkAddressValue . "/" . $prefixLength)) do={
    :put "ERROR FoxOSSiteNetwork must use the canonical network address and match FoxOSSitePrefixLength"
    :set failed true
  }
}
:local routerAddressValue [:toip $routerAddress]
:local mihomoAddressValue [:toip $mihomoAddress]
:local mosdnsAddressValue [:toip $mosdnsAddress]
:local foxosAddressValue [:toip $foxosAddress]
:if ([:typeof $routerAddressValue] != "ip" || [:typeof $mihomoAddressValue] != "ip" || [:typeof $mosdnsAddressValue] != "ip" || [:typeof $foxosAddressValue] != "ip") do={
  :put "ERROR all four site service addresses must be valid IPv4 literals"
  :set failed true
}
:if ($routerAddress = $mihomoAddress || $routerAddress = $mosdnsAddress || $routerAddress = $foxosAddress || $mihomoAddress = $mosdnsAddress || $mihomoAddress = $foxosAddress || $mosdnsAddress = $foxosAddress) do={
  :put "ERROR RouterOS, Mihomo, MosDNS, and FoxOS addresses must be distinct"
  :set failed true
}
:local ipv4MaskDefinitions {"8|255.0.0.0";"9|255.128.0.0";"10|255.192.0.0";"11|255.224.0.0";"12|255.240.0.0";"13|255.248.0.0";"14|255.252.0.0";"15|255.254.0.0";"16|255.255.0.0";"17|255.255.128.0";"18|255.255.192.0";"19|255.255.224.0";"20|255.255.240.0";"21|255.255.248.0";"22|255.255.252.0";"23|255.255.254.0";"24|255.255.255.0";"25|255.255.255.128";"26|255.255.255.192";"27|255.255.255.224";"28|255.255.255.240";"29|255.255.255.248";"30|255.255.255.252";"31|255.255.255.254";"32|255.255.255.255"}
:local netmaskValue
:foreach maskDefinition in=$ipv4MaskDefinitions do={
  :local separator [:find $maskDefinition "|"]
  :if ([:tonum [:pick $maskDefinition 0 $separator]] = $prefixLength) do={ :set netmaskValue [:toip [:pick $maskDefinition ($separator + 1) [:len $maskDefinition]]] }
}
:if ([:typeof $networkAddressValue] = "ip" && [:typeof $netmaskValue] = "ip" && (($networkAddressValue & $netmaskValue) != $networkAddressValue)) do={
  :put "ERROR FoxOSSiteNetwork must use the canonical network address and match FoxOSSitePrefixLength"
  :set failed true
}
:if ([:typeof $networkAddressValue] = "ip" && [:typeof $netmaskValue] = "ip" && [:typeof $routerAddressValue] = "ip" && [:typeof $mihomoAddressValue] = "ip" && [:typeof $mosdnsAddressValue] = "ip" && [:typeof $foxosAddressValue] = "ip") do={
  :local broadcastAddressValue ($networkAddressValue | (~$netmaskValue))
  :foreach serviceAddress in={$routerAddressValue;$mihomoAddressValue;$mosdnsAddressValue;$foxosAddressValue} do={
    :if (($serviceAddress & $netmaskValue) != $networkAddressValue) do={ :put ("ERROR address is outside FoxOSSiteNetwork: " . $serviceAddress); :set failed true }
    :if ($serviceAddress = $networkAddressValue || $serviceAddress = $broadcastAddressValue) do={ :put ("ERROR network or broadcast address is not a usable host: " . $serviceAddress); :set failed true }
  }
}
:local publicHostnameLength [:len $publicHostname]
:local hostnameLabelsValid true
:local publicHostnameDoubleDot ("." . ".")
:if ($publicHostnameLength < 3 || $publicHostnameLength > 253 || !($publicHostname ~ "^[a-z0-9][a-z0-9.-]*[a-z0-9]\$") || [:typeof [:find $publicHostname $publicHostnameDoubleDot]] != "nil" || [:typeof [:find $publicHostname ".-"]] != "nil" || [:typeof [:find $publicHostname "-."]] != "nil" || !($publicHostname ~ "\\.home\\.arpa\$")) do={
  :set hostnameLabelsValid false
}
:local hostnameLabelCursor 0
:while ($hostnameLabelCursor < $publicHostnameLength) do={
  :local hostnameLabelEnd [:find $publicHostname "." $hostnameLabelCursor]
  :if ([:typeof $hostnameLabelEnd] = "nil") do={ :set hostnameLabelEnd $publicHostnameLength }
  :local hostnameLabel [:pick $publicHostname $hostnameLabelCursor $hostnameLabelEnd]
  :local hostnameLabelLength [:len $hostnameLabel]
  :if ($hostnameLabelLength < 1 || $hostnameLabelLength > 63) do={
    :set hostnameLabelsValid false
  } else={
    :if (!($hostnameLabel ~ "^[a-z0-9-]+\$") || [:pick $hostnameLabel 0 1] = "-" || [:pick $hostnameLabel ($hostnameLabelLength - 1) $hostnameLabelLength] = "-") do={
      :set hostnameLabelsValid false
    }
  }
  :set hostnameLabelCursor ($hostnameLabelEnd + 1)
}
:if ($hostnameLabelsValid = false) do={
  :put "ERROR public hostname must be a lowercase, valid name below home.arpa"
  :set failed true
}
:if ([:len $subscriptionPrivateCIDRs] > 0) do={
  :if ([:pick $subscriptionPrivateCIDRs 0 1] = "," || [:pick $subscriptionPrivateCIDRs ([:len $subscriptionPrivateCIDRs] - 1) [:len $subscriptionPrivateCIDRs]] = "," || [:typeof [:find $subscriptionPrivateCIDRs ",,"]] != "nil") do={ :put "ERROR subscription private CIDRs contain an empty entry"; :set failed true }
  :local privateCIDRCursor 0
  :local privateCIDRCount 0
  :local privateCIDRSeen ","
  :local private10Address [:toip "10.0.0.0"]
  :local private10Netmask [:toip "255.0.0.0"]
  :local private172Address [:toip "172.16.0.0"]
  :local private172Netmask [:toip "255.240.0.0"]
  :local private192Address [:toip "192.168.0.0"]
  :local private192Netmask [:toip "255.255.0.0"]
  :local privateULA [:toip6 "fc00::/7"]
  :while ($privateCIDRCursor < [:len $subscriptionPrivateCIDRs]) do={
    :local privateCIDREnd [:find $subscriptionPrivateCIDRs "," $privateCIDRCursor]
    :if ([:typeof $privateCIDREnd] = "nil") do={ :set privateCIDREnd [:len $subscriptionPrivateCIDRs] }
    :local privateCIDR [:pick $subscriptionPrivateCIDRs $privateCIDRCursor $privateCIDREnd]
    :set privateCIDRCursor ($privateCIDREnd + 1)
    :set privateCIDRCount ($privateCIDRCount + 1)
    :local privateCIDRSlash [:find $privateCIDR "/"]
    :local privateCIDRIsIPv6 ([:typeof [:find $privateCIDR ":"]] != "nil")
    :local privateCIDRPrefixValue
    :if ($privateCIDRIsIPv6) do={ :set privateCIDRPrefixValue [:toip6 $privateCIDR] }
    :local privateCIDRValid true
    :if ($privateCIDRCount > 32 || [:typeof [:find $privateCIDRSeen ("," . $privateCIDR . ",")]] != "nil") do={ :set privateCIDRValid false }
    :if ([:typeof $privateCIDRSlash] = "nil" || ($privateCIDRIsIPv6 && [:typeof $privateCIDRPrefixValue] != "ip6-prefix")) do={ :set privateCIDRValid false }
    :if ($privateCIDRValid) do={
      :local privateCIDRAddress
      :if ($privateCIDRIsIPv6) do={ :set privateCIDRAddress [:toip6 [:pick $privateCIDR 0 $privateCIDRSlash]] } else={ :set privateCIDRAddress [:toip [:pick $privateCIDR 0 $privateCIDRSlash]] }
      :local privateCIDRBits [:tonum [:pick $privateCIDR ($privateCIDRSlash + 1) [:len $privateCIDR]]]
      :if (($privateCIDRIsIPv6 && [:typeof $privateCIDRAddress] != "ip6") || (!$privateCIDRIsIPv6 && [:typeof $privateCIDRAddress] != "ip")) do={ :set privateCIDRValid false }
      :if ($privateCIDR != ($privateCIDRAddress . "/" . $privateCIDRBits)) do={ :set privateCIDRValid false }
      :if ($privateCIDRValid && $privateCIDRIsIPv6 = false) do={
        :local privateCIDRNetmask
        :foreach maskDefinition in=$ipv4MaskDefinitions do={
          :local separator [:find $maskDefinition "|"]
          :if ([:tonum [:pick $maskDefinition 0 $separator]] = $privateCIDRBits) do={ :set privateCIDRNetmask [:toip [:pick $maskDefinition ($separator + 1) [:len $maskDefinition]]] }
        }
        :if ([:typeof $privateCIDRNetmask] != "ip" || (($privateCIDRAddress & $privateCIDRNetmask) != $privateCIDRAddress)) do={ :set privateCIDRValid false }
      }
      :local privateCIDRAllowed false
      :if ($privateCIDRValid && $privateCIDRIsIPv6 = false) do={
        :if ($privateCIDRBits >= 8 && (($privateCIDRAddress & $private10Netmask) = $private10Address)) do={ :set privateCIDRAllowed true }
        :if ($privateCIDRBits >= 12 && (($privateCIDRAddress & $private172Netmask) = $private172Address)) do={ :set privateCIDRAllowed true }
        :if ($privateCIDRBits >= 16 && (($privateCIDRAddress & $private192Netmask) = $private192Address)) do={ :set privateCIDRAllowed true }
      }
      :if ($privateCIDRValid && $privateCIDRIsIPv6 && ($privateCIDRAddress in $privateULA) && $privateCIDRBits >= 7) do={ :set privateCIDRAllowed true }
      :if ($privateCIDRAllowed = false) do={ :set privateCIDRValid false }
    }
    :if ($privateCIDRValid = false) do={ :put ("ERROR invalid, duplicate, non-canonical, or non-private subscription CIDR: " . $privateCIDR); :set failed true }
    :set privateCIDRSeen ($privateCIDRSeen . $privateCIDR . ",")
  }
}

:put "=== FoxOS read-only preflight ==="
:local version [/system/resource get version]
:local architecture [/system/resource get architecture-name]
:local imageArchitecture ""
:if ($architecture = "x86" || $architecture = "x86_64") do={ :set imageArchitecture "amd64" }
:local versionBase $version
:local versionSpace [:find $versionBase " "]
:if ([:typeof $versionSpace] != "nil") do={ :set versionBase [:pick $versionBase 0 $versionSpace] }
:put ("RouterOS version: " . $version)
:put ("architecture-name: " . $architecture . " normalized-image-architecture=" . $imageArchitecture)

:if ($imageArchitecture != "amd64") do={
  :put ("ERROR this bundle requires RouterOS x86 or x86_64 for Linux amd64 images, got " . $architecture)
  :set failed true
}
:if ($architecture = "x86_64") do={
  :put "WARNING architecture-name=x86_64 is a non-standard RouterOS environment; compatibility is target-specific and still requires acceptance"
}
:local firstVersionDot [:find $versionBase "."]
:if ([:typeof $firstVersionDot] = "nil") do={
  :put ("ERROR cannot parse RouterOS version: " . $version)
  :set failed true
} else={
  :local versionMajor [:tonum [:pick $versionBase 0 $firstVersionDot]]
  :local versionTail [:pick $versionBase ($firstVersionDot + 1) [:len $versionBase]]
  :local versionMinorEnd [:find ($versionTail . ".") "."]
  :local versionMinor [:tonum [:pick $versionTail 0 $versionMinorEnd]]
  :if ($versionMajor < 7 || ($versionMajor = 7 && $versionMinor < 21)) do={
    :put "ERROR RouterOS 7.21 or newer is required for envlists/mountlists"
    :set failed true
  }
}

:local containerPackage [/system/package find where name="container"]
:if ([:len $containerPackage] != 1) do={
  :put "ERROR container package is not installed exactly once"
  :set failed true
} else={
  :local packageVersion [/system/package get $containerPackage version]
  :local packageDisabled [/system/package get $containerPackage disabled]
  :put ("container package: " . $packageVersion . " disabled=" . $packageDisabled)
  :if ($packageDisabled = true) do={
    :put "ERROR container package is disabled"
    :set failed true
  }
  :if ($packageVersion != $versionBase) do={
    :put "ERROR container package version must exactly match RouterOS version"
    :set failed true
  }
}

:local containerDeviceMode [/system/device-mode get container]
:local schedulerDeviceMode [/system/device-mode get scheduler]
:put ("device-mode container: " . $containerDeviceMode)
:put ("device-mode scheduler: " . $schedulerDeviceMode)
:if ($containerDeviceMode != true && $containerDeviceMode != "yes") do={
  :put "ERROR device-mode container=yes is required"
  :set failed true
}
:if ($schedulerDeviceMode != true && $schedulerDeviceMode != "yes") do={
  :put "ERROR device-mode scheduler=yes is required for the owned ordered cold-start coordinator"
  :set failed true
}

:local bridge [/interface/bridge find where name=$managementBridge]
:put ("management bridge " . $managementBridge . ": " . [:len $bridge])
:if ([:len $bridge] != 1) do={
	  :put ("ERROR management bridge is missing or ambiguous: " . $managementBridge)
  :set failed true
}

:local storageFree 0
:if ($storageMode = "internal") do={
  :set storageFree [/system/resource get free-hdd-space]
  :put ("storage mode=internal root=" . $storageRoot . " free bytes: " . $storageFree)
} else={
  :local diskID [/disk find where slot=$storageRoot]
  :if ([:len $diskID] != 1) do={
    :put ("ERROR mounted storage slot is missing or ambiguous: " . $storageRoot)
    :set failed true
  } else={
    :set storageFree [/disk get $diskID free]
    :put ("storage mode=disk slot=" . $storageRoot . " free bytes: " . $storageFree)
  }
}
:if ($storageFree < $minimumFreeBytes) do={
  :put ("ERROR " . $storageRoot . " free space is below 512 MiB after upload")
  :set failed true
}

:local routerIP [/ip/address find where address~($routerAddress . "/")]
:put ("RouterOS management address " . $routerAddress . ": " . [:len $routerIP])
:if ([:len $routerIP] != 1) do={
	  :put ("ERROR RouterOS management address is missing or ambiguous: " . $routerCIDR)
  :set failed true
} else={
	  :if ([/ip/address get $routerIP address] != $routerCIDR || [/ip/address get $routerIP interface] != $managementBridge) do={
	    :put ("ERROR RouterOS management address must be exactly " . $routerCIDR . " on " . $managementBridge)
    :set failed true
  }
}

:local wwwService [/ip/service find where name="www" && dynamic=no]
:if ([:len $wwwService] != 1) do={
  :put "ERROR RouterOS www/REST service is missing or ambiguous"
  :set failed true
} else={
  :local wwwDisabled [/ip/service get $wwwService disabled]
  :local wwwPort [/ip/service get $wwwService port]
  :local wwwAddresses [/ip/service get $wwwService address]
  :put ("REST service www: disabled=" . $wwwDisabled . " port=" . $wwwPort . " address=" . $wwwAddresses)
  :if ($wwwDisabled = true || $wwwPort != 80) do={
    :put "ERROR FoxOS full bundle expects enabled RouterOS www/REST on port 80"
    :set failed true
  }
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
	  :if ($wwwAllowed = false || $wwwCount < 1) do={
	    :put ("ERROR www/REST address list may contain only exact entries " . $siteNetwork . " and/or " . $foxosRESTAddress)
	    :set failed true
	  }
}

:put "=== reserved management addresses ==="
:local reservedDefinitions {($mihomoAddress . "|" . $mihomoAddress . "/" . $prefixLength . "|veth-mihomo|foxos:mihomo");($mosdnsAddress . "|" . $mosdnsAddress . "/" . $prefixLength . "|veth-mosdns|foxos:mosdns");($foxosAddress . "|" . $foxosAddress . "/" . $prefixLength . "|veth-foxos|foxos:admin")}
:foreach definition in=$reservedDefinitions do={
  :local p1 [:find $definition "|"]
  :local p2 [:find $definition "|" ($p1 + 1)]
  :local p3 [:find $definition "|" ($p2 + 1)]
  :local probeAddress [:pick $definition 0 $p1]
  :local probeCIDR [:pick $definition ($p1 + 1) $p2]
  :local vethName [:pick $definition ($p2 + 1) $p3]
  :local owner [:pick $definition ($p3 + 1) [:len $definition]]
  :local configuredIP [/ip/address find where address~($probeAddress . "/")]
  :local lease [/ip/dhcp-server/lease find where address=$probeAddress]
  :local arp [/ip/arp find where address=$probeAddress && status!="failed"]
  :local replies [/ping address=$probeAddress count=2 interval=200ms]
  :local expectedVeth [/interface/veth find where name=$vethName]
  :local addressVeth [/interface/veth find where address=$probeCIDR]
  :local owned false

  :if ([:len $expectedVeth] = 1) do={
    :if ([/interface/veth get $expectedVeth comment] = $owner && [/interface/veth get $expectedVeth address] = $probeCIDR) do={
      :set owned true
    } else={
      :put ("ERROR existing " . $vethName . " does not match the FoxOS ownership/address contract")
      :set failed true
    }
  }
  :if ([:len $expectedVeth] > 1) do={
    :put ("ERROR veth name is ambiguous: " . $vethName)
    :set failed true
  }
  :if ([:len $addressVeth] > 0 && $owned = false) do={
    :put ("ERROR management address is used by a non-FoxOS veth: " . $probeCIDR)
    :set failed true
  }
  :if ([:len $configuredIP] > 0 || [:len $lease] > 0) do={
    :put ("ERROR reserved address is configured as a RouterOS IP or DHCP lease: " . $probeAddress)
    :set failed true
  }
  :if (([:len $arp] > 0 || $replies > 0) && $owned = false) do={
    :put ("ERROR reserved address responds or appears in ARP without an owned veth: " . $probeAddress)
    :set failed true
  }
  :put ($probeAddress . " occupancy: ip=" . [:len $configuredIP] . " lease=" . [:len $lease] . " arp=" . [:len $arp] . " ping=" . $replies . " owned=" . $owned)
}

:put ("=== required files under " . $storageRoot . "/ ===")
:local requiredFiles {($upgradeDirectory . "/foxos-amd64.tar|1048576");($upgradeDirectory . "/upgrade-inspect.rsc|1000");($upgradeDirectory . "/upgrade-plan.rsc|100");($upgradeDirectory . "/upgrade.rsc|100");($upgradeDirectory . "/upgrade-promote-inspect.rsc|1000");($upgradeDirectory . "/upgrade-promote-plan.rsc|100");($upgradeDirectory . "/upgrade-promote.rsc|100");($upgradeDirectory . "/rollback-inspect.rsc|1000");($upgradeDirectory . "/rollback-plan.rsc|100");($upgradeDirectory . "/rollback.rsc|100");($upgradeDirectory . "/upgrade-cleanup-inspect.rsc|1000");($upgradeDirectory . "/upgrade-cleanup-plan.rsc|100");($upgradeDirectory . "/upgrade-cleanup-apply.rsc|100");($upgradeDirectory . "/UPGRADE-MANIFEST.txt|100");($upgradeDirectory . "/SHA256SUMS|100");"mihomo_amd64.tar|20971520";"mosdns-amd64.tar|5242880";"provenance/mihomo-container.lock.json|100";"provenance/mosdns-container.lock.json|100";"site-config.example.rsc|100";"site-config.rsc|100";"site-config.rsc.sha512|128";"seal-site-config.sh|100";"load-site-config.rsc|1000";"preflight.rsc|100";"foxos-install-inspect.rsc|1000";"foxos-plan.rsc|100";"foxos-full-install.rsc|1000";"foxos-start-all.rsc|100";"foxos-verify.rsc|100";"foxos-dns-plan.rsc|100";"foxos-dns-apply.rsc|100";"foxos-uninstall-inspect.rsc|1000";"uninstall-plan.rsc|100";"uninstall-apply.rsc|100";"SHA256SUMS|100";"RELEASE-MANIFEST.txt|100";"QUICK-INSTALL.md|100"}
:foreach definition in=$requiredFiles do={
  :local separator [:find $definition "|"]
  :local fileName [:pick $definition 0 $separator]
  :local minimumSize [:tonum [:pick $definition ($separator + 1) [:len $definition]]]
  :local filePath ($storageRoot . "/" . $fileName)
  :local fileID [/file find where name=$filePath]
  :if ([:len $fileID] != 1) do={
    :put ("ERROR missing or ambiguous file: " . $filePath)
    :set failed true
  } else={
    :local fileSize [/file get $fileID size]
    :put ($filePath . " size=" . $fileSize)
    :if ($fileSize < $minimumSize) do={
      :put ("ERROR file is unexpectedly small or truncated: " . $filePath)
      :set failed true
    }
  }
}
:foreach configFile in={"mihomo-config/config.yaml";"mihomo-config/base.yaml";"mosdns-config/config_custom.yaml"} do={
  :local configPath ($storageRoot . "/" . $configFile)
  :local configID [/file find where name=$configPath]
  :put ("config " . $configPath . ": " . [:len $configID])
  :if ([:len $configID] != 1) do={
    :put ("ERROR missing config entry: " . $configPath)
    :set failed true
  } else={
    :if ([/file get $configID size] < 10) do={
      :put ("ERROR empty or truncated config: " . $configPath)
      :set failed true
    }
  }
}

:put "NOTE SHA256SUMS must be verified on the workstation before upload; RouterOS preflight verifies presence and minimum sizes."
:put "=== existing FoxOS ownership markers ==="
:put ("FoxOS containers: " . [:len [/container find where comment~"^foxos:"]])
:put ("FoxOS veths: " . [:len [/interface/veth find where comment~"^foxos:"]])
:put ("FoxOS mounts: " . [:len [/container/mounts find where list~"^foxos-"]])

:if ($failed) do={
  :error "FoxOS preflight failed. No RouterOS configuration was changed. Fix every ERROR and run it again."
}
:put ("PRECHECK PASSED: sealed manifest, topology, files, and prerequisites are valid. No RouterOS resource was changed. Review " . $storageRoot . "/foxos-plan.rsc before installation.")
