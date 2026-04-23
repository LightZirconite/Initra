//go:build windows

package setup

import (
	"fmt"
	"sync/atomic"
	"syscall"
	"time"
)

var (
	user32DLL                 = syscall.NewLazyDLL("user32.dll")
	kernel32DLL               = syscall.NewLazyDLL("kernel32.dll")
	procGetAsyncKeyState      = user32DLL.NewProc("GetAsyncKeyState")
	procSetWindowPos          = user32DLL.NewProc("SetWindowPos")
	procMessageBeep           = user32DLL.NewProc("MessageBeep")
	procGetConsoleWindowLocal = kernel32DLL.NewProc("GetConsoleWindow")

	hostedSessionTopmost atomic.Bool
)

const (
	vkEscape      = 0x1B
	swpNoMove     = 0x0002
	swpNoSize     = 0x0001
	swpShowWindow = 0x0040
)

func startHostedSessionController(logger *Logger) func() {
	hostedSessionTopmost.Store(true)
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		var heldFor time.Duration
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				if isEscapePressed() {
					heldFor += 100 * time.Millisecond
					if heldFor >= 5*time.Second {
						toggle := !hostedSessionTopmost.Load()
						hostedSessionTopmost.Store(toggle)
						_ = applyConsoleTopmost(toggle)
						beepHostedSession()
						if toggle {
							fmt.Println("\nInitra focus mode is active again. Helper windows can still come to the front when needed.")
							logger.Println("hosted-session topmost restored via escape hold")
						} else {
							fmt.Println("\nInitra focus mode has been relaxed. You can interact with the rest of Windows again.")
							logger.Println("hosted-session topmost relaxed via escape hold")
						}
						heldFor = 0
					}
				} else {
					heldFor = 0
				}
			}
		}
	}()
	return func() {
		close(done)
	}
}

func hostedSessionTopmostEnabled() bool {
	return hostedSessionTopmost.Load()
}

func setHostedSessionTopmostEnabled(enabled bool) {
	hostedSessionTopmost.Store(enabled)
}

func applyConsoleTopmost(enabled bool) error {
	hwnd, _, _ := procGetConsoleWindowLocal.Call()
	if hwnd == 0 {
		return nil
	}
	insertAfter := uintptr(^uintptr(1))
	if enabled {
		insertAfter = uintptr(^uintptr(0))
	}
	_, _, err := procSetWindowPos.Call(hwnd, insertAfter, 0, 0, 0, 0, swpNoMove|swpNoSize|swpShowWindow)
	if err != syscall.Errno(0) {
		return err
	}
	return nil
}

func isEscapePressed() bool {
	value, _, _ := procGetAsyncKeyState.Call(vkEscape)
	return value&0x8000 != 0
}

func beepHostedSession() {
	_, _, _ = procMessageBeep.Call(0xFFFFFFFF)
}
