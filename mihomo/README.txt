========================================
Mihomo 安装包说明
========================================

本安装包包含 Mihomo 代理工具的完整安装文件，包括：
- Docker 镜像文件（多架构支持）
- Web UI 界面
- GeoIP 数据库
- GeoSite 数据库
- 配置文件

========================================
目录结构
========================================

mihomo/
├── config/              # 配置文件目录
│   ├── config.yaml      # Mihomo 主配置文件
│   ├── GeoIP.dat        # GeoIP 数据库
│   ├── geosite.dat      # GeoSite 数据库
│   ├── ui/              # Web UI 文件
│   └── ruleset/         # 规则集文件
├── install/             # 安装镜像与工作流目录
│   ├── mihomo_amd64.tar    # x86_64 架构镜像
│   ├── mihomo_arm64.tar    # ARM64 架构镜像
│   └── mihomo_arm_v7.tar   # ARMv7 架构镜像
└── script/              # 辅助安装脚本目录
    └── install_hy2.sh            # Hysteria 2 一键安装脚本

========================================
版本信息
========================================

打包时间: 2026-06-08 21:45:44 CST

Docker 镜像列表:
  - mihomo_amd64.tar ( 79M) - 架构: amd64
  - mihomo_arm64.tar ( 82M) - 架构: arm64
  - mihomo_arm_v7.tar ( 72M) - 架构: arm_v7

镜像下载时间: 2026-06-08 21:45:44


UI 文件数量: 103
UI 更新时间: 2026-06-06 02:03:58


GeoIP 数据库: GeoIP.dat ( 19M)
GeoIP 更新时间: 2026-06-08 21:45:32
GeoSite 数据库: geosite.dat (4.1M)
GeoSite 更新时间: 2026-06-08 21:45:35


========================================
架构选择指南
========================================

请根据您的设备架构选择对应的镜像文件：

1. mihomo_amd64.tar
   适用于: x86_64 / amd64 架构
   - RouterOS x86/x64 版本
   - 标准 PC 服务器
   - Intel/AMD 处理器的设备

2. mihomo_arm64.tar
   适用于: ARM64 / aarch64 架构
   - RouterOS ARM64 版本
   - 树莓派 4/5 (64位系统)
   - Apple Silicon Mac (M1/M2/M3)
   - 现代 ARM 服务器

3. mihomo_arm_v7.tar
   适用于: ARMv7 / arm 架构
   - RouterOS ARM 版本 (如 RB3011, RB4011)

========================================
安装步骤
========================================

1. 解压本安装包到目标位置
2. 根据设备架构选择对应的 .tar 镜像文件
3. 使用 Docker 加载镜像:
   docker load -i install/mihomo_<架构>.tar

4. 修改 config/config.yaml 配置文件（如需要）
5. 启动容器:
   docker run -d \
     --name mihomo \
     --restart always \
     -p 7890:7890 \
     -p 9090:9090 \
     -v $(pwd)/config:/root/.config/mihomo \
     metacubex/mihomo:v1.19.17

6. 访问 Web UI:
   http://<设备IP>:9090/ui

========================================
附加功能脚本说明
========================================

本安装包内 `script/` 目录下还附带了相关协议的辅助安装脚本：

- install_hy2.sh:
  Hysteria 2 一键安装脚本，用于在您的 VPS（服务端）快速部署 Hysteria 2。
  用法：将脚本上传至目标 VPS 并赋予执行权限，然后运行 `bash install_hy2.sh` 即按提示完成安装。

具体每种协议的完整图文教程，请前往我们的教学网站查看：
http://ros.wallentv.com:8888

========================================
如何确定设备架构
========================================

RouterOS 设备:
  /system resource print
  查看 "architecture-name" 字段

Linux 设备:
  uname -m
  或
  dpkg --print-architecture

Docker 环境:
  docker version
  查看 "Server" 部分的 "Arch" 字段

========================================
更新建议
========================================

建议每月检查更新一次，以获取：
- 最新的安全补丁
- 功能改进
- GeoIP 数据库更新
- 规则集更新

更新方法:
1. 重新运行下载脚本获取最新版本
2. 重新打包
3. 替换旧的安装包

========================================
技术支持与教程
========================================

配套的完整图文教程，请访问：
http://ros.wallentv.com:8888

项目地址: https://github.com/MetaCubeX/mihomo
UI 项目: https://github.com/MetaCubeX/metacubexd
规则集: https://github.com/MetaCubeX/meta-rules-dat

========================================
