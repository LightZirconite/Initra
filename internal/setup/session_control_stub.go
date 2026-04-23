//go:build !windows

package setup

func startHostedSessionController(logger *Logger) func() {
	return func() {}
}

func hostedSessionTopmostEnabled() bool {
	return false
}

func setHostedSessionTopmostEnabled(enabled bool) {}
