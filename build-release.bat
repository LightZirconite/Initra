@echo off
setlocal EnableExtensions EnableDelayedExpansion

set "ROOT=%~dp0"
cd /d "%ROOT%"

set "BASE_URL=%INITRA_BASE_URL%"
if not defined BASE_URL set "BASE_URL=%SETUPCTL_BASE_URL%"
if not defined BASE_URL set "BASE_URL=https://git.justw.tf/LightZirconite/setup-win/raw/branch/main"
set "RELEASE_DIR=%ROOT%releases"
set "WIN_BIN=%RELEASE_DIR%\initra-windows-amd64.exe"
set "LINUX_BIN=%RELEASE_DIR%\initra-linux-amd64"
set "ASSET_DIR=%RELEASE_DIR%\app"
set "CATALOG_DIR=%RELEASE_DIR%\catalog"
set "FIREFOX_LAYOUT_SRC=%ROOT%assets\firefox\layout"
set "FIREFOX_LAYOUT_DIR=%RELEASE_DIR%\assets\firefox\layout"
set "MANIFEST=%RELEASE_DIR%\latest.json"
set "CHECKSUMS=%RELEASE_DIR%\checksums.txt"

where go >nul 2>nul
if errorlevel 1 (
  echo [ERROR] Go was not found in PATH.
  exit /b 1
)

for /f "usebackq delims=" %%i in (`git describe --tags --always --dirty 2^>nul`) do set "VERSION=%%i"
if not defined VERSION (
  for /f "usebackq delims=" %%i in (`powershell -NoProfile -Command "(Get-Date).ToUniversalTime().ToString('yyyy.MM.dd.HHmmss')"` ) do set "VERSION=%%i"
)

echo [1/6] Running tests...
go test ./...
if errorlevel 1 goto :fail

if not exist "%RELEASE_DIR%" mkdir "%RELEASE_DIR%"
if exist "%RELEASE_DIR%\setupctl-windows-amd64.exe" del /q "%RELEASE_DIR%\setupctl-windows-amd64.exe"
if exist "%RELEASE_DIR%\setupctl-linux-amd64" del /q "%RELEASE_DIR%\setupctl-linux-amd64"
if exist "%RELEASE_DIR%\app" rmdir /s /q "%RELEASE_DIR%\app"
if exist "%RELEASE_DIR%\catalog" rmdir /s /q "%RELEASE_DIR%\catalog"
if exist "%RELEASE_DIR%\assets" rmdir /s /q "%RELEASE_DIR%\assets"

echo [2/6] Building Windows binary...
go build -trimpath -ldflags "-s -w -X main.version=%VERSION%" -o "%WIN_BIN%" ./cmd/setupctl
if errorlevel 1 goto :fail

echo [3/6] Building Linux binary...
set "GOOS=linux"
set "GOARCH=amd64"
go build -trimpath -ldflags "-s -w -X main.version=%VERSION%" -o "%LINUX_BIN%" ./cmd/setupctl
if errorlevel 1 goto :fail
set "GOOS="
set "GOARCH="

echo [4/6] Staging bootstrap scripts...
copy /Y "%ROOT%bootstrap\install.ps1" "%RELEASE_DIR%\install.ps1" >nul
if errorlevel 1 goto :fail
copy /Y "%ROOT%bootstrap\install.sh" "%RELEASE_DIR%\install.sh" >nul
if errorlevel 1 goto :fail
if not exist "%ASSET_DIR%" mkdir "%ASSET_DIR%"
copy /Y "%ROOT%app\pack-emoji.ttf" "%ASSET_DIR%\pack-emoji.ttf" >nul
if errorlevel 1 goto :fail
if not exist "%CATALOG_DIR%" mkdir "%CATALOG_DIR%"
copy /Y "%ROOT%catalog\catalog.yaml" "%CATALOG_DIR%\catalog.yaml" >nul
if errorlevel 1 goto :fail
if exist "%FIREFOX_LAYOUT_SRC%" (
  if not exist "%RELEASE_DIR%\assets" mkdir "%RELEASE_DIR%\assets"
  if not exist "%RELEASE_DIR%\assets\firefox" mkdir "%RELEASE_DIR%\assets\firefox"
  if not exist "%FIREFOX_LAYOUT_DIR%" mkdir "%FIREFOX_LAYOUT_DIR%"
  copy /Y "%FIREFOX_LAYOUT_SRC%\*" "%FIREFOX_LAYOUT_DIR%\" >nul
  if errorlevel 1 goto :fail
)

echo [5/6] Writing manifest and checksums...
powershell -NoProfile -ExecutionPolicy Bypass -Command ^
  "$ErrorActionPreference='Stop';" ^
  "function Get-Sha256([string]$Path) { $stream=[System.IO.File]::OpenRead($Path); try { $sha=[System.Security.Cryptography.SHA256]::Create(); try { ([System.BitConverter]::ToString($sha.ComputeHash($stream))).Replace('-', '').ToLower() } finally { $sha.Dispose() } } finally { $stream.Dispose() } };" ^
  "$winHash=Get-Sha256 '%WIN_BIN%';" ^
  "$linuxHash=Get-Sha256 '%LINUX_BIN%';" ^
  "$manifest=[ordered]@{" ^
  "  version='%VERSION%';" ^
  "  published=(Get-Date).ToUniversalTime().ToString('o');" ^
  "  notes='Built by build-release.bat';" ^
  "  artifacts=[ordered]@{" ^
  "    'windows-amd64'='%BASE_URL%/releases/initra-windows-amd64.exe';" ^
  "    'linux-amd64'='%BASE_URL%/releases/initra-linux-amd64'" ^
  "  };" ^
  "  sha256=[ordered]@{" ^
  "    'windows-amd64'=$winHash;" ^
  "    'linux-amd64'=$linuxHash" ^
  "  }" ^
  "};" ^
  "$manifest | ConvertTo-Json -Depth 5 | Set-Content -Encoding utf8 '%MANIFEST%';" ^
  "@('initra-windows-amd64.exe  ' + $winHash, 'initra-linux-amd64      ' + $linuxHash) | Set-Content -Encoding utf8 '%CHECKSUMS%';"
if errorlevel 1 goto :fail

echo [6/6] Done.
echo.
echo Version: %VERSION%
echo Release directory: %RELEASE_DIR%
echo.
echo Windows Run command:
echo powershell -NoProfile -ExecutionPolicy Bypass -Command "& ([ScriptBlock]::Create((Invoke-RestMethod '%BASE_URL%/releases/install.ps1')))"
echo.
echo Linux command:
echo curl -fsSL %BASE_URL%/releases/install.sh ^| sh
exit /b 0

:fail
echo.
echo [ERROR] Release build failed.
exit /b 1
