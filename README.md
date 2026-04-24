# Initra

<p align="center">
  <img src="assets/logo.png" alt="Initra logo" width="128" />
</p>

Cross-platform workstation bootstrapper for Windows and Linux.

## Run It

Windows `Win+R`:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -Command "& ([ScriptBlock]::Create((Invoke-RestMethod 'https://git.justw.tf/LightZirconite/setup-win/raw/branch/main/releases/install.ps1')))"
```

Windows `Win+R` with preset `personal`:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -Command "& ([ScriptBlock]::Create((Invoke-RestMethod 'https://git.justw.tf/LightZirconite/setup-win/raw/branch/main/releases/install.ps1'))) --preset personal"
```

Linux:

```bash
curl -fsSL https://git.justw.tf/LightZirconite/setup-win/raw/branch/main/releases/install.sh | sh
```

Linux with preset `personal`:

```bash
curl -fsSL https://git.justw.tf/LightZirconite/setup-win/raw/branch/main/releases/install.sh | sh -s -- --preset personal
```

Preset notes:

- `generic` is the neutral base setup.
- `personal` is the broader personal preset.
- `light` is still accepted as a backward-compatible alias for `personal`.

## What To Publish

Publish the `releases/` folder after running `build-release.bat`.

Expected release files:

- `releases/initra-windows-amd64.exe`
- `releases/initra-linux-amd64`
- `releases/install.ps1`
- `releases/install.sh`
- `releases/latest.json`
- `releases/checksums.txt`
- `releases/catalog/catalog.yaml`
- `releases/app/pack-emoji.ttf`
- `releases/app/marketplace-settings.json`
- `releases/app/vencord-settings.json`
- `releases/assets/firefox/layout/ui-layout.json`
- `releases/assets/wallpaper.png`

Source assets live under `app/` and `assets/`. The `releases/` tree is the staged/published copy consumed by the bootstrap scripts, so release builds copy app and asset files into `releases/`.

## Local Use

One-click Windows release build:

```bat
.\build-release.bat
```

Direct local build:

```bash
go build -o dist/initra.exe ./cmd/setupctl
GOOS=linux GOARCH=amd64 go build -o dist/initra-linux-amd64 ./cmd/setupctl
```

Do not use plain `go build ./cmd/setupctl` from the repository root unless you intentionally want a temporary local `setupctl.exe` artifact there. The publishable binaries are the ones written into `dist/` or `releases/`.

Run locally:

```bash
dist/initra.exe --preset generic
dist/initra.exe --dry-run
dist/initra.exe --diagnose
dist/initra.exe --resume
dist/initra.exe --self-update
dist/initra.exe --capture-firefox-layout
```

## File Roles

- `initra-windows-amd64.exe` is the real Windows CLI.
- `initra-linux-amd64` is the real Linux CLI.
- `install.ps1` is only a Windows bootstrapper that downloads and launches the Windows binary.
- `install.sh` is only a Linux bootstrapper that downloads and launches the Linux binary.

So there is not a separate “PowerShell version” and “shell version” of Initra itself. There is one native binary per OS, plus a tiny launcher script for each platform.

## Firefox Layout

Capture the current machine's non-sensitive Firefox UI layout into the repository:

```powershell
dist\initra.exe --capture-firefox-layout
```

This captures toolbar/button placement and a few safe UI prefs only. It does not export logins, cookies, browsing history, or bookmarks.

## Notes

- Linux keeps the main CLI running as the normal user now. Only privileged package-manager commands are escalated when needed. This keeps desktop actions like dark theme, default browser, and refresh-rate tuning usable.
- `releases/latest.json` is used by `--self-update`.
- The repository root executable should not be published. The publishable output is `releases/`.
- Initra intentionally does not automate SmartScreen disablement, Defender bypasses, third-party Edge removal, unsupported Office/Windows activation flows, or RDP security weakening.
