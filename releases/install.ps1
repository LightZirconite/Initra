param(
  [Parameter(ValueFromRemainingArguments = $true)]
  [string[]]$CliArgs
)

$ErrorActionPreference = "Stop"

$baseOverride = if ($env:INITRA_BASE_URL) { $env:INITRA_BASE_URL } else { $env:SETUPCTL_BASE_URL }
$BaseUrl = if ($baseOverride) { $baseOverride.TrimEnd('/') } else { "https://git.justw.tf/LightZirconite/setup-win" }
$BinaryUrl = "$BaseUrl/releases/initra-windows-amd64.exe"
$TargetDir = Join-Path $env:TEMP "initra"
$CatalogDir = Join-Path $TargetDir "catalog"
$TargetExe = Join-Path $TargetDir "initra.exe"
$CatalogPath = Join-Path $CatalogDir "catalog.yaml"

New-Item -ItemType Directory -Force -Path $TargetDir | Out-Null
New-Item -ItemType Directory -Force -Path $CatalogDir | Out-Null
Invoke-WebRequest -Uri $BinaryUrl -OutFile $TargetExe
Invoke-WebRequest -Uri "$BaseUrl/releases/catalog/catalog.yaml" -OutFile $CatalogPath

$argList = @()
if ($CliArgs.Count -gt 0) {
  $argList += $CliArgs
}

$principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
$isAdmin = $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)

if (-not $isAdmin) {
  Start-Process -FilePath $TargetExe -Verb RunAs -ArgumentList $argList | Out-Null
  exit 0
}

& $TargetExe @argList
