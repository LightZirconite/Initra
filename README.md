# Initra

![Initra logo](assets/logo.png)

Cross-platform workstation bootstrapper for Windows and Linux.

## Run It

Windows `Win+R`:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -Command "& ([ScriptBlock]::Create((Invoke-RestMethod 'https://git.justw.tf/LightZirconite/setup-win/releases/install.ps1')))"
```

Windows `Win+R` with preset `light`:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -Command "& ([ScriptBlock]::Create((Invoke-RestMethod 'https://git.justw.tf/LightZirconite/setup-win/releases/install.ps1'))) --preset light"
```

Linux:

```bash
curl -fsSL https://git.justw.tf/LightZirconite/setup-win/releases/install.sh | sh
```

Linux with preset `light`:

```bash
curl -fsSL https://git.justw.tf/LightZirconite/setup-win/releases/install.sh | sh -s -- --preset light
```

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

Run locally:

```bash
dist/initra.exe --preset generic
dist/initra.exe --dry-run
dist/initra.exe --diagnose
dist/initra.exe --resume
dist/initra.exe --self-update
```

## File Roles

- `initra-windows-amd64.exe` is the real Windows CLI.
- `initra-linux-amd64` is the real Linux CLI.
- `install.ps1` is only a Windows bootstrapper that downloads and launches the Windows binary.
- `install.sh` is only a Linux bootstrapper that downloads and launches the Linux binary.

So there is not a separate “PowerShell version” and “shell version” of Initra itself. There is one native binary per OS, plus a tiny launcher script for each platform.

## Notes

- Linux keeps the main CLI running as the normal user now. Only privileged package-manager commands are escalated when needed. This keeps desktop actions like dark theme, default browser, and refresh-rate tuning usable.
- `releases/latest.json` is used by `--self-update`.
- The repository root executable should not be published. The publishable output is `releases/`.
