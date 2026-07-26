# FoxOS 发布、安装、升级与回滚

本文适用于把 FoxOS 作为独立 RouterOS Container 部署。Mihomo 和 MosDNS 可以继续使用仓库中已有的运行包；FoxOS 不要求特定品牌的服务器，但最终运行设备必须满足 RouterOS Container 的架构、存储和网络要求。

## 1. 边界

- FoxOS：`10.0.0.4:8090`
- RouterOS：`10.0.0.1`
- Mihomo：`10.0.0.2:9090`
- MosDNS：`10.0.0.3:53`
- 本阶段不修改 RouterOS DNS、DHCP DNS、Mihomo DNS、MosDNS 配置。
- 不添加默认路由，不把整个 LAN 自动导向代理。
- 只有用户明确选择并确认的设备静态租约才允许写入。
- RouterOS L2TP 为只读清单，不能在当前版本通过 FoxOS 新增、编辑或删除。

## 2. 支持架构

GitHub `Release artifacts` 工作流构建：

| RouterOS 架构 | 构建产物 |
|---|---|
| `x86_64` | `foxos-amd64.tar` |
| `arm64` / `aarch64` | `foxos-arm64.tar` |

32 位 ARM、MIPS、SMIPS 不在当前发布矩阵内。先在 RouterOS 执行：

```routeros
/system/resource/print
/system/device-mode/print
/container/print
```

必须确认 `container=yes`、CPU 架构匹配、磁盘空间充足。设备模式切换可能要求物理确认，按 RouterOS 官方提示执行。

## 3. 构建

在 GitHub Actions 手动运行 `Release artifacts`，或推送 `v*` 标签。每个平台会得到 Go 二进制和 RouterOS 可导入的容器镜像 tar。

本地构建示例：

```bash
docker buildx build \
  --platform linux/arm64 \
  --output type=docker,dest=foxos-arm64.tar \
  .
```

不要把带实际环境变量或密码的文件打入镜像。

## 4. RouterOS 服务账号

建议创建独立 `foxos-service` 用户组和用户，不要使用 `admin`。权限必须按实际 RouterOS 版本验证，只授予 FoxOS 读取资源、接口、DHCP/ARP、L2TP，以及写入 FoxOS 自有 DHCP Lease 所需的最小集合。不要授予 `policy`、`password`、`sensitive`、`reboot` 等无关能力。

账号创建属于安全敏感操作，仓库不内置密码。请在 RouterOS 终端中使用本地生成的高强度密码，随后把密码只写入 RouterOS Container env list。不得提交到 GitHub、README、日志或截图。

FoxOS Writer 还会执行第二层限制：

- 只接受 DHCP Lease `PUT/PATCH`。
- comment 必须以 `foxos:device:` 开头。
- HMAC 确认令牌绑定完整计划，五分钟过期且只能使用一次。
- 写后重新读取并核对 MAC、IP、静态状态和 comment。

## 5. 准备运行变量

复制 `deploy/routeros/foxos-env.example.rsc` 到 Git 管理范围之外，替换全部 `CHANGE_ME`。两个 32 字符以上密钥必须不同：

```bash
openssl rand -hex 32
openssl rand -hex 32
```

浏览器使用 `FOXOS_API_TOKEN` 访问业务 API；`FOXOS_CONFIRMATION_KEY` 只供服务端签署高风险操作计划。

Mihomo Controller 必须允许来自 FoxOS 容器的管理访问。`FOXOS_MIHOMO_RUNTIME_CONFIG` 是 Mihomo 进程看到的配置路径，不是 FoxOS 容器内路径。

## 6. 安装

1. 上传正确架构的 `foxos-*.tar`。
2. 在本地导入已填好的 env 文件。
3. 执行 `deploy/routeros/preflight.rsc`。
4. 打开 `deploy/routeros/install.rsc`，确认：
   - `imageFile`
   - `managementBridge`
   - `foxosAddress`
   - `foxosGateway`
5. 导入安装脚本。
6. 等待 `/container/print` 中导入任务完成，再启动 `foxos:active`。

安装脚本只创建 `veth-foxos`、把它接入指定的现有管理桥，并添加 FoxOS 容器；不会创建或修改 DNS、NAT、默认路由、Mangle、策略路由或防火墙。

健康检查：

```bash
curl http://10.0.0.4:8090/api/v1/health/live
curl http://10.0.0.4:8090/api/v1/health/ready
```

业务接口需要：

```bash
curl -H "Authorization: Bearer $FOXOS_API_TOKEN" \
  http://10.0.0.4:8090/api/v1/routeros/overview
```

首次打开 Web UI 后，在设置页填入 API Token。Token 只保存在当前浏览器 localStorage，不写入仓库或后端数据库。

## 7. 升级

升级前：

1. 导出 RouterOS 配置。
2. 备份 FoxOS `/data/foxos.db`。
3. 保留当前镜像或 root-dir。
4. 上传新 tar。
5. 执行 `upgrade.rsc`。

脚本把旧容器标记为 `foxos:rollback`，新容器标记为 `foxos:active`。新镜像导入后需手动启动并验证：

- live/ready 健康接口；
- RouterOS/Mihomo 只读状态；
- 节点读取和 TCP 探测；
- 审计日志；
- 一台测试设备的静态租约计划与回读。

不要在验证前删除回滚槽位。

## 8. 回滚

执行 `rollback.rsc` 会停止新容器并启动保留槽位。回滚后检查健康接口和 SQLite 数据版本。如果数据库结构已发生不兼容变更，恢复升级前的数据库备份。

脚本不会自动删除失败镜像，避免不可恢复的数据丢失。确认回滚稳定后再人工清理。

## 9. 故障排查

| 现象 | 检查 |
|---|---|
| Web 能打开但显示演示数据 | 设置页 API Token、浏览器网络请求、Bearer Token 长度 |
| RouterOS 离线 | URL、证书、`foxos-service` 权限、管理桥连通性 |
| Mihomo 离线 | Controller 地址、secret、9090 访问范围 |
| 固定 IP 计划冲突 | 目标 IP 是否已被其他 MAC 使用 |
| 执行后验证失败 | DHCP Lease 的 MAC/IP/dynamic/comment 是否与计划一致 |
| 容器无法启动 | `/container/log/print`、架构、env list、磁盘空间 |
| 刷新页面 404 | 应通过 FoxOS 的 8090 端口访问，前端使用 hash 路由 |

提交故障信息时先删除 Token、密码、节点分享链接、RouterOS 导出中的敏感字段。

## 10. 上线验收

- [ ] Core CI 的 Go 和 Web 作业通过。
- [ ] Release workflow 成功生成目标架构镜像。
- [ ] RouterOS preflight 无错误。
- [ ] `10.0.0.1` 至 `10.0.0.4` 无地址冲突。
- [ ] live/ready 正常。
- [ ] RouterOS 与 Mihomo 只读状态正常。
- [ ] MosDNS 页面只读，所有 DNS 设置不变。
- [ ] 未出现新增默认路由、DNS、NAT、Mangle 或全 LAN 接管。
- [ ] 静态 IP 操作经过计划、确认、审计、回读。
- [ ] 升级前数据库与 RouterOS 配置已备份。
- [ ] 回滚槽位可启动。

在没有真实 RouterOS 设备或隔离实验环境时，只能完成构建和自动化测试，不能把“脚本可解析”表述为“生产设备已验证”。
