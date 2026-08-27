# manygit installer for native Windows (PowerShell). Usage:
#   irm https://raw.githubusercontent.com/rabeeh-ta/manygit/main/install.ps1 | iex
#
# Installs the latest release by default. To pin a version (e.g. to roll back),
# set $env:MANYGIT_VERSION before running, or download this script and pass it
# as -Version:
#   $env:MANYGIT_VERSION = "v1.0.7"; irm .../install.ps1 | iex
#
# On Git Bash, MSYS2, Cygwin or WSL, use install.sh instead — it detects
# Windows the same way and installs the same binary.
param(
    [string]$Version = $env:MANYGIT_VERSION
)

$ErrorActionPreference = 'Stop'

$repo = 'rabeeh-ta/manygit'
$exe = 'manygit.exe'
$installDir = if ($env:MANYGIT_INSTALL_DIR) { $env:MANYGIT_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'manygit\bin' }

function Die($msg) {
    Write-Error "error: $msg"
    exit 1
}

if (-not (Get-Command tar -ErrorAction SilentlyContinue)) {
    Die "tar.exe is required (bundled with Windows 10 1803+ and Windows 11)"
}

switch ($env:PROCESSOR_ARCHITECTURE) {
    'AMD64' { $arch = 'amd64' }
    'ARM64' { $arch = 'arm64' }
    default { Die "unsupported architecture: $env:PROCESSOR_ARCHITECTURE" }
}

# An explicit version (-Version or MANYGIT_VERSION) pins the install;
# otherwise the newest release wins. Pinning is how you roll back to an
# earlier build.
$pinned = [bool]$Version
if ($pinned) {
    $tag = if ($Version.StartsWith('v')) { $Version } else { "v$Version" }
    try {
        Invoke-RestMethod -Uri "https://api.github.com/repos/$repo/releases/tags/$tag" -UseBasicParsing | Out-Null
    } catch {
        Die "no release tagged $tag (see https://github.com/$repo/releases)"
    }
} else {
    $latest = Invoke-RestMethod -Uri "https://api.github.com/repos/$repo/releases/latest" -UseBasicParsing
    $tag = $latest.tag_name
    if (-not $tag) { Die "no published release found for $repo yet" }
}

$asset = "manygit_windows_${arch}.tar.gz"
$url = "https://github.com/$repo/releases/download/$tag/$asset"

Write-Host "Installing $exe $tag (windows/$arch)..."

$tmp = Join-Path ([System.IO.Path]::GetTempPath()) "manygit-install-$tag-$arch"
New-Item -ItemType Directory -Force -Path $tmp | Out-Null
try {
    $archivePath = Join-Path $tmp $asset
    try {
        Invoke-WebRequest -Uri $url -OutFile $archivePath -UseBasicParsing
    } catch {
        Die "download failed: $url"
    }
    # tar.exe (bundled since Windows 10 1803) reads .tar.gz directly — no
    # separate zip archive to maintain just for this platform.
    tar -xzf $archivePath -C $tmp
    if ($LASTEXITCODE -ne 0) { Die "could not extract $asset" }
    $extracted = Join-Path $tmp $exe
    if (-not (Test-Path $extracted)) { Die "archive did not contain $exe" }

    New-Item -ItemType Directory -Force -Path $installDir | Out-Null
    $dest = Join-Path $installDir $exe
    Move-Item -Force $extracted $dest
    Write-Host "Installed to $dest"
} finally {
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}

# A pinned version is usually a rollback, and manygit's launch check would
# offer to pull it straight back to newest. Say so instead of letting it
# surprise them.
if ($pinned) {
    Write-Host "Pinned to $tag. manygit checks for a newer release on launch -- answer `"n`","
    Write-Host "or use --no-update-check / MANYGIT_NO_UPDATE_CHECK=1 to stay on this version."
}

# Put installDir on the user's PATH if it isn't already.
$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
$entries = @()
if ($userPath) { $entries = $userPath -split ';' }
if ($entries -notcontains $installDir) {
    $newPath = if ($userPath) { "$installDir;$userPath" } else { $installDir }
    [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
    $env:Path = "$installDir;$env:Path"
    Write-Host "Added $installDir to your PATH. Open a new terminal to pick it up everywhere."
}

Write-Host "Done. Run: manygit"
