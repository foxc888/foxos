[CmdletBinding()]
param(
    [string]$BundleDirectory = $PSScriptRoot,
    [string]$ConfigSourceDirectory = ""
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

function New-RandomHex {
    param([int]$Bytes = 32)

    $buffer = New-Object byte[] $Bytes
    $generator = [System.Security.Cryptography.RandomNumberGenerator]::Create()
    try {
        $generator.GetBytes($buffer)
    }
    finally {
        $generator.Dispose()
    }
    return (($buffer | ForEach-Object { $_.ToString("x2") }) -join "")
}

function Write-Utf8NoBom {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Content
    )

    $encoding = New-Object System.Text.UTF8Encoding($false)
    [System.IO.File]::WriteAllText($Path, $Content, $encoding)
}

$bundle = [System.IO.Path]::GetFullPath($BundleDirectory)
$templatePath = Join-Path $bundle "full-install.template.rsc"
$outputPath = Join-Path $bundle "foxos-full-install.local.rsc"
$credentialsPath = Join-Path $bundle "FOXOS-LOGIN.txt"
$mihomoConfigPath = Join-Path $bundle "mihomo-config\config.yaml"
$mosdnsConfigPath = Join-Path $bundle "mosdns-config\config_custom.yaml"

if (-not (Test-Path -LiteralPath $mihomoConfigPath) -or -not (Test-Path -LiteralPath $mosdnsConfigPath)) {
    $sourceRoot = $null
    $downloadRoot = $null

    if ($ConfigSourceDirectory) {
        $sourceRoot = [System.IO.Path]::GetFullPath($ConfigSourceDirectory)
    }
    else {
        Write-Host "正在从 foxc888/foxos 的 agent/foxos-core 分支下载你自己的配置..." -ForegroundColor Cyan
        $downloadRoot = Join-Path ([System.IO.Path]::GetTempPath()) ("foxos-config-" + [Guid]::NewGuid().ToString("N"))
        $archivePath = Join-Path $downloadRoot "foxos-source.zip"
        $extractPath = Join-Path $downloadRoot "source"
        New-Item -ItemType Directory -Path $downloadRoot | Out-Null
        Invoke-WebRequest `
            -Uri "https://github.com/foxc888/foxos/archive/refs/heads/agent/foxos-core.zip" `
            -OutFile $archivePath
        Expand-Archive -LiteralPath $archivePath -DestinationPath $extractPath
        $configFile = Get-ChildItem -LiteralPath $extractPath -Filter config.yaml -File -Recurse |
            Where-Object { $_.FullName -match '[\\/]mihomo[\\/]config[\\/]config\.yaml$' } |
            Select-Object -First 1
        if (-not $configFile) {
            throw "下载的源码中没有找到 mihomo/config/config.yaml"
        }
        $sourceRoot = $configFile.Directory.Parent.Parent.FullName
    }

    $mihomoSource = Join-Path $sourceRoot "mihomo\config"
    $mosdnsSource = Join-Path $sourceRoot "mosdns-config"
    if (-not (Test-Path -LiteralPath (Join-Path $mihomoSource "config.yaml"))) {
        throw "配置源缺少 mihomo/config/config.yaml：$sourceRoot"
    }
    if (-not (Test-Path -LiteralPath (Join-Path $mosdnsSource "config_custom.yaml"))) {
        throw "配置源缺少 mosdns-config/config_custom.yaml：$sourceRoot"
    }

    Copy-Item -LiteralPath $mihomoSource -Destination (Join-Path $bundle "mihomo-config") -Recurse
    Copy-Item -LiteralPath $mosdnsSource -Destination (Join-Path $bundle "mosdns-config") -Recurse

    if ($downloadRoot -and (Test-Path -LiteralPath $downloadRoot)) {
        Remove-Item -LiteralPath $downloadRoot -Recurse -Force
    }
}

$required = @(
    $templatePath,
    (Join-Path $bundle "foxos-start-all.rsc"),
    (Join-Path $bundle "foxos-amd64.tar"),
    (Join-Path $bundle "mihomo_amd64.tar"),
    (Join-Path $bundle "mosdns-amd64.tar"),
    $mihomoConfigPath,
    $mosdnsConfigPath
)
foreach ($item in $required) {
    if (-not (Test-Path -LiteralPath $item)) {
        throw "安装包不完整，缺少：$item"
    }
}

$routerPassword = New-RandomHex
$mihomoSecret = New-RandomHex
$apiToken = New-RandomHex
$confirmationKey = New-RandomHex

$template = [System.IO.File]::ReadAllText($templatePath)
$replacements = @{
    "__ROUTEROS_PASSWORD__" = $routerPassword
    "__MIHOMO_SECRET__" = $mihomoSecret
    "__FOXOS_API_TOKEN__" = $apiToken
    "__FOXOS_CONFIRMATION_KEY__" = $confirmationKey
}
foreach ($placeholder in $replacements.Keys) {
    if (-not $template.Contains($placeholder)) {
        throw "安装模板缺少占位符：$placeholder"
    }
    $template = $template.Replace($placeholder, $replacements[$placeholder])
}
if ($template -match "__[A-Z0-9_]+__") {
    throw "安装模板仍包含未替换的占位符"
}
Write-Utf8NoBom -Path $outputPath -Content $template

$mihomoConfig = [System.IO.File]::ReadAllText($mihomoConfigPath)
$secretPattern = "(?m)^secret:\s*.*$"
if (-not [System.Text.RegularExpressions.Regex]::IsMatch($mihomoConfig, $secretPattern)) {
    throw "mihomo-config/config.yaml 中没有找到顶层 secret 字段"
}
$mihomoConfig = [System.Text.RegularExpressions.Regex]::Replace(
    $mihomoConfig,
    $secretPattern,
    ('secret: "' + $mihomoSecret + '"'),
    1
)
Write-Utf8NoBom -Path $mihomoConfigPath -Content $mihomoConfig

$credentials = @"
FoxOS 管理地址: http://10.0.0.4:8090
FoxOS API Token: $apiToken

这个文件只保存在你的电脑上，不要上传到 GitHub 或 RouterOS。
登录成功并安全保存 Token 后，可以删除本文件。
"@
Write-Utf8NoBom -Path $credentialsPath -Content $credentials

Write-Host ""
Write-Host "准备完成。" -ForegroundColor Green
Write-Host "1. FoxOS 登录 Token 已写入：$credentialsPath"
Write-Host "2. RouterOS 安装脚本已生成：$outputPath"
Write-Host "3. 请按 QUICK-INSTALL.md 上传文件并执行两次 /import。"
Write-Host ""
Write-Warning "不要把 FOXOS-LOGIN.txt 或 foxos-full-install.local.rsc 提交到 GitHub。"
