# Read-only plan for one owned local DNS record. It never enables DNS service,
# changes upstream resolvers, or changes DHCP-advertised DNS servers.

:global FoxOSSiteManifestVersion
:global FoxOSSitePublicHostname
:global FoxOSSiteFoxOSAddress
:if ($FoxOSSiteManifestVersion != 1) do={ :error "先导入已审核的 site-config.rsc" }
:local hostname $FoxOSSitePublicHostname
:local address $FoxOSSiteFoxOSAddress
:local records [/ip/dns/static find where name=$hostname]
:if ([:len $records] > 1) do={ :error ("同名 DNS 记录不唯一，拒绝计划: " . $hostname) }
:if ([:len $records] = 1) do={
  :if ([/ip/dns/static get $records comment] != "foxos:dns:admin" || [/ip/dns/static get $records address] != $address || [/ip/dns/static get $records type] != "A") do={
    :error ("已存在非 FoxOS 所有或内容不同的 DNS 记录，拒绝接管: " . $hostname)
  }
  :put ("NO CHANGE: owned A record already maps " . $hostname . " to " . $address)
} else={
  :put ("PLAN: add exactly one owned A record " . $hostname . " -> " . $address . " ttl=5m")
  :put "UNTOUCHED: allow-remote-requests, upstream DNS, cache, DHCP networks, DHCP options, NAT, and firewall."
  :put ("To confirm, run :global FoxOSDNSConfirmation \"ADD " . $hostname . " " . $address . "\" then import foxos-dns-apply.rsc.")
}
