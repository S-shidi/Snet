param(
    [string]$OutFile = ""
)

$ErrorActionPreference = "Stop"

$Version = "0.14.1"
$Url = "https://www.wintun.net/builds/wintun-$Version.zip"
$ZipSha256 = "07C256185D6EE3652E09FA55C0B673E2624B565E02C4B9091C79CA7D2F24EF51"
$DllSha256 = "E5DA8447DC2C320EDC0FC52FA01885C103DE8C118481F683643CACC3220DAFCE"

if (-not $OutFile) {
    $OutFile = Join-Path (Split-Path -Parent $PSScriptRoot) "assets\wintun.dll"
}

$tmp = Join-Path ([IO.Path]::GetTempPath()) ("wintun-" + [guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $tmp | Out-Null
try {
    $zip = Join-Path $tmp "wintun.zip"
    Write-Host "Downloading wintun $Version..."
    Invoke-WebRequest -UseBasicParsing -Uri $Url -OutFile $zip
    if ((Get-FileHash $zip -Algorithm SHA256).Hash -ne $ZipSha256) {
        throw "wintun zip checksum mismatch"
    }

    Expand-Archive -Path $zip -DestinationPath $tmp -Force
    $dll = Join-Path $tmp "wintun\bin\amd64\wintun.dll"
    if (-not (Test-Path $dll)) {
        throw "wintun.dll (amd64) missing from archive"
    }
    if ((Get-FileHash $dll -Algorithm SHA256).Hash -ne $DllSha256) {
        throw "wintun.dll checksum mismatch"
    }

    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $OutFile) | Out-Null
    Copy-Item $dll $OutFile -Force
    Write-Host "wintun.dll $Version -> $OutFile ($DllSha256)"
} finally {
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}
