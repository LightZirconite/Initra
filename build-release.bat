@echo off
setlocal EnableExtensions EnableDelayedExpansion

set "ROOT=%~dp0"
cd /d "%ROOT%"

set "BUILD_TARGET=%~1"
if not defined BUILD_TARGET set "BUILD_TARGET=all"
if /I "%BUILD_TARGET%"=="initra" set "BUILD_TARGET=setup"
if /I "%BUILD_TARGET%"=="setupctl" set "BUILD_TARGET=setup"
set "BUILD_SETUP=0"
set "BUILD_AGENT=0"
if /I "%BUILD_TARGET%"=="all" (
  set "BUILD_SETUP=1"
  set "BUILD_AGENT=1"
) else if /I "%BUILD_TARGET%"=="setup" (
  set "BUILD_SETUP=1"
) else if /I "%BUILD_TARGET%"=="agent" (
  set "BUILD_AGENT=1"
) else (
  echo Usage: build-release.bat [all^|setup^|agent]
  exit /b 1
)

set "BASE_URL=%INITRA_BASE_URL%"
if not defined BASE_URL set "BASE_URL=%SETUPCTL_BASE_URL%"
if not defined BASE_URL set "BASE_URL=https://git.justw.tf/LightZirconite/setup-win/raw/branch/main"
set "RELEASE_DIR=%ROOT%releases"
set "WIN_BIN=%RELEASE_DIR%\initra-windows-amd64.exe"
set "LINUX_BIN=%RELEASE_DIR%\initra-linux-amd64"
set "AGENT_WIN_BIN=%RELEASE_DIR%\initra-agent-windows-amd64.exe"
set "AGENT_LINUX_BIN=%RELEASE_DIR%\initra-agent-linux-amd64"
set "ASSET_DIR=%RELEASE_DIR%\app"
set "CATALOG_DIR=%RELEASE_DIR%\catalog"
set "RUNTIME_ASSET_DIR=%RELEASE_DIR%\assets"
set "FIREFOX_LAYOUT_SRC=%ROOT%assets\firefox\layout"
set "FIREFOX_LAYOUT_DIR=%RELEASE_DIR%\assets\firefox\layout"
set "MANIFEST=%RELEASE_DIR%\latest.json"
set "CHECKSUMS=%RELEASE_DIR%\checksums.txt"

set "GO_EXE=C:\Program Files\Go\bin\go.exe"
if exist "%GO_EXE%" goto :go_found
where go >nul 2>nul
if errorlevel 1 (
  echo [ERROR] Go was not found in PATH.
  exit /b 1
)
for /f "usebackq delims=" %%i in (`where go`) do (
  set "GO_EXE=%%i"
  goto :go_found
)
:go_found
if not defined GO_EXE (
  echo [ERROR] Go path could not be resolved.
  exit /b 1
)

for /f "usebackq delims=" %%i in (`git describe --tags --always --dirty 2^>nul`) do set "VERSION=%%i"
if not defined VERSION (
  for /f "usebackq delims=" %%i in (`powershell -NoProfile -Command "(Get-Date).ToUniversalTime().ToString('yyyy.MM.dd.HHmmss')"` ) do set "VERSION=%%i"
)

echo [1/8] Running tests...
"%GO_EXE%" test ./...
if errorlevel 1 goto :fail

if exist "%ROOT%setupctl.exe" del /q "%ROOT%setupctl.exe" >nul 2>nul
if exist "%ROOT%initra.exe" del /q "%ROOT%initra.exe" >nul 2>nul
if exist "%ROOT%initra-agent.exe" del /q "%ROOT%initra-agent.exe" >nul 2>nul
if exist "%ROOT%setupctl" del /q "%ROOT%setupctl" >nul 2>nul
if exist "%ROOT%initra" del /q "%ROOT%initra" >nul 2>nul
if exist "%ROOT%initra-agent" del /q "%ROOT%initra-agent" >nul 2>nul
if exist "%RELEASE_DIR%\*.exe~" del /q "%RELEASE_DIR%\*.exe~" >nul 2>nul

if not exist "%RELEASE_DIR%" mkdir "%RELEASE_DIR%"
if exist "%RELEASE_DIR%\app" rmdir /s /q "%RELEASE_DIR%\app"
if exist "%RELEASE_DIR%\catalog" rmdir /s /q "%RELEASE_DIR%\catalog"
if exist "%RELEASE_DIR%\assets" rmdir /s /q "%RELEASE_DIR%\assets"

if "%BUILD_SETUP%"=="1" (
  echo [2/8] Building Windows setup binary...
  "%GO_EXE%" build -trimpath -ldflags "-s -w -X main.version=%VERSION%" -o "%WIN_BIN%" ./cmd/setupctl
  if errorlevel 1 goto :fail

  echo [3/8] Building Linux setup binary...
  set "GOOS=linux"
  set "GOARCH=amd64"
  "%GO_EXE%" build -trimpath -ldflags "-s -w -X main.version=%VERSION%" -o "%LINUX_BIN%" ./cmd/setupctl
  if errorlevel 1 goto :fail
  set "GOOS="
  set "GOARCH="
) else (
  echo [2/8] Skipping setup binaries.
  echo [3/8] Skipping setup binaries.
)

if "%BUILD_AGENT%"=="1" (
  if exist "%ROOT%cmd\setupctl\rsrc_windows_amd64.syso" copy /Y "%ROOT%cmd\setupctl\rsrc_windows_amd64.syso" "%ROOT%cmd\initra-agent\rsrc_windows_amd64.syso" >nul
  if exist "%ROOT%cmd\setupctl\rsrc_windows_386.syso" copy /Y "%ROOT%cmd\setupctl\rsrc_windows_386.syso" "%ROOT%cmd\initra-agent\rsrc_windows_386.syso" >nul

  echo [4/8] Building Windows agent binary...
  "%GO_EXE%" build -trimpath -ldflags "-s -w -X main.version=%VERSION%" -o "%AGENT_WIN_BIN%" ./cmd/initra-agent
  if errorlevel 1 goto :fail

  echo [5/8] Building Linux agent binary...
  set "GOOS=linux"
  set "GOARCH=amd64"
  "%GO_EXE%" build -trimpath -ldflags "-s -w -X main.version=%VERSION%" -o "%AGENT_LINUX_BIN%" ./cmd/initra-agent
  if errorlevel 1 goto :fail
  set "GOOS="
  set "GOARCH="
) else (
  echo [4/8] Skipping agent binaries.
  echo [5/8] Skipping agent binaries.
)

echo [6/8] Staging bootstrap scripts...
copy /Y "%ROOT%bootstrap\install.ps1" "%RELEASE_DIR%\install.ps1" >nul
if errorlevel 1 goto :fail
copy /Y "%ROOT%bootstrap\install.sh" "%RELEASE_DIR%\install.sh" >nul
if errorlevel 1 goto :fail
if not exist "%ASSET_DIR%" mkdir "%ASSET_DIR%"
if exist "%ROOT%app\pack-emoji.ttf" (
  copy /Y "%ROOT%app\pack-emoji.ttf" "%ASSET_DIR%\pack-emoji.ttf" >nul
  if errorlevel 1 goto :fail
)
if not exist "%CATALOG_DIR%" mkdir "%CATALOG_DIR%"
copy /Y "%ROOT%catalog\catalog.yaml" "%CATALOG_DIR%\catalog.yaml" >nul
if errorlevel 1 goto :fail
if not exist "%RUNTIME_ASSET_DIR%" mkdir "%RUNTIME_ASSET_DIR%"
if exist "%ROOT%assets\wallpaper.png" (
  copy /Y "%ROOT%assets\wallpaper.png" "%RUNTIME_ASSET_DIR%\wallpaper.png" >nul
  if errorlevel 1 goto :fail
)
if exist "%FIREFOX_LAYOUT_SRC%" (
  if not exist "%RELEASE_DIR%\assets\firefox" mkdir "%RELEASE_DIR%\assets\firefox"
  if not exist "%FIREFOX_LAYOUT_DIR%" mkdir "%FIREFOX_LAYOUT_DIR%"
  copy /Y "%FIREFOX_LAYOUT_SRC%\*" "%FIREFOX_LAYOUT_DIR%\" >nul
  if errorlevel 1 goto :fail
)
for %%F in ("%ROOT%app\marketplace-settings*.json") do (
  if exist "%%~fF" (
    copy /Y "%%~fF" "%ASSET_DIR%\marketplace-settings.json" >nul
    if errorlevel 1 goto :fail
    goto :marketplace_done
  )
)
:marketplace_done
for %%F in ("%ROOT%app\vencord-settings*.json") do (
  if exist "%%~fF" (
    copy /Y "%%~fF" "%ASSET_DIR%\vencord-settings.json" >nul
    if errorlevel 1 goto :fail
    goto :vencord_done
  )
)
:vencord_done
if exist "%ROOT%app\spicetify-config-xpui.ini" (
  copy /Y "%ROOT%app\spicetify-config-xpui.ini" "%ASSET_DIR%\spicetify-config-xpui.ini" >nul
  if errorlevel 1 goto :fail
)
if exist "%ROOT%app\vencord-quickCss.css" (
  copy /Y "%ROOT%app\vencord-quickCss.css" "%ASSET_DIR%\vencord-quickCss.css" >nul
  if errorlevel 1 goto :fail
)
if exist "%ROOT%app\profile.xml" (
  copy /Y "%ROOT%app\profile.xml" "%ASSET_DIR%\profile.xml" >nul
  if errorlevel 1 goto :fail
)

echo [7/8] Writing manifest and checksums...
if not exist "%WIN_BIN%" (
  echo [ERROR] Missing %WIN_BIN%. Run build-release.bat setup or all first.
  goto :fail
)
if not exist "%LINUX_BIN%" (
  echo [ERROR] Missing %LINUX_BIN%. Run build-release.bat setup or all first.
  goto :fail
)
if not exist "%AGENT_WIN_BIN%" (
  echo [ERROR] Missing %AGENT_WIN_BIN%. Run build-release.bat agent or all first.
  goto :fail
)
if not exist "%AGENT_LINUX_BIN%" (
  echo [ERROR] Missing %AGENT_LINUX_BIN%. Run build-release.bat agent or all first.
  goto :fail
)
powershell -NoProfile -ExecutionPolicy Bypass -Command ^
  "$ErrorActionPreference='Stop';" ^
  "function Get-Sha256([string]$Path) { $stream=[System.IO.File]::OpenRead($Path); try { $sha=[System.Security.Cryptography.SHA256]::Create(); try { ([System.BitConverter]::ToString($sha.ComputeHash($stream))).Replace('-', '').ToLower() } finally { $sha.Dispose() } } finally { $stream.Dispose() } };" ^
  "$winHash=Get-Sha256 '%WIN_BIN%';" ^
  "$linuxHash=Get-Sha256 '%LINUX_BIN%';" ^
  "$agentWinHash=Get-Sha256 '%AGENT_WIN_BIN%';" ^
  "$agentLinuxHash=Get-Sha256 '%AGENT_LINUX_BIN%';" ^
  "$manifest=[ordered]@{" ^
  "  version='%VERSION%';" ^
  "  published=(Get-Date).ToUniversalTime().ToString('o');" ^
  "  notes='Built by build-release.bat';" ^
  "  artifacts=[ordered]@{" ^
  "    'windows-amd64'='%BASE_URL%/releases/initra-windows-amd64.exe';" ^
  "    'linux-amd64'='%BASE_URL%/releases/initra-linux-amd64';" ^
  "    'agent-windows-amd64'='%BASE_URL%/releases/initra-agent-windows-amd64.exe';" ^
  "    'agent-linux-amd64'='%BASE_URL%/releases/initra-agent-linux-amd64'" ^
  "  };" ^
  "  sha256=[ordered]@{" ^
  "    'windows-amd64'=$winHash;" ^
  "    'linux-amd64'=$linuxHash;" ^
  "    'agent-windows-amd64'=$agentWinHash;" ^
  "    'agent-linux-amd64'=$agentLinuxHash" ^
  "  }" ^
  "};" ^
  "$utf8NoBom=New-Object System.Text.UTF8Encoding($false);" ^
  "[System.IO.File]::WriteAllText('%MANIFEST%', ($manifest | ConvertTo-Json -Depth 5), $utf8NoBom);" ^
  "$checksums=@();" ^
  "$checksums += 'initra-windows-amd64.exe        ' + $winHash;" ^
  "$checksums += 'initra-linux-amd64            ' + $linuxHash;" ^
  "$checksums += 'initra-agent-windows-amd64.exe  ' + $agentWinHash;" ^
  "$checksums += 'initra-agent-linux-amd64      ' + $agentLinuxHash;" ^
  "[System.IO.File]::WriteAllLines('%CHECKSUMS%', [string[]]$checksums, $utf8NoBom);"
if errorlevel 1 goto :fail

echo [8/8] Done.
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
