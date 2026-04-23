param(
  [Parameter(ValueFromRemainingArguments = $true)]
  [string[]]$CliArgs
)

$ErrorActionPreference = "Stop"

$baseOverride = if ($env:INITRA_BASE_URL) { $env:INITRA_BASE_URL } else { $env:SETUPCTL_BASE_URL }
$BaseUrl = if ($baseOverride) { $baseOverride.TrimEnd('/') } else { "https://git.justw.tf/LightZirconite/setup-win/raw/branch/main" }
$ManifestUrl = "$BaseUrl/releases/latest.json"
$TargetDir = Join-Path $env:TEMP "initra"
$CatalogDir = Join-Path $TargetDir "catalog"
$TargetExe = Join-Path $TargetDir "initra.exe"
$CatalogPath = Join-Path $CatalogDir "catalog.yaml"
New-Item -ItemType Directory -Force -Path $TargetDir | Out-Null
New-Item -ItemType Directory -Force -Path $CatalogDir | Out-Null

$manifest = Invoke-RestMethod -Uri $ManifestUrl
$BinaryUrl = if ($manifest.artifacts.'windows-amd64') { $manifest.artifacts.'windows-amd64' } else { "$BaseUrl/releases/initra-windows-amd64.exe" }
$ExpectedSha256 = "$($manifest.sha256.'windows-amd64')".ToLowerInvariant()

Invoke-WebRequest -Uri $BinaryUrl -OutFile $TargetExe
Invoke-WebRequest -Uri "$BaseUrl/releases/catalog/catalog.yaml" -OutFile $CatalogPath

if ($ExpectedSha256) {
  $actualSha256 = (Get-FileHash -LiteralPath $TargetExe -Algorithm SHA256).Hash.ToLowerInvariant()
  if ($actualSha256 -ne $ExpectedSha256) {
    Remove-Item -LiteralPath $TargetExe -Force -ErrorAction SilentlyContinue
    throw "Downloaded Initra binary failed integrity verification. Expected SHA-256 $ExpectedSha256 but got $actualSha256."
  }
}

$argList = @()
if ($CliArgs.Count -gt 0) {
  $argList += $CliArgs
}

$principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
$isAdmin = $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)

if (-not $isAdmin) {
  if ($CliArgs.Count -gt 0) {
    Start-Process -FilePath $TargetExe -Verb RunAs -ArgumentList $argList | Out-Null
  } else {
    Start-Process -FilePath $TargetExe -Verb RunAs | Out-Null
  }
  exit 0
}

& $TargetExe @argList
