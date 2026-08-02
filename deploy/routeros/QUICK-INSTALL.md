# FoxOS 全栈快速安装（RouterOS x86_64）

本包用于备用 RouterOS 或 CHR 验收。仓库已完成自动化、浏览器、静态脚本和 Linux 网络命名空间验证，但尚未执行 CHR 或实体 RouterOS 验收；容器 `running`、CI 绿色或模拟连通都不能记为 RouterOS 部署成功。

当前 RC1 的 `7.21+` 表示脚本语法下限，不表示所有后续版本已经兼容。目标设备的精确版本必须先通过第 1 节的同版本 CHR container 契约门禁，实际证明复数 `envlists`/`mountlists`、命名挂载 source 规范化、五个 `mode=rw` mount、唯一 `mode=ro` secret mount、四文件可读、敏感 env/日志值为零和卸载零残留，才可进入备用设备安装。最短安全路径固定为：下载同一 SHA 制品 -> 工作站两层校验 -> 同版本 CHR 兼容门禁 -> 同 SHA Artifact 生命周期门禁 -> 封存唯一站点清单 -> 加密备份并下载 -> 零碰撞检查 -> 上传 -> 只读 doctor/plan -> 摘要确认 install -> start -> verify。

## 0. 先审核站点清单

发布包中的 `site-config.example.rsc` 是不可变模板。先在工作站复制出唯一可编辑清单并独立封存：

工作站需要 Bash、Python 3、`tar`、`unzip`，以及 OpenSSL 或 `shasum`；命令行上传还需要 OpenSSH `scp`。缺少任一依赖时先补齐，不要在 RouterOS 上尝试运行这些工作站命令。

```bash
cp site-config.example.rsc site-config.rsc
${EDITOR:-vi} site-config.rsc
./seal-site-config.sh site-config.rsc
```

`site-config.rsc` 及其 `site-config.rsc.sha512` 是 RouterOS 部署的唯一拓扑来源；它们故意不进入发布包 `SHA256SUMS`，避免编辑清单破坏供应链摘要。`site-config.rsc` 只作为数据，不得直接 import；RouterOS 只能 import 包内固定的 `load-site-config.rsc`，由 loader 先核对独立 SHA-512 和 11 项精确赋值白名单，再设置全局值。默认样例如下：

| 项目 | 默认值 |
|---|---|
| 管理桥 | `bridge-lan` |
| 存储根 | `disk1` |
| 管理网段 | `10.0.0.0/24` |
| RouterOS | `10.0.0.1` |
| Mihomo | `10.0.0.2` |
| MosDNS | `10.0.0.3` |
| FoxOS | `10.0.0.4` |
| 管理主机名 | `foxos.home.arpa` |

四个服务地址必须唯一、可用且位于管理网段内，RouterOS 地址必须以清单中的 prefix 配置在管理桥上；hostname 必须是小写 `home.arpa` 子域。不要在其他脚本中改地址。每次运行 plan/install、DNS、upgrade、rollback、cleanup、uninstall 或 verify 前都 import 同一份不可变 loader；preflight 会在 RouterOS 上重算 SHA-512，并核对 loader 对当前内容和每个字段的证明。

外置存储仍是推荐方式：`FoxOSSiteStorageRoot "disk1"` 表示必须存在且唯一的 `/disk slot=disk1`。没有 `/disk` 对象、但系统盘容量和耐久度足够的 x86 设备，可以显式设为唯一保留值 `FoxOSSiteStorageRoot "foxos"`；此时 preflight/doctor/install 从 `/system/resource free-hdd-space` 读取系统盘空间。除此之外的任何值都按外置磁盘槽位处理，拼错的 `disk1` 不会自动降级到系统盘。

下文命令使用默认 `disk1`。内部系统盘模式必须把所有 `file-name=disk1/...` 和工作站 `storage_root=disk1` 一并替换为 `foxos`，不能只改其中一处。

官方 RouterOS 的 amd64 设备通常报告 `architecture-name=x86`；安装脚本也把非标准环境返回的 `x86_64` 归一为 Linux `amd64`，但会输出目标环境兼容警告，不能据此声称官方 CHR 或实体 RouterOS 已验收。其他架构继续拒绝 amd64 镜像。

## 安全边界

安装器只创建或复用匹配 FoxOS owner 的服务用户、env、mount、三个 veth/bridge port 和三个容器。它不会：

- 创建或接管管理桥、磁盘、RouterOS 管理地址或 REST 服务。
- 启用/修改 RouterOS DNS、DHCP server/network、DHCP 下发 DNS。
- 修改默认路由、NAT、Mangle、Filter、FastTrack 或用户防火墙。
- 建立 Mihomo TUN/redir/透明代理数据平面。
- 覆盖没有 `foxos:` marker/comment 的同名资源。

FoxOS 只检查 MosDNS TCP 53；MosDNS 9099 API 仅监听容器 loopback，包内不提供其独立管理 UI。

`foxos-env` 只保存非敏感配置。首次安装在站点存储根的 `foxos-secrets/` 下生成 `api-token`、`confirmation-key`、`routeros-password`、`mihomo-secret` 四个文件，并通过唯一的 `foxos-secrets` 只读挂载提供给 FoxOS 管理容器。管理容器仍固定 `logging=no` 作为纵深防护；若验收发现敏感键名或值进入新日志，立即停止容器并轮换本次全部秘密。Mihomo 与 MosDNS 不继承这些秘密，保留 `logging=yes` 便于诊断。

可选 DNS 脚本只在精确确认后添加一条 `foxos:dns:admin` A 记录，不启用 DNS，不改 upstream，不改 DHCP。只有原本使用 RouterOS DNS 的客户端才会得到该记录。

## 1. RouterOS 前置条件

必须全部满足：

- RouterOS 7.21 或更高的语法下限，`architecture-name=x86`，或待目标机验收的非标准 `x86_64`；目标完整版本必须先通过下述同版本 CHR 门禁。
- 安装且启用与 RouterOS 完全同版本的 x86 `container` package。
- `/system/device-mode get container` 与 `get scheduler` 都为 `yes`。启用其中任一能力都可能按 MikroTik 官方流程要求设备操作者在物理设备上确认；脚本只读检查，绝不代为修改 device-mode。
- 清单指定的管理桥和 RouterOS 地址已存在且唯一；存储要么是唯一 `/disk` 槽位，要么显式使用内部保留根 `foxos`。
- 上传完整包后存储仍至少有 512 MiB 可用。
- RouterOS `www`/REST 已启用在 TCP 80，并限制为清单网段或 FoxOS `/32`。
- Mihomo、MosDNS、FoxOS 三个保留地址未被 RouterOS address、DHCP Lease、其他 veth、ARP 或在线主机占用。

部署候选还必须先在同版本、可丢弃的 CHR 上保存以下命令的原始输出，并用包内 `chr-envlists-smoke.rsc` 完成一次最小 env、`mode=rw` mount、VETH、带 `envlists`/`mountlists` 容器的 add/get/delete 回读。先把 smoke 脚本与包内 `foxos-upgrade-<release-id>/foxos-amd64.tar` 上传到 CHR 的临时存储，再运行：

```routeros
/console/inspect request=completion input="/container/add "
/console/inspect request=completion input="/container/mounts/add "
/console/inspect request=completion input="/container/mounts/add mode="
:global FoxOSCHREnvlistsSmokeStorageRoot "disk1"
:global FoxOSCHREnvlistsSmokeImagePath "disk1/foxos-upgrade-<release-id>/foxos-amd64.tar"
:global FoxOSCHREnvlistsSmokeConfirm "RUN-ON-DISPOSABLE-CHR"
/import file-name=disk1/chr-envlists-smoke.rsc
```

完整准备、证据与失败清理要求见包内 `chr-envlists-smoke.md`。基础 smoke 必须证明目标完整版本的命令元数据与实际回读都接受复数 `envlists`/`mountlists`，mount source 的 raw/normalized 身份一致且命名挂载为 `mode=rw`，残留计数全为零并最终出现 `CHR_ENVLISTS_SMOKE PASS`。随后还必须用同 SHA Artifact 在同版本可丢弃 CHR 验证四个固定 secret files、唯一 RO secret mount、管理容器无敏感 env/日志值、升级保留和卸载零残留。两层门禁都通过后，该候选才能进入后续实体首装、升级、回滚和卸载验收。升级 RouterOS patch/minor 后必须重新执行；不能把实体设备作为第一次拼写或生命周期试验对象。

安装器会创建仅含 `read,write,api,rest-api` 的 `foxos-service` 专用组，并把用户登录来源限制为 FoxOS VETH `/32`。RouterOS 7.23.2 的 REST 登录需要 `rest-api`，后续命令执行还需要 `api`；缺少后者会出现先登录成功、再 `via api` 失败并返回 HTTP 500。RouterOS REST 在管理 LAN 内仍是 HTTP，因此管理 LAN 必须可信且隔离；不得暴露到 WAN。FoxOS 浏览器/API 访问则强制使用本地 CA 保护的 HTTPS。

只读人工检查示例；内部 `foxos` 模式允许 `/disk/print` 为空，但 `free-hdd-space` 必须满足空间门禁：

```routeros
/system/resource/print
/system/package/print where name="container"
/system/device-mode/print
/interface/bridge/print
/disk/print
/ip/address/print
/ip/service/print where name="www" && dynamic=no
/container/print
```

## 2. 下载并验证

### Core CI artifact

先确认同一 40 位 `<commit>` 的 `FoxOS Core CI` 与独立 `FoxOS CodeQL` 都为绿色，打开该次 Core CI run 的 `Artifacts`，下载唯一的 `foxos-full-amd64-<commit>`。浏览器下载的是同名 ZIP，先解开外层 artifact ZIP，再验证包：

```bash
ci_commit="REPLACE_WITH_40_CHARACTER_COMMIT_SHA"
ci_artifact="foxos-full-amd64-${ci_commit}"
ci_download_dir="${ci_artifact}-download"
mkdir -- "${ci_download_dir}"
unzip "${ci_artifact}.zip" -d "${ci_download_dir}"
cd "${ci_download_dir}"
sha256sum --check "${ci_artifact}.tar.gz.sha256"
tar -xzf "${ci_artifact}.tar.gz"
cd "${ci_artifact}"
sha256sum --check SHA256SUMS
```

### GitHub Release

从 GitHub Release 下载同一版本的 `.tar.gz` 与 `.tar.gz.sha256`，在这两个文件所在目录独立执行：

```bash
release_id="REPLACE_WITH_RELEASE_TAG"
release_artifact="foxos-full-amd64-${release_id}"
sha256sum --check "${release_artifact}.tar.gz.sha256"
tar -xzf "${release_artifact}.tar.gz"
cd "${release_artifact}"
sha256sum --check SHA256SUMS
```

不得混用 `<commit>` 与 `<release-id>` 两套名称，也不得用另一提交的 CodeQL 结果替代。macOS 把所选代码块内两处 `sha256sum --check` 分别改为 `shasum -a 256 -c`。三个镜像 tar 是 RouterOS 所需的单层、未压缩 Docker v1 archive，不要继续解压或转换。它们由 workflow 从固定来源构建并逐张扫描；同时审核 `provenance/*.lock.json`。RouterOS preflight 只检查文件存在性和最小大小，密码学 checksum 必须在上传前由工作站验证。

仍在该解压目录中按第 0 节生成并审核 `site-config.rsc` 与 `.sha512`。封存后不要再编辑；若要修改，必须重新封存、重新运行 plan 并重新确认。

## 3. 先备份、检查零碰撞，再上传

在 RouterOS 接收任何 FoxOS 文件前，先使用一个从未用过的时间戳名称导出配置并保存 AES 加密 binary backup；将示例中的 `YYYYMMDD-HHMM` 和密码替换为本次维护窗口的唯一值：

```routeros
/export hide-sensitive file=before-foxos-YYYYMMDD-HHMM
/system/backup/save name=before-foxos-YYYYMMDD-HHMM password="<unique-offline-password>" encryption=aes-sha256
```

没有密码的 RouterOS v7 binary backup 不会加密。先确认 `.rsc` 与 `.backup` 均存在，再通过 WinBox `Files` 下载到离线位置，或从工作站执行：

```bash
router_address="REPLACE_WITH_ROUTEROS_MANAGEMENT_ADDRESS"
backup_id="before-foxos-YYYYMMDD-HHMM"
backup_dir="../routeros-backups/${backup_id}"
umask 077
mkdir -p -- "$backup_dir"
chmod 700 "$backup_dir"
scp "admin@${router_address}:${backup_id}.rsc" "${backup_dir}/${backup_id}.rsc"
scp "admin@${router_address}:${backup_id}.backup" "${backup_dir}/${backup_id}.backup"
test -s "${backup_dir}/${backup_id}.rsc" && test -s "${backup_dir}/${backup_id}.backup"
shasum -a 256 "${backup_dir}/${backup_id}.rsc" "${backup_dir}/${backup_id}.backup"
```

`backup_dir` 必须位于当前 bundle 目录之外，也不能是它的子目录；这样后续 `scp -r ./*` 不会把备份重新上传到设备。完成后再把该 `0700` 目录移到离线备份介质。RouterOS binary restore 会重启并覆盖设备配置，只能在同版本、同设备的维护窗口再次明确确认后执行。它不保护普通文件存储，因此后面的零碰撞检查不能省略。

首次安装最终会把解压目录中的全部内容上传到站点存储根，不要再套一层全量包目录。`foxos-upgrade-<release-id>/` 本身必须保持为一个子目录；至少应有：

```text
disk1/site-config.rsc
disk1/site-config.rsc.sha512
disk1/site-config.example.rsc
disk1/seal-site-config.sh
disk1/load-site-config.rsc
disk1/chr-envlists-smoke.md
disk1/chr-envlists-smoke.rsc
disk1/foxos-doctor.rsc
disk1/mihomo_amd64.tar
disk1/mosdns-amd64.tar
disk1/foxos-upgrade-<release-id>/foxos-amd64.tar
disk1/foxos-upgrade-<release-id>/SHA256SUMS
disk1/foxos-upgrade-<release-id>/UPGRADE-MANIFEST.txt
disk1/foxos-upgrade-<release-id>/upgrade-inspect.rsc
disk1/foxos-upgrade-<release-id>/upgrade-plan.rsc
disk1/foxos-upgrade-<release-id>/upgrade.rsc
disk1/foxos-upgrade-<release-id>/upgrade-promote-inspect.rsc
disk1/foxos-upgrade-<release-id>/upgrade-promote-plan.rsc
disk1/foxos-upgrade-<release-id>/upgrade-promote.rsc
disk1/foxos-upgrade-<release-id>/rollback-inspect.rsc
disk1/foxos-upgrade-<release-id>/rollback-plan.rsc
disk1/foxos-upgrade-<release-id>/rollback.rsc
disk1/foxos-upgrade-<release-id>/upgrade-cleanup-inspect.rsc
disk1/foxos-upgrade-<release-id>/upgrade-cleanup-plan.rsc
disk1/foxos-upgrade-<release-id>/upgrade-cleanup-apply.rsc
disk1/provenance/mihomo-container.lock.json
disk1/provenance/mosdns-container.lock.json
disk1/mihomo-config/config.yaml
disk1/mihomo-config/base.yaml
disk1/mosdns-config/config_custom.yaml
disk1/preflight.rsc
disk1/foxos-install-inspect.rsc
disk1/foxos-plan.rsc
disk1/foxos-full-install.rsc
disk1/foxos-start-all.rsc
disk1/foxos-verify.rsc
disk1/foxos-dns-plan.rsc
disk1/foxos-dns-apply.rsc
disk1/foxos-uninstall-inspect.rsc
disk1/uninstall-plan.rsc
disk1/uninstall-apply.rsc
disk1/SHA256SUMS
disk1/RELEASE-MANIFEST.txt
disk1/QUICK-INSTALL.md
```

在上传前，把下面的 `disk1` 改成封存站点配置中的 `FoxOSSiteStorageRoot`，并把 `<release-id>` 改成解压目录里的实际版本目录名。该清单覆盖本次 `scp -r ./*` 的每个顶层目标；任何计数不为零都必须中止：

```routeros
:local storageRoot "disk1"
:local uploadTargets {"QUICK-INSTALL.md";"RELEASE-MANIFEST.txt";"SHA256SUMS";"chr-envlists-smoke.md";"chr-envlists-smoke.rsc";"foxos-dns-apply.rsc";"foxos-dns-plan.rsc";"foxos-doctor.rsc";"foxos-full-install.rsc";"foxos-install-inspect.rsc";"foxos-plan.rsc";"foxos-start-all.rsc";"foxos-uninstall-inspect.rsc";"foxos-verify.rsc";"load-site-config.rsc";"mihomo-config";"mihomo_amd64.tar";"mosdns-amd64.tar";"mosdns-config";"preflight.rsc";"provenance";"seal-site-config.sh";"site-config.example.rsc";"site-config.rsc";"site-config.rsc.sha512";"foxos-upgrade-<release-id>";"uninstall-apply.rsc";"uninstall-plan.rsc"}
:local uploadCollisions 0
:foreach target in=$uploadTargets do={
  :local path ($storageRoot . "/" . $target)
  :local count [:len [/file find where name=$path]]
  :put ("UPLOAD-TARGET " . $path . " count=" . $count)
  :set uploadCollisions ($uploadCollisions + $count)
}
:put ("UPLOAD-COLLISIONS total=" . $uploadCollisions)
:if ($uploadCollisions > 0) do={ :error "upload target collision; archive and investigate every existing path before continuing" }
```

只有最终输出 `UPLOAD-COLLISIONS total=0` 才能继续。若存在碰撞，单独下载并归档相关普通文件或目录，确认它们不属于既有 FoxOS/其他服务后再制定处理方案；不要删除、改名或直接覆盖。保留的 `foxos-data`、`foxos-backups`、`foxos-secrets`、RO secret mount 或 FoxOS env 表示这不是空白首装，应转入升级或恢复流程。

零碰撞后，使用 WinBox 时，内部模式先在 `Files` 中创建或确认空的顶层 `foxos` 目录，外置模式使用清单指定的磁盘目录。打开该存储根，选中解压目录内的全部内容并拖入；不要拖入外层 `foxos-full-amd64-*` 目录。使用命令行时，在已完成两层校验并生成 `site-config.rsc.sha512` 的解压目录执行：

```bash
router_address="REPLACE_WITH_ROUTEROS_MANAGEMENT_ADDRESS"
storage_root=disk1
scp -r ./* "admin@${router_address}:${storage_root}/"
```

上传完成后在 RouterOS 终端运行以下只读计数；每项必须为 `1`，随后第 4 节的 preflight 会检查完整清单和文件下限：

```routeros
:foreach required in={"disk1/SHA256SUMS";"disk1/load-site-config.rsc";"disk1/foxos-doctor.rsc";"disk1/foxos-plan.rsc";"disk1/foxos-full-install.rsc"} do={ :put ($required . " count=" . [:len [/file find where name=$required]]) }
```

先加载封存清单并运行严格只读的首装 doctor；它只读取版本、架构、package、device-mode、桥、地址、磁盘、REST 和保留的 FoxOS 对象名/数量，不读取 env value、密码、脚本 source、文件内容或日志：

```routeros
/import file-name=disk1/load-site-config.rsc
/import file-name=disk1/foxos-doctor.rsc
```

空白首装要求最终输出 `PASS|summary|needs-action-count=0|conflict-count=0|first-install=ready-for-plan`。`NEEDS-ACTION` 表示先补齐主机前置资源；`CONFLICT` 表示存在同名或保留状态，禁止把它当作空白首装覆盖。doctor 不替代下一节绑定 release 文件、地址占用和完整前态的 preflight/plan，也不证明 CHR 门禁或实体设备验收。

## 4. 只读预检与精确计划

```routeros
/import file-name=disk1/load-site-config.rsc
/import file-name=disk1/foxos-plan.rsc
```

`foxos-plan.rsc` 会自动重跑 preflight 和共享 inspector。preflight 必须以 `PRECHECK PASSED` 结束；计划逐项列出 `CREATE/REUSE/FAIL`、站点地址、`23/24` 键非敏感 FoxOS env 安全基线（站点清单显式配置私网订阅 allowlist 时为 24 键）、四个 secret files、五个可写 mount、唯一只读 secret mount、veth、bridge port、容器、不触碰项和回滚路径，并输出 `APPROVED PLAN SHA-512`。到此没有写入。

若设备、清单、备份、计划或回滚路径有任何不确定，不执行下一节。

## 5. 创建资源并导入镜像

操作者确认精确计划后：

```routeros
:global FoxOSInstallConfirmation "<paste APPROVED PLAN SHA-512>"
/import file-name=disk1/foxos-full-install.rsc
```

不要输入尖括号文本，要原样粘贴计划输出的 128 位摘要；计划后不要改清单或 RouterOS 前态。正式安装器会在首次资源写入前重跑 preflight/inspector 并比较批准摘要、确认摘要和当前摘要，任一不一致都不写入。首次执行在 `foxos-secrets/` 中随机生成 `routeros-password`、`mihomo-secret`、`api-token` 和 `confirmation-key` 四个固定文件；四项都不会写入 `foxos-env`。重复执行只复用完整且有效的四文件集合；complete 安装缺失任一文件、目录存在额外文件、遗留敏感 env、未知额外非敏感 env、固定值不匹配或同名非 FoxOS 资源都会失败关闭，绝不隐式补齐或轮换秘密。

`/container/add file=...` 异步导入。等待三个容器全部 `status=stopped`：

```routeros
/container/print
/log/print where topics~"container"
```

导入期间不要重启。

## 6. 启动、导入 CA、自动验证

```routeros
/import file-name=disk1/load-site-config.rsc
/import file-name=disk1/foxos-start-all.rsc
```

启动脚本按 Mihomo、MosDNS、FoxOS 顺序启动，只验证容器进入 `running`。它接受 stopped/running 混合前态，不重复启动已运行容器；后序失败时停止本次启动的前序容器。此时三个 `start-on-boot` 都保持 `no`。

FoxOS 首次启动在 `foxos-data/tls` 生成持久 ECDSA 本地 CA 和 397 天叶证书。先验证包来源和存储路径，再导入 CA：

```routeros
/certificate/import file-name=disk1/foxos-data/tls/foxos-local-ca.pem passphrase=""
/certificate/print detail where common-name="FoxOS Local CA"
```

确认只有一张预期 CA，核对 SHA-256 指纹后设为 trusted：

```routeros
/certificate/set [find where common-name="FoxOS Local CA"] trusted=yes
/import file-name=disk1/load-site-config.rsc
/import file-name=disk1/foxos-verify.rsc
```

`foxos-verify.rsc` 必须同时通过容器 running、live、ready、站点清单、页面和带认证只读 API。它使用 HTTPS 且不会跳过证书验证。该结果只证明 RouterOS 到 FoxOS 管理面，不证明设备流量经过 Mihomo。

验证通过后，只通过 WinBox Files 或工作站 SCP 下载一次精确的 `<storage-root>/foxos-secrets/api-token`。WinBox 下载目标必须是受控工作站上的专用临时文件；命令行可按下例强制本地权限 `0600`：

```bash
router_address="REPLACE_WITH_ROUTEROS_MANAGEMENT_ADDRESS"
storage_root=disk1
umask 077
token_tmp="$(mktemp)"
scp "admin@${router_address}:${storage_root}/foxos-secrets/api-token" "$token_tmp"
chmod 0600 "$token_tmp"
```

从该文件导入离线密码库，不要在终端展开内容。确认密码库已保存后立即销毁临时副本：

```bash
rm -f -- "$token_tmp"
unset token_tmp
```

禁止使用 `:put`、`/file get ... contents`、`/container/envs ... value` 或任何终端输出读取秘密。不要把 Token 放入 URL、日志、截图或聊天。用该 Token 登录 `https://foxos.home.arpa`。

## 7. 可选本地 DNS 与无端口访问

先运行只读计划：

```routeros
/import file-name=disk1/load-site-config.rsc
/import file-name=disk1/foxos-dns-plan.rsc
```

若计划显示要新增记录，按其输出设置完全一致的确认字符串，再执行：

```routeros
:global FoxOSDNSConfirmation "ADD foxos.home.arpa 10.0.0.4"
/import file-name=disk1/load-site-config.rsc
/import file-name=disk1/foxos-dns-apply.rsc
```

自定义站点必须使用计划输出中的实际 hostname 和地址，不能照抄默认确认串。已有同名非 FoxOS 记录或内容不同会失败关闭。

CA 已导入客户端信任库且 DNS 可解析后，访问 `https://foxos.home.arpa`。DNS 尚未配置时可用证书包含的 IP SAN 访问 `https://<FoxOS 地址>`。`http://<FoxOS 地址>` 只返回到 public hostname 的 308 跳转，不承载 Bearer Token。

## 8. 实体 RouterOS 验收清单

以下项目都要保存设备输出、API 回读和网络证据；本仓库当前尚未完成：

1. 三个容器 owner 唯一、运行状态符合计划、`start-on-boot=no`，且唯一 owned 顺序启动 scheduler 已启用。
2. HTTPS CA、live、ready、页面、站点清单和只读依赖全部通过。
3. RouterOS、Mihomo、MosDNS 各自显示实时来源；单项失败不伪造在线。
4. DNS、DHCP network、默认路由、NAT、Mangle、Filter 和 FastTrack 与安装前 diff 符合“不接管”边界。
5. 使用可恢复测试设备完成动态 Lease 采用、确认、make-static、回读、审计和外部并发变更拒绝。
6. 验证 DHCP 容量口径：范围首尾包含，`.100-.200=101`、`.10-.254=245`，只有范围内排除一个保留地址才是 244。
7. 仅在 readiness 可用时验证 `blocked` 和 `l2tp`；主设备不得作为首个测试对象。
8. `mihomo-node` 和 `proxy-chain` 当前应返回不可用。不得用路由 marker、Controller 在线或 mixed port 出口替代透明入口、回程、管理旁路、FastTrack 和真实客户端出口 IP 证据。
9. 在维护窗口演练 pending 升级门禁、自动恢复和 SQLite 兼容回滚。
10. 下载并验证 RouterOS binary backup、FoxOS manifest 备份和旧镜像/root-dir 的恢复路径。

没有上述证据时，不得记录“实体部署成功”或“真实透明代理已启用”。

## 9. 升级、卸载与首次安装回滚

升级时绝对不要把新全量包覆盖到存储根。先在工作站验证新包和 `foxos-upgrade-<release-id>/SHA256SUMS`，只上传整个 `foxos-upgrade-<release-id>/` 子目录；保留现有 `site-config*`、loader、Mihomo/MosDNS 配置、数据、备份、containers、旧 payload、`foxos-secrets` 四文件和唯一 RO secret mount。升级 payload 不包含也不轮换秘密。以下每组命令都必须先审核 plan 输出，再原样粘贴它给出的 128 位摘要；不要输入尖括号文本。自定义存储根时，把命令中的 `disk1` 一并替换。

阶段 1 只导入并登记 stopped pending，当前 active 保持运行：

```routeros
/import file-name=disk1/load-site-config.rsc
/import file-name=disk1/foxos-upgrade-<release-id>/upgrade-plan.rsc
:global FoxOSUpgradeConfirmation "<paste APPROVED UPGRADE SHA-512>"
/import file-name=disk1/foxos-upgrade-<release-id>/upgrade.rsc
```

等待 `/container/print` 显示新 `foxos:pending` 为 `stopped`，再进入阶段 2。promote 会先让当前 active 创建 SQLite 兼容检查点，然后停止旧槽、启动并自动验收 pending；失败时按已批准流程请求恢复旧 active：

```routeros
/import file-name=disk1/load-site-config.rsc
/import file-name=disk1/foxos-upgrade-<release-id>/upgrade-promote-plan.rsc
:global FoxOSUpgradePromoteConfirmation "<paste APPROVED PROMOTE SHA-512>"
/import file-name=disk1/foxos-upgrade-<release-id>/upgrade-promote.rsc
```

检查点会先排空 HTTP 写请求和完整后台任务，再冻结 SQLite；普通写请求在终态确认前返回 503。checkpoint、promoted、aborted 都绑定同一 release ID 并做有界幂等重试。pending 启动或首次验收失败时，只有 plan 冻结的两个槽位身份仍完全匹配，脚本才按 `旧 active live -> aborted/restored -> ready` 恢复写入。每次写 promoted 前（包括 switched 重试）都会重新检查新 active 的 running、live、ready、页面、认证只读 API 和两个槽位身份。最终验收失败或 promoted 响应六次后仍不确定时，脚本保留新 active 与 stopped rollback，不会自动 abort 或回滚；检查依赖与日志后重新运行 `upgrade-promote-plan.rsc` 并按新摘要重试。不要在此状态下手工交换 owner。

需要回到旧版本时，使用同一版本化 payload。rollback 只操作 plan 冻结的 active/rollback 两个对象；旧二进制会在打开共享 SQLite 前验证并按需原子恢复检查点：

```routeros
/import file-name=disk1/load-site-config.rsc
/import file-name=disk1/foxos-upgrade-<release-id>/rollback-plan.rsc
:global FoxOSRollbackConfirmation "<paste APPROVED ROLLBACK SHA-512>"
/import file-name=disk1/foxos-upgrade-<release-id>/rollback.rsc
```

新版本和维护窗口回滚演练完成前不要归档回滚证据。验收完成后再运行 cleanup；它只把 plan 冻结的 stopped `foxos:rollback` 或 `foxos:rollback-complete` 改为 `foxos:retained`，不删除容器、root-dir、镜像、数据、备份或检查点：

```routeros
/import file-name=disk1/load-site-config.rsc
/import file-name=disk1/foxos-upgrade-<release-id>/upgrade-cleanup-plan.rsc
:global FoxOSUpgradeCleanupConfirmation "<paste APPROVED CLEANUP SHA-512>"
/import file-name=disk1/foxos-upgrade-<release-id>/upgrade-cleanup-apply.rsc
```

真实回滚后下一次升级必须使用新的 release ID，不能复用已经 retained 的容器名或 root-dir。

正常卸载必须先计划再确认：

```routeros
/import file-name=disk1/load-site-config.rsc
/import file-name=disk1/uninstall-plan.rsc
:global FoxOSUninstallConfirmation "<paste APPROVED UNINSTALL SHA-512>"
/import file-name=disk1/uninstall-apply.rsc
```

卸载 plan 会先确认 `foxos-backups/mihomo/.foxos-mihomo-apply.json` 不存在，并把四个 secret file 的 handle、长度和 SHA-512 evidence 绑定进摘要；apply 停止全部 owned 容器后、删除 scheduler、容器、env 或 secret files 前还会再次检查。发现 pending journal 时必须先恢复并清除该操作，不能手工删 journal，也不能删除或轮换四项秘密。

卸载 apply 只删除摘要中精确 owned 的 RouterOS 容器、veth/bridge port、mount、env、四个 secret files、空 secret 目录、服务用户/组和可选 owned DNS 记录；数据、配置、镜像、所有版本化 root-dir、备份、站点清单和本地 CA 仍保留。由于这些持久内容绑定原四项秘密，卸载后不能把保留的 `foxos-data` 或 `foxos-backups` 当作全新安装直接覆盖；安装 plan 会失败关闭。要复用保留状态，必须从验证过的离线备份恢复原四文件集合，不能只恢复 confirmation key；要全新安装，必须先验证备份并把旧数据、备份归档到非活动路径。不要手工批量删除未知用户资源，也不要递归删除存储根。

首次安装没有容器 rollback 槽，恢复路径按实际前态选择：

1. `foxos-plan.rsc` 或安装器在首个写入前失败：设备未变，不执行 restore；修正前置条件后重新生成 plan。
2. 安装器已创建 FoxOS-owned 资源，但 RouterOS 管理面仍正常：先运行 `uninstall-plan.rsc`，审核摘要后再执行 `uninstall-apply.rsc`，然后与本次 `before-foxos-<timestamp>.rsc` 做配置 diff；该路径删除四个 secret files、空 secret 目录和 RO mount，保留其他持久数据。
3. 设备需要精确回到安装前配置：确认本次 `before-foxos-<timestamp>.backup` 已下载且密码可用，只在同一设备、同一 RouterOS 版本的维护窗口通过 WinBox `Files` 上传备份，再在 `System -> Backup` 选择该文件执行 Restore。binary restore 会覆盖当前配置并重启；恢复后重新检查资源、package、bridge、address、service、container，并保留新的 `hide-sensitive` export 作为恢复证据。不要把 binary backup 恢复到另一台设备或另一 RouterOS 版本。

## 常见故障

本项目脚本下限为 RouterOS 7.21。若缺少 container package，上传与 RouterOS 完全同版本的 x86 包（x86 单包通常名为 `container-<version>.npk`），然后执行 `/system/package/apply-changes`。普通 `/system/reboot` 不会应用 7.21 的待安装 package；设备重启后必须用 `/system/package/print where name="container"` 回读版本与 disabled 状态，并在精确版本 CHR 上重新通过 container env/mount 契约门禁。

| 现象 | 检查 |
|---|---|
| 脚本要求站点清单 | 每个操作前是否重新 import 正确存储根下的不可变 `load-site-config.rsc`；不要直接 import 可编辑清单 |
| `bad command name container` | 同版本 x86 container package 是否已上传、执行 `/system/package/apply-changes`，并在重启后回读为 enabled |
| `not allowed by device-mode` | `container=yes` 与 `scheduler=yes` 是否都完成所需物理确认 |
| preflight 地址/存储失败 | `site-config.rsc` 是否与现有管理桥、地址和存储完全一致；内部模式是否精确写成 `foxos`，外置模式是否存在同名唯一 `/disk slot` |
| 镜像长期不为 stopped | checksum、文件大小、磁盘、package、container 日志 |
| FoxOS running 但 verify 失败 | CA 是否唯一且 trusted、HTTPS 443、SQLite/挂载、RouterOS REST、Mihomo 9090、MosDNS TCP 53 |
| REST 先登录成功、随后 `via api` 失败 | `foxos-rest` 是否精确包含 `read,write,api,rest-api`；不要轮换密码或扩大用户来源地址 |
| hostname 不解析 | 客户端是否原本使用 RouterOS DNS；DNS plan/apply 是否完成 |
| API 返回 `https_required` | 是否仍在用普通 LAN HTTP 或直接访问内部 8090 |
| Mihomo 设备出口不可选 | 当前预期行为；查看 capability `missing`，不要伪造透明数据平面 |

升级、应用回滚和证据归档使用第 9 节的包内命令。
