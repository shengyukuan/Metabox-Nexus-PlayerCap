$ErrorActionPreference = 'Stop'

$trayPath = Join-Path $PSScriptRoot 'Metabox-Nexus-PlayerCap-Tray.exe'
if (-not (Test-Path -LiteralPath $trayPath)) {
    throw "找不到托盘程序: $trayPath"
}

Start-Process -FilePath $trayPath -ArgumentList '--install' -WindowStyle Hidden
Write-Host '已启动托盘程序，并启用当前用户登录自动启动。'
