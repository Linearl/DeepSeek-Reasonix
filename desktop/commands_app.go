package main

import (
	"reasonix/internal/control"
	"reasonix/internal/i18n"
	"reasonix/internal/skill"
)

// CommandInfo describes one available slash command for the composer's "/" menu.
type CommandInfo struct {
	Name        string `json:"name"` // without the leading slash
	Description string `json:"description"`
	Hint        string `json:"hint,omitempty"`  // argument hint, if any
	Kind        string `json:"kind"`            // "builtin" | "custom" | "mcp" | "skill" | "subagent"
	Group       string `json:"group,omitempty"` // menu group; older frontends can ignore it
	Plugin      string `json:"plugin,omitempty"`
	Color       string `json:"color,omitempty"`
}

// Commands lists the slash commands available this session — built-in actions,
// custom commands (.reasonix/commands), and MCP prompts — for the composer's "/"
// autocomplete menu.
func (a *App) Commands() []CommandInfo {
	out := []CommandInfo{
		{Name: "new", Description: i18n.M.CmdNew, Kind: "builtin", Group: "actions"},
		{Name: "clear", Description: i18n.M.CmdClear, Kind: "builtin", Group: "actions"},
		{Name: "compact", Description: i18n.M.CmdCompact, Kind: "builtin", Group: "actions"},
		{Name: "model", Description: i18n.M.CmdModel, Kind: "builtin", Group: "actions"},
		{Name: "provider", Description: i18n.M.CmdProvider, Kind: "builtin", Group: "management"},
		{Name: "effort", Description: i18n.M.CmdEffort, Kind: "builtin", Group: "actions"},
		{Name: "memory", Description: i18n.M.CmdMemory, Kind: "builtin", Group: "management"},
		{Name: "migrate", Description: i18n.M.CmdMigrate, Kind: "builtin", Group: "management"},
		{Name: "goal", Description: i18n.M.CmdGoal, Kind: "builtin", Group: "actions"},
		{Name: "remember", Description: i18n.M.CmdRemember, Kind: "builtin", Group: "management"},
		{Name: "mcp", Description: i18n.M.CmdMcp, Kind: "builtin", Group: "integrations"},
		{Name: "hooks", Description: i18n.M.CmdHooks, Kind: "builtin", Group: "management"},
		{Name: "plugins", Description: i18n.M.CmdPlugins, Kind: "builtin", Group: "integrations"},
		{Name: "theme", Description: i18n.M.CmdTheme, Kind: "builtin", Group: "management"},
		{Name: "skill", Description: i18n.M.CmdSkill, Kind: "builtin", Group: "skills"},
		{Name: "reload-cmd", Description: i18n.M.CmdReloadCmd, Kind: "builtin", Group: "management"},
		{Name: "reload", Description: i18n.M.CmdReload, Kind: "builtin", Group: "management"},
	}
	a.mu.RLock()
	ctrl := a.activeCtrlLocked()
	a.mu.RUnlock()
	if ctrl == nil {
		return append(out, docsBuiltinCommand(control.DocsSlashName))
	}
	commands := ctrl.Commands()
	slashSkills := ctrl.SlashSkills()
	out = append(out, docsBuiltinCommand(control.ResolvedBuiltinSlashName(control.DocsSlashName, commands, slashSkills)))
	// Skills are invocable as slash commands (the model runs inline ones; subagent ones
	// run isolated). Listing them here is what surfaces /init, /explore, … in the
	// composer's slash menu; selecting one submits its displayed slash name, which the controller
	// resolves via RunSkill.
	for _, s := range slashSkills {
		kind := "skill"
		if s.RunAs == skill.RunSubagent {
			kind = "subagent"
		}
		group := "skills"
		if kind == "subagent" {
			group = "subagents"
		}
		out = append(out, CommandInfo{Name: s.SlashName(), Description: s.Description, Kind: kind, Group: group, Plugin: s.Plugin, Color: s.Color})
	}
	for _, c := range commands {
		if c.Hidden {
			continue
		}
		out = append(out, CommandInfo{Name: c.Name, Description: c.Description, Hint: c.ArgHint, Kind: "custom", Group: "skills", Plugin: c.Plugin})
	}
	if h := ctrl.Host(); h != nil {
		for _, p := range h.Prompts() {
			out = append(out, CommandInfo{Name: p.Name, Description: p.Description, Kind: "mcp", Group: "integrations"})
		}
	}
	return resolveDocsCommand(out)
}

func docsBuiltinCommand(name string) CommandInfo {
	return CommandInfo{Name: name, Description: i18n.M.CmdDocs, Hint: "<question>", Kind: "builtin", Group: "integrations"}
}

func resolveDocsCommand(commands []CommandInfo) []CommandInfo {
	winner := -1
	winnerRank := -1
	for i, cmd := range commands {
		if cmd.Name != "docs" {
			continue
		}
		rank := 0
		switch cmd.Kind {
		case "custom":
			rank = 2
		case "skill", "subagent":
			rank = 1
		}
		if rank > winnerRank {
			winner = i
			winnerRank = rank
		}
	}
	if winner < 0 {
		return commands
	}
	out := make([]CommandInfo, 0, len(commands))
	for i, cmd := range commands {
		if cmd.Name != "docs" || i == winner {
			out = append(out, cmd)
		}
	}
	return out
}
