param(
    [switch]$Force,
    [switch]$SkipFFmpeg
)

$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"

$root = Split-Path -Parent $PSScriptRoot
$bin = Join-Path $root "bin"
$licenses = Join-Path $bin "licenses"
$temp = Join-Path ([System.IO.Path]::GetTempPath()) ("dm-tools-" + [guid]::NewGuid().ToString("N"))

New-Item -ItemType Directory -Force -Path $bin, $licenses | Out-Null
New-Item -ItemType Directory -Force -Path $temp | Out-Null

function Get-File([string]$Url, [string]$Destination) {
    Write-Host "Downloading $Url"
    Invoke-WebRequest -UseBasicParsing -Uri $Url -OutFile $Destination
}

function Expand-Zip([string]$Archive, [string]$Destination) {
    Expand-Archive -LiteralPath $Archive -DestinationPath $Destination -Force
}

function Copy-First([string]$Source, [string]$Filter, [string]$Destination) {
    $file = Get-ChildItem -LiteralPath $Source -Recurse -File -Filter $Filter | Select-Object -First 1
    if (-not $file) {
        throw "Could not find $Filter in $Source"
    }
    Copy-Item -LiteralPath $file.FullName -Destination $Destination -Force
}

function Get-Checksum([string]$ChecksumsPath, [string]$FileName) {
    foreach ($line in Get-Content -LiteralPath $ChecksumsPath) {
        if ($line -match '^([0-9a-fA-F]{64})\s+\*?(.+?)\s*$' -and $Matches[2] -eq $FileName) {
            return $Matches[1].ToLowerInvariant()
        }
    }
    throw "No checksum entry for $FileName in $ChecksumsPath"
}

function Assert-Checksum([string]$Path, [string]$Expected, [string]$Label) {
    if ($Expected -notmatch '^[0-9a-fA-F]{64}$') {
        throw "Invalid checksum for $Label"
    }
    $actual = (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($actual -ne $Expected.ToLowerInvariant()) {
        throw "Checksum mismatch for $Label. Expected $Expected but got $actual."
    }
}

try {
    if ($Force -or -not (Test-Path -LiteralPath (Join-Path $bin "aria2c.exe"))) {
        $zip = Join-Path $temp "aria2.zip"
        $out = Join-Path $temp "aria2"
        Get-File "https://github.com/aria2/aria2/releases/download/release-1.37.0/aria2-1.37.0-win-64bit-build1.zip" $zip
        Assert-Checksum $zip "67d015301eef0b612191212d564c5bb0a14b5b9c4796b76454276a4d28d9b288" "aria2"
        Expand-Zip $zip $out
        Copy-First $out "aria2c.exe" (Join-Path $bin "aria2c.exe")
        Copy-First $out "COPYING" (Join-Path $licenses "aria2-COPYING.txt")
    }

    if ($Force -or -not (Test-Path -LiteralPath (Join-Path $bin "yt-dlp.exe"))) {
        $ytdlp = Join-Path $temp "yt-dlp.exe"
        $ytdlpSums = Join-Path $temp "yt-dlp-SHA2-256SUMS"
        Get-File "https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp.exe" $ytdlp
        Get-File "https://github.com/yt-dlp/yt-dlp/releases/latest/download/SHA2-256SUMS" $ytdlpSums
        Assert-Checksum $ytdlp (Get-Checksum $ytdlpSums "yt-dlp.exe") "yt-dlp.exe"
        Copy-Item -LiteralPath $ytdlp -Destination (Join-Path $bin "yt-dlp.exe") -Force
        try {
            Get-File "https://raw.githubusercontent.com/yt-dlp/yt-dlp/master/LICENSE" (Join-Path $licenses "yt-dlp-LICENSE.txt")
        } catch {
            Write-Warning "Could not download the yt-dlp license: $($_.Exception.Message)"
        }
    }

    if (-not $SkipFFmpeg -and ($Force -or -not (Test-Path -LiteralPath (Join-Path $bin "ffmpeg.exe")) -or -not (Test-Path -LiteralPath (Join-Path $bin "ffprobe.exe")))) {
        $zip = Join-Path $temp "ffmpeg.zip"
        $out = Join-Path $temp "ffmpeg"
        $ffmpegSums = Join-Path $temp "ffmpeg-checksums.sha256"
        Get-File "https://github.com/BtbN/FFmpeg-Builds/releases/download/latest/ffmpeg-master-latest-win64-gpl.zip" $zip
        Get-File "https://github.com/BtbN/FFmpeg-Builds/releases/download/latest/checksums.sha256" $ffmpegSums
        Assert-Checksum $zip (Get-Checksum $ffmpegSums "ffmpeg-master-latest-win64-gpl.zip") "ffmpeg"
        Expand-Zip $zip $out
        Copy-First $out "ffmpeg.exe" (Join-Path $bin "ffmpeg.exe")
        Copy-First $out "ffprobe.exe" (Join-Path $bin "ffprobe.exe")
        Copy-First $out "LICENSE.txt" (Join-Path $licenses "ffmpeg-LICENSE.txt")
    }

    if ($SkipFFmpeg) {
        Write-Warning "Skipped ffmpeg. Video merging and some formats will not work."
    }

    Write-Host "Tools ready in $bin"
} finally {
    Remove-Item -LiteralPath $temp -Recurse -Force -ErrorAction SilentlyContinue
}
