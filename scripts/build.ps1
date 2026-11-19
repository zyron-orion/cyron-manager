param(
    [switch]$Nsis,
    [switch]$NoFetch
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
Set-Location -LiteralPath $root

$wails = (Get-Command wails -ErrorAction SilentlyContinue).Source
if (-not $wails) {
    $gopath = $env:GOPATH
    if (-not $gopath) {
        $gopath = (& go env GOPATH).Trim()
    }
    $candidate = Join-Path $gopath "bin\wails.exe"
    if (Test-Path -LiteralPath $candidate) {
        $wails = $candidate
    }
}
if (-not $wails) {
    throw "wails is not installed. Install Wails v2 and add it to PATH, or set GOPATH."
}

$required = @("aria2c.exe", "yt-dlp.exe", "ffmpeg.exe", "ffprobe.exe")
$missing = $required | Where-Object { -not (Test-Path -LiteralPath (Join-Path $root "bin\$_")) }
if ($missing -and -not $NoFetch) {
    & (Join-Path $PSScriptRoot "fetch-tools.ps1")
}
if ($missing -and $NoFetch) {
    Write-Warning "Missing tools: $($missing -join ', ')"
}

$arguments = @("build", "-clean")
if ($Nsis) {
    $arguments += "-nsis"
}

& $wails @arguments
if ($LASTEXITCODE -ne 0) {
    throw "wails build failed with exit code $LASTEXITCODE"
}

$source = Join-Path $root "bin"
$output = Join-Path $root "build\bin"
$target = Join-Path $output "bin"
New-Item -ItemType Directory -Force -Path $target | Out-Null
$toolFiles = Get-ChildItem -LiteralPath $source -Filter "*.exe" -File -ErrorAction SilentlyContinue
if ($toolFiles) {
    Copy-Item -Path $toolFiles.FullName -Destination $target -Force
} else {
    Write-Warning "No runtime tools found in $source. The app will build, but download engines will need tools at runtime."
}
$licenseTarget = Join-Path $target "licenses"
New-Item -ItemType Directory -Force -Path $licenseTarget | Out-Null
if (Test-Path -LiteralPath (Join-Path $source "licenses")) {
    Copy-Item -Path (Join-Path $source "licenses\*") -Destination $licenseTarget -Force
}
Copy-Item -LiteralPath (Join-Path $root "LICENSE") -Destination (Join-Path $licenseTarget "app-LICENSE.txt") -Force
Copy-Item -LiteralPath (Join-Path $root "LICENSE") -Destination (Join-Path $output "LICENSE.txt") -Force
Copy-Item -LiteralPath (Join-Path $root "THIRD_PARTY_NOTICES.md") -Destination (Join-Path $output "THIRD_PARTY_NOTICES.md") -Force
Copy-Item -LiteralPath (Join-Path $root "README.md") -Destination (Join-Path $output "README.md") -Force
$assetLicenses = Join-Path $output "licenses"
New-Item -ItemType Directory -Force -Path $assetLicenses | Out-Null
$fontLicenses = Join-Path $root "frontend\dist\assets\fonts"
Copy-Item -Path (Join-Path $fontLicenses "OFL-*.txt") -Destination $assetLicenses -Force
Copy-Item -LiteralPath (Join-Path $root "frontend\dist\assets\gentleface-LICENSE.txt") -Destination $assetLicenses -Force

Write-Host "Build ready: $output\CYRON.exe"
