//go:build !windows

package setup

func enableWindowsVirtualTerminal() bool {
	return true
}
