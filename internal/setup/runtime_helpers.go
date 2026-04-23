package setup

import (
	"context"
	"fmt"
	"time"
)

func prepareHostedWindowsSession(ctx context.Context, logger *Logger) error {
	setHostedSessionTopmostEnabled(true)
	script := `
$signature = @"
using System;
using System.Runtime.InteropServices;
public static class InitraWindowHost {
  [DllImport("kernel32.dll")] public static extern IntPtr GetConsoleWindow();
  [DllImport("user32.dll")] public static extern bool ShowWindow(IntPtr hWnd, int nCmdShow);
  [DllImport("user32.dll")] public static extern bool SetWindowPos(IntPtr hWnd, IntPtr hWndInsertAfter, int X, int Y, int cx, int cy, uint uFlags);
  [DllImport("user32.dll")] public static extern IntPtr GetSystemMenu(IntPtr hWnd, bool bRevert);
  [DllImport("user32.dll")] public static extern bool DeleteMenu(IntPtr hMenu, uint uPosition, uint uFlags);
  public static readonly IntPtr HWND_TOPMOST = new IntPtr(-1);
}
"@
Add-Type -TypeDefinition $signature -ErrorAction SilentlyContinue
$hwnd = [InitraWindowHost]::GetConsoleWindow()
if ($hwnd -ne [IntPtr]::Zero) {
  [void][InitraWindowHost]::ShowWindow($hwnd, 3)
  [void][InitraWindowHost]::SetWindowPos($hwnd, [InitraWindowHost]::HWND_TOPMOST, 0, 0, 0, 0, 0x0003 -bor 0x0040)
  $menu = [InitraWindowHost]::GetSystemMenu($hwnd, $false)
  if ($menu -ne [IntPtr]::Zero) {
    [void][InitraWindowHost]::DeleteMenu($menu, 0xF060, 0)
  }
}
$host.UI.RawUI.WindowTitle = 'Initra setup session'
try {
  $shell = New-Object -ComObject WScript.Shell
  Start-Sleep -Milliseconds 150
  if ($shell.AppActivate('Initra setup session')) {
    $shell.SendKeys('{F11}')
  }
} catch {}
Write-Host 'Initra is running in a hosted setup session. Keep this maximized window in the foreground until it finishes.'
`
	return runWindowsPowerShellScript(ctx, logger, script)
}

func setWindowsConsoleTopmost(ctx context.Context, logger *Logger, enabled bool) error {
	target := "HWND_NOTOPMOST"
	if enabled {
		target = "HWND_TOPMOST"
	}
	script := fmt.Sprintf(`
$signature = @"
using System;
using System.Runtime.InteropServices;
public static class InitraWindowHost {
  [DllImport("kernel32.dll")] public static extern IntPtr GetConsoleWindow();
  [DllImport("user32.dll")] public static extern bool SetWindowPos(IntPtr hWnd, IntPtr hWndInsertAfter, int X, int Y, int cx, int cy, uint uFlags);
  public static readonly IntPtr HWND_TOPMOST = new IntPtr(-1);
  public static readonly IntPtr HWND_NOTOPMOST = new IntPtr(-2);
}
"@
Add-Type -TypeDefinition $signature -ErrorAction SilentlyContinue
$hwnd = [InitraWindowHost]::GetConsoleWindow()
if ($hwnd -ne [IntPtr]::Zero) {
  [void][InitraWindowHost]::SetWindowPos($hwnd, [InitraWindowHost]::%s, 0, 0, 0, 0, 0x0003 -bor 0x0040)
}
`, target)
	return runWindowsPowerShellScript(ctx, logger, script)
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
