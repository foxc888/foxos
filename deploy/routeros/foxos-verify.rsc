# Verify all runtime gates through trusted HTTPS, then enable the owned cold-boot
# coordinator. Individual containers always keep start-on-boot disabled.

:global FoxOSSiteManifestVersion
:global FoxOSSiteFoxOSAddress
:global FoxOSSitePublicHostname
:global FoxOSSiteStorageRoot
:global FoxOSContainerCompatVersion
:global FoxOSContainerState
:global FoxOSContainerRoot
:if ($FoxOSSiteManifestVersion != 2) do={ :error "先导入不可变的 load-site-config.rsc" }
/import file-name=($FoxOSSiteStorageRoot . "/load-site-config.rsc")
:if ($FoxOSContainerCompatVersion != 1) do={ :error "container compatibility contract is unavailable" }
:local foxosURL ("https://" . $FoxOSSiteFoxOSAddress)
:local containerDeviceMode [/system/device-mode get container]
:local schedulerDeviceMode [/system/device-mode get scheduler]
:if (($containerDeviceMode != true && $containerDeviceMode != "yes") || ($schedulerDeviceMode != true && $schedulerDeviceMode != "yes")) do={
  :error "device-mode container=yes 与 scheduler=yes 必须在验证和启用冷启动协调器前保持有效"
}
:local active [/container find where comment="foxos:active"]
:local mihomo [/container find where comment="foxos:mihomo"]
:local mosdns [/container find where comment="foxos:mosdns"]
:if ([:len $active] != 1 || [:len $mihomo] != 1 || [:len $mosdns] != 1) do={ :error "三个 FoxOS 容器必须各自唯一" }
:local mihomoByName [/container find where name="foxos-mihomo"]
:local mosdnsByName [/container find where name="foxos-mosdns"]
:local activeName [/container get $active name]
:if ([:len $mihomoByName] != 1 || $mihomoByName != $mihomo || [/container get $mihomo interface] != "veth-mihomo" || [/container get $mihomo envlists] != "" || [/container get $mihomo mountlists] != "foxos-mihomo-runtime" || [$FoxOSContainerRoot $mihomo] != ($FoxOSSiteStorageRoot . "/containers/mihomo") || ([/container get $mihomo start-on-boot] != false && [/container get $mihomo start-on-boot] != "no") || ([/container get $mihomo logging] != true && [/container get $mihomo logging] != "yes")) do={ :error "Mihomo 容器完整身份契约不匹配" }
:if ([:len $mosdnsByName] != 1 || $mosdnsByName != $mosdns || [/container get $mosdns interface] != "veth-mosdns" || [/container get $mosdns envlists] != "foxos-mosdns-env" || [/container get $mosdns mountlists] != "foxos-mosdns-runtime" || [$FoxOSContainerRoot $mosdns] != ($FoxOSSiteStorageRoot . "/containers/mosdns") || ([/container get $mosdns start-on-boot] != false && [/container get $mosdns start-on-boot] != "no") || ([/container get $mosdns logging] != true && [/container get $mosdns logging] != "yes")) do={ :error "MosDNS 容器完整身份契约不匹配" }
:if (!($activeName ~ "^foxos-[A-Za-z0-9._-]+\$") || [:len [/container find where name=$activeName]] != 1 || [/container get $active interface] != "veth-foxos" || [/container get $active envlists] != "foxos-env" || [/container get $active mountlists] != "foxos-mihomo-config,foxos-data,foxos-backups" || [$FoxOSContainerRoot $active] != ($FoxOSSiteStorageRoot . "/containers/" . $activeName) || ([/container get $active start-on-boot] != false && [/container get $active start-on-boot] != "no") || ([/container get $active logging] != false && [/container get $active logging] != "no")) do={ :error "FoxOS active 容器完整身份契约不匹配" }
:foreach containerID in={$mihomo;$mosdns;$active} do={
  :if ([$FoxOSContainerState $containerID] != "running") do={ :error ("容器未处于 running: " . [/container get $containerID name]) }
}
:if ([:len [/certificate find where common-name="FoxOS Local CA" trusted=yes]] != 1) do={
  :error "RouterOS 中缺少唯一且 trusted=yes 的 FoxOS Local CA；禁止跳过证书校验"
}
:local tokenID [/container/envs find where list="foxos-env" key="FOXOS_API_TOKEN"]
:if ([:len $tokenID] != 1) do={ :error "FOXOS_API_TOKEN 缺失或不唯一" }
:local apiToken [/container/envs get $tokenID value]
:local authHeader ("Authorization: Bearer " . $apiToken)

:local live [/tool/fetch url=($foxosURL . "/api/v1/health/live") check-certificate=yes-without-crl output=user as-value]
:if (($live->"status") != "finished" || [:typeof [:find ($live->"data") "\"status\":\"ok\""]] = "nil") do={ :error "live 验证失败" }
:local ready [/tool/fetch url=($foxosURL . "/api/v1/health/ready") check-certificate=yes-without-crl output=user as-value]
:if (($ready->"status") != "finished" || [:typeof [:find ($ready->"data") "\"status\":\"ready\""]] = "nil") do={ :error "ready 验证失败" }
:local site [/tool/fetch url=($foxosURL . "/api/v1/site") check-certificate=yes-without-crl output=user as-value]
:if (($site->"status") != "finished" || [:typeof [:find ($site->"data") ("\"publicHostname\":\"" . $FoxOSSitePublicHostname . "\"")]] = "nil") do={ :error "站点清单回读失败" }
:local audit [/tool/fetch url=($foxosURL . "/api/v1/audit-events?limit=1") check-certificate=yes-without-crl http-header-field=$authHeader output=user as-value]
:if (($audit->"status") != "finished") do={ :error "带认证的只读 API 验证失败" }
:local page [/tool/fetch url=($foxosURL . "/") check-certificate=yes-without-crl output=user as-value]
:if (($page->"status") != "finished" || [:typeof [:find ($page->"data") "id=\"root\""]] = "nil") do={ :error "Web 页面验证失败" }

:local expectedStartSource (":delay 20s; /import file-name=" . $FoxOSSiteStorageRoot . "/load-site-config.rsc; /import file-name=" . $FoxOSSiteStorageRoot . "/foxos-start-all.rsc")
:local startScript [/system/script find where name="foxos-start-sequence" comment="foxos:start-sequence"]
:local startScheduler [/system/scheduler find where name="foxos-start-sequence" comment="foxos:start-sequence"]
:if ([:len $startScript] != 1 || [/system/script get $startScript source] != $expectedStartSource || [/system/script get $startScript policy] != {"read";"write";"test"}) do={ :error "冷启动协调脚本缺失或内容不匹配" }
:if ([:len $startScheduler] != 1 || [/system/scheduler get $startScheduler on-event] != "foxos-start-sequence" || [/system/scheduler get $startScheduler start-time] != "startup" || [/system/scheduler get $startScheduler interval] != 0s || [/system/scheduler get $startScheduler policy] != {"read";"write";"test"}) do={ :error "冷启动 scheduler 缺失或内容不匹配" }

:local bootEnabled false
:onerror bootError in={
  :foreach containerID in={$mihomo;$mosdns;$active} do={
    /container/set $containerID start-on-boot=no
    :if ([/container get $containerID start-on-boot] != false && [/container get $containerID start-on-boot] != "no") do={ :error ("container autostart disable readback failed: " . [/container get $containerID name]) }
  }
  /system/scheduler set $startScheduler disabled=no
  :if ([/system/scheduler get $startScheduler disabled] != false && [/system/scheduler get $startScheduler disabled] != "no") do={ :error "sequential scheduler enable readback failed" }
  :set bootEnabled true
} do={
  /system/scheduler set $startScheduler disabled=yes
  :foreach containerID in={$mihomo;$mosdns;$active} do={ /container/set $containerID start-on-boot=no }
  :error ("健康验证已通过，但顺序启动协调器设置失败并已禁用: " . $bootError)
}
:if ($bootEnabled = false) do={ :error "顺序启动协调器未启用" }

:put ("HTTPS VERIFIED: live, ready, site manifest, read-only API, and page passed at " . $foxosURL . "; owned scheduler will cold-start Mihomo, then MosDNS, then FoxOS")
:put "该结果只验证 RouterOS 到 FoxOS 管理面，不代表透明代理或实体客户端出口已验收。"
