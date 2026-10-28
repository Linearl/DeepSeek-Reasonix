package cli

import (
	"flag"
	"fmt"
	"io"
	"os"

	"reasonix/internal/agent"
	"reasonix/internal/config"
)

// runDagSessions implements `reasonix dag-sessions` (tl-1220 counter ②): the
// stored-DAG-session census over every session directory this machine keeps —
// the tl1173 §4.3 read-compatibility retirement counter (发起评估条件：存量 DAG
// 会话数 <10 或距第一步合入 ≥3 个月). Read-only: the census probes event-log
// heads and never writes, materializes, or repairs anything.
//
// Output mirrors tool-stats (task 227): a text table by default, the shared
// verdict envelope with --json.
func runDagSessions(args []string) int {
	fs := flag.NewFlagSet("dag-sessions", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "print the census as a structured verdict envelope (schema_version/tool/verdict)")
	if code, ok := parseCommandFlags(fs, args); !ok {
		return code
	}
	return renderDagSessions(os.Stdout, dagSessionCensusDirs(), *jsonOut)
}

// dagSessionCensusDirs assembles the whole library: the support-root session
// stores (v3 + experimental v4) plus every per-workspace session directory.
// Discovery must not depend on which projects are open (same contract as
// config.AllProjectSessionDirs).
func dagSessionCensusDirs() []string {
	dirs := make([]string, 0, 8)
	if d := config.SessionDir(); d != "" {
		dirs = append(dirs, d)
	}
	if d := config.SessionStoreDir(); d != "" {
		dirs = append(dirs, d)
	}
	dirs = append(dirs, config.AllProjectSessionDirs()...)
	return dirs
}

// renderDagSessions runs the census over dirs and prints the reading.
func renderDagSessions(w io.Writer, dirs []string, jsonOut bool) int {
	census := agent.CensusStoredDAGSessions(dirs)

	if jsonOut {
		verdict := VerdictOK
		if census.EventLogSessions == 0 {
			verdict = VerdictEmpty
		}
		code, err := writeVerdictEnvelope(w, "dag-sessions", verdict,
			fmt.Sprintf("%d session(s) with event logs under %d dir(s); dag=%d",
				census.EventLogSessions, census.ScannedDirs, census.DAGSessions),
			census)
		if err != nil {
			fmt.Fprintf(os.Stderr, "dag-sessions: encode: %v\n", err)
			return verdictExitRefuted
		}
		return code
	}

	fmt.Fprintf(w, "stored DAG session census — %d session(s) with event logs under %d dir(s)\n\n",
		census.EventLogSessions, census.ScannedDirs)
	if census.DirErrors > 0 {
		fmt.Fprintf(w, "dir read errors: %d\n", census.DirErrors)
	}
	fmt.Fprintf(w, "%-24s %10s\n", "FIELD", "VALUE")
	fmt.Fprintf(w, "%-24s %10d   <- tl1173 §4.3 retirement counter\n", "dag (probe.dag hit)", census.DAGSessions)
	fmt.Fprintf(w, "%-24s %10d\n", "native (schema-1)", census.NativeSessions)
	fmt.Fprintf(w, "%-24s %10d\n", "other/unreadable", census.OtherSessions)
	fmt.Fprintf(w, "%-24s %10d\n", "archived-only (zst)", census.ArchivedOnly)
	fmt.Fprintf(w, "%-24s %10d\n", "dag archived-only", census.DAGArchivedOnly)
	fmt.Fprintf(w, "%-24s %10d\n", "total log bytes", census.TotalLogBytes)
	return 0
}
