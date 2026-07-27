# 在 full-install 完成镜像解压后执行。
# RouterOS 官方要求首次启动前等待容器 status=stopped。

:global FoxOSSiteManifestVersion
:global FoxOSSiteStorageRoot
:global FoxOSSitePublicHostname
:global FoxOSSiteFoxOSAddress
:if ($FoxOSSiteManifestVersion != 1) do={ :error "先导入已审核的 site-config.rsc" }
:local storageRoot $FoxOSSiteStorageRoot
:local publicHostname $FoxOSSitePublicHostname
:local foxosAddress $FoxOSSiteFoxOSAddress
:local expected 0
:local ready 0

:foreach containerComment in={"foxos:mihomo";"foxos:mosdns";"foxos:active"} do={
  :local ids [/container find where comment=$containerComment]
  :if ([:len $ids] != 1) do={
    :error ("未找到唯一容器: " . $containerComment)
  }
  :set expected ($expected + 1)
  :if ([/container get $ids status] = "stopped") do={
    :set ready ($ready + 1)
  }
}

:if ($ready != $expected) do={
  /container/print
  :error "镜像仍在解压或状态异常；等待三个容器全部 status=stopped 后重试"
}

:put "FoxOS: 启动 Mihomo..."
/container/start [find where comment="foxos:mihomo"]
:delay 3s
:if ([/container get [find where comment="foxos:mihomo"] status] != "running") do={
  :error "Mihomo 未进入 running；未继续启动其他容器，请检查 container 日志"
}
:put "FoxOS: 启动 MosDNS..."
/container/start [find where comment="foxos:mosdns"]
:delay 3s
:if ([/container get [find where comment="foxos:mosdns"] status] != "running") do={
  :error "MosDNS 未进入 running；未启动 FoxOS，请检查 container 日志"
}
:put "FoxOS: 启动管理后台..."
/container/start [find where comment="foxos:active"]
:delay 5s
:if ([/container get [find where comment="foxos:active"] status] != "running") do={
  :error "FoxOS 未进入 running，请检查 container 日志"
}

/container/print
:put ("FoxOS 容器已进入 running；这不等于应用 ready。导入 " . $storageRoot . "/foxos-data/tls/foxos-local-ca.pem 后运行 foxos-verify.rsc。")
:put ("CA 受信任后访问 https://" . $publicHostname . "；IP 备用入口为 https://" . $foxosAddress . "。")
:put "若状态不是 running，请执行 /log/print where topics~\"container\""

:foreach requiredKey in={"FOXOS_ROUTEROS_PASSWORD";"FOXOS_MIHOMO_SECRET";"FOXOS_API_TOKEN";"FOXOS_CONFIRMATION_KEY"} do={
  :if ([:len [/container/envs find where list="foxos-env" key=$requiredKey]] != 1) do={
    :error ("FoxOS 凭据键缺失或不唯一: " . $requiredKey)
  }
}
:local routerPassword [/container/envs get [find where list="foxos-env" key="FOXOS_ROUTEROS_PASSWORD"] value]
:local mihomoSecret [/container/envs get [find where list="foxos-env" key="FOXOS_MIHOMO_SECRET"] value]
:local apiToken [/container/envs get [find where list="foxos-env" key="FOXOS_API_TOKEN"] value]
:local confirmationKey [/container/envs get [find where list="foxos-env" key="FOXOS_CONFIRMATION_KEY"] value]

:put ""
:put "================ FoxOS 安装凭据 ================"
:put ("RouterOS foxos-service 密码: " . $routerPassword)
:put ("Mihomo Controller Secret: " . $mihomoSecret)
:put ("FoxOS 登录 Token: " . $apiToken)
:put ("FoxOS 确认密钥: " . $confirmationKey)
:put "=================================================="
:put "请立即复制保存。不要截图、不要提交到 GitHub。"
