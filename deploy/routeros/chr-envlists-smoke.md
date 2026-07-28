# CHR `envlists` 兼容门禁

这是只允许在可丢弃 CHR 上运行的破坏性兼容测试。禁止在生产路由、保存 FoxOS 数据的
路由或实体 RouterOS 硬件上运行。通过该门禁不等于实体 RouterOS 已验收。

门禁用于证明某一组精确 RouterOS/container package 版本能够用复数 `envlists` 创建并
回读容器。脚本创建随机命名的临时 env、VETH、container 和 root-dir，执行有界停稳检查，
并且只有完整清理后才输出 `CHR_ENVLISTS_SMOKE PASS`。它不会加载 FoxOS 站点清单、连接
bridge、启动测试容器、修改 device-mode 或选择任何 `foxos:*` 正式对象。

## 准备可丢弃 CHR

- 使用与计划部署目标完全相同的 RouterOS patch 版本。
- 使用 x86 CHR，并安装、启用完全同版本的 `container` package。
- 测试前启用 `device-mode container=yes`；脚本只读回读，不会修改 device-mode。
- 挂载可丢弃的可写数据盘，剩余空间至少是测试镜像两倍再加 64 MiB。
- 上传 `chr-envlists-smoke.rsc` 与包内有效的 amd64 RouterOS container tar。脚本读取 tar，
  但不会启动或删除它。

例如，把脚本和版本化 `foxos-amd64.tar` 上传到可丢弃 CHR 的 `disk1` 后运行：

```routeros
/console/inspect request=completion input="/container/add "
:global FoxOSCHREnvlistsSmokeStorageRoot "disk1"
:global FoxOSCHREnvlistsSmokeImagePath "disk1/foxos-upgrade-<release-id>/foxos-amd64.tar"
:global FoxOSCHREnvlistsSmokeConfirm "RUN-ON-DISPOSABLE-CHR"
/import file-name=disk1/chr-envlists-smoke.rsc
```

保留完整终端输出。同一次运行必须同时具备：

- 完整 `/system/resource`、container package 和 device-mode 证据；
- env item、VETH 与 container `envlists` 的精确回读；
- 有界 stop 路径后的 `status=stopped`；
- `CLEANUP` 中所有残留计数均为零；
- 最后一行 `CHR_ENVLISTS_SMOKE PASS`。

任何 error、超时、缺少 PASS 或非零残留计数都表示失败。错误会携带随机 run ID；保留证据
并丢弃或恢复该 CHR，不要把它继续用作部署目标。
