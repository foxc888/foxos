# CHR container env/mount 兼容门禁

这是只允许在可丢弃 CHR 上运行的破坏性兼容测试。禁止在生产路由、保存 FoxOS 数据的
路由或实体 RouterOS 硬件上运行。通过该门禁不等于实体 RouterOS 已验收。

门禁用于证明某一组精确 RouterOS/container package 版本能够使用复数 `envlists`、
`mountlists`、规范化 mount source 和命名挂载的 `mode=rw` 契约创建并回读容器。RouterOS
可能为 source 回读添加一个前导 `/`；脚本同时保留 raw 值，并只移除这一个差异后比较身份。
脚本创建随机命名的临时 env、三个有序 RW mounts、VETH、container 和 root-dir，要求
`mountlists` 原生回读为长度 3 的 array，并按原顺序规范化为精确 CSV。执行有界停稳检查后，
只有完整清理才输出
`CHR_ENVLISTS_SMOKE PASS`。文件名和 PASS 标识为兼容既有制品而保留。它不会加载 FoxOS
站点清单、连接 bridge、启动测试容器、修改 device-mode 或选择任何 `foxos:*` 正式对象。

## 准备可丢弃 CHR

- 使用与计划部署目标完全相同的 RouterOS patch 版本。
- 使用 x86 CHR，并安装、启用完全同版本的 `container` package。
- 测试前启用 `device-mode container=yes`；脚本只读回读，不会修改 device-mode。
- 挂载可丢弃的可写数据盘，剩余空间至少是测试镜像两倍再加 64 MiB。
- 上传 `chr-envlists-smoke.rsc` 与包内有效的 amd64 RouterOS container tar。脚本读取 tar，
  把可丢弃存储根作为临时 mount source，但不会启动容器或删除该 tar。

例如，把脚本和版本化 `foxos-amd64.tar` 上传到可丢弃 CHR 的 `disk1` 后运行：

```routeros
/console/inspect request=completion input="/container/add "
/console/inspect request=completion input="/container/mounts/add "
/console/inspect request=completion input="/container/mounts/add mode="
:global FoxOSCHREnvlistsSmokeStorageRoot "disk1"
:global FoxOSCHREnvlistsSmokeImagePath "disk1/foxos-upgrade-<release-id>/foxos-amd64.tar"
:global FoxOSCHREnvlistsSmokeConfirm "RUN-ON-DISPOSABLE-CHR"
/import file-name=disk1/chr-envlists-smoke.rsc
```

保留完整终端输出。同一次运行必须同时具备：

- 完整 `/system/resource`、container package 和 device-mode 证据；
- env item、三个 mount source raw/normalized、`mode=rw`、VETH，以及 container `envlists` 的精确回读；
- container `mountlists` 的原生类型为 array、长度为 3，规范化后的名称和顺序精确匹配；
- 有界 stop 路径后的 `status=stopped`；
- `CLEANUP` 中 container、按 owner 查找的 mount、按三个 list 查找的 mount、VETH、env 和 root 残留计数均为零；
- 最后一行 `CHR_ENVLISTS_SMOKE PASS`。

任何 error、超时、缺少 PASS 或非零残留计数都表示失败。错误会携带随机 run ID；保留证据
并丢弃或恢复该 CHR，不要把它继续用作部署目标。
