# FoxOS 全栈安装脚本（RouterOS x86_64 / architecture-name=x86）
# 目标拓扑：RouterOS 10.0.0.1、Mihomo 10.0.0.2、MosDNS 10.0.0.3、FoxOS 10.0.0.4:8090。
# 只管理带 foxos: 所有权标识的资源，不修改 DNS、DHCP、默认路由、NAT、Mangle 或现有防火墙。
# 执行前必须先运行 preflight.rsc 和 foxos-plan.rsc，完成 RouterOS export/backup，并由操作者明确确认计划。
# 目标 RouterOS/container package 版本为 7.21+，x86_64 CPU 的 architecture-name 是 x86。

:local managementBridge "bridge-lan"
:local storageRoot "disk1"
:local routerAddress "10.0.0.1"
:local mihomoAddress "10.0.0.2/24"
:local mosdnsAddress "10.0.0.3/24"
:local foxosAddress "10.0.0.4/24"
:local architecture [/system/resource get architecture-name]
:local routerVersion [/system/resource get version]
:local routerVersionBase $routerVersion
:local routerVersionSpace [:find $routerVersionBase " "]
:if ([:typeof $routerVersionSpace] != "nil") do={ :set routerVersionBase [:pick $routerVersionBase 0 $routerVersionSpace] }

:put "=== FoxOS exact change plan (write phase) ==="
:put "Create or reuse only: foxos-rest, foxos-service, foxos-env, foxos-* mounts, foxos:* veth/bridge ports/containers."
:put "Create or reuse addresses: 10.0.0.2/24, 10.0.0.3/24, 10.0.0.4/24 on bridge-lan."
:put "No DNS, DHCP, default route, NAT, Mangle, or existing firewall changes."
:put "Rollback: stop FoxOS containers, restore RouterOS backup, and keep disk1/foxos-data plus old image tar files."

:if ($architecture != "x86") do={
  :error ("此 amd64 安装包仅支持 architecture-name=x86，当前为 " . $architecture)
}
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
:local diskID [/disk find where slot=$storageRoot]
:if ([:len $diskID] != 1) do={
  :error ("未找到持久化磁盘: " . $storageRoot)
}
:local diskFree [/disk get $diskID free]
:if ($diskFree < 536870912) do={
  :error ("disk1 可用空间不足 512 MiB: " . $diskFree)
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
:local deviceMode [/system/device-mode get container]
:if ($deviceMode != true && $deviceMode != "yes") do={
  :error "device-mode container=yes 未启用"
}
:local routerManagementIP [/ip/address find where address~"10.0.0.1/"]
:if ([:len $routerManagementIP] != 1) do={
  :error "未找到唯一 RouterOS 管理地址 10.0.0.1/24"
}
:if ([/ip/address get $routerManagementIP address] != "10.0.0.1/24" || [/ip/address get $routerManagementIP interface] != $managementBridge) do={
  :error "RouterOS 管理地址必须是 bridge-lan 上的 10.0.0.1/24"
}
:local wwwService [/ip/service find where name="www"]
:if ([:len $wwwService] != 1) do={ :error "未找到唯一 RouterOS www/REST 服务" }
:local wwwAddresses [/ip/service get $wwwService address]
:if ([/ip/service get $wwwService disabled] = true || [/ip/service get $wwwService port] != 80) do={
  :error "FoxOS 需要已启用且端口为 80 的 RouterOS www/REST 服务"
}
:if ([:typeof [:find $wwwAddresses "10.0.0.0/24"]] = "nil" && [:typeof [:find $wwwAddresses "10.0.0.4/32"]] = "nil") do={
  :error "www/REST 必须限制为 10.0.0.0/24 或 10.0.0.4/32"
}

:local requiredFiles {"mihomo_amd64.tar";"mosdns-amd64.tar";"foxos-amd64.tar";"preflight.rsc";"foxos-plan.rsc";"foxos-start-all.rsc";"SHA256SUMS"}
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
:local envMarker [/container/envs find where list="foxos-env" key="FOXOS_INSTALL_MARKER" value="foxos"]
:local existingInstall false
:local foxosRouterPassword ""
:local foxosMihomoSecret ""
:local foxosApiToken ""
:local foxosConfirmationKey ""
:if ([:len $envMarker] > 1) do={ :error "FOXOS_INSTALL_MARKER 不唯一，拒绝继续" }
:if ([:len $envMarker] = 0 && [:len [/user/group find where name="foxos-rest"]] > 0) do={
  :error "同名 foxos-rest 用户组已存在且没有 FoxOS 安装标记，拒绝在写入前继续"
}
:if ([:len $envMarker] = 1) do={
  :set existingInstall true
} else={
  :if ([:len [/container/envs find where list="foxos-env"]] > 0) do={
    :error "同名 foxos-env 已存在但没有 FOXOS_INSTALL_MARKER=foxos，拒绝覆盖"
  }
  /container/envs add list=foxos-env key=FOXOS_INSTALL_MARKER value="foxos"
}

:local routerPasswordID [/container/envs find where list="foxos-env" key="FOXOS_ROUTEROS_PASSWORD"]
:if ([:len $routerPasswordID] > 1) do={ :error "FOXOS_ROUTEROS_PASSWORD 不唯一" }
:if ([:len $routerPasswordID] = 0) do={
  :set foxosRouterPassword [:rndstr from=$randomCharacters length=32]
  /container/envs add list=foxos-env key=FOXOS_ROUTEROS_PASSWORD value=$foxosRouterPassword
} else={
  :set foxosRouterPassword [/container/envs get $routerPasswordID value]
  :if ([:len $foxosRouterPassword] < 32) do={ :error "现有 RouterOS 服务密码不足 32 字符，拒绝自动覆盖" }
}

:local mihomoSecretID [/container/envs find where list="foxos-env" key="FOXOS_MIHOMO_SECRET"]
:if ([:len $mihomoSecretID] > 1) do={ :error "FOXOS_MIHOMO_SECRET 不唯一" }
:if ([:len $mihomoSecretID] = 0) do={
  :set foxosMihomoSecret [:rndstr from=$randomCharacters length=48]
  /container/envs add list=foxos-env key=FOXOS_MIHOMO_SECRET value=$foxosMihomoSecret
} else={
  :set foxosMihomoSecret [/container/envs get $mihomoSecretID value]
  :if ([:len $foxosMihomoSecret] < 32) do={ :error "现有 Mihomo Secret 不足 32 字符，拒绝自动覆盖" }
}

:local apiTokenID [/container/envs find where list="foxos-env" key="FOXOS_API_TOKEN"]
:if ([:len $apiTokenID] > 1) do={ :error "FOXOS_API_TOKEN 不唯一" }
:if ([:len $apiTokenID] = 0) do={
  :set foxosApiToken [:rndstr from=$randomCharacters length=64]
  /container/envs add list=foxos-env key=FOXOS_API_TOKEN value=$foxosApiToken
} else={
  :set foxosApiToken [/container/envs get $apiTokenID value]
  :if ([:len $foxosApiToken] < 32) do={ :error "现有 FoxOS API Token 不足 32 字符，拒绝自动覆盖" }
}

:local confirmationKeyID [/container/envs find where list="foxos-env" key="FOXOS_CONFIRMATION_KEY"]
:if ([:len $confirmationKeyID] > 1) do={ :error "FOXOS_CONFIRMATION_KEY 不唯一" }
:if ([:len $confirmationKeyID] = 0) do={
  :set foxosConfirmationKey [:rndstr from=$randomCharacters length=64]
  /container/envs add list=foxos-env key=FOXOS_CONFIRMATION_KEY value=$foxosConfirmationKey
} else={
  :set foxosConfirmationKey [/container/envs get $confirmationKeyID value]
  :if ([:len $foxosConfirmationKey] < 32) do={ :error "现有确认密钥不足 32 字符，拒绝自动覆盖" }
}

:local fixedEnvDefinitions {"FOXOS_ROUTEROS_URL|http://10.0.0.1";"FOXOS_ROUTEROS_USERNAME|foxos-service";"FOXOS_MIHOMO_URL|http://10.0.0.2:9090";"FOXOS_MIHOMO_PROXY_URL|http://10.0.0.2:7890";"FOXOS_MIHOMO_BASE_CONFIG|/data/mihomo/base.yaml";"FOXOS_MIHOMO_LOCAL_CONFIG|/data/mihomo/config.yaml";"FOXOS_MIHOMO_RUNTIME_CONFIG|/root/.config/mihomo/config.yaml";"FOXOS_MIHOMO_BACKUP_DIR|/backups/mihomo";"FOXOS_MIHOMO_VALIDATOR_BINARY|/usr/local/bin/mihomo";"FOXOS_MOSDNS_URL|http://10.0.0.3:53";"FOXOS_BACKUP_DIR|/backups/foxos"}
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
:if ([:len [/container/envs find where list="foxos-env"]] != 16) do={
  :error "foxos-env 包含安全基线之外的键，拒绝继续"
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

:local mountDefinitions {"foxos-mihomo-runtime|mihomo-config|/root/.config/mihomo";"foxos-mihomo-config|mihomo-config|/data/mihomo";"foxos-mosdns-runtime|mosdns-config|/cus/mosdns";"foxos-data|foxos-data|/data";"foxos-backups|foxos-backups|/backups"}
:foreach definition in=$mountDefinitions do={
  :local first [:find $definition "|"]
  :local second [:find $definition "|" ($first + 1)]
  :local mountName [:pick $definition 0 $first]
  :local sourceName [:pick $definition ($first + 1) $second]
  :local destination [:pick $definition ($second + 1) [:len $definition]]
  :local mountID [/container/mounts find where name=$mountName]
  :local sourcePath ($storageRoot . "/" . $sourceName)
  :if ([:len $mountID] = 0) do={
    /container/mounts add name=$mountName src=$sourcePath dst=$destination
  } else={
    :if ([:len $mountID] != 1) do={ :error ("挂载名不唯一: " . $mountName) }
    :if ([/container/mounts get $mountID src] != $sourcePath || [/container/mounts get $mountID dst] != $destination) do={
      :error ("已有挂载内容不匹配，拒绝覆盖: " . $mountName)
    }
  }
}

:local serviceGroup [/user/group find where name="foxos-rest"]
:if ([:len $serviceGroup] = 0) do={
  /user/group add name=foxos-rest policy=read,write,rest-api
} else={
  :if ([:len $serviceGroup] != 1) do={ :error "foxos-rest 用户组不唯一" }
  :if ($existingInstall = false) do={ :error "同名 foxos-rest 用户组已存在且没有 FoxOS 安装标记，拒绝复用" }
  :if ([/user/group get $serviceGroup policy] != "read,write,rest-api") do={
    :error "现有 foxos-rest 权限与安全基线不一致，拒绝自动扩大或缩小权限"
  }
}
:local serviceUser [/user find where name="foxos-service"]
:if ([:len $serviceUser] = 0) do={
  /user add name=foxos-service group=foxos-rest address=10.0.0.4/32 password=$foxosRouterPassword comment="foxos:service"
} else={
  :if ([:len $serviceUser] != 1) do={ :error "foxos-service 用户不唯一" }
  :if ([/user get $serviceUser comment] != "foxos:service") do={ :error "同名 RouterOS 用户不是 FoxOS 创建，拒绝覆盖" }
  /user set $serviceUser group=foxos-rest address=10.0.0.4/32 password=$foxosRouterPassword
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
    :if ([:len [/ip/arp find where address=$addressOnly]] > 0 || [/ping address=$addressOnly count=2 interval=200ms] > 0) do={
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
  /container/add name=foxos-mihomo file=($storageRoot . "/mihomo_amd64.tar") interface=veth-mihomo root-dir=($storageRoot . "/containers/mihomo") mountlists=foxos-mihomo-runtime logging=yes start-on-boot=yes comment="foxos:mihomo"
} else={
  :if ([:len $mihomoContainer] != 1 || [:len $mihomoByName] != 1) do={ :error "Mihomo 容器不唯一" }
  :if ([/container get $mihomoContainer name] != "foxos-mihomo" || [/container get $mihomoContainer interface] != "veth-mihomo") do={ :error "已有 Mihomo 容器属性不匹配" }
  :if ([/container get $mihomoContainer envlists] != "") do={ :error "Mihomo 容器不应继承 FoxOS 密钥环境变量" }
  :if ([/container get $mihomoContainer mountlists] != "foxos-mihomo-runtime") do={ :error "Mihomo 容器挂载不匹配" }
}

:local mosdnsContainer [/container find where comment="foxos:mosdns"]
:local mosdnsByName [/container find where name="foxos-mosdns"]
:if ([:len $mosdnsContainer] = 0) do={
  :if ([:len $mosdnsByName] > 0) do={ :error "foxos-mosdns 同名容器没有 FoxOS 所有权标记" }
  /container/add name=foxos-mosdns file=($storageRoot . "/mosdns-amd64.tar") interface=veth-mosdns root-dir=($storageRoot . "/containers/mosdns") env="MOSDNS_AUTO_INIT=0" mountlists=foxos-mosdns-runtime logging=yes start-on-boot=yes comment="foxos:mosdns"
} else={
  :if ([:len $mosdnsContainer] != 1 || [:len $mosdnsByName] != 1) do={ :error "MosDNS 容器不唯一" }
  :if ([/container get $mosdnsContainer name] != "foxos-mosdns" || [/container get $mosdnsContainer interface] != "veth-mosdns") do={ :error "已有 MosDNS 容器属性不匹配" }
  :if ([/container get $mosdnsContainer envlists] != "") do={ :error "MosDNS 容器不应继承 FoxOS 密钥环境变量" }
  :if ([/container get $mosdnsContainer env] != "MOSDNS_AUTO_INIT=0") do={ :error "MosDNS 容器必须禁用外部自动初始化" }
  :if ([/container get $mosdnsContainer mountlists] != "foxos-mosdns-runtime") do={ :error "MosDNS 容器挂载不匹配" }
}

:local foxosContainer [/container find where comment="foxos:active"]
:local foxosByName [/container find where name="foxos-active"]
:if ([:len $foxosContainer] = 0) do={
  :if ([:len $foxosByName] > 0) do={ :error "foxos-active 同名容器没有 FoxOS 所有权标记" }
  /container/add name=foxos-active file=($storageRoot . "/foxos-amd64.tar") interface=veth-foxos root-dir=($storageRoot . "/containers/foxos") envlists=foxos-env mountlists=foxos-mihomo-config,foxos-data,foxos-backups logging=yes start-on-boot=yes comment="foxos:active"
} else={
  :if ([:len $foxosContainer] != 1 || [:len $foxosByName] != 1) do={ :error "FoxOS active 容器不唯一" }
  :if ([/container get $foxosContainer name] != "foxos-active" || [/container get $foxosContainer interface] != "veth-foxos") do={ :error "已有 FoxOS 容器属性不匹配" }
  :if ([/container get $foxosContainer envlists] != "foxos-env") do={ :error "FoxOS 容器环境变量列表不匹配" }
  :if ([/container get $foxosContainer mountlists] != "foxos-mihomo-config,foxos-data,foxos-backups") do={ :error "FoxOS 容器挂载不匹配" }
}

:put "FoxOS 全栈资源已创建或复用，镜像导入为异步操作。"
:put "等待 Mihomo、MosDNS、FoxOS 三个容器均为 status=stopped，再执行 /import file-name=disk1/foxos-start-all.rsc。"
:put "安装完成后凭据只从 env list 读取一次并保存到离线密码库。"
