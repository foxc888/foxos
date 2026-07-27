# FoxOS 只读变更计划
# 运行本文件不会写入 RouterOS。它用于在执行 foxos-full-install.rsc 前展示精确范围。

:put "=== FoxOS planned RouterOS changes ==="
:put "Prerequisites: RouterOS 7.21+, architecture-name=x86, container package version matches, device-mode container=yes, bridge-lan, disk1."
:put "Reserved management addresses: RouterOS 10.0.0.1, Mihomo 10.0.0.2, MosDNS 10.0.0.3, FoxOS 10.0.0.4:8090."
:put "Create/reuse owned resources only:"
:put "  user group foxos-rest and user foxos-service"
:put "  env list foxos-env (FOXOS_INSTALL_MARKER=foxos), attached only to the FoxOS container; status endpoints use Mihomo :9090/:7890 and MosDNS TCP :53"
:put "  mounts foxos-mihomo-runtime, foxos-mihomo-config, foxos-mosdns-runtime, foxos-data, foxos-backups"
:put "  veth-mihomo, veth-mosdns, veth-foxos and bridge-lan ports"
:put "  containers foxos-mihomo, foxos-mosdns, foxos-active"
:put "Files changed: disk1/mihomo-config/config.yaml secret field only; persistent FoxOS data and backup directories are created on first start."
:put "Explicitly untouched: DNS, DHCP/DHCP DNS, default routes, NAT, Mangle, firewall and user-owned resources."
:put "Backup before execution: /export hide-sensitive file=before-foxos and /system/backup/save name=before-foxos."
:put "Rollback: stop FoxOS containers, restore the RouterOS backup, keep old image tar and disk1/foxos-data."
:put "Run preflight.rsc first. Continue only after every check passes and an operator has confirmed this exact plan."
