# Read-only plan for one owned local DNS record. It never enables DNS service,
# changes upstream resolvers, or changes DHCP-advertised DNS servers.

:global FoxOSSiteManifestVersion
:global FoxOSSiteStorageRoot
:global FoxOSSitePublicHostname
:global FoxOSSiteFoxOSAddress
:if ($FoxOSSiteManifestVersion != 2) do={ :error "先导入不可变的 load-site-config.rsc" }
/import file-name=($FoxOSSiteStorageRoot . "/load-site-config.rsc")
:local hostname $FoxOSSitePublicHostname
:local dnsAddress $FoxOSSiteFoxOSAddress
:local records [/ip/dns/static find where name=$hostname]
:if ([:len $records] > 1) do={ :error ("同名 DNS 记录不唯一，拒绝计划: " . $hostname) }
:if ([:len $records] = 1) do={
  :if ([/ip/dns/static get $records comment] != "foxos:dns:admin" || [/ip/dns/static get $records address] != $dnsAddress || [/ip/dns/static get $records type] != "A") do={
    :error ("已存在非 FoxOS 所有或内容不同的 DNS 记录，拒绝接管: " . $hostname)
  }
  :put ("NO CHANGE: owned A record already maps " . $hostname . " to " . $dnsAddress)
} else={
  :put ("PLAN: add exactly one owned A record " . $hostname . " -> " . $dnsAddress . " ttl=5m")
  :put "UNTOUCHED: allow-remote-requests, upstream DNS, cache, DHCP networks, DHCP options, NAT, and firewall."
  :put ("To confirm, run :global FoxOSDNSConfirmation \"ADD " . $hostname . " " . $dnsAddress . "\" then import foxos-dns-apply.rsc.")
}
