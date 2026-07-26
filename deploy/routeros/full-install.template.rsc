# FoxOS 全栈安装脚本（RouterOS x86_64）
# 适配用户环境：bridge-lan / 10.0.0.0/24
# 不修改 DNS、DHCP、NAT、默认路由、Mangle 或现有防火墙。
# 请先在电脑执行 prepare-install.ps1，生成 foxos-full-install.local.rsc。

:local managementBridge "bridge-lan"
:local routerAddress "10.0.0.1"
:local mihomoAddress "10.0.0.2/24"
:local mosdnsAddress "10.0.0.3/24"
:local foxosAddress "10.0.0.4/24"
:local architecture [/system/resource get architecture-name]

:put "FoxOS: 开始只读预检..."
:if ($architecture != "x86_64") do={
  :error ("此安装包仅支持 x86_64，当前架构: " . $architecture)
}
:if ([:len [/interface/bridge find where name=$managementBridge]] = 0) do={
  :error ("未找到管理桥: " . $managementBridge)
}
:if ([:len [/ip/address find where address~"10.0.0.1/"]] = 0) do={
  :error "未找到 RouterOS LAN 地址 10.0.0.1"
}
:if ([/ip/service get [find where name="www"] disabled] = true) do={
  :error "RouterOS www/REST 服务未启用；请先启用仅限 10.0.0.0/24 的 www 服务"
}
:local freeDisk [/system/resource get free-hdd-space]
:if ($freeDisk < 536870912) do={
  :put ("警告：可用磁盘空间不足 512 MiB，当前字节数: " . $freeDisk)
  :error "空间不足；请释放空间或把安装包与 root-dir 调整到足够大的磁盘"
}

:foreach requiredFile in={"mihomo_amd64.tar";"mosdns-amd64.tar";"foxos-amd64.tar";"mihomo-config";"mosdns-config"} do={
  :if ([:len [/file find where name=$requiredFile]] = 0) do={
    :error ("缺少安装文件或目录: " . $requiredFile)
  }
}

:foreach ownedVeth in={"veth-mihomo";"veth-mosdns";"veth-foxos"} do={
  :if ([:len [/interface/veth find where name=$ownedVeth]] > 0) do={
    :error ("发现已有接口 " . $ownedVeth . "；请先检查是否为未完成的旧安装")
  }
}
:foreach ownedContainer in={"foxos:mihomo";"foxos:mosdns";"foxos:admin"} do={
  :if ([:len [/container find where comment=$ownedContainer]] > 0) do={
    :error ("发现已有容器 " . $ownedContainer . "；请先检查是否为未完成的旧安装")
  }
}
:foreach reservedAddress in={"10.0.0.2/";"10.0.0.3/";"10.0.0.4/"} do={
  :if ([:len [/ip/address find where address~$reservedAddress]] > 0) do={
    :error ("RouterOS 已占用保留地址: " . $reservedAddress)
  }
}

:put "FoxOS: 配置专用 REST 用户..."
:if ([:len [/user/group find where name="foxos-rest"]] = 0) do={
  /user/group add name=foxos-rest policy=read,write,rest-api
} else={
  /user/group set [find where name="foxos-rest"] policy=read,write,rest-api
}
:if ([:len [/user find where name="foxos-service"]] = 0) do={
  /user add name=foxos-service group=foxos-rest address=10.0.0.4/32 password="__ROUTEROS_PASSWORD__" comment="foxos:service"
} else={
  :if ([/user get [find where name="foxos-service"] comment] != "foxos:service") do={
    :error "同名用户 foxos-service 不是 FoxOS 创建，拒绝覆盖"
  }
  /user set [find where name="foxos-service"] group=foxos-rest address=10.0.0.4/32 password="__ROUTEROS_PASSWORD__"
}

:put "FoxOS: 写入容器环境变量..."
/container/envs remove [find where list="foxos-env"]
/container/envs add list=foxos-env key=FOXOS_API_TOKEN value="__FOXOS_API_TOKEN__"
/container/envs add list=foxos-env key=FOXOS_CONFIRMATION_KEY value="__FOXOS_CONFIRMATION_KEY__"
/container/envs add list=foxos-env key=FOXOS_ROUTEROS_URL value="http://10.0.0.1"
/container/envs add list=foxos-env key=FOXOS_ROUTEROS_USERNAME value="foxos-service"
/container/envs add list=foxos-env key=FOXOS_ROUTEROS_PASSWORD value="__ROUTEROS_PASSWORD__"
/container/envs add list=foxos-env key=FOXOS_MIHOMO_URL value="http://10.0.0.2:9090"
/container/envs add list=foxos-env key=FOXOS_MIHOMO_SECRET value="__MIHOMO_SECRET__"
/container/envs add list=foxos-env key=FOXOS_MIHOMO_LOCAL_CONFIG value="/data/mihomo/config.yaml"
/container/envs add list=foxos-env key=FOXOS_MIHOMO_RUNTIME_CONFIG value="/root/.config/mihomo/config.yaml"
/container/envs add list=foxos-env key=FOXOS_MIHOMO_BACKUP_DIR value="/backups/mihomo"

:put "FoxOS: 创建持久化挂载..."
:if ([:len [/container/mounts find where list="mount-mihomo"]] = 0) do={
  /container/mounts add list=mount-mihomo src=mihomo-config dst=/root/.config/mihomo
}
:if ([:len [/container/mounts find where list="mount-mosdns"]] = 0) do={
  /container/mounts add list=mount-mosdns src=mosdns-config dst=/cus/mosdns
}
:if ([:len [/container/mounts find where list="foxos-data"]] = 0) do={
  /container/mounts add list=foxos-data src=foxos-data dst=/data
}
:if ([:len [/container/mounts find where list="foxos-backups"]] = 0) do={
  /container/mounts add list=foxos-backups src=foxos-backups dst=/backups
}

:put "FoxOS: 创建容器网络..."
/interface/veth add name=veth-mihomo address=$mihomoAddress gateway=$routerAddress comment="foxos:mihomo"
/interface/veth add name=veth-mosdns address=$mosdnsAddress gateway=$routerAddress comment="foxos:mosdns"
/interface/veth add name=veth-foxos address=$foxosAddress gateway=$routerAddress comment="foxos:admin"
/interface/bridge/port add bridge=$managementBridge interface=veth-mihomo comment="foxos:mihomo"
/interface/bridge/port add bridge=$managementBridge interface=veth-mosdns comment="foxos:mosdns"
/interface/bridge/port add bridge=$managementBridge interface=veth-foxos comment="foxos:admin"

:put "FoxOS: 导入 Mihomo、MosDNS 和 FoxOS 镜像；此过程可能需要数分钟..."
/container/add file=mihomo_amd64.tar interface=veth-mihomo root-dir=containers/mihomo mountlists=mount-mihomo logging=yes start-on-boot=yes comment="foxos:mihomo"
/container/add file=mosdns-amd64.tar interface=veth-mosdns root-dir=containers/mosdns mountlists=mount-mosdns logging=yes start-on-boot=yes comment="foxos:mosdns"
/container/add file=foxos-amd64.tar interface=veth-foxos root-dir=containers/foxos envlist=foxos-env mountlists=foxos-data,foxos-backups logging=yes start-on-boot=yes comment="foxos:admin"

:put "FoxOS: 镜像导入已排队。"
:put "下一步：反复执行 /container/print，等待三个容器全部 status=stopped。"
:put "然后执行：/import file-name=foxos-start-all.rsc"
