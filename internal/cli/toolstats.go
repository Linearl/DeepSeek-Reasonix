package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"reasonix/internal/config"
)

// runToolStats implements `reasonix tool-stats` (task 227 phase 1): aggregate
// the per-session *.toolstats.json sidecars into one per-tool table. The
// sidecars only ever contain counters keyed by tool name, so the aggregation
// cannot leak arguments, output, or paths (privacy line).
func runToolStats(args []string) int {
	dir := ""
	if len(args) > 0 {
		dir = args[0]
	}
	if strings.TrimSpace(dir) == "" {
		dir = filepath.Join(config.MemoryUserDir(), "sessions")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tool-stats: read %s: %v\n", dir, err)
		return 2
	}

	type row struct {
		calls, hard, soft, sessions int
	}
	perTool := map[string]*row{}
	sessions := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".toolstats.json") {
			continue
		}
		blob, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		var stored struct {
			Tools map[string]struct {
				Calls      int `json:"calls"`
				HardErrors int `json:"hardErrors"`
				SoftErrors int `json:"softErrors"`
			} `json:"tools"`
		}
		if err := json.Unmarshal(blob, &stored); err != nil || len(stored.Tools) == 0 {
			continue
		}
		sessions++
		for tool, c := range stored.Tools {
			r := perTool[tool]
			if r == nil {
				r = &row{}
				perTool[tool] = r
			}
			r.calls += c.Calls
			r.hard += c.HardErrors
			r.soft += c.SoftErrors
		}
	}
	if sessions == 0 {
		fmt.Printf("no toolstats sidecars under %s\n", dir)
		return 0
	}

	names := make([]string, 0, len(perTool))
	for name := range perTool {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		a, b := perTool[names[i]], perTool[names[j]]
		if a.hard+a.soft != b.hard+b.soft {
			return a.hard+a.soft > b.hard+b.soft // most errors first
		}
		return names[i] < names[j]
	})

	w := io.Writer(os.Stdout)
	fmt.Fprintf(w, "tool error statistics — %d session(s) under %s\n\n", sessions, dir)
	fmt.Fprintf(w, "%-28s %8s %8s %8s %10s\n", "TOOL", "CALLS", "HARD", "SOFT", "ERR RATE")
	for _, name := range names {
		r := perTool[name]
		rate := "—"
		if r.calls > 0 {
			rate = fmt.Sprintf("%.1f%%", float64(r.hard+r.soft)/float64(r.calls)*100)
		}
		fmt.Fprintf(w, "%-28s %8d %8d %8d %10s\n", name, r.calls, r.hard, r.soft, rate)
	}
	return 0
}
