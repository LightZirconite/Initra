//go:build !windows

package setup

import "context"

func startHostedSessionController(logger *Logger) func() {
	return func() {}
}

func hostedSessionTopmostEnabled() bool {
	return false
}

func setHostedSessionTopmostEnabled(enabled bool) {}

func applyConsoleFocusMode(enabled bool) error {
	return nil
}

func withWindowsFocusRelaxed(ctx context.Context, logger *Logger, fn func() error) error {
	return fn()
}

func stopProtonVPNProcesses(ctx context.Context, logger *Logger) error {
	return nil
}
