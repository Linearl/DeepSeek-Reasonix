// Package rules loads path-scoped rule documents and assembles them into one
// prompt section. Rules are plain markdown files with optional frontmatter:
//
//	---
//	paths: ["webapp/**/*.ts", "*.go"]
//	---
//	Rule body...
//
// A rule without `paths` always applies. Project rules override user rules with
// the same file name, so a repository can specialise a shared rule. This is the
// local implementation of #9886 (issue #9875 measured Claude's advantage on the
// same model to prompt structure rather than capability).
package rules

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// MaxSectionBytes caps the assembled section. A runaway rules tree must not
// crowd out the task itself; the caller surfaces the warning to the user.
const MaxSectionBytes = 64 * 1024

// Rule is one loaded document.
type Rule struct {
	// Source is the file it came from, for diagnostics.
	Source string
	// Name is the file name relative to its rules root; user rules with the
	// same Name are shadowed by project rules.
	Name string
	// Paths are the glob patterns that make the rule apply. Empty means always.
	Paths []string
	// Body is the markdown content with the frontmatter removed.
	Body string
}

// Options selects the roots to load from.
type Options struct {
	// ProjectRoot is the workspace root; "" skips project rules.
	ProjectRoot string
	// UserRoot is typically <config dir>/rules; "" skips user rules.
	UserRoot string
}

// Load reads every .md file under the two roots. Missing roots are not an
// error: most projects have no rules at all. Warnings describe recoverable
// problems (unreadable file, malformed frontmatter) so the caller can surface
// them without failing the run.
func Load(opts Options) ([]Rule, []string, error) {
	var warnings []string
	byName := map[string]Rule{}

	load := func(root string, project bool) error {
		if strings.TrimSpace(root) == "" {
			return nil
		}
		info, err := os.Stat(root)
		if err != nil || !info.IsDir() {
			return nil
		}
		return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("rules: cannot read %s: %v", path, err))
				return nil
			}
			if d.IsDir() || !strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
				return nil
			}
			raw, readErr := os.ReadFile(path)
			if readErr != nil {
				warnings = append(warnings, fmt.Sprintf("rules: cannot read %s: %v", path, readErr))
				return nil
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				rel = d.Name()
			}
			rel = filepath.ToSlash(rel)
			paths, body, fmErr := splitFrontmatter(string(raw))
			if fmErr != nil {
				warnings = append(warnings, fmt.Sprintf("rules: %s: %v", rel, fmErr))
			}
			rule := Rule{Source: path, Name: rel, Paths: paths, Body: strings.TrimSpace(body)}
			if rule.Body == "" {
				warnings = append(warnings, fmt.Sprintf("rules: %s is empty and was skipped", rel))
				return nil
			}
			// Project rules win over user rules with the same relative name.
			if project {
				byName[rel] = rule
			} else if _, taken := byName[rel]; !taken {
				byName[rel] = rule
			}
			return nil
		})
	}

	// User first so project entries can shadow them.
	if err := load(opts.UserRoot, false); err != nil {
		return nil, warnings, err
	}
	if err := load(filepath.Join(opts.ProjectRoot, ".reasonix", "rules"), true); err != nil {
		return nil, warnings, err
	}

	out := make([]Rule, 0, len(byName))
	for _, rule := range byName {
		out = append(out, rule)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, warnings, nil
}

// splitFrontmatter separates an optional `---` block from the body. Only the
// `paths:` key is interpreted; unknown keys are ignored so a rule file copied
// from another tool still loads.
func splitFrontmatter(raw string) ([]string, string, error) {
	trimmed := strings.TrimLeft(raw, "\uFEFF \t\r\n")
	if !strings.HasPrefix(trimmed, "---") {
		return nil, raw, nil
	}
	rest := strings.TrimPrefix(trimmed, "---")
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return nil, raw, errors.New("frontmatter opened with --- but never closed; treating the whole file as the body")
	}
	header := rest[:end]
	body := rest[end+len("\n---"):]
	paths, err := parsePaths(header)
	return paths, body, err
}

func parsePaths(header string) ([]string, error) {
	for _, line := range strings.Split(header, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "paths:") {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(line, "paths:"))
		value = strings.Trim(value, "[]")
		var out []string
		for _, part := range strings.Split(value, ",") {
			item := strings.Trim(strings.TrimSpace(part), "\"'")
			if item != "" {
				out = append(out, item)
			}
		}
		return out, nil
	}
	return nil, nil
}

// Applies reports whether the rule should be included for a workspace whose
// file list is files (workspace-relative, slash-separated).
func (r Rule) Applies(files []string) bool {
	if len(r.Paths) == 0 {
		return true
	}
	for _, pattern := range r.Paths {
		for _, file := range files {
			if MatchGlob(pattern, file) {
				return true
			}
		}
	}
	return false
}

// MatchGlob is a deliberately small glob matcher: `**` spans directories, `*`
// and `?` stay within one path segment. It documents its divergence from
// Claude's matcher in reasonix-guide rather than pretending to be identical.
func MatchGlob(pattern, path string) bool {
	pattern = strings.TrimSpace(strings.ReplaceAll(pattern, "\\", "/"))
	path = strings.TrimSpace(strings.ReplaceAll(path, "\\", "/"))
	if pattern == "" || path == "" {
		return false
	}
	return matchSegments(strings.Split(pattern, "/"), strings.Split(path, "/"))
}

func matchSegments(pat, seg []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			if len(pat) == 1 {
				return true
			}
			for i := 0; i <= len(seg); i++ {
				if matchSegments(pat[1:], seg[i:]) {
					return true
				}
			}
			return false
		}
		if len(seg) == 0 || !matchSegment(pat[0], seg[0]) {
			return false
		}
		pat, seg = pat[1:], seg[1:]
	}
	return len(seg) == 0
}

func matchSegment(pattern, segment string) bool {
	if pattern == "*" {
		return true
	}
	// Iterative wildcard match for * and ? within one segment.
	var (
		p, s     int
		star, ss int
	)
	star = -1
	for s < len(segment) {
		switch {
		case p < len(pattern) && (pattern[p] == '?' || pattern[p] == segment[s]):
			p++
			s++
		case p < len(pattern) && pattern[p] == '*':
			star, ss = p, s
			p++
		case star >= 0:
			p = star + 1
			ss++
			s = ss
		default:
			return false
		}
	}
	for p < len(pattern) && pattern[p] == '*' {
		p++
	}
	return p == len(pattern)
}

// Assemble renders the prompt section. It returns "" when nothing applies, so
// the caller can skip the section entirely and keep the prefix cache stable.
func Assemble(loaded []Rule, files []string) (section string, used []Rule, truncated bool) {
	var b strings.Builder
	for _, rule := range loaded {
		if !rule.Applies(files) {
			continue
		}
		entry := fmt.Sprintf("### %s\n%s\n\n", rule.Name, rule.Body)
		if b.Len()+len(entry) > MaxSectionBytes {
			truncated = true
			break
		}
		b.WriteString(entry)
		used = append(used, rule)
	}
	if len(used) == 0 {
		// Nothing fit. Report the truncation even though the section is empty,
		// so an oversized rules tree is visible instead of silently ignored.
		return "", nil, truncated
	}
	return "# Rules\n\n" + strings.TrimRight(b.String(), "\n"), used, truncated
}
