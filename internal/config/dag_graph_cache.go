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

// Task 499 (2026-10-05): byte ceilings for the replayed-graph cache. The
// count cap (above) bounds entries, not bytes — the 10-05 reading showed the
// cached set reaching multi-GB because one entry can be a 700 MB+ graph.
// Defaults: 2048 MiB total, 1024 MiB per entry — sized so the known-worst
// real session (728 MB log, task 196) keeps full cache service while the
// closed-session retained set is bounded at 2 GiB worst case. Ranges are
// 16..65536 MiB; 0 in the file means "use the built-in default".
const (
	DagGraphCacheMaxMBDefault      int64 = 2048
	DagGraphCacheEntryMaxMBDefault int64 = 1024
	DagGraphCacheMaxMBMin          int64 = 16
	DagGraphCacheMaxMBMax          int64 = 65536
)

// DagGraphCacheMaxMB returns the effective total byte ceiling in MiB: 0
// (unset) or an out-of-range stored value falls back to the built-in
// default, so a hand-edited config can neither disable the ceiling nor
// request an unbounded cache.
func DagGraphCacheMaxMB(cfg *Config) int64 {
	if cfg == nil || cfg.Agent.DagGraphCacheMaxMB <= 0 {
		return DagGraphCacheMaxMBDefault
	}
	if cfg.Agent.DagGraphCacheMaxMB > DagGraphCacheMaxMBMax {
		return DagGraphCacheMaxMBMax
	}
	if cfg.Agent.DagGraphCacheMaxMB < DagGraphCacheMaxMBMin {
		return DagGraphCacheMaxMBDefault
	}
	return cfg.Agent.DagGraphCacheMaxMB
}

// DagGraphCacheEntryMaxMB returns the effective per-entry ceiling in MiB,
// clamped to never exceed the total ceiling: an entry cap above the total
// would let one admission defeat the total, and the worst retained set must
// stay exactly the total cap.
func DagGraphCacheEntryMaxMB(cfg *Config) int64 {
	total := DagGraphCacheMaxMB(cfg)
	v := cfg.entryMaxMBOrZero()
	if v <= 0 || v < DagGraphCacheMaxMBMin {
		v = DagGraphCacheEntryMaxMBDefault
	}
	if v > DagGraphCacheMaxMBMax {
		v = DagGraphCacheMaxMBMax
	}
	if v > total {
		v = total
	}
	return v
}

// entryMaxMBOrZero reads the stored per-entry value with nil safety.
func (c *Config) entryMaxMBOrZero() int64 {
	if c == nil {
		return 0
	}
	return c.Agent.DagGraphCacheEntryMaxMB
}

// SetDagGraphCacheMaxMB stores the total byte ceiling (task 499). Like the
// capacity setter it refuses out-of-range values: a settings write that
// silently stores a different number would make the UI lie.
func (c *Config) SetDagGraphCacheMaxMB(maxMB int64) error {
	if maxMB < DagGraphCacheMaxMBMin || maxMB > DagGraphCacheMaxMBMax {
		return fmt.Errorf("dag graph cache max MB %d: must be between %d and %d",
			maxMB, DagGraphCacheMaxMBMin, DagGraphCacheMaxMBMax)
	}
	c.Agent.DagGraphCacheMaxMB = maxMB
	return nil
}

// SetDagGraphCacheEntryMaxMB stores the per-entry ceiling (task 499), same
// refusal convention. Whether the stored value exceeds the total is not a
// write error — the reader clamps it under the total at push time.
func (c *Config) SetDagGraphCacheEntryMaxMB(maxMB int64) error {
	if maxMB < DagGraphCacheMaxMBMin || maxMB > DagGraphCacheMaxMBMax {
		return fmt.Errorf("dag graph cache entry max MB %d: must be between %d and %d",
			maxMB, DagGraphCacheMaxMBMin, DagGraphCacheMaxMBMax)
	}
	c.Agent.DagGraphCacheEntryMaxMB = maxMB
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
