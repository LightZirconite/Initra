//go:build !windows

package agent

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
)

type platformPaths struct {
	DataDir string
	LogPath string
}

func agentPaths() (platformPaths, error) {
	dataDir := "/var/lib/initra-agent"
	if os.Geteuid() != 0 {
		home, err := os.UserHomeDir()
		if err != nil {
			return platformPaths{}, err
		}
		dataDir = filepath.Join(home, ".local", "state", "initra-agent")
	}
	return platformPaths{
		DataDir: dataDir,
		LogPath: filepath.Join(dataDir, "agent.log"),
	}, nil
}

func RunService(ctx context.Context, opts Options) error {
	return runAgentLoop(ctx, opts)
}

func DefaultAction(opts Options) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("run sudo ./initra-agent install-service to install the service")
	}
	if err := InstallService(opts); err != nil {
		return err
	}
	return PrintStatus()
}

func InstallService(opts Options) error {
	source := currentExecutablePath()
	if source == "" {
		return fmt.Errorf("could not resolve current executable")
	}
	target := "/usr/local/bin/initra-agent"
	if os.Geteuid() != 0 {
		return fmt.Errorf("install-service must run as root")
	}
	if err := copyFile(source, target, 0o755); err != nil {
		return err
	}
	unit := fmt.Sprintf(`[Unit]
Description=Initra Agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=%s run-service --base-url %s
Restart=always
RestartSec=30

[Install]
WantedBy=multi-user.target
`, target, opts.BaseURL)
	if err := os.WriteFile("/etc/systemd/system/initra-agent.service", []byte(unit), 0o644); err != nil {
		return err
	}
	if err := runCommand("systemctl", "daemon-reload"); err != nil {
		return err
	}
	if err := runCommand("systemctl", "enable", LinuxServiceName+".service"); err != nil {
		return err
	}
	return runCommand("systemctl", "restart", LinuxServiceName+".service")
}

func UninstallService() error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("uninstall-service must run as root")
	}
	_ = runCommand("systemctl", "disable", "--now", LinuxServiceName+".service")
	_ = os.Remove("/etc/systemd/system/initra-agent.service")
	_ = runCommand("systemctl", "daemon-reload")
	return nil
}

func PrintStatus() error {
	fmt.Println("Linux service status:")
	if err := runCommand("systemctl", "status", LinuxServiceName+".service", "--no-pager"); err != nil {
		fmt.Println("Service is not installed or systemd could not query it.")
	}
	paths, err := agentPaths()
	if err == nil {
		fmt.Println()
		fmt.Println("Log file:", paths.LogPath)
	}
	return nil
}

func StageUpdate(staged, target string, logger *log.Logger) error {
	if err := os.Chmod(staged, 0o755); err != nil {
		return err
	}
	if err := os.Rename(staged, target); err != nil {
		return err
	}
	cmd := exec.Command("systemctl", "restart", LinuxServiceName+".service")
	if err := cmd.Start(); err != nil {
		logger.Printf("systemd restart failed after update: %v", err)
	}
	return nil
}

func applyStagedUpdate(args []string) error {
	fs := flag.NewFlagSet("apply-update", flag.ContinueOnError)
	source := fs.String("source", "", "staged update path")
	target := fs.String("target", "", "target binary path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *source == "" || *target == "" {
		return fmt.Errorf("--source and --target are required")
	}
	if err := os.Chmod(*source, 0o755); err != nil {
		return err
	}
	return os.Rename(*source, *target)
}
