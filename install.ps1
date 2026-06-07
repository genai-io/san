# san installer for Windows (PowerShell 5.1+)
#
#   irm https://raw.githubusercontent.com/genai-io/san/main/install.ps1 | iex
#
# Pass a command (install is the default):
#   & ([scriptblock]::Create((irm https://raw.githubusercontent.com/genai-io/san/main/install.ps1))) uninstall

param(
    [ValidateSet('install', 'upgrade', 'uninstall', 'help')]
    [string]$Command = 'install'
)

$ErrorActionPreference = 'Stop'

$Repo = 'genai-io/san'
$Binary = 'san'
$InstallDir = Join-Path $env:LOCALAPPDATA 'san\bin'
$ExePath = Join-Path $InstallDir "$Binary.exe"

function Info($msg) { Write-Host $msg -ForegroundColor Green }
function Warn($msg) { Write-Host $msg -ForegroundColor Yellow }
function Fail($msg) { Write-Host $msg -ForegroundColor Red; exit 1 }

function Get-Usage {
    Write-Host "Usage: install.ps1 [install|upgrade|uninstall]"
    Write-Host ""
    Write-Host "Commands:"
    Write-Host "  install    Install san (default)"
    Write-Host "  upgrade    Upgrade to latest version"
    Write-Host "  uninstall  Remove san and config"
}

# Detect architecture. PROCESSOR_ARCHITEW6432 is set when a 32-bit
# PowerShell runs on 64-bit Windows; prefer it so we pick the real arch.
function Get-Arch {
    $arch = $env:PROCESSOR_ARCHITEW6432
    if (-not $arch) { $arch = $env:PROCESSOR_ARCHITECTURE }
    switch ($arch) {
        'AMD64' { return 'amd64' }
        'ARM64' { return 'arm64' }
        default { Fail "Unsupported architecture: $arch" }
    }
}

function Get-LatestVersion {
    $headers = @{ 'User-Agent' = 'san-installer' }
    $release = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest" -Headers $headers
    return ($release.tag_name -replace '^v', '')
}

function Get-DownloadUrl($version, $arch) {
    $asset = "${Binary}_windows_${arch}.zip"
    $headers = @{ 'User-Agent' = 'san-installer' }
    $release = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/tags/v$version" -Headers $headers
    $match = $release.assets | Where-Object { $_.name -eq $asset } | Select-Object -First 1
    if (-not $match) { Fail "Release asset $asset not found for v$version" }
    return $match.browser_download_url
}

function Get-InstalledVersion {
    if (-not (Test-Path $ExePath)) { return $null }
    try {
        $line = (& $ExePath version 2>$null | Select-Object -First 1)
        $token = ($line -split '\s+')[2]
        return ($token -replace '^v', '')
    } catch {
        return 'unknown'
    }
}

function Add-ToUserPath($dir) {
    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    $entries = @()
    if ($userPath) { $entries = $userPath -split ';' | Where-Object { $_ -ne '' } }
    if ($entries -contains $dir) { return }

    $newPath = (@($entries) + $dir) -join ';'
    [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
    $env:Path = "$env:Path;$dir"
    Warn "Added $dir to your user PATH. Restart your terminal for it to take effect."
}

function Invoke-Install {
    $arch = Get-Arch

    Info "Fetching latest version..."
    $version = Get-LatestVersion
    if (-not $version) { Fail "Failed to get latest version" }

    $current = Get-InstalledVersion
    if ($current) {
        if ($current -eq $version) {
            Info "[OK] san v$version is already installed"
            return
        }
        Info "Upgrading san from v$current to v$version..."
    } else {
        Info "Installing san v$version for windows/$arch..."
    }

    $url = Get-DownloadUrl $version $arch
    $tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("san-" + [System.Guid]::NewGuid().ToString())
    New-Item -ItemType Directory -Path $tmp -Force | Out-Null
    try {
        $zip = Join-Path $tmp 'san.zip'
        Invoke-WebRequest -Uri $url -OutFile $zip -UseBasicParsing
        Expand-Archive -Path $zip -DestinationPath $tmp -Force

        $src = Join-Path $tmp "$Binary.exe"
        if (-not (Test-Path $src)) { Fail "Archive did not contain $Binary.exe" }

        New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
        Move-Item -Path $src -Destination $ExePath -Force

        Add-ToUserPath $InstallDir
        Info "[OK] san v$version installed to $ExePath"
    } finally {
        Remove-Item -Path $tmp -Recurse -Force -ErrorAction SilentlyContinue
    }
}

function Invoke-Uninstall {
    Info "Uninstalling san..."
    if (Test-Path $ExePath) {
        Remove-Item -Path $ExePath -Force
        Info "[OK] Removed $ExePath"
    } else {
        Warn "Binary not found at $ExePath"
    }

    $cfg = Join-Path $env:USERPROFILE '.san'
    if (Test-Path $cfg) {
        $response = Read-Host "Remove config directory $cfg? [y/N]"
        if ($response -match '^[Yy]$') {
            Remove-Item -Path $cfg -Recurse -Force
            Info "[OK] Removed $cfg"
        }
    }
    Info "[OK] Uninstall complete"
}

switch ($Command) {
    'install'   { Invoke-Install }
    'upgrade'   { Invoke-Install }
    'uninstall' { Invoke-Uninstall }
    'help'      { Get-Usage }
}
