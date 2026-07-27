# FoxOS 单容器安装模板
# 本脚本只管理带 foxos: 所有权标识的资源，重复执行会复用同名 FoxOS 资源。
# 执行前必须先运行 preflight.rsc、阅读 foxos-plan.rsc，并完成本地备份与人工确认。
# RouterOS 对 x86_64 CPU 的 architecture-name 是 x86；目标版本需要 7.21+。

:local architecture [/system/resource get architecture-name]
:local imageFile ""
:local managementBridge "bridge-lan"
:local foxosAddress "10.0.0.4/24"
:local foxosGateway "10.0.0.1"
:local storageRoot "disk1"
:local containerRoot ($storageRoot . "/containers/foxos")
:local existingContainer [/container find where comment="foxos:active"]

:if ($architecture = "x86") do={
  :set imageFile "foxos-amd64.tar"
} else={
  :if ($architecture = "arm64") do={
    :set imageFile "foxos-arm64.tar"
  } else={
    :error ("当前脚本不支持此架构: " . $architecture . "（x86_64 RouterOS 应返回 x86）")
  }
}

:if ([:len [/interface/bridge find where name=$managementBridge]] != 1) do={
  :error ("管理桥不存在或不唯一: " . $managementBridge . "；请先运行 preflight")
}
:local imagePath ($storageRoot . "/" . $imageFile)
:if ([:len [/file find where name=$imagePath]] != 1) do={
  :error ("镜像文件不存在或不唯一: " . $imagePath)
}
:if ([:len [/container/envs find where list="foxos-env" key="FOXOS_INSTALL_MARKER" value="foxos"]] != 1) do={
  :error "foxos-env 不存在或不是 FoxOS 创建的列表；拒绝覆盖同名用户资源"
}
:if ([:len [/disk find where slot=$storageRoot]] != 1) do={
  :error ("持久化磁盘不存在: " . $storageRoot)
}
:if ([:len [/ip/address find where address~"10.0.0.4/"]] > 0) do={
  :error "10.0.0.4 已被 RouterOS 地址占用"
}
:if ([:len [/ip/dhcp-server/lease find where address="10.0.0.4"]] > 0) do={
  :error "10.0.0.4 已被 DHCP Lease 占用"
}

:local existingName [/container find where name="foxos-active"]
:if ([:len $existingName] > 0 && [:len $existingContainer] = 0) do={
  :error "同名 foxos-active 容器不是通过 FoxOS comment 标识，拒绝覆盖"
}
:if ([:len $existingContainer] > 1) do={
  :error "发现多个 foxos:active 容器，先人工清理未完成安装"
}

:local veth [/interface/veth find where name="veth-foxos"]
:if ([:len $veth] > 0) do={
  :foreach id in=$veth do={
    :if ([/interface/veth get $id comment] != "foxos:admin") do={
      :error "veth-foxos 已存在但不属于 FoxOS，拒绝覆盖"
    }
    :if ([/interface/veth get $id address] != $foxosAddress || [/interface/veth get $id gateway] != $foxosGateway) do={
      :error "veth-foxos 的地址或网关与计划不一致"
    }
  }
} else={
  :if ([:len [/ip/arp find where address="10.0.0.4"]] > 0 || [/ping address="10.0.0.4" count=2 interval=200ms] > 0) do={
    :error "10.0.0.4 已出现在 ARP 或可达，拒绝创建"
  }
  /interface/veth add name=veth-foxos address=$foxosAddress gateway=$foxosGateway comment="foxos:admin"
}
:local bridgePort [/interface/bridge/port find where interface="veth-foxos"]
:if ([:len $bridgePort] = 0) do={
  /interface/bridge/port add bridge=$managementBridge interface=veth-foxos comment="foxos:admin"
} else={
  :foreach id in=$bridgePort do={
    :if ([/interface/bridge/port get $id bridge] != $managementBridge) do={
      :error "veth-foxos 已加入其他 bridge，拒绝接管"
    }
    :if ([/interface/bridge/port get $id comment] != "foxos:admin") do={
      :error "veth-foxos bridge port 没有 FoxOS 所有权标记"
    }
  }
}

:foreach mountName in={"foxos-data";"foxos-backups"} do={
  :local mountID [/container/mounts find where name=$mountName]
  :local mountSource ($storageRoot . "/" . $mountName)
  :local mountDestination ""
  :if ($mountName = "foxos-data") do={ :set mountDestination "/data" }
  :if ($mountName = "foxos-backups") do={ :set mountDestination "/backups" }
  :if ([:len $mountID] = 0) do={
    /container/mounts add name=$mountName src=$mountSource dst=$mountDestination
  } else={
    :if ([:len $mountID] != 1) do={ :error ("挂载名不唯一: " . $mountName) }
    :if ([/container/mounts get $mountID src] != $mountSource || [/container/mounts get $mountID dst] != $mountDestination) do={
      :error ("已有挂载 " . $mountName . " 内容不匹配，拒绝覆盖")
    }
  }
}

:if ([:len $existingContainer] = 0) do={
  /container/add name=foxos-active file=$imagePath interface=veth-foxos root-dir=$containerRoot envlists=foxos-env mountlists=foxos-data,foxos-backups logging=yes start-on-boot=yes comment="foxos:active"
  :put "FoxOS 镜像导入已排队。等待 /container 显示 status=stopped 后再启动。"
} else={
  :foreach id in=$existingContainer do={
    :if ([/container get $id interface] != "veth-foxos" || [/container get $id comment] != "foxos:active") do={
      :error "已有 FoxOS 容器属性不匹配，拒绝覆盖"
    }
  }
  :put "已复用现有 foxos:active 容器；未修改用户资源。"
}
:put "未修改 DNS、DHCP、默认路由、NAT、Mangle 或现有防火墙。"
