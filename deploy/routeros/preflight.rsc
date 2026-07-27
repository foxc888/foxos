# FoxOS RouterOS full-bundle read-only preflight
# 本脚本只读取配置、文件和网络状态；不会创建、修改或删除 RouterOS 资源。
# RouterOS 的 x86_64 CPU 在 architecture-name 中返回 x86。

:local requiredArchitecture "x86"
:local managementBridge "bridge-lan"
:local storageRoot "disk1"
:local minimumFreeBytes 536870912
:local routerAddress "10.0.0.1"
:local failed false

:put "=== FoxOS read-only preflight ==="
:local version [/system/resource get version]
:local architecture [/system/resource get architecture-name]
:local versionBase $version
:local versionSpace [:find $versionBase " "]
:if ([:typeof $versionSpace] != "nil") do={ :set versionBase [:pick $versionBase 0 $versionSpace] }
:put ("RouterOS version: " . $version)
:put ("architecture-name: " . $architecture . " (expected x86 for an x86_64 CPU)")

:if ($architecture != $requiredArchitecture) do={
  :put ("ERROR architecture mismatch: expected x86, got " . $architecture)
  :set failed true
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

:local deviceMode [/system/device-mode get container]
:put ("device-mode container: " . $deviceMode)
:if ($deviceMode != true && $deviceMode != "yes") do={
  :put "ERROR device-mode container=yes is required"
  :set failed true
}

:local bridge [/interface/bridge find where name=$managementBridge]
:put ("management bridge " . $managementBridge . ": " . [:len $bridge])
:if ([:len $bridge] != 1) do={
  :put "ERROR bridge-lan is missing or ambiguous"
  :set failed true
}

:local diskID [/disk find where slot=$storageRoot]
:if ([:len $diskID] != 1) do={
  :put ("ERROR mounted storage slot is missing or ambiguous: " . $storageRoot)
  :set failed true
} else={
  :local diskFree [/disk get $diskID free]
  :put ("storage " . $storageRoot . " free bytes: " . $diskFree)
  :if ($diskFree < $minimumFreeBytes) do={
    :put "ERROR disk1 free space is below 512 MiB after upload"
    :set failed true
  }
}

:local routerIP [/ip/address find where address~($routerAddress . "/")]
:put ("RouterOS management address " . $routerAddress . ": " . [:len $routerIP])
:if ([:len $routerIP] != 1) do={
  :put "ERROR RouterOS management address 10.0.0.1/24 is missing or ambiguous"
  :set failed true
} else={
  :if ([/ip/address get $routerIP address] != "10.0.0.1/24" || [/ip/address get $routerIP interface] != $managementBridge) do={
    :put "ERROR RouterOS management address must be exactly 10.0.0.1/24 on bridge-lan"
    :set failed true
  }
}

:local wwwService [/ip/service find where name="www"]
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
  :if ([:typeof [:find $wwwAddresses "10.0.0.0/24"]] = "nil" && [:typeof [:find $wwwAddresses "10.0.0.4/32"]] = "nil") do={
    :put "ERROR www/REST must be restricted to 10.0.0.0/24 or 10.0.0.4/32"
    :set failed true
  }
}

:put "=== reserved management addresses ==="
:local reservedDefinitions {"10.0.0.2|10.0.0.2/24|veth-mihomo|foxos:mihomo";"10.0.0.3|10.0.0.3/24|veth-mosdns|foxos:mosdns";"10.0.0.4|10.0.0.4/24|veth-foxos|foxos:admin"}
:foreach definition in=$reservedDefinitions do={
  :local p1 [:find $definition "|"]
  :local p2 [:find $definition "|" ($p1 + 1)]
  :local p3 [:find $definition "|" ($p2 + 1)]
  :local address [:pick $definition 0 $p1]
  :local cidr [:pick $definition ($p1 + 1) $p2]
  :local vethName [:pick $definition ($p2 + 1) $p3]
  :local owner [:pick $definition ($p3 + 1) [:len $definition]]
  :local configuredIP [/ip/address find where address~($address . "/")]
  :local lease [/ip/dhcp-server/lease find where address=$address]
  :local arp [/ip/arp find where address=$address]
  :local replies [/ping address=$address count=2 interval=200ms]
  :local expectedVeth [/interface/veth find where name=$vethName]
  :local addressVeth [/interface/veth find where address=$cidr]
  :local owned false

  :if ([:len $expectedVeth] = 1) do={
    :if ([/interface/veth get $expectedVeth comment] = $owner && [/interface/veth get $expectedVeth address] = $cidr) do={
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
    :put ("ERROR management address is used by a non-FoxOS veth: " . $cidr)
    :set failed true
  }
  :if ([:len $configuredIP] > 0 || [:len $lease] > 0) do={
    :put ("ERROR reserved address is configured as a RouterOS IP or DHCP lease: " . $address)
    :set failed true
  }
  :if (([:len $arp] > 0 || $replies > 0) && $owned = false) do={
    :put ("ERROR reserved address responds or appears in ARP without an owned veth: " . $address)
    :set failed true
  }
  :put ($address . " occupancy: ip=" . [:len $configuredIP] . " lease=" . [:len $lease] . " arp=" . [:len $arp] . " ping=" . $replies . " owned=" . $owned)
}

:put "=== required files under disk1/ ==="
:local requiredFiles {"foxos-amd64.tar|1048576";"mihomo_amd64.tar|52428800";"mosdns-amd64.tar|5242880";"preflight.rsc|100";"foxos-plan.rsc|100";"foxos-full-install.rsc|1000";"foxos-start-all.rsc|100";"SHA256SUMS|100";"RELEASE-MANIFEST.txt|100"}
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
:foreach configFile in={"mihomo-config/config.yaml";"mosdns-config/config_custom.yaml"} do={
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
:put ("FoxOS mounts: " . [:len [/container/mounts find where name~"^foxos-"]])

:if ($failed) do={
  :error "FoxOS preflight failed. No RouterOS configuration was changed. Fix every ERROR and run it again."
}
:put "PRECHECK PASSED: no RouterOS configuration was changed. Review disk1/foxos-plan.rsc before installation."
