// memorykwtool — read-only keywords coverage auditor for the memory store
// (tl1399②).
//
// What it does: scans the live memory store (root + global + project dirs and
// every new-style project library under projects/<slug>/memory), reports the
// keywords coverage rate, and for every entry saved without keywords prints a
// machine-extracted candidate skeleton (name words, in-body identifiers, and
// the cross-language gap the aliases should fill).
//
// What it deliberately does NOT do (non-goals):
//   - It never writes to the memory store. There is no -apply flag and no
//     write path at all; backfilling existing memories stays a human decision
//     executed through the normal remember/memory tooling.
//   - It does not invent synonyms or translations. The skeleton is limited to
//     words already derivable from the entry (name segments, body
//     identifiers); semantic aliases need a human or a model, so the JSON
//     mode exists to feed that step, not to replace it.
//
// The skeleton alone cannot raise ranking quality — anything it extracts is
// already part of the search text. Its value is (a) turning coverage into a
// measurable number, and (b) handing the semantic-fill step a prepared work
// list.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"reasonix/internal/memory"
)

type libStats struct {
	Dir          string  `json:"dir"`
	Entries      int     `json:"entries"`
	WithKeywords int     `json:"with_keywords"`
	Coverage     float64 `json:"coverage"`
}

type candidate struct {
	Lib         string   `json:"lib"`
	Name        string   `json:"name"`
	Title       string   `json:"title,omitempty"`
	Desc        string   `json:"description,omitempty"`
	LangGap     string   `json:"lang_gap"`
	NameWords   []string `json:"name_words"`
	Idents      []string `json:"body_identifiers"`
	HasTriggers bool     `json:"has_triggers"`
}

type report struct {
	Libs       []libStats  `json:"libs"`
	Total      int         `json:"total_entries"`
	WithKW     int         `json:"with_keywords"`
	Coverage   float64     `json:"coverage"`
	Missing    int         `json:"missing"`
	Candidates []candidate `json:"candidates"`
}

var (
	identBacktick = regexp.MustCompile("`([^`\n]+)`")
	identCamel    = regexp.MustCompile(`[A-Z][a-z0-9]+(?:[A-Z][a-z0-9]+)+`)
	identSnake    = regexp.MustCompile(`[a-z][a-z0-9]*(?:_[a-z0-9]+)+`)
)

func cjkRatio(s string) float64 {
	if s == "" {
		return 0
	}
	total, cjk := 0, 0
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			total++
			if unicode.In(r, unicode.Han) {
				cjk++
			}
		}
	}
	if total == 0 {
		return 0
	}
	return float64(cjk) / float64(total)
}

func nameWords(name string) []string {
	fields := strings.FieldsFunc(name, func(r rune) bool {
		return r == '-' || r == '_' || r == '.' || r == '/' || unicode.IsSpace(r)
	})
	out := make([]string, 0, len(fields))
	seen := map[string]bool{}
	for _, f := range fields {
		low := strings.ToLower(f)
		if low == "" || seen[low] || len([]rune(low)) < 2 {
			continue
		}
		seen[low] = true
		out = append(out, low)
	}
	return out
}

func bodyIdents(body string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] || len(s) > 60 {
			return
		}
		seen[s] = true
		out = append(out, s)
	}
	for _, m := range identBacktick.FindAllStringSubmatch(body, -1) {
		add(m[1])
	}
	for _, m := range identCamel.FindAllString(body, -1) {
		add(m)
	}
	for _, m := range identSnake.FindAllString(body, -1) {
		add(m)
	}
	if len(out) > 8 {
		out = out[:8]
	}
	return out
}

func main() {
	userDir := flag.String("user-dir", "", "reasonix user dir (default: os.UserConfigDir()/reasonix)")
	preview := flag.Int("preview", 5, "how many missing-entry skeletons to print in text mode")
	asJSON := flag.Bool("json", false, "emit the full candidate list as JSON (for a model/human to fill aliases)")
	flag.Parse()

	root := *userDir
	if root == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "user config dir: %v\n", err)
			os.Exit(1)
		}
		root = filepath.Join(base, "reasonix")
	}

	type lib struct {
		label string
		dir   string
	}
	libs := []lib{
		{"memory(root)", filepath.Join(root, "memory")},
		{"memory/global", filepath.Join(root, "memory", "global")},
		{"memory/project", filepath.Join(root, "memory", "project")},
	}
	if projectRoots, err := os.ReadDir(filepath.Join(root, "projects")); err == nil {
		names := make([]string, 0, len(projectRoots))
		for _, e := range projectRoots {
			if e.IsDir() {
				names = append(names, e.Name())
			}
		}
		sort.Strings(names)
		for _, name := range names {
			libs = append(libs, lib{"projects/" + name, filepath.Join(root, "projects", name, "memory")})
		}
	}

	rep := report{}
	for _, l := range libs {
		store := memory.Store{Dir: l.dir}
		entries := store.ListAll()
		if len(entries) == 0 {
			continue
		}
		st := libStats{Dir: l.label, Entries: len(entries)}
		for _, m := range entries {
			if strings.TrimSpace(m.Keywords) != "" {
				st.WithKeywords++
				rep.WithKW++
			} else {
				gap := "latin entry → fill Chinese aliases"
				if cjkRatio(strings.Join([]string{m.Name, m.Title, m.Description}, " ")) >= 0.5 {
					gap = "CJK entry → fill English aliases"
				}
				rep.Candidates = append(rep.Candidates, candidate{
					Lib: l.label, Name: m.Name, Title: m.Title, Desc: m.Description,
					LangGap: gap, NameWords: nameWords(m.Name), Idents: bodyIdents(m.Body),
					HasTriggers: strings.TrimSpace(m.Triggers) != "",
				})
			}
			rep.Total++
		}
		st.Coverage = 0
		if st.Entries > 0 {
			st.Coverage = float64(st.WithKeywords) / float64(st.Entries)
		}
		rep.Libs = append(rep.Libs, st)
	}
	rep.Missing = rep.Total - rep.WithKW
	if rep.Total > 0 {
		rep.Coverage = float64(rep.WithKW) / float64(rep.Total)
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		if err := enc.Encode(rep); err != nil {
			fmt.Fprintf(os.Stderr, "encode: %v\n", err)
			os.Exit(1)
		}
		return
	}

	fmt.Printf("keywords coverage (read-only audit of %s)\n", root)
	for _, s := range rep.Libs {
		fmt.Printf("  %-70s %4d entries, %3d with keywords, coverage %.1f%%\n", s.Dir, s.Entries, s.WithKeywords, s.Coverage*100)
	}
	fmt.Printf("TOTAL: %d entries, %d with keywords, coverage %.1f%% (%d missing)\n", rep.Total, rep.WithKW, rep.Coverage*100, rep.Missing)
	fmt.Println()
	fmt.Printf("first %d entries saved without keywords (skeleton only — semantic aliases need a human or a model):\n", *preview)
	limit := *preview
	if limit > len(rep.Candidates) {
		limit = len(rep.Candidates)
	}
	for _, c := range rep.Candidates[:limit] {
		fmt.Printf("- [%s] %s\n", c.Lib, c.Name)
		if c.Title != "" {
			fmt.Printf("    title: %s\n", c.Title)
		}
		if c.Desc != "" {
			fmt.Printf("    desc: %.120s\n", c.Desc)
		}
		fmt.Printf("    gap: %s\n", c.LangGap)
		if len(c.NameWords) > 0 {
			fmt.Printf("    name words: %s\n", strings.Join(c.NameWords, ", "))
		}
		if len(c.Idents) > 0 {
			fmt.Printf("    body identifiers: %s\n", strings.Join(c.Idents, ", "))
		}
	}
	if !*asJSON && len(rep.Candidates) > limit {
		fmt.Printf("... %d more — rerun with -json for the full list\n", len(rep.Candidates)-limit)
	}
	fmt.Println("\nDry-run only: nothing was written. To backfill, edit entries via the remember/memory tools by hand or with model help.")
}
