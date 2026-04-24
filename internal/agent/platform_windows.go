//go:build windows

package agent

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows/svc"
)

type platformPaths struct {
	DataDir string
	LogPath string
}

type windowsService struct {
	opts Options
}

func agentPaths() (platformPaths, error) {
	programData := os.Getenv("ProgramData")
	if programData == "" {
		programData = `C:\ProgramData`
	}
	dataDir := filepath.Join(programData, "Initra", "Agent")
	return platformPaths{
		DataDir: dataDir,
		LogPath: filepath.Join(dataDir, "agent.log"),
	}, nil
}

func RunService(ctx context.Context, opts Options) error {
	interactive, err := svc.IsAnInteractiveSession()
	if err == nil && !interactive {
		return svc.Run(ServiceName, &windowsService{opts: opts})
	}
	return runAgentLoop(ctx, opts)
}

func DefaultAction(opts Options) error {
	interactive, err := svc.IsAnInteractiveSession()
	if err == nil && !interactive {
		return RunService(context.Background(), opts)
	}
	fmt.Println("Installing Initra Agent as a Windows service...")
	if err := InstallService(opts); err == nil {
		fmt.Println("Initra Agent service installed and started.")
		_ = PrintStatus()
		waitForEnter()
		return nil
	} else {
		fmt.Println("Administrator rights are required. Requesting elevation...")
	}
	source := currentExecutablePath()
	if source == "" {
		err := fmt.Errorf("could not resolve current executable")
		fmt.Println("Error:", err)
		waitForEnter()
		return err
	}
	escapedSource := strings.ReplaceAll(source, "'", "''")
	escapedBaseURL := strings.ReplaceAll(opts.BaseURL, "'", "''")
	command := fmt.Sprintf(`Start-Process -FilePath '%s' -Verb RunAs -Wait -ArgumentList @('install-service','--base-url','%s')`, escapedSource, escapedBaseURL)
	if err := exec.Command("powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", command).Run(); err != nil {
		wrapped := fmt.Errorf("install-service requires administrator rights: %w", err)
		fmt.Println("Error:", wrapped)
		waitForEnter()
		return wrapped
	}
	fmt.Println("Elevated installer finished.")
	_ = PrintStatus()
	waitForEnter()
	return nil
}

func (s *windowsService) Execute(args []string, requests <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = runAgentLoop(ctx, s.opts)
		close(done)
	}()

	changes <- svc.Status{State: svc.StartPending}
	changes <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for request := range requests {
		switch request.Cmd {
		case svc.Interrogate:
			changes <- request.CurrentStatus
		case svc.Stop, svc.Shutdown:
			changes <- svc.Status{State: svc.StopPending}
			cancel()
			<-done
			return false, 0
		}
	}
	cancel()
	<-done
	return false, 0
}

func InstallService(opts Options) error {
	source := currentExecutablePath()
	if source == "" {
		return fmt.Errorf("could not resolve current executable")
	}
	target := filepath.Join(os.Getenv("ProgramFiles"), "Initra Agent", "initra-agent.exe")
	if filepath.Clean(source) != filepath.Clean(target) {
		if err := copyFile(source, target, 0o755); err != nil {
			return err
		}
	}
	binPath := fmt.Sprintf(`"%s" run-service --base-url "%s"`, target, opts.BaseURL)
	_ = runCommandQuiet("sc.exe", "stop", ServiceName)
	_ = runCommandQuiet("sc.exe", "delete", ServiceName)
	if err := runCommand("sc.exe", "create", ServiceName, "binPath=", binPath, "start=", "auto", "obj=", "LocalSystem", "DisplayName=", Name); err != nil {
		return err
	}
	_ = runCommand("sc.exe", "config", ServiceName, "start=", "delayed-auto")
	_ = runCommand("sc.exe", "failure", ServiceName, "reset=", "86400", "actions=", "restart/60000/restart/300000/none/0")
	return runCommand("sc.exe", "start", ServiceName)
}

func UninstallService() error {
	_ = runCommandQuiet("sc.exe", "stop", ServiceName)
	return runCommand("sc.exe", "delete", ServiceName)
}

func PrintStatus() error {
	fmt.Println()
	fmt.Println("Windows service status:")
	if err := runCommand("sc.exe", "query", ServiceName); err != nil {
		fmt.Println("Service is not installed or cannot be queried.")
	}
	paths, err := agentPaths()
	if err == nil {
		fmt.Println()
		fmt.Println("Log file:", paths.LogPath)
	}
	return nil
}

func StageUpdate(staged, target string, logger *log.Logger) error {
	helper := filepath.Join(os.TempDir(), "initra-agent-update-helper.exe")
	if err := copyFile(target, helper, 0o755); err != nil {
		return err
	}
	cmd := exec.Command(helper, "--apply-update", "--source", staged, "--target", target)
	if err := cmd.Start(); err != nil {
		return err
	}
	logger.Printf("launched update helper pid=%d", cmd.Process.Pid)
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
	_ = runCommandQuiet("sc.exe", "stop", ServiceName)
	time.Sleep(3 * time.Second)
	if err := copyFile(*source, *target, 0o755); err != nil {
		return err
	}
	_ = os.Remove(*source)
	return runCommand("sc.exe", "start", ServiceName)
}
