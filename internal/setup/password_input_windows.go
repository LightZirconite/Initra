//go:build windows

package setup

import (
	"os"

	"golang.org/x/sys/windows"
)

func setStdinEcho(enabled bool) (func() error, error) {
	handle := windows.Handle(os.Stdin.Fd())
	var original uint32
	if err := windows.GetConsoleMode(handle, &original); err != nil {
		return nil, err
	}
	next := original
	if enabled {
		next |= windows.ENABLE_ECHO_INPUT
	} else {
		next &^= windows.ENABLE_ECHO_INPUT
	}
	if err := windows.SetConsoleMode(handle, next); err != nil {
		return nil, err
	}
	return func() error {
		return windows.SetConsoleMode(handle, original)
	}, nil
}
