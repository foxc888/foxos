# 在 full-install 完成镜像解压后执行。
# RouterOS 官方要求首次启动前等待容器 status=stopped。

:local expected 0
:local ready 0

:foreach containerComment in={"foxos:mihomo";"foxos:mosdns";"foxos:admin"} do={
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
:put "FoxOS: 启动 MosDNS..."
/container/start [find where comment="foxos:mosdns"]
:delay 3s
:put "FoxOS: 启动管理后台..."
/container/start [find where comment="foxos:admin"]
:delay 5s

/container/print
:put "FoxOS 已提交启动，请访问 http://10.0.0.4:8090"
:put "若状态不是 running，请执行 /log/print where topics~\"container\""

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
