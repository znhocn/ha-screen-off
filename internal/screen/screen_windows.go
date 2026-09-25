//go:build windows

package screen

import (
	"fmt"
	"syscall"
	"unsafe"
)

// Windows display power control via user32.dll + kernel32.dll. Screen power is
// toggled by broadcasting WM_SYSCOMMAND / SC_MONITORPOWER to all top-level
// windows, the standard approach used by "monitor off" utilities.
//
// Notes:
//   - We use SendMessageTimeoutW rather than SendMessageW: SendMessage waits
//     indefinitely for every top-level window to process the message, so a
//     single hung window would freeze this process forever (and with it, the
//     Home Assistant polling loop). SendMessageTimeoutW bounds the wait and
//     aborts on unresponsive windows.
//   - SC_MONITORPOWER=-1 is NOT reliable for turning the display back on on
//     many Windows setups. Waking instead goes through
//     SetThreadExecutionState(ES_DISPLAY_REQUIRED), which forces the display
//     on by resetting the display idle timer (the technique used by all
//     "keep display awake" tools). SC_MONITORPOWER=-1 is still sent too, as a
//     belt-and-suspenders measure.
var (
	user32 = syscall.NewLazyDLL("user32.dll")

	procSendMessageTimeoutW = user32.NewProc("SendMessageTimeoutW")

	kernel32               = syscall.NewLazyDLL("kernel32.dll")
	procSetThreadExecState = kernel32.NewProc("SetThreadExecutionState")
)

const (
	hwndBroadcast  = uintptr(0xFFFF)
	wmSysCommand   = 0x0112
	scMonitorPower = 0xF170
	monitorOff     = int32(2)
	monitorOn      = int32(-1)

	// SendMessageTimeout flags + per-window timeout. The timeout is intentionally
	// short (300ms): SC_MONITORPOWER is handled quickly by the desktop shell, and
	// a hung top-level window would otherwise stall every screen toggle for the
	// full timeout. The message is still delivered to responsive windows.
	smtoBlock       = 0x0001
	smtoAbortIfHung = 0x0002
	sendTimeoutMS   = 300

	// SetThreadExecutionState flags.
	esDisplayRequired = 0x00000002
	esContinuous      = 0x80000000
)

type windowsController struct{}

// newPlatform is the Windows entry point.
func newPlatform(_ string) (Controller, error) {
	if err := user32.Load(); err != nil {
		return nil, fmt.Errorf("load user32.dll: %w", err)
	}
	return &windowsController{}, nil
}

func (w *windowsController) Backend() string { return "windows" }

func (w *windowsController) Available() error {
	if err := user32.Load(); err != nil {
		return fmt.Errorf("load user32.dll: %w", err)
	}
	return nil
}

// sendMonitorPower broadcasts SC_MONITORPOWER with the given lParam.
// lParam is sign-extended to 64-bit because LPARAM is pointer-sized.
func (w *windowsController) sendMonitorPower(lParam int32) error {
	lParamPtr := uintptr(int64(lParam))
	var result uintptr
	r, _, callErr := procSendMessageTimeoutW.Call(
		hwndBroadcast,
		uintptr(wmSysCommand),
		uintptr(scMonitorPower),
		lParamPtr,
		uintptr(smtoBlock|smtoAbortIfHung),
		uintptr(sendTimeoutMS),
		uintptr(unsafe.Pointer(&result)),
	)
	if r == 0 {
		// 0 means the call failed or timed out. A timeout (GetLastError == 0)
		// is reported when some top-level window did not respond in time.
		if errno, ok := callErr.(syscall.Errno); ok && errno != 0 {
			return fmt.Errorf("SendMessageTimeout(SC_MONITORPOWER): %w", callErr)
		}
		return fmt.Errorf("SendMessageTimeout(SC_MONITORPOWER) timed out after %dms (an unresponsive top-level window?)", sendTimeoutMS)
	}
	return nil
}

func (w *windowsController) Off() error { return w.sendMonitorPower(monitorOff) }

func (w *windowsController) On() error {
	// 1) Wake the display by asserting ES_DISPLAY_REQUIRED momentarily.
	// SetThreadExecutionState returns the *previous* flags; a non-zero return
	// means the call succeeded and the display-required flag is now armed. It
	// stays armed until this thread's next SetThreadExecutionState clears it -
	// the Home Assistant polling loop calls this constantly while on, which is
	// exactly the keep-awake behaviour we want, and it naturally clears when
	// the watchdog stops driving.
	if r, _, _ := procSetThreadExecState.Call(uintptr(esDisplayRequired)); r == 0 {
		// Failed (rare) - not fatal: SC_MONITORPOWER=-1 below may still work.
		_, _, _ = procSetThreadExecState.Call(uintptr(esContinuous))
	}
	// 2) Belt-and-suspenders: also broadcast SC_MONITORPOWER=-1, which turns
	// the display back on when the earlier Off() was the one that turned it off.
	return w.sendMonitorPower(monitorOn)
}

// IsOff cannot be queried reliably via public Win32 APIs, so report ErrNoQuery
// and let the watchdog re-assert Off() periodically.
func (w *windowsController) IsOff() (bool, error) { return false, ErrNoQuery }
