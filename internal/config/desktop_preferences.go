package config

import (
	"fmt"
	"strings"
)

// DesktopConfig controls desktop-only UI preferences. It is intentionally
// separate from top-level language and [ui] so desktop choices do not affect CLI
// language, terminal colours, or provider-visible prompt/request data.
type DesktopConfig struct {
	Language                  string   `toml:"language"`                     // auto|en|zh; empty/auto = browser/OS auto-detect
	Currency                  string   `toml:"currency"`                     // legacy display currency; migrated to [billing].display_currency
	LayoutStyle               string   `toml:"layout_style"`                 // workbench|creation; legacy classic is migrated on startup
	Theme                     string   `toml:"theme"`                        // auto|dark|light; empty resolves to auto
	ThemeStyle                string   `toml:"theme_style"`                  // graphite|aurora|slate|carbon|nocturne|amber and legacy aliases
	TerminalTheme             string   `toml:"terminal_theme"`               // auto|dark|light; auto follows the desktop app theme
	ExternalOpener            string   `toml:"external_opener"`              // preferred installed app used by the desktop Open control
	CloseBehavior             string   `toml:"close_behavior"`               // quit|background; desktop window close behavior
	DisplayMode               string   `toml:"display_mode"`                 // standard|compact (legacy "minimal" maps to compact); transcript display mode
	StatusBarStyle            string   `toml:"status_bar_style"`             // icon|text; desktop status bar metric labels
	StatusBarStyleInitialized bool     `toml:"status_bar_style_initialized"` // one-time icon default upgrade; later choices are user-owned
	StatusBarItems            []string `toml:"status_bar_items"`             // ordered visible desktop status bar items
	DefaultToolApprovalMode   string   `toml:"default_tool_approval_mode"`   // ask|auto|yolo; defaults to auto for newly-created desktop sessions
	// Autopilot defaults for newly-created desktop sessions. Autopilot runs a
	// session unattended: the goal machine bounds it by wall clock, and the reviewer
	// answers approval prompts nobody is there to answer.
	Autopilot              bool   `toml:"autopilot"`
	AutopilotMaxRuntime    string `toml:"autopilot_max_runtime"`     // Go duration; required when autopilot is on
	AutopilotApprovalGrace string `toml:"autopilot_approval_grace"` // wait for a human before the reviewer decides; empty = 15s
	CheckUpdates              *bool    `toml:"check_updates"`                // startup update checks; nil keeps the default enabled
	// UpdateChannel is a legacy compatibility field. It is accepted on read but
	// ignored and omitted from future canonical writes.
	UpdateChannel        string   `toml:"update_channel"`
	Telemetry            *bool    `toml:"telemetry"`          // anonymous launch ping plus scrubbed next-launch native crash diagnostics; nil keeps the default enabled
	Metrics              *bool    `toml:"metrics"`            // aggregate desktop metrics (anonymous signal/bucket counts, including lifecycle health; no content); nil keeps the default enabled
	ProviderAccess       []string `toml:"provider_access"`    // desktop-only list of provider entries shown in Settings > Model > Access
	SessionExperience    string   `toml:"session_experience"` // standard|deep; canonical desktop transcript experience
	ExpandThinking       bool     `toml:"expand_thinking"`    // deprecated compatibility alias: true maps to auto
	ReasoningDisplayMode string   `toml:"reasoning_display_mode"`
	ConversationWidth    string   `toml:"conversation_width"` // standard|full; max transcript width; empty = standard
	// QuickCommands are user-defined snippets offered by the desktop composer's
	// + menu (task 18). Array order is the display order; entries with an empty
	// Title or Text are dropped on save.
	QuickCommands []QuickCommandEntry `toml:"quick_commands"`
}

// QuickCommandEntry is one quick-command snippet. Title is the menu label, Text
// is inserted into the composer verbatim (the user still presses send).
type QuickCommandEntry struct {
	Title string `toml:"title" json:"title"`
	Text  string `toml:"text" json:"text"`
	// Enabled is a pointer so configs written before the switch existed keep
	// working: absent means enabled, and only an explicit false hides a snippet.
	Enabled *bool `toml:"enabled" json:"enabled,omitempty"`
}

// QuickCommandLimits bounds what the settings UI may store, so a runaway paste
// cannot bloat the user config or the menu.
const (
	QuickCommandMaxEntries  = 50
	QuickCommandMaxTitle    = 60
	QuickCommandMaxTextSize = 8 * 1024
)

// DesktopExternalOpener returns the selected opener id; unavailable ids fall
// back to the platform file manager in the desktop shell.
func (c *Config) DesktopExternalOpener() string {
	if c == nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(c.Desktop.ExternalOpener))
}

// SetQuickCommands replaces the snippet list after validation. Blank entries are
// dropped; oversized entries are rejected so the failure is visible in the UI
// rather than silently truncated.
func (c *Config) SetQuickCommands(entries []QuickCommandEntry) error {
	if c == nil {
		return nil
	}
	if len(entries) > QuickCommandMaxEntries {
		return fmt.Errorf("too many quick commands: %d (max %d)", len(entries), QuickCommandMaxEntries)
	}
	out := make([]QuickCommandEntry, 0, len(entries))
	for _, entry := range entries {
		title := strings.TrimSpace(entry.Title)
		text := entry.Text
		if title == "" && strings.TrimSpace(text) == "" {
			continue
		}
		if title == "" {
			return fmt.Errorf("quick command text %q needs a title", firstLine(text))
		}
		if len([]rune(title)) > QuickCommandMaxTitle {
			return fmt.Errorf("quick command title %q is too long (max %d characters)", title, QuickCommandMaxTitle)
		}
		if len(text) > QuickCommandMaxTextSize {
			return fmt.Errorf("quick command %q is too large (%d bytes, max %d)", title, len(text), QuickCommandMaxTextSize)
		}
		out = append(out, QuickCommandEntry{Title: title, Text: text, Enabled: entry.Enabled})
	}
	c.Desktop.QuickCommands = out
	return nil
}

// QuickCommands returns a copy so callers cannot mutate config state in place.
func (c *Config) DesktopQuickCommands() []QuickCommandEntry {
	if c == nil || len(c.Desktop.QuickCommands) == 0 {
		return nil
	}
	out := make([]QuickCommandEntry, len(c.Desktop.QuickCommands))
	copy(out, c.Desktop.QuickCommands)
	return out
}

func firstLine(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return s[:i]
	}
	return s
}
