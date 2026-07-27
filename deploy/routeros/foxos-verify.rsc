# Verify a running FoxOS container through the trusted HTTPS data plane.
# Import the generated local CA into RouterOS and mark it trusted first.

:global FoxOSSiteManifestVersion
:global FoxOSSiteFoxOSAddress
:global FoxOSSitePublicHostname
:if ($FoxOSSiteManifestVersion != 1) do={ :error "先导入已审核的 site-config.rsc" }
:local foxosURL ("https://" . $FoxOSSiteFoxOSAddress)
:local active [/container find where comment="foxos:active"]
:if ([:len $active] != 1 || [/container get $active status] != "running") do={ :error "唯一 foxos:active 容器未处于 running" }
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

:put ("HTTPS VERIFIED: live, ready, site manifest, read-only API, and page passed at " . $foxosURL)
:put "该结果只验证 RouterOS 到 FoxOS 管理面，不代表透明代理或实体客户端出口已验收。"
