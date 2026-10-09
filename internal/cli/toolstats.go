package cli

import (
	"encoding/json"
	"flag"
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
//
// 任务 229 G1: --json emits the shared verdict contract envelope (see
// output_contract.go) with the per-tool rows as payload; text mode and exit
// codes are unchanged.
func runToolStats(args []string) int {
	fs := flag.NewFlagSet("tool-stats", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "print the aggregate as a structured verdict envelope (schema_version/tool/verdict)")
	if code, ok := parseCommandFlags(fs, args); !ok {
		return code
	}
	dir := ""
	if fs.NArg() > 0 {
		dir = fs.Arg(0)
	}
	if strings.TrimSpace(dir) == "" {
		dir = filepath.Join(config.MemoryUserDir(), "sessions")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tool-stats: read %s: %v\n", dir, err)
		return verdictExitUsage
	}

	type row struct {
		calls, hard, soft int
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

	if *jsonOut {
		type toolRow struct {
			Tool       string  `json:"tool"`
			Calls      int     `json:"calls"`
			HardErrors int     `json:"hard_errors"`
			SoftErrors int     `json:"soft_errors"`
			ErrorRate  float64 `json:"error_rate"`
		}
		rows := make([]toolRow, 0, len(names))
		for _, name := range names {
			r := perTool[name]
			rate := 0.0
			if r.calls > 0 {
				rate = float64(r.hard+r.soft) / float64(r.calls) * 100
			}
			rows = append(rows, toolRow{Tool: name, Calls: r.calls, HardErrors: r.hard, SoftErrors: r.soft, ErrorRate: rate})
		}
		verdict := VerdictOK
		if sessions == 0 {
			verdict = VerdictEmpty
		}
		code, err := writeVerdictEnvelope(os.Stdout, "tool-stats", verdict,
			fmt.Sprintf("%d session(s) under %s", sessions, dir), rows)
		if err != nil {
			fmt.Fprintf(os.Stderr, "tool-stats: encode: %v\n", err)
			return verdictExitRefuted
		}
		return code
	}

	w := io.Writer(os.Stdout)
	if sessions == 0 {
		fmt.Printf("no toolstats sidecars under %s\n", dir)
		return 0
	}
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
