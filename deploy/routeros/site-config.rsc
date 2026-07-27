# FoxOS site manifest. Edit this file before upload, then import it before every
# preflight, install, DNS, upgrade, rollback, or verification script.
# Keep the four service addresses unique, usable, and inside FoxOSSiteNetwork.

:global FoxOSSiteManifestVersion 1
:global FoxOSSiteManagementBridge "bridge-lan"
:global FoxOSSiteStorageRoot "disk1"
:global FoxOSSiteNetwork "10.0.0.0/24"
:global FoxOSSitePrefixLength 24
:global FoxOSSiteRouterAddress "10.0.0.1"
:global FoxOSSiteMihomoAddress "10.0.0.2"
:global FoxOSSiteMosDNSAddress "10.0.0.3"
:global FoxOSSiteFoxOSAddress "10.0.0.4"
:global FoxOSSitePublicHostname "foxos.home.arpa"

:put ("FoxOS site manifest loaded: bridge=" . $FoxOSSiteManagementBridge . " storage=" . $FoxOSSiteStorageRoot . " network=" . $FoxOSSiteNetwork)
:put ("Services: RouterOS=" . $FoxOSSiteRouterAddress . " Mihomo=" . $FoxOSSiteMihomoAddress . " MosDNS=" . $FoxOSSiteMosDNSAddress . " FoxOS=https://" . $FoxOSSitePublicHostname)
