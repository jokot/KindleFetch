# KindleFetch for PC - Windows installer.
#
#   irm https://raw.githubusercontent.com/jokot/KindleFetch/main/pc/install.ps1 | iex
#
# Installs for the current user only (no admin rights needed):
#   %LOCALAPPDATA%\Programs\KindleFetch\kindlefetch.exe
#   a Start Menu shortcut, and the folder added to the user PATH.
# Running it again updates to the newest release.
#
# To uninstall, run with $env:KF_UNINSTALL = '1' set.

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'   # Invoke-WebRequest is very slow with the progress bar on
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

$Repo = 'jokot/KindleFetch'
$InstallDir = Join-Path $env:LOCALAPPDATA 'Programs\KindleFetch'
$Exe = Join-Path $InstallDir 'kindlefetch.exe'
$Shortcut = Join-Path ([Environment]::GetFolderPath('Programs')) 'KindleFetch.lnk'

function Set-UserPath([string]$Dir, [bool]$Add) {
    $current = [Environment]::GetEnvironmentVariable('Path', 'User')
    $parts = @()
    if ($current) { $parts = $current -split ';' | Where-Object { $_ -and $_ -ne $Dir } }
    if ($Add) { $parts += $Dir }
    [Environment]::SetEnvironmentVariable('Path', ($parts -join ';'), 'User')
}

if ($env:KF_UNINSTALL -eq '1') {
    Remove-Item -Recurse -Force $InstallDir -ErrorAction SilentlyContinue
    Remove-Item -Force $Shortcut -ErrorAction SilentlyContinue
    Set-UserPath $InstallDir $false
    Write-Host 'KindleFetch removed. Your downloaded books and settings were kept.'
    return
}

$arch = if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64' -or $env:PROCESSOR_ARCHITEW6432 -eq 'ARM64') { 'arm64' } else { 'amd64' }
$asset = "kindlefetch-windows-$arch.exe"

Write-Host 'Finding the latest KindleFetch for PC release...'
$releases = Invoke-RestMethod -UseBasicParsing -Headers @{ 'User-Agent' = 'KindleFetch-installer' } `
    "https://api.github.com/repos/$Repo/releases?per_page=30"
$release = $releases | Where-Object { -not $_.draft -and -not $_.prerelease -and $_.tag_name -like 'pc-v*' } |
    Select-Object -First 1
if (-not $release) { throw "No KindleFetch for PC release found at https://github.com/$Repo/releases" }

$exeAsset = $release.assets | Where-Object { $_.name -eq $asset }
$sumAsset = $release.assets | Where-Object { $_.name -eq 'SHA256SUMS' }
if (-not $exeAsset) { throw "Release $($release.tag_name) has no $asset" }

$tmp = Join-Path ([IO.Path]::GetTempPath()) ("kindlefetch-" + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
    Write-Host "Downloading $($release.tag_name) ($arch)..."
    $dl = Join-Path $tmp $asset
    Invoke-WebRequest -UseBasicParsing $exeAsset.browser_download_url -OutFile $dl

    if ($sumAsset) {
        $sums = (Invoke-WebRequest -UseBasicParsing $sumAsset.browser_download_url).Content
        if ($sums -is [byte[]]) { $sums = [Text.Encoding]::UTF8.GetString($sums) }
        $line = ($sums -split "`n") | Where-Object { $_ -match "\s\*?$([regex]::Escape($asset))\s*$" } | Select-Object -First 1
        if (-not $line) { throw "SHA256SUMS has no entry for $asset" }
        $expected = ($line -split '\s+')[0].ToLower()
        $actual = (Get-FileHash -Algorithm SHA256 $dl).Hash.ToLower()
        if ($expected -ne $actual) { throw "Checksum mismatch for $asset - download corrupted or tampered with" }
        Write-Host 'Checksum verified.'
    }

    New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
    # A running exe cannot be overwritten, but it can be renamed: move the old
    # one aside so updating works while KindleFetch is open.
    $old = "$Exe.old"
    Remove-Item -Force $old -ErrorAction SilentlyContinue
    if (Test-Path $Exe) { Move-Item -Force $Exe $old }
    Move-Item -Force $dl $Exe
    Remove-Item -Force $old -ErrorAction SilentlyContinue
} finally {
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}

Set-UserPath $InstallDir $true
$env:Path = "$env:Path;$InstallDir"

$shell = New-Object -ComObject WScript.Shell
$lnk = $shell.CreateShortcut($Shortcut)
$lnk.TargetPath = $Exe
$lnk.WorkingDirectory = [Environment]::GetFolderPath('UserProfile')
$lnk.Description = 'Search and download books for your Kindle'
$lnk.Save()

Write-Host ''
Write-Host "KindleFetch for PC $($release.tag_name) installed."
Write-Host '  Start it from the Start Menu ("KindleFetch"), or type: kindlefetch'
Write-Host '  (open a new terminal window first for the command to be found)'
