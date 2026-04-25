//go:build windows

package setup

import (
	"context"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

var (
	user32DLL                 = syscall.NewLazyDLL("user32.dll")
	kernel32DLL               = syscall.NewLazyDLL("kernel32.dll")
	procGetAsyncKeyState      = user32DLL.NewProc("GetAsyncKeyState")
	procSetProcessDpiAwareCtx = user32DLL.NewProc("SetProcessDpiAwarenessContext")
	procSetWindowPos          = user32DLL.NewProc("SetWindowPos")
	procShowWindow            = user32DLL.NewProc("ShowWindow")
	procSetForegroundWindow   = user32DLL.NewProc("SetForegroundWindow")
	procGetWindowLongPtr      = user32DLL.NewProc("GetWindowLongPtrW")
	procSetWindowLongPtr      = user32DLL.NewProc("SetWindowLongPtrW")
	procGetSystemMenu         = user32DLL.NewProc("GetSystemMenu")
	procDeleteMenu            = user32DLL.NewProc("DeleteMenu")
	procFindWindow            = user32DLL.NewProc("FindWindowW")
	procMonitorFromWindow     = user32DLL.NewProc("MonitorFromWindow")
	procGetMonitorInfo        = user32DLL.NewProc("GetMonitorInfoW")
	procGetWindowRect         = user32DLL.NewProc("GetWindowRect")
	procSetWindowsHookEx      = user32DLL.NewProc("SetWindowsHookExW")
	procUnhookWindowsHookEx   = user32DLL.NewProc("UnhookWindowsHookEx")
	procCallNextHookEx        = user32DLL.NewProc("CallNextHookEx")
	procGetMessage            = user32DLL.NewProc("GetMessageW")
	procTranslateMessage      = user32DLL.NewProc("TranslateMessage")
	procDispatchMessage       = user32DLL.NewProc("DispatchMessageW")
	procPostThreadMessage     = user32DLL.NewProc("PostThreadMessageW")
	procMessageBeep           = user32DLL.NewProc("MessageBeep")
	procClipCursor            = user32DLL.NewProc("ClipCursor")
	procMoveWindow            = user32DLL.NewProc("MoveWindow")
	procGetConsoleWindowLocal = kernel32DLL.NewProc("GetConsoleWindow")
	procGetCurrentThreadID    = kernel32DLL.NewProc("GetCurrentThreadId")
	procSetConsoleCtrlHandler = kernel32DLL.NewProc("SetConsoleCtrlHandler")
	procSetThreadExecState    = kernel32DLL.NewProc("SetThreadExecutionState")

	hostedSessionTopmost atomic.Bool
	kioskInputMode       atomic.Uint32
	gwlStyleIndex        = ^uintptr(15)
	keyboardGuardThread  atomic.Uint32
	mouseGuardThread     atomic.Uint32
	altPressed           atomic.Bool
	ctrlPressed          atomic.Bool
	shiftPressed         atomic.Bool
	ctrlHandlerReady     atomic.Bool
	keyboardHookProcPtr  = syscall.NewCallback(keyboardGuardProc)
	mouseHookProcPtr     = syscall.NewCallback(mouseGuardProc)
	consoleCtrlProcPtr   = syscall.NewCallback(consoleCtrlHandlerProc)
)

const (
	vkEscape                 = 0x1B
	vkTab                    = 0x09
	vkMenu                   = 0x12
	vkControl                = 0x11
	vkShift                  = 0x10
	vkLMenu                  = 0xA4
	vkRMenu                  = 0xA5
	vkLControl               = 0xA2
	vkRControl               = 0xA3
	vkLShift                 = 0xA0
	vkRShift                 = 0xA1
	vkLWin                   = 0x5B
	vkRWin                   = 0x5C
	vkApps                   = 0x5D
	vkSpace                  = 0x20
	vkReturn                 = 0x0D
	vkDelete                 = 0x2E
	vkF4                     = 0x73
	monitorDefaultToNearest  = 0x00000002
	swpNoMove                = 0x0002
	swpNoSize                = 0x0001
	swpFrameChanged          = 0x0020
	swpShowWindow            = 0x0040
	swRestore                = 9
	swMaximize               = 3
	swShow                   = 5
	mfByCommand              = 0x00000000
	scClose                  = 0xF060
	wsCaption                = 0x00C00000
	wsThickFrame             = 0x00040000
	wsMinimizeBox            = 0x00020000
	wsMaximizeBox            = 0x00010000
	wsSysMenu                = 0x00080000
	whKeyboardLL             = 13
	whMouseLL                = 14
	hcAction                 = 0
	wmKeyDown                = 0x0100
	wmKeyUp                  = 0x0101
	wmSysKeyDown             = 0x0104
	wmSysKeyUp               = 0x0105
	wmMouseMove              = 0x0200
	wmLButtonDown            = 0x0201
	wmLButtonUp              = 0x0202
	wmRButtonDown            = 0x0204
	wmRButtonUp              = 0x0205
	wmMButtonDown            = 0x0207
	wmMButtonUp              = 0x0208
	wmMouseWheel             = 0x020A
	wmXButtonDown            = 0x020B
	wmXButtonUp              = 0x020C
	wmMouseHWheel            = 0x020E
	wmQuit                   = 0x0012
	swHide                   = 0
	ctrlCloseEvent           = 2
	ctrlLogoffEvent          = 5
	ctrlShutdownEvent        = 6
	dpiAwarenessPerMonitorV2 = ^uintptr(3)
	esContinuous             = 0x80000000
	esSystemRequired         = 0x00000001
	esDisplayRequired        = 0x00000002
)

const (
	kioskInputDisabled uint32 = iota
	kioskInputRunning
	kioskInputFinal
	kioskInputHelper
)

type keyboardLLHookStruct struct {
	VkCode      uint32
	ScanCode    uint32
	Flags       uint32
	Time        uint32
	DwExtraInfo uintptr
}

type windowsMessage struct {
	Hwnd     uintptr
	Message  uint32
	WParam   uintptr
	LParam   uintptr
	Time     uint32
	PtX      int32
	PtY      int32
	LPrivate uint32
}

type windowsRect struct {
	Left   int32
	Top    int32
	Right  int32
	Bottom int32
}

type monitorInfo struct {
	CbSize    uint32
	RcMonitor windowsRect
	RcWork    windowsRect
	DwFlags   uint32
}

func startHostedSessionController(logger *Logger) func() {
	hostedSessionTopmost.Store(true)
	kioskInputMode.Store(kioskInputRunning)
	ensureConsoleCtrlHandler()
	enablePerMonitorDPIAwareness(logger)
	preventSystemSleep(logger)
	_ = applyConsoleFocusMode(true)
	stopKeyboardGuard := startKeyboardGuard(logger)
	stopMouseGuard := startMouseGuard(logger)
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		var escapeStarted time.Time
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				if isEscapePressed() {
					if escapeStarted.IsZero() {
						escapeStarted = time.Now()
					}
					if time.Since(escapeStarted) >= 5*time.Second && hostedSessionTopmost.Load() {
						hostedSessionTopmost.Store(false)
						kioskInputMode.Store(kioskInputDisabled)
						allowSystemSleep()
						_ = applyConsoleFocusMode(false)
						beepHostedSession()
						fmt.Println("\nInitra kiosk mode has been disabled by technician override.")
						logger.Println("kiosk mode disabled via escape hold")
					}
				} else {
					escapeStarted = time.Time{}
				}
				if hostedSessionTopmost.Load() && kioskInputMode.Load() != kioskInputHelper {
					_ = enforceConsoleFocus()
				}
			}
		}
	}()
	return func() {
		close(done)
		stopKeyboardGuard()
		stopMouseGuard()
		hostedSessionTopmost.Store(false)
		kioskInputMode.Store(kioskInputDisabled)
		allowSystemSleep()
		_ = applyConsoleFocusMode(false)
	}
}

func hostedSessionTopmostEnabled() bool {
	return hostedSessionTopmost.Load()
}

func setHostedSessionTopmostEnabled(enabled bool) {
	hostedSessionTopmost.Store(enabled)
	if enabled {
		kioskInputMode.Store(kioskInputRunning)
	} else {
		kioskInputMode.Store(kioskInputDisabled)
	}
}

func setHostedSessionFinalInputMode(enabled bool) {
	if !hostedSessionTopmost.Load() {
		return
	}
	if enabled {
		kioskInputMode.Store(kioskInputFinal)
		return
	}
	kioskInputMode.Store(kioskInputRunning)
}

func applyConsoleTopmost(enabled bool) error {
	return applyConsoleFocusMode(enabled)
}

func applyConsoleFocusMode(enabled bool) error {
	enablePerMonitorDPIAwareness(nil)
	hwnd, _, _ := procGetConsoleWindowLocal.Call()
	if hwnd == 0 {
		if !enabled {
			releaseCursorClip()
		}
		return nil
	}
	setShellTaskbarHidden(enabled)
	insertAfter := uintptr(^uintptr(1))
	flags := uintptr(swpNoMove | swpNoSize | swpShowWindow | swpFrameChanged)
	if enabled {
		insertAfter = uintptr(^uintptr(0))
		x, y, w, h := consoleMonitorBounds(hwnd)
		if err := setConsoleStyle(hwnd, false); err != nil {
			return err
		}
		_, _, _ = procShowWindow.Call(hwnd, swRestore)
		_, _, _ = procShowWindow.Call(hwnd, swMaximize)
		time.Sleep(150 * time.Millisecond)
		if !consoleCoversMonitor(hwnd, x, y, w, h) {
			_, _, _ = procMoveWindow.Call(hwnd, signedIntArg(x), signedIntArg(y), signedIntArg(w), signedIntArg(h), 1)
		}
		if err := setConsoleStyle(hwnd, true); err != nil {
			return err
		}
		releaseCursorClip()
		_, _, err := procSetWindowPos.Call(hwnd, insertAfter, signedIntArg(x), signedIntArg(y), signedIntArg(w), signedIntArg(h), uintptr(swpShowWindow|swpFrameChanged))
		if err != syscall.Errno(0) {
			return err
		}
		_, _, _ = procShowWindow.Call(hwnd, swShow)
		_, _, _ = procSetForegroundWindow.Call(hwnd)
		return nil
	}
	releaseCursorClip()
	if err := setConsoleStyle(hwnd, false); err != nil {
		return err
	}
	_, _, _ = procShowWindow.Call(hwnd, swRestore)
	_, _, err := procSetWindowPos.Call(hwnd, insertAfter, 0, 0, 0, 0, flags)
	if err != syscall.Errno(0) {
		return err
	}
	return nil
}

func setShellTaskbarHidden(hidden bool) {
	for _, className := range []string{"Shell_TrayWnd", "Shell_SecondaryTrayWnd"} {
		ptr, err := syscall.UTF16PtrFromString(className)
		if err != nil {
			continue
		}
		hwnd, _, _ := procFindWindow.Call(uintptr(unsafe.Pointer(ptr)), 0)
		if hwnd == 0 {
			continue
		}
		showCmd := uintptr(swShow)
		if hidden {
			showCmd = uintptr(swHide)
		}
		_, _, _ = procShowWindow.Call(hwnd, showCmd)
	}
}

func enforceConsoleFocus() error {
	hwnd, _, _ := procGetConsoleWindowLocal.Call()
	if hwnd == 0 {
		return nil
	}
	x, y, w, h := consoleMonitorBounds(hwnd)
	if !consoleCoversMonitor(hwnd, x, y, w, h) {
		_, _, _ = procMoveWindow.Call(hwnd, signedIntArg(x), signedIntArg(y), signedIntArg(w), signedIntArg(h), 1)
	}
	_, _, err := procSetWindowPos.Call(hwnd, uintptr(^uintptr(0)), signedIntArg(x), signedIntArg(y), signedIntArg(w), signedIntArg(h), uintptr(swpShowWindow|swpFrameChanged))
	if err != syscall.Errno(0) {
		return err
	}
	_, _, _ = procSetForegroundWindow.Call(hwnd)
	return nil
}

func consoleCoversMonitor(hwnd uintptr, x, y, width, height int32) bool {
	var rect windowsRect
	ok, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&rect)))
	if ok == 0 {
		return false
	}
	tolerance := int32(6)
	return rect.Left <= x+tolerance &&
		rect.Top <= y+tolerance &&
		rect.Right >= x+width-tolerance &&
		rect.Bottom >= y+height-tolerance
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

func consoleMonitorBounds(hwnd uintptr) (int32, int32, int32, int32) {
	monitor, _, _ := procMonitorFromWindow.Call(hwnd, monitorDefaultToNearest)
	if monitor == 0 {
		return 0, 0, 1920, 1080
	}
	info := monitorInfo{CbSize: uint32(unsafe.Sizeof(monitorInfo{}))}
	ok, _, _ := procGetMonitorInfo.Call(monitor, uintptr(unsafe.Pointer(&info)))
	if ok == 0 {
		return 0, 0, 1920, 1080
	}
	width := info.RcMonitor.Right - info.RcMonitor.Left
	height := info.RcMonitor.Bottom - info.RcMonitor.Top
	if width <= 0 || height <= 0 {
		return 0, 0, 1920, 1080
	}
	return info.RcMonitor.Left, info.RcMonitor.Top, width, height
}

func withWindowsFocusRelaxed(ctx context.Context, logger *Logger, fn func() error) error {
	if !hostedSessionTopmostEnabled() {
		return fn()
	}
	logger.Println("temporarily enabling helper interaction mode")
	kioskInputMode.Store(kioskInputHelper)
	_ = applyConsoleHelperMode()
	defer func() {
		kioskInputMode.Store(kioskInputRunning)
		_ = applyConsoleGuardMode()
		logger.Println("kiosk focus restored after helper window")
	}()
	return fn()
}

func applyConsoleGuardMode() error {
	enablePerMonitorDPIAwareness(nil)
	hwnd, _, _ := procGetConsoleWindowLocal.Call()
	if hwnd == 0 {
		releaseCursorClip()
		return nil
	}
	setShellTaskbarHidden(true)
	x, y, w, h := consoleMonitorBounds(hwnd)
	if err := setConsoleStyle(hwnd, true); err != nil {
		return err
	}
	releaseCursorClip()
	if !consoleCoversMonitor(hwnd, x, y, w, h) {
		_, _, _ = procMoveWindow.Call(hwnd, signedIntArg(x), signedIntArg(y), signedIntArg(w), signedIntArg(h), 1)
	}
	_, _, err := procSetWindowPos.Call(hwnd, uintptr(^uintptr(0)), signedIntArg(x), signedIntArg(y), signedIntArg(w), signedIntArg(h), uintptr(swpShowWindow|swpFrameChanged))
	if err != syscall.Errno(0) {
		return err
	}
	return nil
}

func applyConsoleHelperMode() error {
	enablePerMonitorDPIAwareness(nil)
	hwnd, _, _ := procGetConsoleWindowLocal.Call()
	if hwnd == 0 {
		releaseCursorClip()
		return nil
	}
	setShellTaskbarHidden(true)
	x, y, w, h := consoleMonitorBounds(hwnd)
	if err := setConsoleStyle(hwnd, true); err != nil {
		return err
	}
	releaseCursorClip()
	_, _, _ = procMoveWindow.Call(hwnd, signedIntArg(x), signedIntArg(y), signedIntArg(w), signedIntArg(h), 1)
	_, _, err := procSetWindowPos.Call(hwnd, uintptr(^uintptr(1)), signedIntArg(x), signedIntArg(y), signedIntArg(w), signedIntArg(h), uintptr(swpShowWindow|swpFrameChanged))
	if err != syscall.Errno(0) {
		return err
	}
	return nil
}

func runWindowsSettingsURI(ctx context.Context, logger *Logger, uri string) error {
	uri = strings.TrimSpace(uri)
	if uri == "" {
		return nil
	}
	beforeOutput, _ := runOutput("powershell", "-NoProfile", "-NonInteractive", "-Command", `Get-Process -Name SystemSettings -ErrorAction SilentlyContinue | Select-Object -ExpandProperty Id`)
	before := parseWindowsProcessIDs(beforeOutput)
	logger.Println("opening windows settings", uri)
	if err := runProcess(ctx, Environment{OS: "windows"}, logger, "powershell", "-NoProfile", "-NonInteractive", "-Command", fmt.Sprintf(`Start-Process %q`, uri)); err != nil {
		return err
	}
	time.Sleep(5 * time.Second)
	return closeNewWindowsSettingsProcesses(logger, before)
}

func closeNewWindowsSettingsProcesses(logger *Logger, before []int) error {
	known := make([]string, 0, len(before))
	for _, id := range before {
		known = append(known, strconv.Itoa(id))
	}
	knownList := strings.Join(known, ",")
	if knownList == "" {
		knownList = "0"
	}
	script := fmt.Sprintf(`
$known = @(%s)
Get-Process -Name SystemSettings -ErrorAction SilentlyContinue | Where-Object {
  $known -notcontains $_.Id
} | ForEach-Object {
  try { Stop-Process -Id $_.Id -Force -ErrorAction SilentlyContinue } catch {}
}
`, knownList)
	logger.Println("closing windows settings processes opened by Initra")
	return runWindowsPowerShellScript(context.Background(), logger, script)
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

func startKeyboardGuard(logger *Logger) func() {
	ready := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		threadID, _, _ := procGetCurrentThreadID.Call()
		keyboardGuardThread.Store(uint32(threadID))
		hook, _, err := procSetWindowsHookEx.Call(whKeyboardLL, keyboardHookProcPtr, 0, 0)
		if hook == 0 {
			logger.Println("keyboard guard hook failed", err)
			close(ready)
			close(stopped)
			return
		}
		close(ready)
		var msg windowsMessage
		for {
			ret, _, _ := procGetMessage.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
			if int32(ret) <= 0 {
				break
			}
			_, _, _ = procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
			_, _, _ = procDispatchMessage.Call(uintptr(unsafe.Pointer(&msg)))
		}
		_, _, _ = procUnhookWindowsHookEx.Call(hook)
		keyboardGuardThread.Store(0)
		close(stopped)
	}()
	<-ready
	return func() {
		threadID := keyboardGuardThread.Load()
		if threadID != 0 {
			_, _, _ = procPostThreadMessage.Call(uintptr(threadID), wmQuit, 0, 0)
		}
		<-stopped
	}
}

func startMouseGuard(logger *Logger) func() {
	ready := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		threadID, _, _ := procGetCurrentThreadID.Call()
		mouseGuardThread.Store(uint32(threadID))
		hook, _, err := procSetWindowsHookEx.Call(whMouseLL, mouseHookProcPtr, 0, 0)
		if hook == 0 {
			logger.Println("mouse guard hook failed", err)
			close(ready)
			close(stopped)
			return
		}
		close(ready)
		var msg windowsMessage
		for {
			ret, _, _ := procGetMessage.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
			if int32(ret) <= 0 {
				break
			}
			_, _, _ = procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
			_, _, _ = procDispatchMessage.Call(uintptr(unsafe.Pointer(&msg)))
		}
		_, _, _ = procUnhookWindowsHookEx.Call(hook)
		mouseGuardThread.Store(0)
		close(stopped)
	}()
	<-ready
	return func() {
		threadID := mouseGuardThread.Load()
		if threadID != 0 {
			_, _, _ = procPostThreadMessage.Call(uintptr(threadID), wmQuit, 0, 0)
		}
		<-stopped
	}
}

func keyboardGuardProc(code int, wParam uintptr, lParam uintptr) uintptr {
	if code < hcAction || lParam == 0 {
		next, _, _ := procCallNextHookEx.Call(0, uintptr(code), wParam, lParam)
		return next
	}
	event := (*keyboardLLHookStruct)(unsafe.Pointer(lParam))
	keyDown := wParam == wmKeyDown || wParam == wmSysKeyDown
	keyUp := wParam == wmKeyUp || wParam == wmSysKeyUp
	updateModifierState(event.VkCode, keyDown, keyUp)
	if !hostedSessionTopmostEnabled() {
		next, _, _ := procCallNextHookEx.Call(0, uintptr(code), wParam, lParam)
		return next
	}
	if shouldBlockKeystroke(event.VkCode, keyDown, keyUp, kioskInputMode.Load()) {
		return 1
	}
	next, _, _ := procCallNextHookEx.Call(0, uintptr(code), wParam, lParam)
	return next
}

func mouseGuardProc(code int, wParam uintptr, lParam uintptr) uintptr {
	if code < hcAction || lParam == 0 {
		next, _, _ := procCallNextHookEx.Call(0, uintptr(code), wParam, lParam)
		return next
	}
	if hostedSessionTopmostEnabled() && shouldBlockMouseMessage(uint32(wParam), kioskInputMode.Load()) {
		return 1
	}
	next, _, _ := procCallNextHookEx.Call(0, uintptr(code), wParam, lParam)
	return next
}

func updateModifierState(vk uint32, keyDown, keyUp bool) {
	switch vk {
	case vkMenu, vkLMenu, vkRMenu:
		if keyDown {
			altPressed.Store(true)
		}
		if keyUp {
			altPressed.Store(false)
		}
	case vkControl, vkLControl, vkRControl:
		if keyDown {
			ctrlPressed.Store(true)
		}
		if keyUp {
			ctrlPressed.Store(false)
		}
	case vkShift, vkLShift, vkRShift:
		if keyDown {
			shiftPressed.Store(true)
		}
		if keyUp {
			shiftPressed.Store(false)
		}
	}
}

func shouldBlockMouseMessage(message uint32, mode uint32) bool {
	if mode == kioskInputHelper {
		return false
	}
	switch message {
	case wmLButtonDown, wmLButtonUp,
		wmRButtonDown, wmRButtonUp,
		wmMButtonDown, wmMButtonUp,
		wmXButtonDown, wmXButtonUp:
		return true
	default:
		return false
	}
}

func shouldBlockKeystroke(vk uint32, keyDown, keyUp bool, mode uint32) bool {
	if mode == kioskInputFinal {
		if vk == vkEscape {
			return true
		}
		return vk != vkReturn
	}
	switch vk {
	case vkLWin, vkRWin, vkApps:
		return true
	case vkTab:
		return keyDown && altPressed.Load()
	case vkF4:
		return keyDown && altPressed.Load()
	case vkSpace:
		return keyDown && altPressed.Load()
	case vkEscape:
		if keyDown && altPressed.Load() {
			return true
		}
		if keyDown && ctrlPressed.Load() {
			return true
		}
		return false
	case vkDelete:
		return keyDown && ctrlPressed.Load() && altPressed.Load()
	default:
		if keyUp {
			return false
		}
		return false
	}
}

func signedIntArg(value int32) uintptr {
	return uintptr(int(value))
}

func preventSystemSleep(logger *Logger) {
	if procSetThreadExecState.Find() != nil {
		return
	}
	state := uintptr(esContinuous | esSystemRequired | esDisplayRequired)
	if ret, _, err := procSetThreadExecState.Call(state); ret == 0 && logger != nil {
		logger.Println("sleep prevention failed", err)
	}
}

func allowSystemSleep() {
	if procSetThreadExecState.Find() != nil {
		return
	}
	_, _, _ = procSetThreadExecState.Call(uintptr(esContinuous))
}

func enablePerMonitorDPIAwareness(logger *Logger) {
	if procSetProcessDpiAwareCtx.Find() != nil {
		return
	}
	ok, _, err := procSetProcessDpiAwareCtx.Call(dpiAwarenessPerMonitorV2)
	if ok == 0 && logger != nil && err != syscall.Errno(0) {
		logger.Println("dpi awareness activation skipped", err)
	}
}

func clipCursorToRect(x, y, width, height int32) {
	if width <= 0 || height <= 0 {
		return
	}
	rect := windowsRect{
		Left:   x,
		Top:    y,
		Right:  x + width,
		Bottom: y + height,
	}
	_, _, _ = procClipCursor.Call(uintptr(unsafe.Pointer(&rect)))
}

func releaseCursorClip() {
	_, _, _ = procClipCursor.Call(0)
}

func ensureConsoleCtrlHandler() {
	if ctrlHandlerReady.Load() {
		return
	}
	_, _, _ = procSetConsoleCtrlHandler.Call(consoleCtrlProcPtr, 1)
	ctrlHandlerReady.Store(true)
}

func consoleCtrlHandlerProc(ctrlType uint32) uintptr {
	if !hostedSessionTopmostEnabled() {
		return 0
	}
	switch ctrlType {
	case ctrlCloseEvent, ctrlLogoffEvent, ctrlShutdownEvent:
		beepHostedSession()
		return 1
	default:
		return 0
	}
}
