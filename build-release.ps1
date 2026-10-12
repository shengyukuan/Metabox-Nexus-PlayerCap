$ErrorActionPreference = 'Stop'

function Require-Command([string]$Name, [string]$Hint) {
    $command = Get-Command $Name -ErrorAction SilentlyContinue
    if (-not $command) {
        throw "$Name not found. $Hint"
    }
    return $command.Source
}

$goCommand = Get-Command 'go.exe' -ErrorAction SilentlyContinue
if ($goCommand) {
    $go = $goCommand.Source
} else {
    $knownGo = @(
        (Join-Path ${env:ProgramFiles} 'Go\bin\go.exe'),
        (Join-Path ${env:ProgramFiles(x86)} 'Go\bin\go.exe'),
        (Join-Path $env:LOCALAPPDATA 'Programs\Go\bin\go.exe')
    ) | Where-Object { $_ -and (Test-Path -LiteralPath $_) } | Select-Object -First 1
    if ($knownGo) {
        $go = $knownGo
    } else {
        throw 'go.exe not found. Install Go 1.25 or newer, then reopen PowerShell.'
    }
}
$isccCommand = Get-Command 'iscc.exe' -ErrorAction SilentlyContinue
if (-not $isccCommand) {
    $knownIscc = @(
        (Join-Path ${env:ProgramFiles(x86)} 'Inno Setup 6\ISCC.exe'),
        (Join-Path $env:ProgramFiles 'Inno Setup 6\ISCC.exe')
    ) | Where-Object { $_ -and (Test-Path -LiteralPath $_) } | Select-Object -First 1
    if ($knownIscc) {
        $iscc = $knownIscc
    } else {
        throw 'iscc.exe not found. Install Inno Setup 6, then reopen PowerShell.'
    }
} else {
    $iscc = $isccCommand.Source
}

$root = $PSScriptRoot
$dist = Join-Path $root 'dist'
New-Item -ItemType Directory -Force -Path $dist | Out-Null

Push-Location $root
try {
    Write-Host 'Building core service...'
    & $go build -ldflags '-s -w' -o (Join-Path $dist 'Metabox-Nexus-PlayerCap.exe') '.'
    if ($LASTEXITCODE -ne 0) { throw 'Core service build failed.' }

    Write-Host 'Building tray controller...'
    # Keep the core service as a console application: its stderr is the live
    # diagnostic channel.  The tray starts it with HideWindow, so end users
    # do not see that console while still retaining logs for troubleshooting.
    & $go build -o (Join-Path $dist 'Metabox-Nexus-PlayerCap-Tray.exe') './cmd/metabox-playercap-tray'
    if ($LASTEXITCODE -ne 0) { throw 'Tray controller build failed.' }

    $assets = @(
        'config.yml',
        'lyric_display.html',
        'lyric_page.html',
        'effect_display.html',
        'effect_page.html'
    )
    foreach ($asset in $assets) {
        Copy-Item -LiteralPath (Join-Path $root $asset) -Destination (Join-Path $dist $asset) -Force
    }

    Write-Host 'Building installer...'
    & $iscc (Join-Path $root 'packaging\Metabox-Nexus-PlayerCap.iss')
    if ($LASTEXITCODE -ne 0) { throw 'Installer build failed.' }

    Write-Host ''
    Write-Host 'Build complete:' -ForegroundColor Green
    Write-Host (Join-Path $root 'packaging\Metabox-Nexus-PlayerCap-Setup.exe')
}
finally {
    Pop-Location
}
