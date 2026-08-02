# FoxOS site manifest template. Never edit this immutable example in place.
# Copy it to site-config.rsc, edit only the values below, then seal it with
# seal-site-config.sh before upload. Never import the editable file directly.

:global FoxOSSiteManifestVersion 2
:global FoxOSSiteManagementBridge "bridge-lan"
# A mounted disk slot such as disk1 is recommended. The exact reserved value
# foxos selects internal root storage. Every other value must match one /disk slot.
:global FoxOSSiteStorageRoot "disk1"
:global FoxOSSiteNetwork "10.0.0.0/24"
:global FoxOSSitePrefixLength 24
:global FoxOSSiteRouterAddress "10.0.0.1"
:global FoxOSSiteMihomoAddress "10.0.0.2"
:global FoxOSSiteMosDNSAddress "10.0.0.3"
:global FoxOSSiteFoxOSAddress "10.0.0.4"
:global FoxOSSitePublicHostname "foxos.home.arpa"
# Optional comma-separated canonical RFC1918/ULA prefixes for administrator-
# approved HTTPS subscription feeds. Empty means private targets stay blocked.
:global FoxOSSiteSubscriptionPrivateCIDRs ""
