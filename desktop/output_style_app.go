package main

import (
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

// SetOutputStyle persists the chosen style into [agent] output_style (task
// 385a: the persistence half of the dual-layer switch; the session-level
// instant apply is task 385b). The running session keeps the system prompt it
// already built — the style folds in at boot, so the choice takes effect on
// the next session. An unknown name is an error rather than a silent write:
// the selector only offers styles that exist, and a stale name must not look
// like a successful switch.
func (a *App) SetOutputStyle(name string) error {
	name = strings.TrimSpace(name)
	if name != "" && !strings.EqualFold(name, "default") {
		if _, ok := outputstyle.Resolve(name, outputstyle.Dirs()); !ok {
			return fmt.Errorf("output style %q was not found in %s",
				name, strings.Join(outputstyle.Dirs(), ", "))
		}
	}
	return a.applyConfigOnly(func(c *config.Config) error {
		return c.SetOutputStyle(name)
	})
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
