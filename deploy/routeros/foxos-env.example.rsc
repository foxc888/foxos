# 复制本文件到本机，替换所有 CHANGE_ME；不要提交含真实密钥的副本。
# RouterOS 导入前请确认 /system/device-mode 已允许 container。
# 先导入 site-config.rsc；生产全量安装器会自动生成这些项目，本例仅供手工恢复参考。

:global FoxOSSiteManifestVersion
:global FoxOSSiteManagementBridge
:global FoxOSSiteStorageRoot
:global FoxOSSiteNetwork
:global FoxOSSiteRouterAddress
:global FoxOSSiteMihomoAddress
:global FoxOSSiteMosDNSAddress
:global FoxOSSiteFoxOSAddress
:global FoxOSSitePublicHostname
:if ($FoxOSSiteManifestVersion != 1) do={ :error "先导入已审核的 site-config.rsc" }

/container/envs
add list=foxos-env key=FOXOS_INSTALL_MARKER value="foxos"
add list=foxos-env key=FOXOS_API_TOKEN value="CHANGE_ME_AT_LEAST_32_RANDOM_CHARACTERS"
add list=foxos-env key=FOXOS_CONFIRMATION_KEY value="CHANGE_ME_DIFFERENT_32_RANDOM_CHARACTERS"
add list=foxos-env key=FOXOS_ROUTEROS_URL value=("http://" . $FoxOSSiteRouterAddress)
add list=foxos-env key=FOXOS_ROUTEROS_USERNAME value="foxos-service"
add list=foxos-env key=FOXOS_ROUTEROS_PASSWORD value="CHANGE_ME_ROUTEROS_SERVICE_PASSWORD"
add list=foxos-env key=FOXOS_MIHOMO_URL value=("http://" . $FoxOSSiteMihomoAddress . ":9090")
add list=foxos-env key=FOXOS_MIHOMO_PROXY_URL value=("http://" . $FoxOSSiteMihomoAddress . ":7890")
add list=foxos-env key=FOXOS_MIHOMO_SECRET value="CHANGE_ME_MIHOMO_CONTROLLER_SECRET"
add list=foxos-env key=FOXOS_MIHOMO_BASE_CONFIG value="/data/mihomo/base.yaml"
add list=foxos-env key=FOXOS_MIHOMO_LOCAL_CONFIG value="/data/mihomo/config.yaml"
add list=foxos-env key=FOXOS_MIHOMO_RUNTIME_CONFIG value="/root/.config/mihomo/config.yaml"
add list=foxos-env key=FOXOS_MIHOMO_BACKUP_DIR value="/backups/mihomo"
add list=foxos-env key=FOXOS_MIHOMO_VALIDATOR_BINARY value="/usr/local/bin/mihomo"
add list=foxos-env key=FOXOS_MOSDNS_URL value=("http://" . $FoxOSSiteMosDNSAddress . ":53")
add list=foxos-env key=FOXOS_BACKUP_DIR value="/backups/foxos"
add list=foxos-env key=FOXOS_SITE_MANAGEMENT_BRIDGE value=$FoxOSSiteManagementBridge
add list=foxos-env key=FOXOS_SITE_STORAGE_ROOT value=$FoxOSSiteStorageRoot
add list=foxos-env key=FOXOS_SITE_NETWORK value=$FoxOSSiteNetwork
add list=foxos-env key=FOXOS_SITE_ROUTER_ADDRESS value=$FoxOSSiteRouterAddress
add list=foxos-env key=FOXOS_SITE_MIHOMO_ADDRESS value=$FoxOSSiteMihomoAddress
add list=foxos-env key=FOXOS_SITE_MOSDNS_ADDRESS value=$FoxOSSiteMosDNSAddress
add list=foxos-env key=FOXOS_SITE_FOXOS_ADDRESS value=$FoxOSSiteFoxOSAddress
add list=foxos-env key=FOXOS_SITE_PUBLIC_HOSTNAME value=$FoxOSSitePublicHostname
add list=foxos-env key=FOXOS_HTTPS_ENABLED value="true"
