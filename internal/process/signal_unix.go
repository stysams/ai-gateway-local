//go:build !windows

package process

import (
	"os"
	"os/signal"
	"syscall"
)

// NotifySignals returns a channel fed by SIGINT and SIGTERM. Both map to the
// same graceful shutdown flow as the Windows console events.
func NotifySignals() <-chan os.Signal {
	ch := make(chan os.Signal, 2)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	return ch
}
