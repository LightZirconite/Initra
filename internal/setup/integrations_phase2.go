package setup

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	spicetifyConfigAssetRelPath   = "app/spicetify-config-xpui.ini"
	spicetifyMarketplaceAssetPath = "app/marketplace-settings.json"
	vencordSettingsAssetPath      = "app/vencord-settings.json"
	vencordQuickCSSAssetPath      = "app/vencord-quickCss.css"
	wallpaperAssetPath            = "assets/wallpaper.png"
)

type taskbarPin struct {
	Name string
	Link string
}

func installSpicetifyMarketplace(ctx context.Context, env Environment, logger *Logger, baseURL string) error {
	if !spotifyInstalled(env) {
		fmt.Println("Spotify is not installed, so Spicetify + Marketplace setup was skipped.")
		return nil
	}

	switch env.OS {
	case "windows":
		script := `
$ErrorActionPreference = 'Stop'
iwr -useb https://raw.githubusercontent.com/spicetify/cli/main/install.ps1 | iex
iwr -useb https://raw.githubusercontent.com/spicetify/marketplace/main/resources/install.ps1 | iex
spicetify backup apply
`
		if err := runVisibleUserPowerShellScript(ctx, logger, "Initra Spicetify Setup", script); err != nil {
			return err
		}
	case "linux":
		if err := runShellCommands(ctx, env, logger, []string{
			`curl -fsSL https://raw.githubusercontent.com/spicetify/cli/main/install.sh | sh`,
			`curl -fsSL https://raw.githubusercontent.com/spicetify/marketplace/main/resources/install.sh | sh`,
		}, nil); err != nil {
			return err
		}
	default:
		return nil
	}

	if err := restoreSpicetifyState(ctx, env, logger, baseURL); err != nil {
		return err
	}
	return nil
}

func restoreSpicetifyState(ctx context.Context, env Environment, logger *Logger, baseURL string) error {
	configDir, err := spicetifyConfigDir(env)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(configDir, "Extensions"), 0o755); err != nil {
		return err
	}

	if configPath, cleanup, err := resolveOptionalAssetPath(ctx, env, baseURL, spicetifyConfigAssetRelPath, "app/spicetify-config*.ini"); err == nil {
		if cleanup != nil {
			defer cleanup()
		}
		target := filepath.Join(configDir, "config-xpui.ini")
		if err := backupIfExists(target); err != nil {
			return err
		}
		if err := copyFile(configPath, target, 0o644); err != nil {
			return err
		}
		fmt.Println("Applied bundled Spicetify config-xpui.ini.")
	}

	marketplacePath, cleanup, err := resolveOptionalAssetPath(ctx, env, baseURL, spicetifyMarketplaceAssetPath, "app/marketplace-settings*.json")
	if err != nil {
		if isMissing(err) {
			fmt.Println("No bundled Marketplace state was found. Spicetify was installed without a custom Marketplace restore payload.")
			return nil
		}
		return err
	}
	if cleanup != nil {
		defer cleanup()
	}

	payloadData, err := os.ReadFile(marketplacePath)
	if err != nil {
		return err
	}
	var marketplaceState map[string]string
	if err := json.Unmarshal(payloadData, &marketplaceState); err != nil {
		return err
	}

	restoreScript, err := renderMarketplaceRestoreExtension(filepath.Base(marketplacePath), marketplaceState)
	if err != nil {
		return err
	}
	restorePath := filepath.Join(configDir, "Extensions", "initra-marketplace-restore.js")
	if err := os.WriteFile(restorePath, []byte(restoreScript), 0o644); err != nil {
		return err
	}

	configPath := filepath.Join(configDir, "config-xpui.ini")
	if err := ensureSpicetifyConfigEntries(configPath); err != nil {
		return err
	}

	switch env.OS {
	case "windows":
		script := `
$ErrorActionPreference = 'Stop'
spicetify backup apply
Write-Host 'Spicetify Marketplace restore payload is armed. Launch Spotify once to let the restore extension write its state.'
`
		if err := runVisibleUserPowerShellScript(ctx, logger, "Initra Marketplace Restore", script); err != nil {
			return err
		}
	case "linux":
		if err := runShellCommands(ctx, env, logger, []string{`spicetify backup apply`}, nil); err != nil {
			return err
		}
		fmt.Println("Spicetify Marketplace restore payload is armed. Launch Spotify once to let the restore extension write its state.")
	}

	return nil
}

func renderMarketplaceRestoreExtension(assetName string, payload map[string]string) (string, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`// Initra Marketplace Restore
(function () {
  const payload = %s;
  const stamp = %q;
  const doneKey = "initra:marketplace:restore";
  const apply = () => {
    if (!window.localStorage) return;
    if (window.localStorage.getItem(doneKey) === stamp) return;
    const storage = window.Spicetify && window.Spicetify.LocalStorage
      ? window.Spicetify.LocalStorage
      : { set: (key, value) => window.localStorage.setItem(key, value) };
    Object.entries(payload).forEach(([key, value]) => {
      try {
        storage.set(key, value);
      } catch (_) {}
    });
    window.localStorage.setItem(doneKey, stamp);
    console.info("Initra Marketplace restore applied.");
  };
  let attempts = 0;
  const timer = setInterval(() => {
    attempts += 1;
    if ((window.Spicetify && window.Spicetify.LocalStorage) || attempts >= 60) {
      clearInterval(timer);
      apply();
    }
  }, 1000);
})();`, string(encoded), assetName), nil
}

func ensureSpicetifyConfigEntries(path string) error {
	content := ""
	if data, err := os.ReadFile(path); err == nil {
		content = strings.ReplaceAll(string(data), "\r\n", "\n")
	}
	content = ensureIniValue(content, "AdditionalOptions", "extensions", "initra-marketplace-restore.js", true)
	content = ensureIniValue(content, "AdditionalOptions", "custom_apps", "marketplace", true)
	return os.WriteFile(path, []byte(content), 0o644)
}

func ensureIniValue(content, section, key, value string, pipeList bool) string {
	lines := []string{}
	if strings.TrimSpace(content) != "" {
		lines = strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	}
	if len(lines) == 0 {
		lines = []string{fmt.Sprintf("[%s]", section), fmt.Sprintf("%s = %s", key, value)}
		return strings.Join(lines, "\n") + "\n"
	}

	sectionHeader := fmt.Sprintf("[%s]", section)
	inSection := false
	sectionFound := false
	keyUpdated := false

	for idx, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			if inSection && !keyUpdated {
				insert := fmt.Sprintf("%s = %s", key, value)
				lines = append(lines[:idx], append([]string{insert}, lines[idx:]...)...)
				keyUpdated = true
				break
			}
			inSection = trimmed == sectionHeader
			if inSection {
				sectionFound = true
			}
			continue
		}
		if !inSection {
			continue
		}
		if strings.HasPrefix(strings.ToLower(trimmed), strings.ToLower(key)+" =") || strings.HasPrefix(strings.ToLower(trimmed), strings.ToLower(key)+"=") {
			existing := strings.TrimSpace(strings.SplitN(trimmed, "=", 2)[1])
			if pipeList {
				parts := uniqueStrings(strings.Split(existing, "|"))
				if !contains(parts, value) {
					parts = append(parts, value)
				}
				sort.Strings(parts)
				lines[idx] = fmt.Sprintf("%s = %s", key, strings.Join(parts, "|"))
			} else {
				lines[idx] = fmt.Sprintf("%s = %s", key, value)
			}
			keyUpdated = true
			break
		}
	}

	if !sectionFound {
		lines = append(lines, "", sectionHeader, fmt.Sprintf("%s = %s", key, value))
	} else if !keyUpdated {
		lines = append(lines, fmt.Sprintf("%s = %s", key, value))
	}

	return strings.TrimRight(strings.Join(lines, "\n"), "\n") + "\n"
}

func installVencord(ctx context.Context, env Environment, logger *Logger, baseURL string) error {
	switch env.OS {
	case "windows":
		script := `
$ErrorActionPreference = 'Stop'
if (Get-Command winget -ErrorAction SilentlyContinue) {
  try {
    winget install --id Vendicated.Vencord -e --accept-package-agreements --accept-source-agreements --disable-interactivity
    exit 0
  } catch {
    Write-Host 'winget install failed, falling back to the official Vencord installer.'
  }
}
$target = Join-Path $env:TEMP 'VencordInstaller.exe'
Invoke-WebRequest -UseBasicParsing 'https://github.com/Vencord/Installer/releases/latest/download/VencordInstaller.exe' -OutFile $target
Start-Process -FilePath $target -Wait
`
		if err := runVisibleUserPowerShellScript(ctx, logger, "Initra Vencord Setup", script); err != nil {
			return err
		}
	case "linux":
		if err := runShellCommands(ctx, env, logger, []string{`sh -c "$(curl -sS https://vencord.dev/install.sh)"`}, nil); err != nil {
			return err
		}
	default:
		return nil
	}
	return restoreVencordSettings(ctx, env, logger, baseURL)
}

func restoreVencordSettings(ctx context.Context, env Environment, logger *Logger, baseURL string) error {
	root, err := vencordRootDir(env)
	if err != nil {
		return err
	}
	settingsDir := filepath.Join(root, "settings")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		return err
	}

	settingsAsset, cleanup, err := resolveOptionalAssetPath(ctx, env, baseURL, vencordSettingsAssetPath, "app/vencord-settings*.json")
	if err != nil {
		if !isMissing(err) {
			return err
		}
		fmt.Println("No bundled Vencord settings asset was found.")
		return nil
	}
	if cleanup != nil {
		defer cleanup()
	}
	rawData, err := os.ReadFile(settingsAsset)
	if err != nil {
		return err
	}
	unwrapped, err := unwrapVencordSettings(rawData)
	if err != nil {
		return err
	}

	settingsPath := filepath.Join(settingsDir, "settings.json")
	if err := backupIfExists(settingsPath); err != nil {
		return err
	}
	if err := os.WriteFile(settingsPath, unwrapped, 0o644); err != nil {
		return err
	}
	logger.Println("restored vencord settings to", settingsPath)
	fmt.Println("Restored bundled Vencord settings.")

	quickCSSPath, quickCleanup, err := resolveOptionalAssetPath(ctx, env, baseURL, vencordQuickCSSAssetPath, "app/vencord-quickCss.css")
	if err == nil {
		if quickCleanup != nil {
			defer quickCleanup()
		}
		target := filepath.Join(settingsDir, "quickCss.css")
		if err := backupIfExists(target); err != nil {
			return err
		}
		if err := copyFile(quickCSSPath, target, 0o644); err != nil {
			return err
		}
		fmt.Println("Restored bundled Vencord quickCss.css.")
	}
	return nil
}

func unwrapVencordSettings(raw []byte) ([]byte, error) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	target := raw
	if settings, ok := payload["settings"]; ok && len(settings) > 0 {
		target = settings
	}
	var normalized any
	if err := json.Unmarshal(target, &normalized); err != nil {
		return nil, err
	}
	return json.MarshalIndent(normalized, "", "  ")
}

func installStoat(ctx context.Context, env Environment, logger *Logger) error {
	if env.OS == "windows" {
		if commandExists("winget") {
			if err := runProcess(ctx, env, logger, "winget", "install", "--id", "Stoat.Stoat", "-e", "--accept-package-agreements", "--accept-source-agreements", "--disable-interactivity"); err == nil {
				return nil
			}
		}
		release, err := fetchGitHubRelease(ctx, "stoatchat", "for-desktop")
		if err != nil {
			return err
		}
		for _, asset := range release.Assets {
			if strings.EqualFold(asset.Name, "stoat-desktop-setup.exe") {
				return runDirectInstall(ctx, env, logger, Method{
					URL:      asset.BrowserDownloadURL,
					FileName: asset.Name,
				}, nil)
			}
		}
		return errors.New("could not resolve the Stoat Windows installer")
	}

	release, err := fetchGitHubRelease(ctx, "stoatchat", "for-desktop")
	if err != nil {
		return err
	}
	var assetURL, assetName string
	for _, asset := range release.Assets {
		if strings.Contains(strings.ToLower(asset.Name), "stoat-linux-x64") && strings.HasSuffix(strings.ToLower(asset.Name), ".zip") {
			assetURL = asset.BrowserDownloadURL
			assetName = asset.Name
			break
		}
	}
	if assetURL == "" {
		return errors.New("could not resolve the Stoat Linux release asset")
	}

	downloadPath := filepath.Join(env.TempDir, assetName)
	if err := downloadFile(ctx, assetURL, downloadPath); err != nil {
		return err
	}
	targetDir := filepath.Join(env.HomeDir, ".local", "opt", "stoat")
	if err := os.RemoveAll(targetDir); err != nil {
		return err
	}
	if err := unzipArchive(downloadPath, targetDir); err != nil {
		return err
	}
	binaryPath, err := findFileRecursive(targetDir, "stoat-desktop")
	if err != nil {
		return err
	}
	if err := os.Chmod(binaryPath, 0o755); err != nil {
		return err
	}

	localBin := filepath.Join(env.HomeDir, ".local", "bin")
	if err := os.MkdirAll(localBin, 0o755); err != nil {
		return err
	}
	launcher := filepath.Join(localBin, "stoat-desktop")
	if err := copyFile(binaryPath, launcher, 0o755); err != nil {
		return err
	}

	desktopDir := filepath.Join(env.HomeDir, ".local", "share", "applications")
	if err := os.MkdirAll(desktopDir, 0o755); err != nil {
		return err
	}
	desktopFile := filepath.Join(desktopDir, "stoat.desktop")
	desktopContent := fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=Stoat
Exec=%s
Terminal=false
Categories=Network;Chat;
`, launcher)
	if err := os.WriteFile(desktopFile, []byte(desktopContent), 0o644); err != nil {
		return err
	}

	fmt.Println("Installed Stoat into ~/.local/opt/stoat and created a desktop entry.")
	return nil
}

func applyFirefoxPolicies(ctx context.Context, env Environment, logger *Logger) error {
	if !firefoxInstalled(env) {
		fmt.Println("Firefox is not installed, so Firefox policy deployment was skipped.")
		return nil
	}
	policies := map[string]any{
		"policies": map[string]any{
			"DontCheckDefaultBrowser": true,
			"OverrideFirstRunPage":    "",
			"OverridePostUpdatePage":  "",
			"NoDefaultBookmarks":      true,
		},
	}
	data, err := json.MarshalIndent(policies, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	switch env.OS {
	case "windows":
		firefoxPath := findFirefoxBinaryWindows()
		if firefoxPath == "" {
			fmt.Println("Firefox was not found on disk, so policy deployment was skipped.")
			return nil
		}
		targetDir := filepath.Join(filepath.Dir(firefoxPath), "distribution")
		if err := os.MkdirAll(targetDir, 0o755); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(targetDir, "policies.json"), data, 0o644)
	case "linux":
		candidates := []string{
			"/usr/lib/firefox/distribution",
			"/usr/lib64/firefox/distribution",
			"/opt/firefox/distribution",
		}
		tempPath := filepath.Join(env.TempDir, "firefox-policies.json")
		if err := os.WriteFile(tempPath, data, 0o644); err != nil {
			return err
		}
		for _, candidate := range candidates {
			parent := filepath.Dir(candidate)
			if _, err := os.Stat(parent); err == nil {
				if env.IsAdmin {
					if err := os.MkdirAll(candidate, 0o755); err != nil {
						return err
					}
					return os.WriteFile(filepath.Join(candidate, "policies.json"), data, 0o644)
				}
				if env.HasSudo {
					if err := runProcess(ctx, env, logger, "sudo", "mkdir", "-p", candidate); err != nil {
						return err
					}
					return runProcess(ctx, env, logger, "sudo", "install", "-m", "644", tempPath, filepath.Join(candidate, "policies.json"))
				}
				return errors.New("firefox policy deployment on Linux requires root or sudo")
			}
		}
		fmt.Println("Firefox policy deployment was skipped on Linux because no supported Firefox distribution directory was found.")
		return nil
	default:
		return nil
	}
}

func applyWallpaper(ctx context.Context, env Environment, logger *Logger, baseURL string) error {
	source, cleanup, err := resolveAssetPath(ctx, env, baseURL, wallpaperAssetPath)
	if err != nil {
		return err
	}
	if cleanup != nil {
		defer cleanup()
	}
	paths, err := resolvePaths(CLIOptions{})
	if err != nil {
		return err
	}
	targetDir := filepath.Join(paths.BaseDir, "assets")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return err
	}
	target := filepath.Join(targetDir, "wallpaper.png")
	if err := copyFile(source, target, 0o644); err != nil {
		return err
	}

	switch env.OS {
	case "windows":
		script := fmt.Sprintf(`
$signature = @"
using System;
using System.Runtime.InteropServices;
public static class WallpaperApi {
  [DllImport("user32.dll", SetLastError=true)]
  public static extern bool SystemParametersInfo(int uiAction, int uiParam, string pvParam, int fWinIni);
}
"@
Add-Type -TypeDefinition $signature -ErrorAction SilentlyContinue
Set-ItemProperty 'HKCU:\Control Panel\Desktop' -Name WallpaperStyle -Value '10'
Set-ItemProperty 'HKCU:\Control Panel\Desktop' -Name TileWallpaper -Value '0'
[WallpaperApi]::SystemParametersInfo(20, 0, '%s', 3) | Out-Null
`, filepath.Clean(target))
		return runWindowsPowerShellScript(ctx, logger, script)
	case "linux":
		uri := "file://" + filepath.ToSlash(target)
		if env.Capabilities["gsettings"] && strings.Contains(strings.ToLower(env.DesktopSession), "gnome") {
			if err := runProcess(ctx, env, logger, "gsettings", "set", "org.gnome.desktop.background", "picture-uri", uri); err != nil {
				return err
			}
			return runProcess(ctx, env, logger, "gsettings", "set", "org.gnome.desktop.background", "picture-uri-dark", uri)
		}
		if (strings.Contains(strings.ToLower(env.DesktopSession), "kde") || strings.Contains(strings.ToLower(env.DesktopSession), "plasma")) && env.Capabilities["plasma-apply-wallpaperimage"] {
			return runProcess(ctx, env, logger, "plasma-apply-wallpaperimage", target)
		}
		fmt.Println("Wallpaper application was skipped on Linux because only GNOME and KDE Plasma desktops are handled automatically right now.")
	}
	return nil
}

func applyWindowsDefaultApps(ctx context.Context, env Environment, logger *Logger) error {
	if env.OS != "windows" {
		return nil
	}
	_ = setFirefoxDefault(ctx, env, logger)
	fmt.Println("Windows does not support fully silent default-app changes for every handler. Opening Default Apps so you can validate browser, mailto, Photos and Media Player associations.")
	return runWindowsSettingsURI(ctx, logger, "ms-settings:defaultapps")
}

func applyWindowsTaskbarCleanup(ctx context.Context, env Environment, logger *Logger) error {
	if env.OS != "windows" {
		return nil
	}
	paths, err := resolvePaths(CLIOptions{})
	if err != nil {
		return err
	}
	taskbarDir := filepath.Join(paths.BaseDir, "taskbar")
	if err := os.MkdirAll(taskbarDir, 0o755); err != nil {
		return err
	}
	linkDir := filepath.Join(os.Getenv("APPDATA"), "Microsoft", "Windows", "Start Menu", "Programs", "Initra")
	if err := os.MkdirAll(linkDir, 0o755); err != nil {
		return err
	}

	var pins []taskbarPin
	pins = append(pins, taskbarPin{Name: "File Explorer", Link: "Microsoft.Windows.Explorer"})

	for _, entry := range []struct {
		Name       string
		Shortcut   string
		Binary     string
		AppID      string
		ShortcutOK func() string
	}{
		{Name: "Firefox", Shortcut: filepath.Join(linkDir, "Firefox.lnk"), ShortcutOK: func() string { return findFirefoxBinaryWindows() }},
		{Name: "Spotify", Shortcut: filepath.Join(linkDir, "Spotify.lnk"), ShortcutOK: findSpotifyBinaryWindows},
		{Name: "Steam", Shortcut: filepath.Join(linkDir, "Steam.lnk"), ShortcutOK: findSteamBinaryWindows},
		{Name: "Stoat", Shortcut: filepath.Join(linkDir, "Stoat.lnk"), ShortcutOK: findStoatBinaryWindows},
		{Name: "Discord", Shortcut: filepath.Join(linkDir, "Discord.lnk"), ShortcutOK: findDiscordBinaryWindows},
		{Name: "Discord PTB", Shortcut: filepath.Join(linkDir, "Discord PTB.lnk"), ShortcutOK: findDiscordPTBBinaryWindows},
	} {
		target := ""
		if entry.ShortcutOK != nil {
			target = entry.ShortcutOK()
		}
		if target == "" {
			continue
		}
		if err := createWindowsShortcut(entry.Shortcut, target); err != nil {
			logger.Println("create shortcut failed", entry.Name, err)
			continue
		}
		pins = append(pins, taskbarPin{Name: entry.Name, Link: entry.Shortcut})
	}

	xmlPath := filepath.Join(taskbarDir, "taskbar-layout.xml")
	if err := os.WriteFile(xmlPath, []byte(renderTaskbarLayoutXML(pins)), 0o644); err != nil {
		return err
	}

	commands := []string{
		fmt.Sprintf(`reg add "HKCU\Software\Policies\Microsoft\Windows\Explorer" /v StartLayoutFile /t REG_SZ /d "%s" /f`, xmlPath),
	}
	if isWindows10(env) {
		commands = append(commands,
			`reg add "HKCU\Software\Microsoft\Windows\CurrentVersion\Explorer\Advanced" /v TaskbarSmallIcons /t REG_DWORD /d 1 /f`,
			`reg add "HKCU\Software\Microsoft\Windows\CurrentVersion\Search" /v SearchboxTaskbarMode /t REG_DWORD /d 1 /f`,
		)
	}
	if err := runShellCommands(ctx, env, logger, commands, nil); err != nil {
		return err
	}

	fmt.Println("Applied an Initra taskbar layout policy. It should settle after Explorer restarts or the next sign-in.")
	return nil
}

func renderTaskbarLayoutXML(pins []taskbarPin) string {
	var builder strings.Builder
	builder.WriteString(`<?xml version="1.0" encoding="utf-8"?>` + "\n")
	builder.WriteString(`<LayoutModificationTemplate xmlns="http://schemas.microsoft.com/Start/2014/LayoutModification" xmlns:defaultlayout="http://schemas.microsoft.com/Start/2014/FullDefaultLayout" xmlns:taskbar="http://schemas.microsoft.com/Start/2014/TaskbarLayout" Version="1">` + "\n")
	builder.WriteString(`  <CustomTaskbarLayoutCollection PinListPlacement="Replace">` + "\n")
	builder.WriteString(`    <defaultlayout:TaskbarLayout>` + "\n")
	builder.WriteString(`      <taskbar:TaskbarPinList>` + "\n")
	for _, pin := range pins {
		if pin.Link == "Microsoft.Windows.Explorer" {
			builder.WriteString(`        <taskbar:DesktopApp DesktopApplicationID="Microsoft.Windows.Explorer"/>` + "\n")
			continue
		}
		builder.WriteString(fmt.Sprintf(`        <taskbar:DesktopApp DesktopApplicationLinkPath="%s"/>`+"\n", xmlEscape(pin.Link)))
	}
	builder.WriteString(`      </taskbar:TaskbarPinList>` + "\n")
	builder.WriteString(`    </defaultlayout:TaskbarLayout>` + "\n")
	builder.WriteString(`  </CustomTaskbarLayoutCollection>` + "\n")
	builder.WriteString(`</LayoutModificationTemplate>` + "\n")
	return builder.String()
}

func runWindowsStartupCleanup(ctx context.Context, env Environment, logger *Logger) error {
	if env.OS != "windows" {
		return nil
	}
	script := `
$patterns = @('proton', 'steam', 'xbox', 'roblox', 'discord')
$runPaths = @(
  'HKCU:\Software\Microsoft\Windows\CurrentVersion\Run',
  'HKLM:\Software\Microsoft\Windows\CurrentVersion\Run'
)
foreach ($path in $runPaths) {
  if (-not (Test-Path $path)) { continue }
  $props = Get-ItemProperty -Path $path
  foreach ($prop in $props.PSObject.Properties) {
    if ($prop.Name -like 'PS*') { continue }
    $blob = ($prop.Name + ' ' + [string]$prop.Value).ToLowerInvariant()
    if ($patterns | Where-Object { $blob -like ('*' + $_ + '*') }) {
      try { Remove-ItemProperty -Path $path -Name $prop.Name -Force -ErrorAction SilentlyContinue } catch {}
    }
  }
}
$startupFolders = @(
  [Environment]::GetFolderPath('Startup'),
  "$env:ProgramData\Microsoft\Windows\Start Menu\Programs\Startup"
)
foreach ($folder in $startupFolders) {
  if (-not (Test-Path $folder)) { continue }
  Get-ChildItem -LiteralPath $folder -Force -ErrorAction SilentlyContinue | ForEach-Object {
    $name = $_.Name.ToLowerInvariant()
    if ($patterns | Where-Object { $name -like ('*' + $_ + '*') }) {
      try { Remove-Item -LiteralPath $_.FullName -Force -ErrorAction SilentlyContinue } catch {}
    }
  }
}
$discordRoots = @(
  "$env:APPDATA\discord\settings.json",
  "$env:APPDATA\discordptb\settings.json",
  "$env:APPDATA\discordcanary\settings.json"
)
foreach ($settingsPath in $discordRoots) {
  if (-not (Test-Path $settingsPath)) { continue }
  try {
    $payload = Get-Content -LiteralPath $settingsPath -Raw | ConvertFrom-Json
    $payload | Add-Member -NotePropertyName OPEN_ON_STARTUP -NotePropertyValue $false -Force
    $payload | ConvertTo-Json -Depth 50 | Set-Content -LiteralPath $settingsPath -Encoding UTF8
  } catch {}
}
Write-Host 'Startup cleanup finished.'
`
	return runWindowsPowerShellScript(ctx, logger, script)
}

func maybeInstallSteamDeckLCDDrivers(ctx context.Context, env Environment, logger *Logger) error {
	if env.OS != "windows" || !isSteamDeckLCD(env) {
		return nil
	}
	fmt.Println("Steam Deck LCD hardware detected. Applying the official Valve Windows driver bundle.")

	paths, err := resolvePaths(CLIOptions{})
	if err != nil {
		return err
	}
	targetRoot := filepath.Join(paths.BaseDir, "steamdeck-lcd-drivers")
	if err := os.MkdirAll(targetRoot, 0o755); err != nil {
		return err
	}

	driverZips := []string{
		"https://steamdeck-packages.steamos.cloud/misc/windows/drivers/Aerith_Sephiroth_Windows_Driver_2309131113.zip",
		"https://steamdeck-packages.steamos.cloud/misc/windows/drivers/RTLWlanE_WindowsDriver_2024.0.10.137_Drv_3.00.0039_Win11.L.zip",
		"https://steamdeck-packages.steamos.cloud/misc/windows/drivers/RTBlueR_FilterDriver_1041.3005_1201.2021_new_L.zip",
		"https://steamdeck-packages.steamos.cloud/misc/windows/drivers/BayHub_SD_STOR_installV3.4.01.89_W10W11_logoed_20220228.zip",
		"https://steamdeck-packages.steamos.cloud/misc/windows/drivers/cs35l41-V1.2.1.0.zip",
		"https://steamdeck-packages.steamos.cloud/misc/windows/drivers/NAU88L21_x64_1.0.6.0_WHQL%20-%20DUA_BIQ_WHQL.zip",
	}
	for _, driverURL := range driverZips {
		name := filepath.Base(strings.Split(driverURL, "?")[0])
		zipPath := filepath.Join(targetRoot, name)
		if err := downloadFile(ctx, driverURL, zipPath); err != nil {
			return err
		}
		dest := filepath.Join(targetRoot, strings.TrimSuffix(name, filepath.Ext(name)))
		if err := os.MkdirAll(dest, 0o755); err != nil {
			return err
		}
		if err := unzipArchive(zipPath, dest); err != nil {
			return err
		}
	}

	for _, executable := range []string{"setup.exe", "install.bat", "installdriver.cmd"} {
		matches, err := findFilesRecursive(targetRoot, executable)
		if err != nil {
			return err
		}
		for _, match := range matches {
			fmt.Printf("Running Steam Deck driver helper %s\n", match)
			if err := runProcess(ctx, env, logger, match); err != nil {
				logger.Println("steamdeck driver helper failed", match, err)
			}
		}
	}
	for _, infName := range []string{"cs35l41.inf", "NAU88L21.inf"} {
		matches, err := findFilesRecursive(targetRoot, infName)
		if err != nil {
			return err
		}
		for _, match := range matches {
			if err := runProcess(ctx, env, logger, "pnputil", "/add-driver", match, "/install"); err != nil {
				logger.Println("steamdeck inf install failed", match, err)
			}
		}
	}

	fmt.Printf("Steam Deck LCD driver bundle prepared in %s\n", targetRoot)
	fmt.Println("A reboot will likely be needed after the driver sequence.")
	return nil
}

func spotifyInstalled(env Environment) bool {
	switch env.OS {
	case "windows":
		return findSpotifyBinaryWindows() != ""
	case "linux":
		return commandExists("spotify") || commandExitOK("flatpak", "info", "com.spotify.Client")
	default:
		return false
	}
}

func firefoxInstalled(env Environment) bool {
	switch env.OS {
	case "windows":
		return findFirefoxBinaryWindows() != ""
	case "linux":
		return commandExists("firefox")
	default:
		return false
	}
}

func spicetifyConfigDir(env Environment) (string, error) {
	switch env.OS {
	case "windows":
		appData := os.Getenv("APPDATA")
		if appData == "" {
			return "", errors.New("APPDATA is not set")
		}
		return filepath.Join(appData, "spicetify"), nil
	case "linux":
		if env.HomeDir == "" {
			return "", errors.New("home directory is not available")
		}
		return filepath.Join(env.HomeDir, ".config", "spicetify"), nil
	default:
		return "", fmt.Errorf("spicetify config path is not implemented on %s", env.OS)
	}
}

func vencordRootDir(env Environment) (string, error) {
	switch env.OS {
	case "windows":
		appData := os.Getenv("APPDATA")
		if appData == "" {
			return "", errors.New("APPDATA is not set")
		}
		return filepath.Join(appData, "Vencord"), nil
	case "linux":
		if env.HomeDir == "" {
			return "", errors.New("home directory is not available")
		}
		return filepath.Join(env.HomeDir, ".config", "Vencord"), nil
	default:
		return "", fmt.Errorf("vencord path is not implemented on %s", env.OS)
	}
}

func resolveOptionalAssetPath(ctx context.Context, env Environment, baseURL, remoteRelPath string, localPatterns ...string) (string, func(), error) {
	candidates := []string{}
	roots := []string{
		mustAbs("."),
		filepath.Join(mustAbs("."), "releases"),
	}
	if execPath, err := os.Executable(); err == nil {
		execDir := filepath.Dir(execPath)
		roots = append(roots, execDir, filepath.Join(execDir, ".."), filepath.Join(execDir, "releases"))
	}
	for _, pattern := range localPatterns {
		pattern = filepath.FromSlash(strings.ReplaceAll(pattern, `\`, `/`))
		for _, root := range roots {
			matches, _ := filepath.Glob(filepath.Join(root, pattern))
			candidates = append(candidates, matches...)
		}
	}
	candidates = uniqueStrings(candidates)
	sort.Strings(candidates)
	for i := len(candidates) - 1; i >= 0; i-- {
		if _, err := os.Stat(candidates[i]); err == nil {
			return candidates[i], nil, nil
		}
	}
	return resolveAssetPath(ctx, env, baseURL, remoteRelPath)
}

func runVisibleUserPowerShellScript(ctx context.Context, logger *Logger, title, body string) error {
	wasTopmost := hostedSessionTopmostEnabled()
	if wasTopmost {
		_ = setWindowsConsoleTopmost(ctx, logger, false)
	}
	defer func() {
		if wasTopmost && hostedSessionTopmostEnabled() {
			_ = setWindowsConsoleTopmost(context.Background(), logger, true)
		}
	}()

	tempDir, err := os.MkdirTemp("", "initra-userps-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tempDir)

	doneFile := filepath.Join(tempDir, "done.ok")
	errorFile := filepath.Join(tempDir, "error.txt")
	scriptPath := filepath.Join(tempDir, "script.ps1")
	launcherPath := filepath.Join(tempDir, "launch.cmd")

	wrapped := fmt.Sprintf(`$Host.UI.RawUI.WindowTitle = %q
try {
%s
  Set-Content -LiteralPath %q -Value 'ok' -Encoding UTF8
} catch {
  $_ | Out-String | Set-Content -LiteralPath %q -Encoding UTF8
  exit 1
}`, title, body, doneFile, errorFile)
	if err := os.WriteFile(scriptPath, []byte(wrapped), 0o644); err != nil {
		return err
	}
	launcher := fmt.Sprintf("@echo off\r\npowershell.exe -NoProfile -ExecutionPolicy Bypass -File \"%s\"\r\n", scriptPath)
	if err := os.WriteFile(launcherPath, []byte(launcher), 0o644); err != nil {
		return err
	}

	escapedLauncher := strings.ReplaceAll(launcherPath, `'`, `''`)
	shellScript := fmt.Sprintf(`$shell = New-Object -ComObject Shell.Application; $shell.ShellExecute('cmd.exe', '/c ""%s""', '', 'open', 1)`, escapedLauncher)
	if _, err := runOutput("powershell", "-NoProfile", "-NonInteractive", "-Command", shellScript); err != nil {
		return err
	}

	for {
		if _, err := os.Stat(doneFile); err == nil {
			return nil
		}
		if data, err := os.ReadFile(errorFile); err == nil && len(strings.TrimSpace(string(data))) > 0 {
			return errors.New(strings.TrimSpace(string(data)))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func unzipArchive(zipPath, dest string) error {
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer reader.Close()

	for _, file := range reader.File {
		targetPath := filepath.Join(dest, file.Name)
		cleanDest := filepath.Clean(dest) + string(os.PathSeparator)
		cleanTarget := filepath.Clean(targetPath)
		if !strings.HasPrefix(cleanTarget, cleanDest) && cleanTarget != filepath.Clean(dest) {
			return fmt.Errorf("zip entry %s escapes destination", file.Name)
		}
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(cleanTarget, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(cleanTarget), 0o755); err != nil {
			return err
		}
		src, err := file.Open()
		if err != nil {
			return err
		}
		dst, err := os.OpenFile(cleanTarget, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, file.Mode())
		if err != nil {
			src.Close()
			return err
		}
		if _, err := io.Copy(dst, src); err != nil {
			dst.Close()
			src.Close()
			return err
		}
		dst.Close()
		src.Close()
	}
	return nil
}

func backupIfExists(path string) error {
	if _, err := os.Stat(path); err != nil {
		if isMissing(err) {
			return nil
		}
		return err
	}
	backup := fmt.Sprintf("%s.bak-%s", path, time.Now().Format("20060102-150405"))
	return copyFile(path, backup, 0o644)
}

func copyFile(src, dst string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return nil
}

func findFileRecursive(root, name string) (string, error) {
	files, err := findFilesRecursive(root, name)
	if err != nil {
		return "", err
	}
	if len(files) == 0 {
		return "", fmt.Errorf("could not find %s under %s", name, root)
	}
	return files[0], nil
}

func findFilesRecursive(root, name string) ([]string, error) {
	var matches []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.EqualFold(d.Name(), name) {
			matches = append(matches, path)
		}
		return nil
	})
	return matches, err
}

func isSteamDeckLCD(env Environment) bool {
	manufacturer := strings.ToLower(env.Windows.Manufacturer)
	model := strings.ToLower(env.Windows.Model)
	return strings.Contains(manufacturer, "valve") || strings.Contains(model, "steam deck") || strings.Contains(model, "jupiter")
}

func createWindowsShortcut(shortcutPath, targetPath string) error {
	return createWindowsShortcutEx(shortcutPath, targetPath, filepath.Dir(targetPath), "", "")
}

func createWindowsShortcutEx(shortcutPath, targetPath, workingDirectory, arguments, iconLocation string) error {
	ps := fmt.Sprintf(`$w = New-Object -ComObject WScript.Shell; $s = $w.CreateShortcut('%s'); $s.TargetPath = '%s'; $s.WorkingDirectory = '%s'; $s.Arguments = '%s'; $s.IconLocation = '%s'; $s.Save()`,
		strings.ReplaceAll(shortcutPath, `'`, `''`),
		strings.ReplaceAll(targetPath, `'`, `''`),
		strings.ReplaceAll(workingDirectory, `'`, `''`),
		strings.ReplaceAll(arguments, `'`, `''`),
		strings.ReplaceAll(iconLocation, `'`, `''`),
	)
	_, err := runOutput("powershell", "-NoProfile", "-NonInteractive", "-Command", ps)
	return err
}

func xmlEscape(value string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		`"`, "&quot;",
		"<", "&lt;",
		">", "&gt;",
	)
	return replacer.Replace(value)
}

func findSpotifyBinaryWindows() string {
	candidates := []string{
		filepath.Join(os.Getenv("APPDATA"), "Spotify", "Spotify.exe"),
		filepath.Join(os.Getenv("LOCALAPPDATA"), "Microsoft", "WindowsApps", "Spotify.exe"),
		filepath.Join(os.Getenv("ProgramFiles"), "Spotify", "Spotify.exe"),
	}
	for _, candidate := range candidates {
		if candidate != "" {
			if _, err := os.Stat(candidate); err == nil {
				return candidate
			}
		}
	}
	return ""
}

func findSteamBinaryWindows() string {
	candidates := []string{
		filepath.Join(os.Getenv("ProgramFiles(x86)"), "Steam", "steam.exe"),
		filepath.Join(os.Getenv("ProgramFiles"), "Steam", "steam.exe"),
	}
	for _, candidate := range candidates {
		if candidate != "" {
			if _, err := os.Stat(candidate); err == nil {
				return candidate
			}
		}
	}
	return ""
}

func findStoatBinaryWindows() string {
	base := filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "Stoat")
	candidates := []string{
		filepath.Join(base, "Stoat.exe"),
		filepath.Join(base, "stoat-desktop.exe"),
	}
	for _, candidate := range candidates {
		if candidate != "" {
			if _, err := os.Stat(candidate); err == nil {
				return candidate
			}
		}
	}
	return ""
}

func findDiscordBinaryWindows() string {
	return findDiscordBinaryWindowsVariant("discord")
}

func findDiscordPTBBinaryWindows() string {
	return findDiscordBinaryWindowsVariant("discordptb")
}

func findDiscordBinaryWindowsVariant(folder string) string {
	root := filepath.Join(os.Getenv("LOCALAPPDATA"), folder)
	matches, _ := filepath.Glob(filepath.Join(root, "app-*", filepath.Base(folder)+".exe"))
	if len(matches) == 0 {
		matches, _ = filepath.Glob(filepath.Join(root, "app-*", "Discord.exe"))
	}
	sort.Strings(matches)
	for i := len(matches) - 1; i >= 0; i-- {
		if _, err := os.Stat(matches[i]); err == nil {
			return matches[i]
		}
	}
	return ""
}
