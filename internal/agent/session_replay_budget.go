package agent

import (
	"os"
)

// SessionReplayBudget reports how close a session event log is to the replay
// fence (task 51 backend). ratio is size/limit clamped to [0, 2]; callers warn
// at >= 0.90 so a user can compact before inbox delivery starts failing.
type SessionReplayBudget struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	Limit  int64  `json:"limit"`
	Ratio  float64 `json:"ratio"`
	Exists bool   `json:"exists"`
}

// SessionReplayBudgetFor measures one session event log against the adaptive
// replay allowance (the same limitsForSessionLog the loader uses).
func SessionReplayBudgetFor(path string) SessionReplayBudget {
	limits := limitsForSessionLog(path, defaultSessionReplayLimits)
	out := SessionReplayBudget{Path: path, Limit: limits.maxBytes}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return out
	}
	out.Exists = true
	out.Size = info.Size()
	if limits.maxBytes > 0 {
		out.Ratio = float64(out.Size) / float64(limits.maxBytes)
		if out.Ratio > 2 {
			out.Ratio = 2
		}
	}
	return out
}

// ApproachingFence reports whether the log is inside the warning band below
// the hard replay refusal (default 90%).
func (b SessionReplayBudget) ApproachingFence(threshold float64) bool {
	if threshold <= 0 {
		threshold = 0.90
	}
	return b.Exists && b.Limit > 0 && b.Ratio >= threshold
}
