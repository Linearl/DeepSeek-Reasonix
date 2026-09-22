// Package claimcheck turns existence claims into structured verdicts
// (claim-hygiene L1, task 222).
//
// Background: a `find | head -N` truncation was once cited as proof that a
// workflow file did not exist; the false "missing" claim made it into a
// handoff document. This package removes the truncatable free-text stream:
// the only output is a verdict (CONFIRMED/REFUTED) with the full evidence
// trail (bases tried, hit counts, skipped entries).
//
// Scope guard (anti-fake-compliance): a verdict speaks ONLY to file/dir
// existence. "The file exists" never implies "the feature works". Callers
// must not widen the verdict into content-level or behavior-level claims.
//
// Semantics (aligned with the Python prototype, with its known defects fixed):
//   - claim=missing: any hit → REFUTED; no hit → CONFIRMED
//   - claim=exists:  any hit → CONFIRMED; no hit → REFUTED
//   - Uncollectable entries (permission errors, long paths) are COUNTED, not
//     silently swallowed. skipped > 0 forces uncertain=true — a clean verdict
//     is refused because a hit may hide among the skipped entries.
//   - Symlinks and junctions are NOT followed (filepath.WalkDir semantics);
//     a link itself counts as an existing entry, its target is not validated.
//   - Pattern matching is case-sensitive on every platform (path.Match).
package claimcheck

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Verdict is the only conclusion this package can produce.
type Verdict string

const (
	// Confirmed means the claim holds on the inspected scope.
	Confirmed Verdict = "CONFIRMED"
	// Refuted means the claim is contradicted by at least one hit.
	Refuted Verdict = "REFUTED"
)

// ScopeNote is the fixed range statement every result carries: callers must
// not widen an existence verdict into a content or behavior claim.
const ScopeNote = "existence-only: this verdict says nothing about file content or behavior"

// maxShown caps the example paths in the result; the verdict is computed from
// full counts and never from the shown slice.
const maxShown = 5

// skipDirs are traversal black holes excluded from pattern walks (aligned
// with the Python prototype). A pattern that ONLY exists under these is
// reported as no-hit, which is the honest answer for "is it part of the
// repository".
var skipDirs = map[string]bool{
	"node_modules": true,
	".git":         true,
	"__pycache__":  true,
}

// TriedEntry records one probe the checker actually performed.
type TriedEntry struct {
	Kind   string `json:"kind"` // "path" | "base"
	Path   string `json:"path"`
	Hit    bool   `json:"hit"`
	Usable bool   `json:"usable,omitempty"` // base dirs: directory existed
	Hits   int    `json:"hits,omitempty"`   // base dirs: total matches
}

// Result is the structured verdict — the ONLY output shape of a check.
type Result struct {
	Claim     string       `json:"claim"`
	Verdict   Verdict      `json:"verdict"`
	TotalHits int          `json:"totalHits"`
	Tried     []TriedEntry `json:"tried"`
	Shown     []string     `json:"shown"`
	Skipped   int          `json:"skipped"`
	Uncertain bool         `json:"uncertain"`
	Scope     string       `json:"scope"`
}

// ErrUsage describes an unusable invocation (no path, or pattern without base).
var ErrUsage = fmt.Errorf("claim-check: provide --path, or --pattern with at least one --base")

// Check evaluates the claim. claim is "missing" or "exists"; exactly one of
// path / (pattern + bases) must be provided. It never returns a bare error
// for inspection problems — unreadable entries land in Skipped and flip
// Uncertain — ErrUsage is the only error.
func Check(claim, path, pattern string, bases []string) (Result, error) {
	if claim != "missing" && claim != "exists" {
		return Result{}, fmt.Errorf("%w: --claim must be \"missing\" or \"exists\"", ErrUsage)
	}
	path = strings.TrimSpace(path)
	if path == "" && (strings.TrimSpace(pattern) == "" || len(bases) == 0) {
		return Result{}, ErrUsage
	}
	if path != "" && pattern != "" {
		return Result{}, fmt.Errorf("%w: --path and --pattern are mutually exclusive", ErrUsage)
	}

	res := Result{Claim: claim, Scope: ScopeNote, Shown: []string{}}
	if path != "" {
		res.checkPath(path)
	} else {
		res.checkPattern(pattern, bases)
	}
	res.Uncertain = res.Skipped > 0
	res.Verdict = decide(claim, res.TotalHits)
	return res, nil
}

func (r *Result) addHit(p string) {
	r.TotalHits++
	if len(r.Shown) < maxShown {
		r.Shown = append(r.Shown, p)
	}
}

func (r *Result) checkPath(path string) {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	// Lstat, not Stat: links and junctions count as existing entries without
	// validating (or following) their targets.
	_, err = os.Lstat(abs)
	hit := err == nil
	if err != nil && !os.IsNotExist(err) {
		// Inaccessible (permission, path length): the claim cannot be cleanly
		// verified on this path.
		r.Skipped++
	}
	r.Tried = append(r.Tried, TriedEntry{Kind: "path", Path: abs, Hit: hit})
	if hit {
		r.addHit(abs)
	}
}

func (r *Result) checkPattern(pattern string, bases []string) {
	for _, base := range bases {
		abs, err := filepath.Abs(base)
		if err != nil {
			abs = base
		}
		info, statErr := os.Lstat(abs)
		usable := statErr == nil && info.IsDir()
		entry := TriedEntry{Kind: "base", Path: abs, Usable: usable}
		if !usable {
			r.Tried = append(r.Tried, entry)
			continue
		}
		hits, shown, skipped := walkPattern(abs, pattern)
		entry.Hits = hits
		r.Tried = append(r.Tried, entry)
		r.TotalHits += hits
		r.Skipped += skipped
		for _, p := range shown {
			if len(r.Shown) < maxShown {
				r.Shown = append(r.Shown, p)
			}
		}
	}
}

// walkPattern counts case-sensitive matches of a base filename pattern under
// root, skipping black-hole dirs, and counting (not swallowing) entries that
// cannot be read.
func walkPattern(root, pattern string) (hits int, shown []string, skipped int) {
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			// Unreadable dir entry: count it and keep walking siblings.
			skipped++
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if p != root && skipDirs[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if ok, _ := path.Match(pattern, d.Name()); ok {
			hits++
			if len(shown) < maxShown {
				shown = append(shown, p)
			}
		}
		return nil
	})
	sort.Strings(shown)
	return hits, shown, skipped
}

func decide(claim string, hits int) Verdict {
	hit := hits > 0
	if claim == "missing" {
		if hit {
			return Refuted
		}
		return Confirmed
	}
	if hit {
		return Confirmed
	}
	return Refuted
}
