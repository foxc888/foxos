@echo off
chcp 65001 >nul
title FoxOS 安装准备

echo.
echo 正在准备 Mihomo 和 MosDNS 配置...
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0fetch-configs.ps1" -BundleDirectory "%~dp0"
if errorlevel 1 (
  echo.
  echo 配置下载失败，请检查电脑能否访问 GitHub。
  pause
  exit /b 1
)

echo.
echo 配置已经准备好，不需要填写任何密钥。
echo 请按 QUICK-INSTALL.md 上传文件。
pause
