package main

import (
	"errors"
	"fmt"
	"strings"

	"reasonix/internal/config"
	"reasonix/internal/outputstyle"
)

// OutputStyleOption is one selectable answer style for the lab's 回答风格
// selector (task 385a): a built-in or a custom .md file that actually loaded.
type OutputStyleOption struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Builtin     bool   `json:"builtin"`
	KeepCoding  bool   `json:"keepCoding"`
	Path        string `json:"path"`   // "" for built-ins
	Active      bool   `json:"active"` // matches the persisted [agent] output_style
}

// OutputStyleIssue is a style file that did not load. The boot path skips
// such files silently on purpose; the selector surfaces them, because a file
// the user just wrote that never appears must say why (task 385a: bad
// frontmatter is a visible error, never a silent no-op).
type OutputStyleIssue struct {
	Path   string `json:"path"`
	Name   string `json:"name"`   // filename stem
	Reason string `json:"reason"` // human-readable cause
}

// OutputStyleListView is the whole selector payload in one round trip.
type OutputStyleListView struct {
	// Active is the persisted [agent] output_style ("" = default, no style).
	Active string `json:"active"`
	// Options are the loadable styles, sorted by name (built-ins included).
	Options []OutputStyleOption `json:"options"`
	// Issues are the files List had to skip, sorted by path.
	Issues []OutputStyleIssue `json:"issues"`
	// Dirs is the search path the list was built from, for the "open style
	// directory" hint (the file-management entry itself is task 385c).
	Dirs []string `json:"dirs"`
}

// ListOutputStyles returns every loadable style via outputstyle.ListReport —
// integration only, no second loader — plus the persisted active value and the
// files that failed to load.
func (a *App) ListOutputStyles() (OutputStyleListView, error) {
	cfg, _, err := a.loadDesktopUserConfigForView()
	if err != nil {
		return OutputStyleListView{}, err
	}
	dirs := outputstyle.Dirs()
	styles, loadIssues := outputstyle.ListReport(dirs)
	active := strings.TrimSpace(cfg.Agent.OutputStyle)
	view := OutputStyleListView{
		Active:  active,
		Options: make([]OutputStyleOption, 0, len(styles)),
		Issues:  make([]OutputStyleIssue, 0, len(loadIssues)),
		Dirs:    dirs,
	}
	for _, st := range styles {
		// "default" is the no-style sentinel: Resolve() always refuses it, so
		// a file with that name could never be applied. Skip it instead of
		// offering a choice that cannot stick; the selector carries its own
		// synthetic "no style" entry.
		if strings.EqualFold(st.Name, "default") {
			continue
		}
		view.Options = append(view.Options, OutputStyleOption{
			Name:        st.Name,
			Description: st.Description,
			Builtin:     st.Builtin,
			KeepCoding:  st.KeepCoding,
			Path:        st.Path,
			Active:      active != "" && strings.EqualFold(st.Name, active),
		})
	}
	for _, is := range loadIssues {
		view.Issues = append(view.Issues, OutputStyleIssue{Path: is.Path, Name: is.Name, Reason: is.Reason})
	}
	return view, nil
}

// SetOutputStyle persists the chosen style into [agent] output_style, then
// refreshes the ACTIVE tab's runtime so the new style takes effect in the
// current session (task 385b; 385a shipped the persistence half). The refresh
// goes through rebuildSetting — the same runtimeRebuildMu build+swap
// orchestration the model/effort switches use — so it is failure-atomic: a
// failed build leaves the old controller and its old-style system prompt
// running untouched, and the error is surfaced to the caller.
//
// Order is persist-first, never rebuild-first: a streaming turn must not lose
// the switch. When the active tab is mid-turn (rebuildBusyError) or another
// process holds the session lease, the choice is already on disk and the
// deferred-rebuild loop replays the refresh once the tab goes idle, announcing
// it with the usual "output style applied: session refreshed ..." notice — a
// streaming turn is never killed. Other tabs are never touched: only the
// active tab rebuilds, and every other runtime keeps the style it booted with
// (its own refresh comes from its next rebuild or next session).
//
// The returned string is a non-empty warning when the save landed but the
// refresh is deferred or unavailable; the settings banner renders it.
func (a *App) SetOutputStyle(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name != "" && !strings.EqualFold(name, "default") {
		if _, ok := outputstyle.Resolve(name, outputstyle.Dirs()); !ok {
			return "", fmt.Errorf("output style %q was not found in %s",
				name, strings.Join(outputstyle.Dirs(), ", "))
		}
	}
	if err := a.applyConfigOnly(func(c *config.Config) error {
		return c.SetOutputStyle(name)
	}); err != nil {
		return "", err
	}
	if a.ctx != nil && a.activeTab() == nil {
		// Frontend attached but no session yet: the save is the whole job —
		// the next session builds with the new style (same soft-warning shape
		// SetOptimisticWrite uses for its restart-only case).
		return "output style saved — it takes effect on the next session", nil
	}
	if err := a.rebuildSetting("output style"); err != nil {
		if warning, ok := a.deferredRebuildWarning("output style", err); ok {
			return warning, nil // foreign lease: saved + scheduled for replay
		}
		var busy *rebuildBusyError
		if errors.As(err, &busy) {
			if tab := a.activeTab(); tab != nil {
				// Streaming turn: never kill it. The retry loop keeps waiting
				// while controllerHasActiveRuntimeWork is set, then replays
				// this rebuild with the already-persisted value.
				a.scheduleDeferredRebuild(tab.ID, "output style")
				return "output style saved; it will apply when the current turn finishes", nil
			}
		}
		// Real build failure: atomic — the old runtime is still live with the
		// old style — so report it instead of pretending the switch happened.
		return "", err
	}
	return "", nil
}

// SetExperimentalOutputStyleUI toggles the lab's 回答风格 section (task 385a).
// It gates the UI surface only — prompt injection keeps reading [agent]
// output_style regardless, so a hand-written toml entry still applies with
// the switch off.
func (a *App) SetExperimentalOutputStyleUI(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error {
		return c.SetExperimentalOutputStyleUI(enabled)
	})
}
