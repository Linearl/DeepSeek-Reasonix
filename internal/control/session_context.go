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
			if block, stats := c.capabilityDirectory(); block != "" {
				executorSections.CapabilityCatalog = block
				plannerSections.CapabilityCatalog = block
				if stats.Truncated {
					// Over-cap is a growth ratchet signal (task 969), not a
					// fault: the block truncated to whole rows and the model
					// was told to drill down via inspect.
					c.noteDirectoryTruncation(stats)
				}
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

// noteDirectoryTruncation logs the 20KB ratchet crossing once per distinct
// drop count so a persistently oversized catalog warns without spamming every
// turn.
func (c *Controller) noteDirectoryTruncation(stats capability.DirectoryStats) {
	prev := c.directoryTruncationDropped.Swap(int64(stats.Dropped))
	if prev == int64(stats.Dropped) {
		return
	}
	slog.Warn("control: capability directory truncated at 20KB cap",
		"rows", stats.Rows, "dropped", stats.Dropped, "bytes", stats.Bytes)
}
