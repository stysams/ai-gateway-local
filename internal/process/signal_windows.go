//go:build windows

package process

import (
	"os"
	"os/signal"
	"syscall"
)

// Windows console control events (wincon.h). CTRL_C and CTRL_BREAK are
// already delivered by the Go runtime as os.Interrupt; CTRL_CLOSE (window
// close) otherwise terminates the process without any signal, so it must be
// intercepted via SetConsoleCtrlHandler to honor the graceful shutdown
// contract (docs/v1-scheme.md §14.3).
const (
	ctrlCEvent     = 0
	ctrlBreakEvent = 1
	ctrlCloseEvent = 2
)

// consoleEvents carries CTRL_CLOSE events delivered on a system thread by the
// console handler. Buffered so the callback never blocks.
var consoleEvents = make(chan os.Signal, 4)

// consoleCtrlHandler is the SetConsoleCtrlHandler callback. It must not
// capture Go values; it only forwards to the package-level channel. CTRL_C
// and CTRL_BREAK return 0 so the Go runtime's own handling (os.Interrupt)
// stays authoritative.
func consoleCtrlHandler(ctrlType uintptr) uintptr {
	switch ctrlType {
	case ctrlCEvent, ctrlBreakEvent:
		return 0
	case ctrlCloseEvent:
		select {
		case consoleEvents <- os.Interrupt:
		default:
		}
		return 1 // handled: give the process time to shut down gracefully
	}
	return 0
}

var consoleHandlerInstalled bool

func installConsoleHandler() {
	if consoleHandlerInstalled {
		return
	}
	k32 := syscall.NewLazyDLL("kernel32.dll")
	proc := k32.NewProc("SetConsoleCtrlHandler")
	cb := syscall.NewCallback(consoleCtrlHandler)
	if r, _, _ := proc.Call(cb, 1); r != 0 {
		consoleHandlerInstalled = true
	}
}

// NotifySignals returns a channel fed by OS signals (Ctrl+C / SIGTERM) plus,
// on Windows, console close events. The caller should receive from it until
// graceful shutdown begins.
func NotifySignals() <-chan os.Signal {
	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	installConsoleHandler()
	merged := make(chan os.Signal, 4)
	go func() {
		for {
			select {
			case s := <-sigCh:
				merged <- s
			case s := <-consoleEvents:
				merged <- s
			}
		}
	}()
	return merged
}
