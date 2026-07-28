# Apply only the exact DNS plan confirmed after foxos-dns-plan.rsc.

:global FoxOSSiteManifestVersion
:global FoxOSSiteStorageRoot
:global FoxOSSitePublicHostname
:global FoxOSSiteFoxOSAddress
:global FoxOSDNSConfirmation
:if ($FoxOSSiteManifestVersion != 2) do={ :error "先导入不可变的 load-site-config.rsc" }
/import file-name=($FoxOSSiteStorageRoot . "/load-site-config.rsc")
:local hostname $FoxOSSitePublicHostname
:local address $FoxOSSiteFoxOSAddress
:local expectedConfirmation ("ADD " . $hostname . " " . $address)
:if ($FoxOSDNSConfirmation != $expectedConfirmation) do={ :error "DNS 精确计划未确认或站点清单已变化" }
:local records [/ip/dns/static find where name=$hostname]
:if ([:len $records] > 1) do={ :error ("同名 DNS 记录不唯一，拒绝写入: " . $hostname) }
:if ([:len $records] = 1) do={
  :if ([/ip/dns/static get $records comment] != "foxos:dns:admin" || [/ip/dns/static get $records address] != $address || [/ip/dns/static get $records type] != "A") do={
    :error ("执行前状态变化或记录不属于 FoxOS，拒绝写入: " . $hostname)
  }
} else={
  /ip/dns/static add name=$hostname type=A address=$address ttl=5m comment="foxos:dns:admin"
}
:set FoxOSDNSConfirmation ""
:local verified [/ip/dns/static find where name=$hostname address=$address type=A comment="foxos:dns:admin"]
:if ([:len $verified] != 1) do={ :error "DNS 写入后回读失败" }
:put ("DNS VERIFIED: " . $hostname . " -> " . $address)
:put "未启用或接管 RouterOS DNS；只有原本使用该 RouterOS DNS 的客户端会获得此记录。"
