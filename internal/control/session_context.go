package control

import (
	"context"
	"log/slog"

	"reasonix/internal/agent"
	"reasonix/internal/capability"
	"reasonix/internal/sessioncontext"
	"reasonix/internal/skill"
)

// withTurnContext attaches current role-specific snapshots to a host turn.
// Synthetic turns receive bootstrap-only context: they may repair an upgraded
// legacy session with no snapshot, but never advertise a mid-session update.
func (c *Controller) withTurnContext(ctx context.Context, realUserTurn bool) context.Context {
	if c == nil {
		return ctx
	}
	base := c.sessionContextStatic
	if mem := c.memory.current(); mem != nil {
		base.BackgroundMemory = mem.BackgroundDataBlock()
	}

	executorSections := base
	plannerSections := base
	if !c.disableImplicitSkillInvocation {
		sk := c.skills.list()
		executorSections.SkillsCatalog = skill.CatalogBlock(sk)
		plannerSections.SkillsCatalog = skill.ReadOnlyCatalogBlock(sk)
		// Task 969: the standing L1 directory rides the same model-facing
		// discovery gate as the skills catalog — it carries skill rows, so
		// hiding skill discovery must hide the directory too. Both roles read
		// the same block; it is discovery metadata, and use_capability stays
		// the drill-down entry for both.
		if c.capabilityDirectory != nil {
			block, stats := c.capabilityDirectory()
			c.trackDirectoryPressure(stats)
			c.contextDirectoryBytes.Store(int64(len(block)))
			if block != "" {
				executorSections.CapabilityCatalog = block
				plannerSections.CapabilityCatalog = block
			}
		}
	}
	bundle := agent.TurnContextBundle{
		Executor:      sessioncontext.Build(executorSections),
		Planner:       sessioncontext.Build(plannerSections),
		BootstrapOnly: !realUserTurn,
	}
	return agent.WithTurnContextBundle(ctx, bundle)
}

// trackDirectoryPressure fires the over-line warning (task 1224) when the
// standing directory crosses the 64KB warning line: one warn per climb, reset
// while the catalog sits back under it so the next climb warns again. Phase 2
// removed the hard cap — nothing is ever truncated — so this latch is the only
// log-side signal; the composer gauge carries the user-facing visual state.
func (c *Controller) trackDirectoryPressure(stats capability.DirectoryStats) {
	if !stats.NearCap {
		c.directoryPressureWarned.Store(false)
		return
	}
	if !c.directoryPressureWarned.CompareAndSwap(false, true) {
		return
	}
	slog.Warn("control: capability directory over the warning line (rendered in full, nothing truncated)",
		"bytes", stats.Bytes, "warn_bytes", capability.DirectoryWarnBytes,
		"rows", stats.Rows, "warn_rows", capability.DirectoryWarnRows)
}
