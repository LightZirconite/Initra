package setup

import (
	"context"
	"fmt"
	"time"
)

func prepareHostedWindowsSession(ctx context.Context, logger *Logger) error {
	setHostedSessionTopmostEnabled(true)
	return applyConsoleFocusMode(true)
}

func setWindowsConsoleTopmost(ctx context.Context, logger *Logger, enabled bool) error {
	return applyConsoleFocusMode(enabled)
}

func windowsRebootPending(ctx context.Context, logger *Logger) (bool, error) {
	script := `
$pending = $false
if (Test-Path 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Component Based Servicing\RebootPending') { $pending = $true }
if (Test-Path 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\WindowsUpdate\Auto Update\RebootRequired') { $pending = $true }
$sessionManager = Get-ItemProperty 'HKLM:\SYSTEM\CurrentControlSet\Control\Session Manager' -ErrorAction SilentlyContinue
if ($sessionManager -and $sessionManager.PendingFileRenameOperations) { $pending = $true }
try {
  if (Get-Module -ListAvailable -Name PSWindowsUpdate) {
    Import-Module PSWindowsUpdate -Force | Out-Null
    $status = Get-WURebootStatus -Silent -ErrorAction SilentlyContinue
    if ($status -and $status.RebootRequired) { $pending = $true }
  }
} catch {}
if ($pending) { 'true' } else { 'false' }
`
	output, err := runOutput("powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	if err != nil {
		logger.Println("windows-reboot-pending check failed", err)
		return false, err
	}
	return output == "true", nil
}

func persistRebootState(paths Paths, logger *Logger, state *RunState, reason string) error {
	if state == nil {
		return nil
	}
	state.UpdatedAt = time.Now()
	if err := saveJSON(paths.StatePath, state); err != nil {
		return err
	}
	if err := setupResumeHook(paths, logger); err != nil {
		return err
	}
	fmt.Println(reason)
	fmt.Println("The machine will restart in 5 seconds.")
	return triggerManagedReboot(context.Background(), logger)
}

func triggerManagedReboot(ctx context.Context, logger *Logger) error {
	return runWindowsPowerShellScript(ctx, logger, `shutdown.exe /r /t 5 /c "Initra will resume setup automatically after sign-in."`)
}
