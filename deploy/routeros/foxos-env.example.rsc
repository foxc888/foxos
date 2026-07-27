# 复制本文件到本机，替换所有 CHANGE_ME；不要提交含真实密钥的副本。
# RouterOS 导入前请确认 /system/device-mode 已允许 container。

/container/envs
add list=foxos-env key=FOXOS_INSTALL_MARKER value="foxos"
add list=foxos-env key=FOXOS_API_TOKEN value="CHANGE_ME_AT_LEAST_32_RANDOM_CHARACTERS"
add list=foxos-env key=FOXOS_CONFIRMATION_KEY value="CHANGE_ME_DIFFERENT_32_RANDOM_CHARACTERS"
add list=foxos-env key=FOXOS_ROUTEROS_URL value="http://10.0.0.1"
add list=foxos-env key=FOXOS_ROUTEROS_USERNAME value="foxos-service"
add list=foxos-env key=FOXOS_ROUTEROS_PASSWORD value="CHANGE_ME_ROUTEROS_SERVICE_PASSWORD"
add list=foxos-env key=FOXOS_MIHOMO_URL value="http://10.0.0.2:9090"
add list=foxos-env key=FOXOS_MIHOMO_PROXY_URL value="http://10.0.0.2:7890"
add list=foxos-env key=FOXOS_MIHOMO_SECRET value="CHANGE_ME_MIHOMO_CONTROLLER_SECRET"
add list=foxos-env key=FOXOS_MIHOMO_LOCAL_CONFIG value="/data/mihomo/config.yaml"
add list=foxos-env key=FOXOS_MIHOMO_RUNTIME_CONFIG value="/root/.config/mihomo/config.yaml"
add list=foxos-env key=FOXOS_MIHOMO_BACKUP_DIR value="/backups/mihomo"
add list=foxos-env key=FOXOS_MOSDNS_URL value="http://10.0.0.3:53"
add list=foxos-env key=FOXOS_BACKUP_DIR value="/backups/foxos"
