package control

import (
	"log/slog"
	"path/filepath"
	"strings"
)

// Task 231 — autopilot pre-approval for the managed-path write classes.
//
// The user story: under autopilot, edits the user already opted into (skill
// files, hooks, session stores, bash sandbox escapes) still stopped on a human
// approval prompt nobody was there to answer. The approve callbacks are the
// only place that can decide this — guiding the model around them is
// impossible (the gate lives below the model), so the bypass lives here:
// master switch AND autopilot AND the per-category checkbox must ALL hold,
// otherwise the prompt behaves exactly as it does today.
//
// All five values ship false (铁律 2): the zero value is byte-for-byte the
// current behavior, and every bypassed approval is still logged with its
// category and subject so an unattended run stays auditable.

// PreapproveManagedOptions is the boot-resolved snapshot the controller keeps
// for the lifetime of a run. The three paths are computed once at boot (from
// the Reasonix home) so classification needs no config import here and cannot
// drift from what the settings checkboxes mean.
type PreapproveManagedOptions struct {
	// Enabled is the experimental_preapprove_managed_paths master switch.
	Enabled bool
	// The four independent checkboxes (settings preapprove_* keys).
	Skills     bool
	Hooks      bool
	Stores     bool
	BashEscape bool
	// SkillsDir / HooksFile / StoresDir are absolute paths boot derives from
	// config.MemoryUserDir(); empty means that class can never match.
	SkillsDir string
	HooksFile string
	StoresDir string
}

// ManagedWriteKind names one approval class this feature can pre-approve.
// "" (other) is everything the checkboxes do not cover — config.toml repairs
// keep prompting regardless, so a checkmark can never widen past its class.
type ManagedWriteKind string

const (
	managedKindSkills ManagedWriteKind = "skills"
	managedKindHooks  ManagedWriteKind = "hooks"
	managedKindStores ManagedWriteKind = "stores"
	managedKindBash   ManagedWriteKind = "bash_escape"
	managedKindOther  ManagedWriteKind = "other"
)

// allows is the single decision point: master switch, autopilot, and the
// category checkbox, all required. Kept a pure function so every combination
// is table-testable without a Controller.
func (o PreapproveManagedOptions) allows(autopilot bool, kind ManagedWriteKind) bool {
	if !o.Enabled || !autopilot {
		return false
	}
	switch kind {
	case managedKindSkills:
		return o.Skills
	case managedKindHooks:
		return o.Hooks
	case managedKindStores:
		return o.Stores
	case managedKindBash:
		return o.BashEscape
	default:
		// Unclassified targets (config.toml and friends) are never covered:
		// the checkboxes promise exactly four classes, not "everything".
		return false
	}
}

// classifyManagedWrite maps a file-write target onto one of the four classes.
// Order matters: settings.json is a hook file wherever it sits, skills wins
// over the stores prefix (skills live inside the home too), and stores is the
// home directory itself — the same region the session-data guard protects.
func classifyManagedWrite(target string, o PreapproveManagedOptions) ManagedWriteKind {
	abs := filepath.Clean(target)
	// hooks: the exact boot-computed path when provided, plus the file name
	// everywhere — settings.json is a hook file in the Reasonix home and in a
	// project's .claude/ alike, and both are what the checkbox means.
	if o.HooksFile != "" && abs == filepath.Clean(o.HooksFile) {
		return managedKindHooks
	}
	if filepath.Base(abs) == "settings.json" {
		return managedKindHooks
	}
	if o.SkillsDir != "" && withinDir(abs, o.SkillsDir) {
		return managedKindSkills
	}
	// Not the checkboxes' business: the config files themselves. Checked
	// before the stores fallthrough so a home-rooted StoresDir can never
	// swallow config.toml or the credentials file.
	base := filepath.Base(abs)
	if base == "config.toml" || base == "config.json" || base == ".env" {
		return managedKindOther
	}
	// stores: any path inside a sessions/ directory (the home store and every
	// project's), or — when StoresDir is the home — anything left in it,
	// mirroring what the session-data guard protects.
	if hasPathSegment(abs, "sessions") {
		return managedKindStores
	}
	if o.StoresDir != "" && withinDir(abs, o.StoresDir) {
		return managedKindStores
	}
	return managedKindOther
}

// hasPathSegment reports whether any path element equals seg (Windows- and
// POSIX-safe; a plain Contains would also match "sessions-backup").
func hasPathSegment(path, seg string) bool {
	for _, part := range strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == seg {
			return true
		}
	}
	return false
}

// withinDir reports whether target is dir itself or nested under it, using
// path-segment comparison (never a raw prefix: /home/user/skills-old must not
// count as inside /home/user/skills).
func withinDir(target, dir string) bool {
	target = filepath.Clean(target)
	dir = filepath.Clean(dir)
	if target == dir {
		return true
	}
	rel, err := filepath.Rel(dir, target)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// auditPreapprove records every approval this feature skipped. An unattended
// run must stay reconstructable after the fact: which class bypassed, what
// subject, and the autopilot context it ran under. This is the task-231
// audit-trail requirement — the bypass never becomes silent.
func auditPreapprove(kind, subject string, autopilot bool) {
	slog.Info("preapproved without a human prompt (task 231)",
		"feature", "managed_paths_preapprove",
		"class", kind,
		"subject", subject,
		"autopilot", autopilot)
}
