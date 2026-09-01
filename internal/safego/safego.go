// Package safego wraps goroutine launches and synchronous callbacks with a
// recover that logs the panic site instead of letting an unrecovered panic
// kill the process. Windows GUI builds have no console, so any panic output
// that relies on fd=2 is lost entirely (crash-fatal stays 0 bytes when the
// runtime dump is not produced); safego routes the recovered stack through
// the standard log package instead, which the desktop redirects to a rolling
// file and the CLI keeps on stderr.
package safego

import (
	"log"
	"runtime/debug"
)

// Go launches fn on a new goroutine with a named recover guard. site names
// the launch point for the log line ("servepool.manager.loop").
func Go(site string, fn func()) {
	go func() {
		defer Recover(site)
		fn()
	}()
}

// Guard runs fn synchronously under the same recover guard, for callbacks
// invoked from foreign code (wails event hooks, native bindings) where a
// panic would otherwise escape through the host.
func Guard(site string, fn func()) {
	defer Recover(site)
	fn()
}

// Recover turns a recovered panic into a log line with the stack. Call it via
// defer at the top of a goroutine or callback.
func Recover(site string) {
	if r := recover(); r != nil {
		log.Printf("[safego] %s: recovered panic: %v\n%s", site, r, debug.Stack())
	}
}
