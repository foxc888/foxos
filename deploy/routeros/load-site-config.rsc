# Immutable FoxOS site manifest loader. This script validates the independently
# sealed editable manifest as data and assigns only the 11 allowed globals. It
# never imports or otherwise executes site-config.rsc.

:global FoxOSSiteLoadedDigest
:global FoxOSSiteLoadedConfigPath
:global FoxOSSiteLoaderVersion
:set FoxOSSiteLoadedDigest ""
:set FoxOSSiteLoadedConfigPath ""
:set FoxOSSiteLoaderVersion 0

:local configFiles [/file find where name~"(^|/)site-config.rsc\$"]
:if ([:len $configFiles] != 1) do={ :error "必须且只能上传一份 site-config.rsc" }
:local configFile $configFiles
:local configPath [/file get $configFile name]
:local configSize [/file get $configFile size]
:if ($configSize < 100 || $configSize > 8192) do={ :error "site-config.rsc 大小超出 100..8192 字节安全边界" }
:local configContents [/file get $configFile contents]
:if ([:len $configContents] != $configSize) do={ :error "无法完整读取 site-config.rsc" }

:local digestFiles [/file find where name=($configPath . ".sha512")]
:if ([:len $digestFiles] != 1) do={ :error "site-config.rsc.sha512 缺失或不唯一" }
:local digestContents [/file get $digestFiles contents]
:if ([:len $digestContents] != 129 || [:pick $digestContents 128 129] != "\n") do={ :error "站点清单摘要必须是 128 位小写 SHA-512 加换行" }
:local expectedDigest [:pick $digestContents 0 128]
:if (!($expectedDigest ~ "^[0-9a-f]+\$")) do={ :error "站点清单摘要包含非十六进制字符" }
:local actualDigest [:convert $configContents transform=sha512 to=hex]
:if ($actualDigest != $expectedDigest) do={ :error "site-config.rsc 与独立 SHA-512 不一致" }
:if ([:typeof [:find $configContents "\r"]] != "nil" || [:typeof [:find $configContents ";"]] != "nil" || [:typeof [:find $configContents "\\"]] != "nil") do={
  :error "site-config.rsc 包含禁止的换行、分号或转义字符"
}

:local manifestVersionCount 0
:local managementBridgeCount 0
:local storageRootCount 0
:local networkCount 0
:local prefixLengthCount 0
:local routerAddressCount 0
:local mihomoAddressCount 0
:local mosdnsAddressCount 0
:local foxosAddressCount 0
:local publicHostnameCount 0
:local privateCIDRsCount 0
:local manifestVersion
:local managementBridge
:local storageRoot
:local siteNetwork
:local prefixLength
:local routerAddress
:local mihomoAddress
:local mosdnsAddress
:local foxosAddress
:local publicHostname
:local privateCIDRs
:local cursor 0
:local contentLength [:len $configContents]

:while ($cursor < $contentLength) do={
  # RouterOS :find excludes the byte at its start offset. Skip an LF at the
  # cursor so a blank line cannot become a prefix of the following line.
  :if ([:pick $configContents $cursor ($cursor + 1)] = "\n") do={
    :set cursor ($cursor + 1)
    :continue
  }
  :local lineEnd [:find $configContents "\n" $cursor]
  :if ([:typeof $lineEnd] = "nil") do={ :set lineEnd $contentLength }
  :local line [:pick $configContents $cursor $lineEnd]
  :set cursor ($lineEnd + 1)
  :if ([:len $line] = 0) do={ :continue }
  :if ([:pick $line 0 1] = "#") do={ :continue }
  :local matched false
  :if ($line = ":global FoxOSSiteManifestVersion 2") do={
    :set manifestVersionCount ($manifestVersionCount + 1)
    :set manifestVersion 2
    :set matched true
  }
  :if ($line ~ "^:global FoxOSSiteManagementBridge \"[A-Za-z0-9][A-Za-z0-9._-]*\"\$") do={
    :set managementBridgeCount ($managementBridgeCount + 1)
    :set managementBridge [:pick $line [:len ":global FoxOSSiteManagementBridge \""] ([:len $line] - 1)]
    :set matched true
  }
  :if ($line ~ "^:global FoxOSSiteStorageRoot \"[A-Za-z0-9][A-Za-z0-9._-]*\"\$") do={
    :set storageRootCount ($storageRootCount + 1)
    :set storageRoot [:pick $line [:len ":global FoxOSSiteStorageRoot \""] ([:len $line] - 1)]
    :set matched true
  }
  :if ($line ~ "^:global FoxOSSiteNetwork \"[0-9.]+/[0-9]+\"\$") do={
    :set networkCount ($networkCount + 1)
    :set siteNetwork [:pick $line [:len ":global FoxOSSiteNetwork \""] ([:len $line] - 1)]
    :set matched true
  }
  :if ($line ~ "^:global FoxOSSitePrefixLength [0-9]+\$") do={
    :set prefixLengthCount ($prefixLengthCount + 1)
    :set prefixLength [:tonum [:pick $line [:len ":global FoxOSSitePrefixLength "] [:len $line]]]
    :set matched true
  }
  :if ($line ~ "^:global FoxOSSiteRouterAddress \"[0-9.]+\"\$") do={
    :set routerAddressCount ($routerAddressCount + 1)
    :set routerAddress [:pick $line [:len ":global FoxOSSiteRouterAddress \""] ([:len $line] - 1)]
    :set matched true
  }
  :if ($line ~ "^:global FoxOSSiteMihomoAddress \"[0-9.]+\"\$") do={
    :set mihomoAddressCount ($mihomoAddressCount + 1)
    :set mihomoAddress [:pick $line [:len ":global FoxOSSiteMihomoAddress \""] ([:len $line] - 1)]
    :set matched true
  }
  :if ($line ~ "^:global FoxOSSiteMosDNSAddress \"[0-9.]+\"\$") do={
    :set mosdnsAddressCount ($mosdnsAddressCount + 1)
    :set mosdnsAddress [:pick $line [:len ":global FoxOSSiteMosDNSAddress \""] ([:len $line] - 1)]
    :set matched true
  }
  :if ($line ~ "^:global FoxOSSiteFoxOSAddress \"[0-9.]+\"\$") do={
    :set foxosAddressCount ($foxosAddressCount + 1)
    :set foxosAddress [:pick $line [:len ":global FoxOSSiteFoxOSAddress \""] ([:len $line] - 1)]
    :set matched true
  }
  :if ($line ~ "^:global FoxOSSitePublicHostname \"[a-z0-9][a-z0-9.-]*[a-z0-9]\"\$") do={
    :set publicHostnameCount ($publicHostnameCount + 1)
    :set publicHostname [:pick $line [:len ":global FoxOSSitePublicHostname \""] ([:len $line] - 1)]
    :set matched true
  }
  :if ($line ~ "^:global FoxOSSiteSubscriptionPrivateCIDRs \"[0-9A-Fa-f:.,/]*\"\$") do={
    :set privateCIDRsCount ($privateCIDRsCount + 1)
    :set privateCIDRs [:pick $line [:len ":global FoxOSSiteSubscriptionPrivateCIDRs \""] ([:len $line] - 1)]
    :set matched true
  }
  :if ($matched = false) do={ :error ("site-config.rsc 包含非白名单行: " . $line) }
}

:foreach count in={$manifestVersionCount;$managementBridgeCount;$storageRootCount;$networkCount;$prefixLengthCount;$routerAddressCount;$mihomoAddressCount;$mosdnsAddressCount;$foxosAddressCount;$publicHostnameCount;$privateCIDRsCount} do={
  :if ($count != 1) do={ :error "site-config.rsc 必须且只能包含 11 个指定赋值各一次" }
}
:local publicHostnameLength [:len $publicHostname]
:local publicHostnameDoubleDot ("." . ".")
:if ($publicHostnameLength < 3 || $publicHostnameLength > 253 || !($publicHostname ~ "^[a-z0-9][a-z0-9.-]*[a-z0-9]\$") || [:typeof [:find $publicHostname $publicHostnameDoubleDot]] != "nil" || [:typeof [:find $publicHostname ".-"]] != "nil" || [:typeof [:find $publicHostname "-."]] != "nil" || !($publicHostname ~ "\\.home\\.arpa\$")) do={
  :error "管理主机名必须是 home.arpa 下总长不超过 253 字节的小写 ASCII 名称"
}
:local hostnameLabelCursor 0
:while ($hostnameLabelCursor < $publicHostnameLength) do={
  :local hostnameLabelEnd [:find $publicHostname "." $hostnameLabelCursor]
  :if ([:typeof $hostnameLabelEnd] = "nil") do={ :set hostnameLabelEnd $publicHostnameLength }
  :local hostnameLabel [:pick $publicHostname $hostnameLabelCursor $hostnameLabelEnd]
  :local hostnameLabelLength [:len $hostnameLabel]
  :if ($hostnameLabelLength < 1 || $hostnameLabelLength > 63) do={
    :error "管理主机名的每个 label 必须包含 1..63 个 ASCII 字节"
  }
  :if (!($hostnameLabel ~ "^[a-z0-9-]+\$") || [:pick $hostnameLabel 0 1] = "-" || [:pick $hostnameLabel ($hostnameLabelLength - 1) $hostnameLabelLength] = "-") do={
    :error "管理主机名 label 只能包含小写 ASCII 字母、数字和内部连字符"
  }
  :set hostnameLabelCursor ($hostnameLabelEnd + 1)
}
:if ([:len $privateCIDRs] > 0) do={
  :if ([:pick $privateCIDRs 0 1] = "," || [:pick $privateCIDRs ([:len $privateCIDRs] - 1) [:len $privateCIDRs]] = "," || [:typeof [:find $privateCIDRs ",,"]] != "nil") do={
    :error "订阅私网 allowlist 包含空项"
  }
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
  :local ipv4MaskDefinitions {"8|255.0.0.0";"9|255.128.0.0";"10|255.192.0.0";"11|255.224.0.0";"12|255.240.0.0";"13|255.248.0.0";"14|255.252.0.0";"15|255.254.0.0";"16|255.255.0.0";"17|255.255.128.0";"18|255.255.192.0";"19|255.255.224.0";"20|255.255.240.0";"21|255.255.248.0";"22|255.255.252.0";"23|255.255.254.0";"24|255.255.255.0";"25|255.255.255.128";"26|255.255.255.192";"27|255.255.255.224";"28|255.255.255.240";"29|255.255.255.248";"30|255.255.255.252";"31|255.255.255.254";"32|255.255.255.255"}
  :while ($privateCIDRCursor < [:len $privateCIDRs]) do={
    :local privateCIDREnd [:find $privateCIDRs "," $privateCIDRCursor]
    :if ([:typeof $privateCIDREnd] = "nil") do={ :set privateCIDREnd [:len $privateCIDRs] }
    :local privateCIDR [:pick $privateCIDRs $privateCIDRCursor $privateCIDREnd]
    :set privateCIDRCursor ($privateCIDREnd + 1)
    :set privateCIDRCount ($privateCIDRCount + 1)
    :if ($privateCIDRCount > 32 || [:typeof [:find $privateCIDRSeen ("," . $privateCIDR . ",")]] != "nil") do={
      :error "订阅私网 allowlist 超过 32 项或包含重复项"
    }
    :local privateCIDRSlash [:find $privateCIDR "/"]
    :local privateCIDRIsIPv6 ([:typeof [:find $privateCIDR ":"]] != "nil")
    :local privateCIDRPrefixValue
    :if ($privateCIDRIsIPv6) do={ :set privateCIDRPrefixValue [:toip6 $privateCIDR] }
    :if ([:typeof $privateCIDRSlash] = "nil" || ($privateCIDRIsIPv6 && [:typeof $privateCIDRPrefixValue] != "ip6-prefix")) do={
      :error ("订阅私网 allowlist 包含无效 CIDR: " . $privateCIDR)
    }
    :local privateCIDRAddress
    :if ($privateCIDRIsIPv6) do={ :set privateCIDRAddress [:toip6 [:pick $privateCIDR 0 $privateCIDRSlash]] } else={ :set privateCIDRAddress [:toip [:pick $privateCIDR 0 $privateCIDRSlash]] }
    :local privateCIDRBits [:tonum [:pick $privateCIDR ($privateCIDRSlash + 1) [:len $privateCIDR]]]
    :if (($privateCIDRIsIPv6 && [:typeof $privateCIDRAddress] != "ip6") || (!$privateCIDRIsIPv6 && [:typeof $privateCIDRAddress] != "ip")) do={
      :error ("订阅私网 allowlist 包含无效网络地址: " . $privateCIDR)
    }
    :if ($privateCIDR != ($privateCIDRAddress . "/" . $privateCIDRBits)) do={
      :error ("订阅私网 allowlist 必须使用 canonical network address: " . $privateCIDR)
    }
    :if ($privateCIDRIsIPv6 = false) do={
      :local privateCIDRNetmask
      :foreach maskDefinition in=$ipv4MaskDefinitions do={
        :local separator [:find $maskDefinition "|"]
        :if ([:tonum [:pick $maskDefinition 0 $separator]] = $privateCIDRBits) do={ :set privateCIDRNetmask [:toip [:pick $maskDefinition ($separator + 1) [:len $maskDefinition]]] }
      }
      :if ([:typeof $privateCIDRNetmask] != "ip" || (($privateCIDRAddress & $privateCIDRNetmask) != $privateCIDRAddress)) do={
        :error ("订阅私网 allowlist 必须使用有效的 canonical IPv4 network address: " . $privateCIDR)
      }
    }
    :local privateCIDRAllowed false
    :if ($privateCIDRIsIPv6 = false) do={
      :if ($privateCIDRBits >= 8 && (($privateCIDRAddress & $private10Netmask) = $private10Address)) do={ :set privateCIDRAllowed true }
      :if ($privateCIDRBits >= 12 && (($privateCIDRAddress & $private172Netmask) = $private172Address)) do={ :set privateCIDRAllowed true }
      :if ($privateCIDRBits >= 16 && (($privateCIDRAddress & $private192Netmask) = $private192Address)) do={ :set privateCIDRAllowed true }
    }
    :if ($privateCIDRIsIPv6 && ($privateCIDRAddress in $privateULA) && $privateCIDRBits >= 7) do={ :set privateCIDRAllowed true }
    :if ($privateCIDRAllowed = false) do={ :error ("订阅私网 allowlist 只接受 RFC1918 或 IPv6 ULA 前缀: " . $privateCIDR) }
    :set privateCIDRSeen ($privateCIDRSeen . $privateCIDR . ",")
  }
}
:local configSuffix "/site-config.rsc"
:local suffixPosition [:find $configPath $configSuffix]
:if ([:typeof $suffixPosition] = "nil" || [:pick $configPath 0 $suffixPosition] != $storageRoot) do={
  :error "site-config.rsc 中的存储根必须与实际上传目录一致"
}

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
:set FoxOSSiteManifestVersion $manifestVersion
:set FoxOSSiteManagementBridge $managementBridge
:set FoxOSSiteStorageRoot $storageRoot
:set FoxOSSiteNetwork $siteNetwork
:set FoxOSSitePrefixLength $prefixLength
:set FoxOSSiteRouterAddress $routerAddress
:set FoxOSSiteMihomoAddress $mihomoAddress
:set FoxOSSiteMosDNSAddress $mosdnsAddress
:set FoxOSSiteFoxOSAddress $foxosAddress
:set FoxOSSitePublicHostname $publicHostname
:set FoxOSSiteSubscriptionPrivateCIDRs $privateCIDRs
:set FoxOSSiteLoadedDigest $actualDigest
:set FoxOSSiteLoadedConfigPath $configPath

# RouterOS 7.23.2 exposes container identity as the native find handle, state
# as dynamic boolean flags, and may prefix root-dir readback with one slash.
# Keep that compatibility contract in one loader-owned runtime primitive.
:global FoxOSContainerCompatVersion 1
:global FoxOSContainerState do={
  :local container $1
  :local running [/container get $container running]
  :local stopped [/container get $container stopped]
  :local isRunning ($running = true || $running = "yes")
  :local isStopped ($stopped = true || $stopped = "yes")
  :if ($isRunning && $isStopped) do={ :return "invalid" }
  :if ($isRunning) do={ :return "running" }
  :if ($isStopped) do={ :return "stopped" }
  :return "transitional"
}
:global FoxOSContainerRoot do={
  :local container $1
  :local rootDirectory [/container get $container root-dir]
  :if ([:typeof $rootDirectory] != "str") do={ :return "" }
  :if ([:len $rootDirectory] > 0 && [:pick $rootDirectory 0 1] = "/") do={
    :return [:pick $rootDirectory 1 [:len $rootDirectory]]
  }
  :return $rootDirectory
}
# RouterOS 7.23.2 exposes named mount access through mode instead of the
# legacy read-only property. Keep consumers bound to one normalized getter.
:global FoxOSMountCompatVersion 1
:global FoxOSWritableMountMode "rw"
:global FoxOSMountMode do={
  :local mount $1
  :local mountMode [/container/mounts get $mount mode]
  :if ([:typeof $mountMode] != "str") do={ :return "invalid" }
  :if ($mountMode = "ro" || $mountMode = "ro,noexec" || $mountMode = "rw" || $mountMode = "rw,noexec") do={ :return $mountMode }
  :return "invalid"
}
:set FoxOSSiteLoaderVersion 1
:put ("SITE CONFIG LOADED: SHA-512=" . $FoxOSSiteLoadedDigest . " storage=" . $FoxOSSiteStorageRoot)
