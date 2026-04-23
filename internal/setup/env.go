package setup

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

func detectEnvironment() (Environment, error) {
	host, _ := os.Hostname()
	userName := os.Getenv("USERNAME")
	if userName == "" {
		userName = os.Getenv("USER")
	}
	home, _ := os.UserHomeDir()
	env := Environment{
		OS:           runtime.GOOS,
		Arch:         runtime.GOARCH,
		Hostname:     host,
		UserName:     userName,
		HomeDir:      home,
		TempDir:      os.TempDir(),
		Capabilities: map[string]bool{},
		Raw:          map[string]string{},
	}

	if runtime.GOOS == "windows" {
		env.DocumentsDir = firstExisting(
			filepath.Join(home, "Documents"),
			filepath.Join(home, "documents"),
		)
		if env.DocumentsDir == "" {
			env.DocumentsDir = filepath.Join(home, "Documents")
		}
		if err := detectWindowsDetails(&env); err != nil {
			return env, err
		}
		return env, nil
	}

	env.DocumentsDir = firstExisting(
		filepath.Join(home, "Documents"),
		filepath.Join(home, "documents"),
	)
	if env.DocumentsDir == "" {
		env.DocumentsDir = filepath.Join(home, "Documents")
	}
	if err := detectLinuxDetails(&env); err != nil {
		return env, err
	}
	return env, nil
}

func detectWindowsDetails(env *Environment) error {
	adminOutput, _ := runOutput("powershell", "-NoProfile", "-NonInteractive", "-Command", `([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)`)
	env.IsAdmin = strings.EqualFold(strings.TrimSpace(adminOutput), "true")
	env.HasSudo = false
	env.HasWinget = commandExists("winget")
	if env.HasWinget {
		output, _ := runOutput("winget", "--version")
		env.WingetVersion = strings.TrimSpace(output)
	}
	env.Capabilities["powershell"] = commandExists("powershell")
	env.Capabilities["appinstaller_registration"] = true

	script := `
$info = [ordered]@{}
$cv = Get-ItemProperty 'HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion'
$cs = Get-CimInstance Win32_ComputerSystem
$gpu = (Get-CimInstance Win32_VideoController | Select-Object -First 1 -ExpandProperty Name)
$cpu = (Get-CimInstance Win32_Processor | Select-Object -First 1 -ExpandProperty Manufacturer)
$info.ProductName = $cv.ProductName
$info.DisplayVersion = $cv.DisplayVersion
$info.EditionID = $cv.EditionID
$info.ReleaseID = $cv.ReleaseId
$info.CurrentBuild = $cv.CurrentBuild
$info.Manufacturer = $cs.Manufacturer
$info.Model = $cs.Model
$info.GPUVendor = $gpu
$info.CPUVendor = $cpu
$info | ConvertTo-Json -Compress
`

	output, err := runOutput("powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	if err == nil {
		var raw struct {
			ProductName    string `json:"ProductName"`
			DisplayVersion string `json:"DisplayVersion"`
			EditionID      string `json:"EditionID"`
			ReleaseID      string `json:"ReleaseID"`
			CurrentBuild   string `json:"CurrentBuild"`
			Manufacturer   string `json:"Manufacturer"`
			Model          string `json:"Model"`
			GPUVendor      string `json:"GPUVendor"`
			CPUVendor      string `json:"CPUVendor"`
		}
		if json.Unmarshal([]byte(output), &raw) == nil {
			build, _ := strconv.Atoi(strings.TrimSpace(raw.CurrentBuild))
			env.Windows = WindowsInfo{
				ProductName:  raw.ProductName,
				DisplayVer:   raw.DisplayVersion,
				EditionID:    raw.EditionID,
				ReleaseID:    raw.ReleaseID,
				CurrentBuild: build,
				IsLTSC:       strings.Contains(strings.ToLower(raw.ProductName), "ltsc"),
				IsIoT:        strings.Contains(strings.ToLower(raw.ProductName), "iot") || strings.Contains(strings.ToLower(raw.EditionID), "iot"),
				Manufacturer: raw.Manufacturer,
				Model:        raw.Model,
				GPUVendor:    raw.GPUVendor,
				CPUVendor:    raw.CPUVendor,
			}
		}
	}

	env.Raw["windows_product_name"] = env.Windows.ProductName
	env.Raw["windows_display_version"] = env.Windows.DisplayVer
	return nil
}

func detectLinuxDetails(env *Environment) error {
	env.IsAdmin = os.Geteuid() == 0
	env.HasSudo = commandExists("sudo")
	env.HasWinget = commandExists("winget")
	if env.HasWinget {
		output, _ := runOutput("winget", "--version")
		env.WingetVersion = strings.TrimSpace(output)
	}

	osRelease, err := os.ReadFile("/etc/os-release")
	if err == nil {
		for _, line := range strings.Split(string(osRelease), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") || !strings.Contains(line, "=") {
				continue
			}
			parts := strings.SplitN(line, "=", 2)
			key := parts[0]
			value := strings.Trim(parts[1], `"`)
			env.Raw["os_release."+key] = value
			switch key {
			case "ID":
				env.DistroID = strings.ToLower(value)
			case "NAME":
				env.DistroName = value
			case "ID_LIKE":
				fields := strings.Fields(strings.ToLower(value))
				env.DistroLike = append(env.DistroLike, fields...)
			}
		}
	}

	env.DesktopSession = strings.TrimSpace(firstNonEmpty(os.Getenv("XDG_CURRENT_DESKTOP"), os.Getenv("DESKTOP_SESSION")))
	env.SessionType = strings.TrimSpace(os.Getenv("XDG_SESSION_TYPE"))
	if env.DesktopSession != "" {
		env.Raw["desktop_session"] = env.DesktopSession
	}
	if env.SessionType != "" {
		env.Raw["session_type"] = env.SessionType
	}

	for _, mgr := range []string{"apt-get", "dnf", "pacman", "flatpak", "fwupdmgr", "gsettings", "xrandr", "xdg-settings", "timedatectl", "systemctl"} {
		if commandExists(mgr) {
			env.Capabilities[mgr] = true
			switch mgr {
			case "apt-get":
				env.PackageManagers = append(env.PackageManagers, "apt")
			case "dnf":
				env.PackageManagers = append(env.PackageManagers, "dnf")
			case "pacman":
				env.PackageManagers = append(env.PackageManagers, "pacman")
			}
		}
	}

	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func firstExisting(paths ...string) string {
	for _, path := range paths {
		if path == "" {
			continue
		}
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func commandExitOK(name string, args ...string) bool {
	cmd := exec.Command(name, args...)
	if err := cmd.Run(); err != nil {
		return false
	}
	return true
}

func runOutput(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if stderr.Len() > 0 {
			return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
		}
		return "", err
	}
	return strings.TrimSpace(stdout.String()), nil
}
