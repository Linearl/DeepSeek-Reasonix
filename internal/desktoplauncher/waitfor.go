package desktoplauncher

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// relaunchWaitTimeout bounds how long --wait-for will block on the exiting
// desktop. Long enough for a graceful quit that flushes sessions; short
// enough that a wedged process cannot black-hole the relaunch forever.
const relaunchWaitTimeout = 90 * time.Second

// extractWaitFor peels "--wait-for <pid>" / "--wait-for=<pid>" out of the
// launcher arguments. The token is a handoff between the restarting desktop
// and this launcher; it must never reach the desktop's own flag parsing.
func extractWaitFor(args []string) (int, []string) {
	for i := range args {
		var (
			pid int
			err error
			rest []string
		)
		switch {
		case args[i] == "--wait-for" && i+1 < len(args):
			pid, err = strconv.Atoi(args[i+1])
			rest = append(append([]string{}, args[:i]...), args[i+2:]...)
		case strings.HasPrefix(args[i], "--wait-for="):
			pid, err = strconv.Atoi(strings.TrimPrefix(args[i], "--wait-for="))
			rest = append(append([]string{}, args[:i]...), args[i+1:]...)
		default:
			continue
		}
		if err == nil && pid > 0 {
			return pid, rest
		}
		// Malformed handoff token: drop it and start without waiting, so a
		// stale flag can never wedge the normal launch path.
		return 0, rest
	}
	return 0, args
}

// waitForHandoff is the Run-side wrapper: never fails the launch, because a
// timeout only means the previous desktop is wedged and the user still wants
// the new one to come up (the gateway port then keeps its own retry story).
func waitForHandoff(pid int) {
	if pid <= 0 {
		return
	}
	if err := waitForProcessExit(pid, relaunchWaitTimeout); err != nil {
		fmt.Fprintln(os.Stderr, "warning: wait for previous desktop:", err)
	}
}
