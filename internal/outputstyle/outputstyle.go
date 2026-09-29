// Package outputstyle adds a selectable "output style" — a block of persona /
// tone instructions appended to (or replacing) the system prompt — so the user
// can shift how the agent communicates without rewriting the system prompt. It
// mirrors the skill/command loaders: built-in styles plus markdown files with
// frontmatter discovered under the project and home convention dirs.
package outputstyle

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	fileencoding "reasonix/internal/fileutil/encoding"
	"reasonix/internal/frontmatter"
)

// OutputStyle is one selectable persona. Body is appended to the system prompt
// (KeepCoding true) or used as the whole prompt (false). Name is the selector
// (case-insensitive); Builtin marks the baked-in ones for listing.
type OutputStyle struct {
	Name        string
	Description string
	Body        string
	KeepCoding  bool // true: append to the coding system prompt; false: replace it
	Builtin     bool
	Path        string // file it loaded from ("" for built-ins)
}

// builtins are the always-available styles. Default ("" / "default") is absent
// on purpose — no style means the unmodified system prompt.
var builtins = []OutputStyle{
	{
		Name:        "explanatory",
		Description: "Explain non-obvious implementation choices as you go",
		KeepCoding:  true,
		Builtin:     true,
		Body: "Communication style — Explanatory: as you work, surface the reasoning behind " +
			"non-obvious choices. After a substantive change, add a short \"## Insight\" note " +
			"covering the key trade-off or why an alternative was rejected. Teach the why, not just the what; keep it brief.",
	},
	{
		Name:        "learning",
		Description: "Collaborate and leave TODO(human) stubs for the user to complete",
		KeepCoding:  true,
		Builtin:     true,
		Body: "Communication style — Learning: work collaboratively rather than doing everything. " +
			"When a meaningful implementation decision comes up, pause and ask the user to make the call. " +
			"For the most instructive pieces, write the surrounding code but leave a small, clearly-marked " +
			"`TODO(human)` stub with a one-line description for the user to implement themselves.",
	},
	{
		Name:        "concise",
		Description: "Terse replies: minimal prose, code and bullets only",
		KeepCoding:  true,
		Builtin:     true,
		Body: "Communication style — Concise: keep replies terse. No preamble or postamble, no restating " +
			"the request. Prefer code and short bullet points over paragraphs; answer in the fewest words that are still clear.",
	},
}

// Dirs returns the output-style search directories in load order (later wins),
// mirroring command/skill discovery: home convention dirs, then project ones.
// Home convention dirs are skipped when REASONIX_HOME is set (isolated runtime).
func Dirs() []string {
	var dirs []string
	if os.Getenv("REASONIX_HOME") == "" {
		if home, err := os.UserHomeDir(); err == nil {
			for _, v := range slices.Backward(conventionDirs) {
				dirs = append(dirs, filepath.Join(home, v, "output-styles"))
			}
		}
	}
	for _, v := range slices.Backward(conventionDirs) {
		dirs = append(dirs, filepath.Join(".", v, "output-styles"))
	}
	return dirs
}

// conventionDirs mirrors config.ConventionDirs (kept local to avoid an import
// cycle; config imports nothing from here, but this package stays dependency-light).
var conventionDirs = []string{".reasonix", ".agents", ".agent", ".claude"}

// Issue reports one style file that had to be skipped. List drops such files
// without a word — the boot path must not fail on a stray file — but a UI
// selector needs the same skip list surfaced, because a file the user just
// wrote that never loads must be a visible error, not a silent no-op (task
// 385a).
type Issue struct {
	Path   string // file that could not be loaded
	Name   string // filename stem — the name the file would have had
	Reason string // human-readable cause
}

// List returns every available style — built-ins plus the markdown files under
// dirs — deduped by lowercased name, with custom files overriding built-ins.
// Sorted by name. Unloadable files are skipped (see ListReport for why).
func List(dirs []string) []OutputStyle {
	styles, _ := collect(dirs)
	return styles
}

// ListReport is List plus every file List had to skip, so callers can show
// "your .md did not load, here is why" instead of silently hiding it. Built-in
// styles never appear as issues.
func ListReport(dirs []string) ([]OutputStyle, []Issue) {
	return collect(dirs)
}

func collect(dirs []string) ([]OutputStyle, []Issue) {
	byName := map[string]OutputStyle{}
	for _, b := range builtins {
		byName[strings.ToLower(b.Name)] = b
	}
	var issues []Issue
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			path := filepath.Join(dir, e.Name())
			st, reason, ok := parseFile(path)
			if !ok {
				issues = append(issues, Issue{
					Path:   path,
					Name:   strings.TrimSuffix(e.Name(), ".md"),
					Reason: reason,
				})
				continue
			}
			byName[strings.ToLower(st.Name)] = st
		}
	}
	out := make([]OutputStyle, 0, len(byName))
	for _, st := range byName {
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	sort.Slice(issues, func(i, j int) bool { return issues[i].Path < issues[j].Path })
	return out, issues
}

// Resolve finds the style named name (case-insensitive) among dirs + built-ins.
// An empty or "default" name returns ok=false (no style — leave the prompt as-is).
func Resolve(name string, dirs []string) (OutputStyle, bool) {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" || n == "default" {
		return OutputStyle{}, false
	}
	for _, st := range List(dirs) {
		if strings.ToLower(st.Name) == n {
			return st, true
		}
	}
	return OutputStyle{}, false
}

// Apply folds a style into a base system prompt: appended when KeepCoding is set,
// otherwise the style replaces the prompt (a pure persona). A style with an empty
// body leaves the base untouched.
func Apply(base string, st OutputStyle) string {
	if strings.TrimSpace(st.Body) == "" {
		return base
	}
	if !st.KeepCoding {
		return st.Body
	}
	if strings.TrimSpace(base) == "" {
		return st.Body
	}
	return base + "\n\n" + st.Body
}

// parseFile loads one <name>.md output-style file. The name is the filename
// stem; frontmatter supplies description and keep-coding-instructions; the body
// is the prompt text. reason is non-empty exactly when ok is false, and always
// says why the file did not load.
func parseFile(path string) (OutputStyle, string, bool) {
	b, err := fileencoding.ReadFileUTF8(path)
	if err != nil {
		return OutputStyle{}, "read failed: " + err.Error(), false
	}
	content := string(b)
	if unclosedFrontmatterFence(content) {
		return OutputStyle{}, "frontmatter fence opened but never closed", false
	}
	// frontmatter.Split is deliberately permissive: a malformed YAML block is
	// dropped without a word, which would load the style with its metadata
	// (including keep-coding-instructions) silently lost. Validate first so a
	// broken frontmatter is reported instead of half-loading.
	if _, err := frontmatter.Decode(content, new(map[string]any), frontmatter.DecodeOptions{}); err != nil {
		return OutputStyle{}, "invalid frontmatter: " + err.Error(), false
	}
	meta, body := frontmatter.Split(content)
	name := meta["name"]
	if name == "" {
		name = strings.TrimSuffix(filepath.Base(path), ".md")
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return OutputStyle{}, "empty body: the file has no prompt text", false
	}
	keep := true // default: augment the coding prompt rather than replace it
	if v, ok := meta["keep-coding-instructions"]; ok {
		keep = !isFalse(v)
	}
	return OutputStyle{
		Name:        name,
		Description: meta["description"],
		Body:        body,
		KeepCoding:  keep,
		Path:        path,
	}, "", true
}

// unclosedFrontmatterFence reports a file that opens a --- frontmatter fence
// and never closes it. frontmatter.Split treats such a file as body-only (no
// partial parse), which would silently swallow the whole file as prompt text.
func unclosedFrontmatterFence(s string) bool {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	lines := strings.Split(s, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return false
	}
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "---" {
			return false
		}
	}
	return true
}

func isFalse(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "false", "no", "0", "off":
		return true
	}
	return false
}

// DescribeList renders the available styles as a short listing for /output-style.
func DescribeList(styles []OutputStyle, active string) string {
	var b strings.Builder
	for _, st := range styles {
		marker := "  "
		if strings.EqualFold(st.Name, active) {
			marker = "* "
		}
		scope := "builtin"
		if !st.Builtin {
			scope = "custom"
		}
		fmt.Fprintf(&b, "%s%s (%s) — %s\n", marker, st.Name, scope, st.Description)
	}
	return strings.TrimRight(b.String(), "\n")
}
