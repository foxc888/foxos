@echo off
chcp 65001 >nul
title FoxOS 安装准备

echo.
echo [1/2] 正在准备 Mihomo 和 MosDNS 配置...
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0fetch-configs.ps1" -BundleDirectory "%~dp0"
if errorlevel 1 (
  echo.
  echo 配置下载失败，请检查电脑能否访问 GitHub。
  pause
  exit /b 1
)

echo.
echo [2/2] 即将打开安装文件。
echo 只修改最上方的四个填写项，保存后关闭记事本。
start "" notepad.exe "%~dp0foxos-full-install.rsc"
echo.
echo 配置已经准备好。填写并保存 foxos-full-install.rsc 后，按 QUICK-INSTALL.md 上传。
pause
