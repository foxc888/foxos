[CmdletBinding()]
param(
    [string]$BundleDirectory = $PSScriptRoot,
    [string]$ConfigSourceDirectory = ""
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$bundle = [System.IO.Path]::GetFullPath($BundleDirectory)
$mihomoDestination = Join-Path $bundle "mihomo-config"
$mosdnsDestination = Join-Path $bundle "mosdns-config"

if ((Test-Path -LiteralPath (Join-Path $mihomoDestination "config.yaml")) -and
    (Test-Path -LiteralPath (Join-Path $mosdnsDestination "config_custom.yaml"))) {
    Write-Host "Mihomo 和 MosDNS 配置已经存在，不需要重复下载。" -ForegroundColor Green
    exit 0
}

$downloadRoot = $null
try {
    if ($ConfigSourceDirectory) {
        $sourceRoot = [System.IO.Path]::GetFullPath($ConfigSourceDirectory)
    }
    else {
        Write-Host "正在下载 foxc888/foxos 的配置文件..." -ForegroundColor Cyan
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
        throw "缺少 mihomo/config/config.yaml：$sourceRoot"
    }
    if (-not (Test-Path -LiteralPath (Join-Path $mosdnsSource "config_custom.yaml"))) {
        throw "缺少 mosdns-config/config_custom.yaml：$sourceRoot"
    }

    if (Test-Path -LiteralPath $mihomoDestination) {
        Remove-Item -LiteralPath $mihomoDestination -Recurse -Force
    }
    if (Test-Path -LiteralPath $mosdnsDestination) {
        Remove-Item -LiteralPath $mosdnsDestination -Recurse -Force
    }
    Copy-Item -LiteralPath $mihomoSource -Destination $mihomoDestination -Recurse
    Copy-Item -LiteralPath $mosdnsSource -Destination $mosdnsDestination -Recurse

    Write-Host "配置准备完成。" -ForegroundColor Green
}
finally {
    if ($downloadRoot -and (Test-Path -LiteralPath $downloadRoot)) {
        Remove-Item -LiteralPath $downloadRoot -Recurse -Force
    }
}
