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
$BootstrapLog = Join-Path $TargetDir "bootstrap.log"
try { Start-Transcript -LiteralPath $BootstrapLog -Append | Out-Null } catch {}

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

function Join-ProcessArguments([string[]]$Args) {
  $quoted = @()
  foreach ($arg in $Args) {
    if ($arg -match '[\s"]') {
      $quoted += '"' + ($arg -replace '"', '\"') + '"'
    } else {
      $quoted += $arg
    }
  }
  return ($quoted -join ' ')
}

$principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
$isAdmin = $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)

if (-not $isAdmin) {
  $startArgs = @{
    FilePath = $TargetExe
    Verb = "RunAs"
    WorkingDirectory = $TargetDir
    Wait = $true
    PassThru = $true
  }
  if ($CliArgs.Count -gt 0) {
    $startArgs.ArgumentList = Join-ProcessArguments $argList
  }
  $child = Start-Process @startArgs
  try { Stop-Transcript | Out-Null } catch {}
  exit $child.ExitCode
}

Push-Location $TargetDir
try {
  & $TargetExe @argList
  $exitCode = $LASTEXITCODE
} finally {
  Pop-Location
  try { Stop-Transcript | Out-Null } catch {}
}
exit $exitCode
