package config

import (
	"fmt"
)

// Built-in default for the replayed-graph cache capacity (task 196fix2): the
// 0928 reading proved capacity 1 scores zero hits under interleaved
// multi-session saves, so the reviewed default moves to 3 and stays tunable
// (user 2026-09-28: "LRU 我建议可调"). Range 1-16; 0 in the file means
// "use the built-in default".
const DagGraphCacheCapacityDefault = 3

// DagGraphCacheCapacityMin/Max bound the tunable range (task 196fix2).
const (
	DagGraphCacheCapacityMin = 1
	DagGraphCacheCapacityMax = 16
)

// DagGraphCacheCapacity returns the effective LRU capacity for the
// replayed-graph cache: 0 (unset) or an out-of-range stored value falls back
// to the built-in default, so a hand-edited config can neither disable the
// cache nor request an unbounded one.
func DagGraphCacheCapacity(cfg *Config) int {
	if cfg == nil {
		return DagGraphCacheCapacityDefault
	}
	if cfg.Agent.DagGraphCacheCapacity <= 0 {
		return DagGraphCacheCapacityDefault
	}
	if cfg.Agent.DagGraphCacheCapacity > DagGraphCacheCapacityMax {
		return DagGraphCacheCapacityMax
	}
	return cfg.Agent.DagGraphCacheCapacity
}

// SetDagGraphCacheCapacity stores the tunable LRU capacity (task 196fix2).
// The setter refuses out-of-range values instead of clamping: a settings
// slider that silently stores a different number than the user picked would
// make the UI lie about what is saved.
func (c *Config) SetDagGraphCacheCapacity(capacity int) error {
	if capacity < DagGraphCacheCapacityMin || capacity > DagGraphCacheCapacityMax {
		return fmt.Errorf("dag graph cache capacity %d: must be between %d and %d",
			capacity, DagGraphCacheCapacityMin, DagGraphCacheCapacityMax)
	}
	c.Agent.DagGraphCacheCapacity = capacity
	return nil
}

// Built-in defaults for the cold-cache pass (task 297): 600 KiB of stored
// context and 5h of idle — the two numbers the user set on 2026-09-24. A
// zero/absent file value means "use these", never "disable".
const (
	ColdCacheCompactMinBytesDefault    int64 = 600 * 1024
	ColdCacheCompactIdleMinutesDefault int   = 300
)

// ColdCacheCompactEffectiveMinBytes/IdleMinutes report the task-297 knob the
// view should display: the stored value when set, the built-in default when
// 0/absent — the settings field never shows a bare 0.
func ColdCacheCompactEffectiveMinBytes(cfg *Config) int64 {
	if cfg == nil || cfg.Agent.ColdCacheCompactMinBytes <= 0 {
		return ColdCacheCompactMinBytesDefault
	}
	return cfg.Agent.ColdCacheCompactMinBytes
}

func ColdCacheCompactEffectiveIdleMinutes(cfg *Config) int {
	if cfg == nil || cfg.Agent.ColdCacheCompactIdleMinutes <= 0 {
		return ColdCacheCompactIdleMinutesDefault
	}
	return cfg.Agent.ColdCacheCompactIdleMinutes
}
