# FoxOS 全栈快速安装（RouterOS x86_64）

这份安装包用于下列固定环境：

| 组件 | 地址 | 说明 |
|---|---:|---|
| RouterOS | `10.0.0.1` | 宿主与现有网关 |
| Mihomo | `10.0.0.2` | RouterOS Container |
| MosDNS | `10.0.0.3` | RouterOS Container |
| FoxOS | `10.0.0.4:8090` | RouterOS Container |
| 管理桥 | `bridge-lan` | 已存在 |
| 架构 | `x86_64` | amd64 安装包 |

安装脚本不会修改 DNS、DHCP、NAT、默认路由、Mangle 或现有防火墙。你的 LAN DHCP 池从 `10.0.0.10` 开始，因此 `.2`、`.3`、`.4` 不会与 DHCP 客户端冲突。

## 安装前

1. 用 `/system/resource/print` 确认 `architecture-name=x86_64`。
2. 用 `/system/package/print where name="container"` 确认已安装与 RouterOS **完全相同版本**的 `container` package。如果没有输出，请先从 MikroTik 对应版本的 Extra packages 中取得 x86 `container-*.npk`，上传到 RouterOS 并重启。
3. 用 `/system/device-mode/print` 确认 `container: yes`。
4. 如果刚执行过 `/system/device-mode/update container=yes`，请按 RouterOS 提示完成物理确认；x86 通常需要彻底断电后再开机。
5. 执行 `/container/print`，确认命令有效，并且目前没有旧的 FoxOS、Mihomo、MosDNS 容器。
6. 用 `/system/resource/print` 确认可用磁盘空间至少 `512 MiB`。安装器会再次检查。
7. 备份 RouterOS：

   ```routeros
   /export hide-sensitive file=before-foxos
   /system/backup/save name=before-foxos
   ```

## 第 1 步：在 Windows 电脑生成本机密钥

解压 GitHub Actions 下载的 ZIP。进入解压后的 `foxos-full-amd64-*` 目录，在 PowerShell 中执行：

```powershell
Set-ExecutionPolicy -Scope Process Bypass
.\prepare-install.ps1
```

脚本会：

- 从你自己的公开仓库 `foxc888/foxos` 的 `agent/foxos-core` 分支下载 Mihomo、MosDNS 配置到本机安装目录；配置不会被重新发布到 GitHub Actions Artifact；
- 自动生成 FoxOS API Token、确认密钥、RouterOS 专用服务密码和 Mihomo Controller 密钥；
- 生成 `foxos-full-install.local.rsc`；
- 只在本地修改 `mihomo-config/config.yaml` 的 `secret`，不会修改任何 DNS 字段；
- 生成仅保存在电脑上的 `FOXOS-LOGIN.txt`。

不要把 `FOXOS-LOGIN.txt` 或 `foxos-full-install.local.rsc` 提交到 GitHub。

如果电脑无法访问 GitHub，但已经有本仓库源码，可指定本地源码目录：

```powershell
.\prepare-install.ps1 -ConfigSourceDirectory "D:\foxos-agent-foxos-core"
```

## 第 2 步：上传到 RouterOS

用 WinBox 打开 **Files**，把以下内容拖到 RouterOS 文件根目录：

```text
foxos-amd64.tar
mihomo_amd64.tar
mosdns-amd64.tar
mihomo-config/
mosdns-config/
foxos-full-install.local.rsc
foxos-start-all.rsc
```

请保留目录结构，不要只上传目录中的单个文件。镜像和配置合计较大，等待 WinBox 上传完成后再继续。

## 第 3 步：导入全栈

在 RouterOS Terminal 执行：

```routeros
/import file-name=foxos-full-install.local.rsc
```

该脚本会进行预检，然后创建：

- 专用用户 `foxos-service` 和最小化权限组 `foxos-rest`；
- `veth-mihomo`、`veth-mosdns`、`veth-foxos`；
- 三个持久化配置/数据挂载；
- Mihomo、MosDNS、FoxOS 三个容器。

反复执行：

```routeros
/container/print
```

等待三个容器全部显示 `status=stopped`。镜像导入期间不要重启 RouterOS。

## 第 4 步：首次启动

三个容器全部为 `stopped` 后执行：

```routeros
/import file-name=foxos-start-all.rsc
```

然后检查：

```routeros
/container/print
/log/print where topics~"container"
```

三个容器应逐步变为 `running`。浏览器打开：

```text
http://10.0.0.4:8090
```

把电脑上 `FOXOS-LOGIN.txt` 里的 API Token 填入 FoxOS 登录/设置页。

## 为什么是两次 import

RouterOS 在 `/container/add file=...` 后会异步解压镜像，而且不会自动进行首次启动。官方要求等待容器变为 `status=stopped` 后再启动。因此这里使用“两次 import”，避免用固定延迟猜测解压时间。

## 安装后清理

确认三项服务正常后：

1. 把 `FOXOS-LOGIN.txt` 的 Token 存到密码管理器并删除该文件；
2. 在 RouterOS Files 中删除 `foxos-full-install.local.rsc`；
3. 镜像 tar 可在容器正常运行后删除以释放空间；不要删除 `mihomo-config`、`mosdns-config`、`foxos-data` 或 `foxos-backups`。

```routeros
/file/remove [find where name="foxos-full-install.local.rsc"]
/file/remove [find where name="foxos-amd64.tar"]
/file/remove [find where name="mihomo_amd64.tar"]
/file/remove [find where name="mosdns-amd64.tar"]
```

## 常见故障

### `failure: not allowed by device-mode`

容器模式尚未完成物理确认：

```routeros
/system/device-mode/update container=yes
```

按照终端提示确认；x86 通常需要彻底断电再开机，然后重新检查 `/system/device-mode/print`。

### `bad command name container`

当前 RouterOS 没有安装 `container` package，或 package 与 RouterOS 主系统版本不一致。安装匹配版本的 x86 `container-*.npk` 并重启，再执行：

```routeros
/system/package/print where name="container"
/container/print
```

### 提示缺少文件或目录

回到 WinBox **Files**，确认七项上传内容都在 RouterOS 根目录，且目录名完全一致。

### 容器长期不是 `stopped`

```routeros
/container/print detail
/log/print where topics~"container"
/system/resource/print
/file/print
```

重点检查磁盘空间、镜像上传是否完整和 Container package/device-mode。

### 页面打不开

```routeros
/container/print detail
/ping 10.0.0.4
/log/print where topics~"container"
```

确认 `foxos:admin` 为 `running`，客户端位于 `10.0.0.0/24`，并且你的现有 `bridge-lan` 输入规则仍允许 LAN 管理。

## 安全提醒

- 不要把 RouterOS 的 `www` 或 FoxOS `8090` 暴露到 WAN。
- 当前内网 REST 使用 HTTP，凭据会以明文在 LAN 上传输；后续生产化建议配置 `www-ssl` 和可信证书。
- 如果密码、Token、节点订阅或私钥曾发到聊天或公开 GitHub，请立即轮换。
- 本地脚本会复制你仓库中已有的 Mihomo/MosDNS 配置；如果其中节点凭据是真实的，而仓库曾公开，这些凭据也必须先轮换。
