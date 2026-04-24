//go:build !windows

package setup

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
)

func startHostedSessionController(logger *Logger) func() {
	stopSleepInhibitor := startPortableSleepInhibitor(logger)
	if runtime.GOOS == "linux" {
		fmt.Print("\x1b[?1049h\x1b[2J\x1b[H\x1b[?25l")
	}
	return func() {
		if runtime.GOOS == "linux" {
			fmt.Print("\x1b[?25h\x1b[?1049l")
		}
		stopSleepInhibitor()
	}
}

func hostedSessionTopmostEnabled() bool {
	return false
}

func setHostedSessionTopmostEnabled(enabled bool) {}

func setHostedSessionFinalInputMode(enabled bool) {}

func applyConsoleFocusMode(enabled bool) error {
	return nil
}

func withWindowsFocusRelaxed(ctx context.Context, logger *Logger, fn func() error) error {
	return fn()
}

func runWindowsSettingsURI(ctx context.Context, logger *Logger, uri string) error {
	return nil
}

func stopProtonVPNProcesses(ctx context.Context, logger *Logger) error {
	return nil
}

func startPortableSleepInhibitor(logger *Logger) func() {
	if runtime.GOOS != "linux" {
		return func() {}
	}
	if _, err := exec.LookPath("systemd-inhibit"); err != nil {
		return func() {}
	}
	cmd := exec.Command(
		"systemd-inhibit",
		"--what=sleep:idle",
		"--why=Initra setup is provisioning this workstation",
		"--mode=block",
		"sleep",
		"infinity",
	)
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		if logger != nil {
			logger.Println("linux sleep inhibitor failed", err)
		}
		return func() {}
	}
	return func() {
		if cmd.Process == nil {
			return
		}
		_ = cmd.Process.Signal(os.Interrupt)
		_, _ = cmd.Process.Wait()
	}
}
