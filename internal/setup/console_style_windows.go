//go:build windows

package setup

import (
	"os"
	"syscall"
	"unsafe"
)

func enableWindowsVirtualTerminal() bool {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	getConsoleMode := kernel32.NewProc("GetConsoleMode")
	setConsoleMode := kernel32.NewProc("SetConsoleMode")

	handle := syscall.Handle(os.Stdout.Fd())
	var mode uint32
	ok, _, _ := getConsoleMode.Call(uintptr(handle), uintptr(unsafe.Pointer(&mode)))
	if ok == 0 {
		return false
	}
	const enableVirtualTerminalProcessing = 0x0004
	ok, _, _ = setConsoleMode.Call(uintptr(handle), uintptr(mode|enableVirtualTerminalProcessing))
	return ok != 0
}
