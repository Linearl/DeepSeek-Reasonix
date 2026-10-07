// Package pidalive is a zero-dependency leaf: "does this pid name a live
// process?" plus the lock-holder sidecar pid parser. It lives apart from
// baseproc because baseproc itself imports config, which imports
// sessioncollab — both sessioncollab and collabinbox need this oracle for the
// 任务511 复发断根 stale-holder recycle, and a leaf is the only shape both can
// import without an import cycle. Alive is defined per platform in
// pidalive_windows.go / pidalive_unix.go.
package pidalive

import (
	"strconv"
	"strings"
)

// ParseHolderPid extracts the pid from a lock-holder sidecar line — the
// "pid=N held_since=…" format writeLockHolderInfo stamps in both collabinbox
// (.collab-inbox.lock.holder) and sessioncollab (.mail.lock.holder). Returns 0
// when the line is empty or unparsable: callers must treat 0 as "cannot
// judge" and leave the sidecar alone (never guess, never clear on a guess).
func ParseHolderPid(info string) int {
	for _, field := range strings.Fields(info) {
		value, ok := strings.CutPrefix(field, "pid=")
		if !ok {
			continue
		}
		pid, err := strconv.Atoi(value)
		if err != nil || pid <= 0 {
			return 0
		}
		return pid
	}
	return 0
}
