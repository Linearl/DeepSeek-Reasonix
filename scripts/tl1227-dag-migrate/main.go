// Command tl1227-dag-migrate migrates legacy schema-2 DAG session event logs
// to the schema-1 flat form (tl1227: DAG 读兼容退役第二阶段).
//
// It walks a Reasonix projects root for every *.events.jsonl sidecar (main,
// guardian and repairlog variants), classifies each the same way the reader's
// probe does, and either verifies (check) or migrates (apply) every schema-2
// log through agent.MigrateSessionDAGLog. The script is idempotent: migrated
// sessions report skipped-not-dag on re-runs, and check mode never writes.
//
// Usage:
//
//	go run ./scripts/tl1227-dag-migrate -mode check                    # 全库只读校验
//	go run ./scripts/tl1227-dag-migrate -mode apply                    # 全库迁移（跳过他方租约/损坏）
//	go run ./scripts/tl1227-dag-migrate -mode apply -session <path>    # 单会话抽样演练
//	go run ./scripts/tl1227-dag-migrate -mode check -out report.jsonl  # 逐会话报告落盘
//
// Each report line is one SessionDAGMigrationReport JSON object; the final
// line is a summary object with kind=summary.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"reasonix/internal/agent"
)

type summary struct {
	Kind        string `json:"kind"`
	Root        string `json:"root"`
	Mode        string `json:"mode"`
	Scanned     int    `json:"scanned"`
	DAG         int    `json:"dag"`
	Migrated    int    `json:"migrated"`
	Checked     int    `json:"checked"`
	Skipped     int    `json:"skipped"`
	Failed      int    `json:"failed"`
	ElapsedMs   int64  `json:"elapsed_ms"`
	DAGSessions []string `json:"dag_sessions,omitempty"`
}

func main() {
	root := flag.String("root", defaultProjectsRoot(), "Reasonix projects root to walk")
	mode := flag.String("mode", "check", "check (read-only) or apply")
	single := flag.String("session", "", "migrate one session transcript path instead of walking the root")
	out := flag.String("out", "", "optional JSONL report path (default stdout only)")
	flag.Parse()

	switch agent.SessionDAGMigrationMode(*mode) {
	case agent.SessionDAGMigrationCheck, agent.SessionDAGMigrationApply:
	default:
		fmt.Fprintf(os.Stderr, "unknown mode %q\n", *mode)
		os.Exit(2)
	}

	var reportFile *os.File
	if *out != "" {
		f, err := os.Create(*out)
		if err != nil {
			fmt.Fprintf(os.Stderr, "create report: %v\n", err)
			os.Exit(1)
		}
		defer f.Close()
		reportFile = f
	}

	writeReport := func(v any) {
		b, err := json.Marshal(v)
		if err != nil {
			return
		}
		fmt.Fprintln(os.Stdout, string(b))
		if reportFile != nil {
			fmt.Fprintln(reportFile, string(b))
		}
	}

	started := time.Now()
	ctx := context.Background()
	sum := summary{Kind: "summary", Root: *root, Mode: *mode}
	fail := false

	migrateOne := func(sessionPath string) {
		sum.Scanned++
		report, err := agent.MigrateSessionDAGLog(ctx, sessionPath, agent.SessionDAGMigrationMode(*mode))
		if report != nil {
			switch report.Status {
			case agent.SessionDAGMigrated:
				sum.Migrated++
			case agent.SessionDAGChecked:
				sum.Checked++
				sum.DAG++
				sum.DAGSessions = append(sum.DAGSessions, sessionPath)
			case agent.SessionDAGSkipNotDAG:
				// not DAG: not interesting for the tally beyond scanned
			default:
				sum.Skipped++
				sum.DAG++
				sum.DAGSessions = append(sum.DAGSessions, sessionPath)
			}
			if report.Status != agent.SessionDAGSkipNotDAG || *single != "" {
				writeReport(report)
			}
		}
		if err != nil {
			sum.Failed++
			fail = true
			writeReport(map[string]string{"kind": "error", "session": sessionPath, "error": err.Error()})
		}
	}

	if *single != "" {
		migrateOne(*single)
	} else {
		err := filepath.WalkDir(*root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, sessionEventsSuffix) {
				return nil
			}
			migrateOne(sessionTranscriptFor(path))
			return nil
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "walk: %v\n", err)
			os.Exit(1)
		}
	}

	sum.ElapsedMs = time.Since(started).Milliseconds()
	writeReport(sum)
	if fail {
		os.Exit(1)
	}
}

// sessionEventsSuffix matches every sidecar variant the reader probes: main,
// guardian and repairlog event logs all end in ".events.jsonl".
const sessionEventsSuffix = ".events.jsonl"

// sessionTranscriptFor maps an event-log path back to the transcript path the
// migration API expects (it re-derives the sidecars from it).
func sessionTranscriptFor(logPath string) string {
	return strings.TrimSuffix(logPath, sessionEventsSuffix) + ".jsonl"
}

func defaultProjectsRoot() string {
	appdata := os.Getenv("APPDATA")
	if appdata == "" {
		return "."
	}
	return filepath.Join(appdata, "reasonix", "projects")
}
