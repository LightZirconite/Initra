//go:build !windows

package setup

import (
	"os"

	"golang.org/x/sys/unix"
)

func setStdinEcho(enabled bool) (func() error, error) {
	fd := int(os.Stdin.Fd())
	original, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return nil, err
	}
	next := *original
	if enabled {
		next.Lflag |= unix.ECHO
	} else {
		next.Lflag &^= unix.ECHO
	}
	if err := unix.IoctlSetTermios(fd, unix.TCSETS, &next); err != nil {
		return nil, err
	}
	return func() error {
		return unix.IoctlSetTermios(fd, unix.TCSETS, original)
	}, nil
}
