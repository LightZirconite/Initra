//go:build windows

package setup

import (
	"context"
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
	procShowWindow            = user32DLL.NewProc("ShowWindow")
	procSetForegroundWindow   = user32DLL.NewProc("SetForegroundWindow")
	procGetWindowLongPtr      = user32DLL.NewProc("GetWindowLongPtrW")
	procSetWindowLongPtr      = user32DLL.NewProc("SetWindowLongPtrW")
	procGetSystemMetrics      = user32DLL.NewProc("GetSystemMetrics")
	procGetSystemMenu         = user32DLL.NewProc("GetSystemMenu")
	procDeleteMenu            = user32DLL.NewProc("DeleteMenu")
	procMessageBeep           = user32DLL.NewProc("MessageBeep")
	procGetConsoleWindowLocal = kernel32DLL.NewProc("GetConsoleWindow")

	hostedSessionTopmost atomic.Bool
	gwlStyleIndex        = ^uintptr(15)
)

const (
	vkEscape          = 0x1B
	swpNoMove         = 0x0002
	swpNoSize         = 0x0001
	swpFrameChanged   = 0x0020
	swpShowWindow     = 0x0040
	smXVirtualScreen  = 76
	smYVirtualScreen  = 77
	smCxVirtualScreen = 78
	smCyVirtualScreen = 79
	swRestore         = 9
	swShow            = 5
	mfByCommand       = 0x00000000
	scClose           = 0xF060
	wsCaption         = 0x00C00000
	wsThickFrame      = 0x00040000
	wsMinimizeBox     = 0x00020000
	wsMaximizeBox     = 0x00010000
	wsSysMenu         = 0x00080000
)

func startHostedSessionController(logger *Logger) func() {
	hostedSessionTopmost.Store(true)
	_ = applyConsoleFocusMode(true)
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(250 * time.Millisecond)
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
						_ = applyConsoleFocusMode(toggle)
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
				if hostedSessionTopmost.Load() {
					_ = enforceConsoleFocus()
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
	return applyConsoleFocusMode(enabled)
}

func applyConsoleFocusMode(enabled bool) error {
	hwnd, _, _ := procGetConsoleWindowLocal.Call()
	if hwnd == 0 {
		return nil
	}
	if err := setConsoleStyle(hwnd, enabled); err != nil {
		return err
	}
	insertAfter := uintptr(^uintptr(1))
	flags := uintptr(swpNoMove | swpNoSize | swpShowWindow | swpFrameChanged)
	if enabled {
		insertAfter = uintptr(^uintptr(0))
		x := getSystemMetric(smXVirtualScreen)
		y := getSystemMetric(smYVirtualScreen)
		w := getSystemMetric(smCxVirtualScreen)
		h := getSystemMetric(smCyVirtualScreen)
		_, _, err := procSetWindowPos.Call(hwnd, insertAfter, uintptr(x), uintptr(y), uintptr(w), uintptr(h), swpShowWindow|swpFrameChanged)
		if err != syscall.Errno(0) {
			return err
		}
		_, _, _ = procShowWindow.Call(hwnd, swShow)
		_, _, _ = procSetForegroundWindow.Call(hwnd)
		return nil
	}
	_, _, _ = procShowWindow.Call(hwnd, swRestore)
	_, _, err := procSetWindowPos.Call(hwnd, insertAfter, 0, 0, 0, 0, flags)
	if err != syscall.Errno(0) {
		return err
	}
	return nil
}

func enforceConsoleFocus() error {
	hwnd, _, _ := procGetConsoleWindowLocal.Call()
	if hwnd == 0 {
		return nil
	}
	x := getSystemMetric(smXVirtualScreen)
	y := getSystemMetric(smYVirtualScreen)
	w := getSystemMetric(smCxVirtualScreen)
	h := getSystemMetric(smCyVirtualScreen)
	_, _, err := procSetWindowPos.Call(hwnd, uintptr(^uintptr(0)), uintptr(x), uintptr(y), uintptr(w), uintptr(h), swpShowWindow|swpFrameChanged)
	if err != syscall.Errno(0) {
		return err
	}
	_, _, _ = procSetForegroundWindow.Call(hwnd)
	return nil
}

func setConsoleStyle(hwnd uintptr, strict bool) error {
	style, _, err := procGetWindowLongPtr.Call(hwnd, gwlStyleIndex)
	if style == 0 && err != syscall.Errno(0) {
		return err
	}
	if strict {
		style &^= wsCaption | wsThickFrame | wsMinimizeBox | wsMaximizeBox | wsSysMenu
		menu, _, _ := procGetSystemMenu.Call(hwnd, 0)
		if menu != 0 {
			_, _, _ = procDeleteMenu.Call(menu, scClose, mfByCommand)
		}
	} else {
		style |= wsCaption | wsThickFrame | wsMinimizeBox | wsMaximizeBox | wsSysMenu
	}
	_, _, err = procSetWindowLongPtr.Call(hwnd, gwlStyleIndex, style)
	if err != syscall.Errno(0) {
		return err
	}
	return nil
}

func getSystemMetric(metric int32) int32 {
	value, _, _ := procGetSystemMetrics.Call(uintptr(metric))
	return int32(value)
}

func withWindowsFocusRelaxed(ctx context.Context, logger *Logger, fn func() error) error {
	if !hostedSessionTopmostEnabled() {
		return fn()
	}
	logger.Println("temporarily relaxing focus mode for helper window")
	_ = applyConsoleFocusMode(false)
	defer func() {
		_ = applyConsoleFocusMode(true)
		logger.Println("focus mode restored after helper window")
	}()
	return fn()
}

func stopProtonVPNProcesses(ctx context.Context, logger *Logger) error {
	script := `
$patterns = @('ProtonVPN', 'Proton VPN', 'ProtonVPNService')
Get-Process -ErrorAction SilentlyContinue | Where-Object {
  $name = $_.ProcessName
  $patterns | Where-Object { $name -like ($_ + '*') -or $name -eq $_ }
} | ForEach-Object {
  try { Stop-Process -Id $_.Id -Force -ErrorAction SilentlyContinue } catch {}
}
`
	return runWindowsPowerShellScript(ctx, logger, script)
}

func isEscapePressed() bool {
	value, _, _ := procGetAsyncKeyState.Call(vkEscape)
	return value&0x8000 != 0
}

func beepHostedSession() {
	_, _, _ = procMessageBeep.Call(0xFFFFFFFF)
}
