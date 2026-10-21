package main

import (
	"fmt"
	"reasonix/internal/agent"
	"reasonix/internal/config"
	"strings"
)

func setDefaultModelConfig(c *config.Config, ref string) error {

	resolved, err := selectableDesktopModelRef(c, ref)
	if err != nil {
		return err
	}
	c.DefaultModel = resolved
	return nil
}

func setPlannerModelConfig(c *config.Config, ref string) error {

	if ref != "" {
		resolved, err := selectableDesktopModelRef(c, ref)
		if err != nil {
			return err
		}
		ref = resolved
	}
	c.Agent.PlannerModel = ref
	return nil
}

// setGuardianModelConfig sets the reviewer model that guards autopilot runs: it
// judges approval prompts nobody answers, and screens risky actions before they
// reach a human. Empty clears it, which falls back to the conversation model.
func setGuardianModelConfig(c *config.Config, ref string) error {
	ref = strings.TrimSpace(ref)
	if ref == "" || strings.EqualFold(ref, "auto") {
		c.Agent.GuardianModel = ""
		return nil
	}
	resolved, err := selectableDesktopModelRef(c, ref)
	if err != nil {
		return err
	}
	c.Agent.GuardianModel = resolved
	return nil
}

func setVisionModelConfig(c *config.Config, ref string) error {

	ref = strings.TrimSpace(ref)
	if ref == "" || strings.EqualFold(ref, "auto") {
		c.Agent.VisionModel = strings.ToLower(ref)
		return nil
	}
	resolved, err := selectableDesktopVisionModelRef(c, ref)
	if err != nil {
		return err
	}
	c.Agent.VisionModel = resolved
	return nil
}

func setSubagentModelConfig(c *config.Config, ref string) error {

	ref = strings.TrimSpace(ref)
	if ref != "" {
		resolved, err := selectableDesktopModelRef(c, ref)
		if err != nil {
			return err
		}
		ref = resolved
	}
	c.Agent.SubagentModel = ref
	return nil
}

func setSubagentEffortConfig(c *config.Config, level string) error {

	level = strings.TrimSpace(level)
	if level == "" || level == "auto" {
		c.Agent.SubagentEffort = ""
		return nil
	}
	model := strings.TrimSpace(c.Agent.SubagentModel)
	if model == "" {
		model = c.DefaultModel
	}
	entry, ok := c.ResolveModel(model)
	if !ok {
		return fmt.Errorf("unknown subagent model %q", model)
	}
	effort, err := config.NormalizeEffort(entry, level)
	if err != nil {
		return err
	}
	c.Agent.SubagentEffort = effort
	return nil
}

// setDefaultModelEffortConfig writes the reasoning-effort level assigned to
// the default model (task 1042). "auto"/empty clears it. The level is
// validated against the exact entry the panel shows (per-model override
// applied via ResolveModel, same as the read path) so a pick that would be
// rejected by config.NormalizeEffort never reaches disk.
func setDefaultModelEffortConfig(c *config.Config, level string) error {
	level = strings.TrimSpace(level)
	ref := strings.TrimSpace(c.DefaultModel)
	if ref == "" {
		if resolved, _, ok := c.ResolveNewSessionChatModel(); ok {
			ref = resolved
		}
	}
	if ref == "" {
		return fmt.Errorf("no default model is configured")
	}
	entry, ok := c.ResolveModel(ref)
	if !ok {
		return fmt.Errorf("unknown default model %q", ref)
	}
	if level == "" || level == "auto" {
		entry.DefaultEffort = ""
	} else {
		effort, err := config.NormalizeEffort(entry, level)
		if err != nil {
			return err
		}
		entry.DefaultEffort = effort
	}
	// UpsertProvider replaces the whole entry; ResolveModel returned a copy
	// with the per-model override already folded in (applyModelOverride), so
	// writing it back must not materialise the override as provider-level
	// config. Only the default_effort field is edited on the untouched base
	// entry; a per-model override's own default_effort (if any) keeps winning
	// on read, exactly as before this edit.
	base, ok := c.Provider(entry.Name)
	if !ok {
		return fmt.Errorf("unknown provider %q", entry.Name)
	}
	updated := *base
	updated.DefaultEffort = entry.DefaultEffort
	return c.UpsertProvider(updated)
}

func setSubagentProfileModelConfig(c *config.Config, name, ref string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("name is required")
	}

	ref = strings.TrimSpace(ref)
	if ref == "" {
		deleteSubagentOverrideAliases(c.Agent.SubagentModels, name)
		return nil
	}
	resolved, err := selectableDesktopModelRef(c, ref)
	if err != nil {
		return err
	}
	if c.Agent.SubagentModels == nil {
		c.Agent.SubagentModels = map[string]string{}
	}
	deleteSubagentOverrideAliases(c.Agent.SubagentModels, name)
	c.Agent.SubagentModels[name] = resolved
	return nil
}

func setSubagentProfileEffortConfig(c *config.Config, name, level string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("name is required")
	}

	level = strings.TrimSpace(level)
	if level == "" || level == "auto" {
		deleteSubagentOverrideAliases(c.Agent.SubagentEfforts, name)
		return nil
	}
	// Validate against the model the override will actually apply to:
	// the alias-aware per-name model override first, then the global
	// subagent default, then the session default.
	model := subagentOverrideFor(c.Agent.SubagentModels, name)
	if model == "" {
		model = strings.TrimSpace(c.Agent.SubagentModel)
	}
	if model == "" {
		model = c.DefaultModel
	}
	entry, ok := c.ResolveModel(model)
	if !ok {
		return fmt.Errorf("unknown subagent model %q", model)
	}
	effort, err := config.NormalizeEffort(entry, level)
	if err != nil {
		return err
	}
	if c.Agent.SubagentEfforts == nil {
		c.Agent.SubagentEfforts = map[string]string{}
	}
	deleteSubagentOverrideAliases(c.Agent.SubagentEfforts, name)
	c.Agent.SubagentEfforts[name] = effort
	return nil
}

func setMaxSubagentDepthConfig(c *config.Config, depth int) error {

	c.Agent.MaxSubagentDepth = desktopMaxSubagentDepth(depth)
	return nil
}

func setMaxSubagentConcurrencyConfig(c *config.Config, n int) error {

	total, writers := agent.NormalizeConcurrencyLimits(n, c.Agent.MaxParallelWriters)
	c.Agent.MaxSubagentConcurrency = total
	c.Agent.MaxParallelWriters = writers
	return nil
}

func setMaxParallelWritersConfig(c *config.Config, n int) error {

	total, writers := agent.NormalizeConcurrencyLimits(c.Agent.MaxSubagentConcurrency, n)
	c.Agent.MaxSubagentConcurrency = total
	c.Agent.MaxParallelWriters = writers
	return nil
}
