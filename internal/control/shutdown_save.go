package control

import (
	"reasonix/internal/agent"
)

// recoverShutdownSave answers a save lock that stayed held for the whole
// bounded wait during the shutdown snapshot. tl1280: the schema-2 unlocked
// append (AppendForShutdownWithoutLock) left with the write-path slow lane, so
// every session now takes the recovery-copy path below — the same one a
// schema-1 session always had.
func (c *Controller) recoverShutdownSave(s *agent.Session, path string, saveErr error, forceRewrite bool) (string, error) {
	_ = s
	_ = forceRewrite
	return c.recoverShutdownSnapshot(path, saveErr)
}

// listingDeferredAfterUnlockedAppend accepted a listing-sidecar refresh that
// failed on the very lock the shutdown append just worked around. tl1280:
// there is no unlocked append any more, so a projection failure is a real
// failure the caller must surface.
func listingDeferredAfterUnlockedAppend(s *agent.Session, path string, err error) bool {
	_ = s
	_ = path
	_ = err
	return false
}
