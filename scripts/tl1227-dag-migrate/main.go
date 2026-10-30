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
//	go run ./scripts/tl1227-dag-migrate -mode apply -only <substr>     # 只处理路径含该子串的会话
//
// Output contract: stdout carries nothing but the JSONL report (one object per
// handled session, then a kind=summary line) so it stays pipe/assert friendly.
// Every human-facing progress line goes to stderr, and stays pure ASCII because
// a Chinese Windows console is GBK and cannot print arbitrary UTF-8.
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
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"reasonix/internal/agent"
)

type summary struct {
	Kind        string `json:"kind"`
	Root        string `json:"root"`
	Mode        string `json:"mode"`
	Total       int    `json:"total"`
	Scanned     int    `json:"scanned"`
	DAG         int    `json:"dag"`
	Migrated    int    `json:"migrated"`
	Checked     int    `json:"checked"`
	Skipped     int    `json:"skipped"`
	Failed      int    `json:"failed"`
	Interrupted bool   `json:"interrupted,omitempty"`
	ElapsedMs   int64  `json:"elapsed_ms"`
	StartedAt   string `json:"started_at,omitempty"`
	FinishedAt  string `json:"finished_at,omitempty"`

	// SkipReasons breaks Skipped down by status so a run that silently left
	// work behind (lease held / damaged / oversize) is visible in one glance.
	SkipReasons map[string]int `json:"skip_reasons,omitempty"`
	// SnapshotsExcluded counts self-heal *.pre-repair-* snapshot trees that
	// were deliberately left untouched: they are rollback evidence, never
	// loaded as sessions, so rewriting them would edit the only copy worth
	// keeping. -include-backups opts in.
	SnapshotsExcluded int `json:"snapshots_excluded,omitempty"`
	// DagBackups lists the schema-2 originals preserved by apply.
	DagBackups     []string `json:"dag_backups,omitempty"`
	DAGSessions    []string `json:"dag_sessions,omitempty"`
	FailedSessions []string `json:"failed_sessions,omitempty"`
	WalkWarnings   []string `json:"walk_warnings,omitempty"`
}

func main() {
	// main only forwards the exit code: os.Exit skips deferred calls, so the
	// report file must be closed inside run().
	os.Exit(run())
}

func run() int {
	root := flag.String("root", defaultProjectsRoot(), "Reasonix projects root to walk")
	mode := flag.String("mode", "check", "check (read-only) or apply")
	single := flag.String("session", "", "migrate one session transcript path instead of walking the root")
	out := flag.String("out", "", "optional JSONL report path (default stdout only)")
	only := flag.String("only", "", "only handle sessions whose path contains this substring")
	force := flag.Bool("force", false, "apply even when a reasonix process looks alive")
	verbose := flag.Bool("v", false, "also report sessions that are already flat (skip-not-dag)")
	quiet := flag.Bool("quiet", false, "suppress every stderr progress line (stdout JSONL only)")
	every := flag.Int("progress-every", 25, "print a progress line every N untouched sessions")
	includeBackups := flag.Bool("include-backups", false, "also migrate self-heal *.pre-repair-* snapshot directories")
	flag.Parse()

	migrationMode := agent.SessionDAGMigrationMode(*mode)
	switch migrationMode {
	case agent.SessionDAGMigrationCheck, agent.SessionDAGMigrationApply:
	default:
		fmt.Fprintf(os.Stderr, "unknown mode %q (want check or apply)\n", *mode)
		return 2
	}

	logf := func(format string, a ...any) {
		if *quiet {
			return
		}
		fmt.Fprintf(os.Stderr, format+"\n", a...)
	}

	// --- guard 1: never rewrite logs under a live app ---------------------
	// The session API already skips sessions whose lease another runtime holds,
	// but a live desktop can also append between our probe and our rename, so
	// refuse outright instead of racing it.
	if migrationMode == agent.SessionDAGMigrationApply && !*force {
		if name, alive := reasonixRunning(); alive {
			logf("")
			logf("REFUSED: %s is running.", name)
			logf("Quit Reasonix completely (tray icon included, plus any `reasonix serve`),")
			logf("then re-run. Migrating while it runs can race with a concurrent save")
			logf("and lose messages. Pass -force only if you are certain nothing writes.")
			logf("")
			return 3
		}
	}

	// --- guard 2: Ctrl+C stops the batch cleanly --------------------------
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// --- report writer ---------------------------------------------------
	var reportFile *os.File
	if *out != "" {
		f, err := os.Create(*out)
		if err != nil {
			logf("FATAL create report %s: %v", *out, err)
			return 1
		}
		reportFile = f
		defer func() {
			if err := f.Close(); err != nil {
				logf("WARN closing report %s: %v", *out, err)
			}
		}()
	}

	writeReport := func(v any) {
		b, err := json.Marshal(v)
		if err != nil {
			logf("WARN marshal report object: %v", err)
			return
		}
		line := string(b)
		if _, err := fmt.Fprintln(os.Stdout, line); err != nil {
			logf("WARN write stdout: %v", err)
		}
		if reportFile != nil {
			if _, err := fmt.Fprintln(reportFile, line); err != nil {
				logf("WARN write report %s: %v", *out, err)
			}
		}
	}

	// --- target discovery ------------------------------------------------
	started := time.Now()
	sum := summary{
		Kind:        "summary",
		Root:        *root,
		Mode:        *mode,
		SkipReasons: map[string]int{},
		StartedAt:   started.Format(time.RFC3339),
	}

	var targets []string
	if *single != "" {
		// An explicit path wins over every classification rule: the caller
		// named the file, including a snapshot copy on purpose.
		targets = []string{*single}
	} else {
		var backups []string
		var err error
		targets, backups, sum.WalkWarnings, err = collectTargets(*root, *only)
		if err != nil {
			logf("FATAL scan %s: %v", *root, err)
			return 1
		}
		for _, w := range sum.WalkWarnings {
			logf("WARN walk: %s", w)
		}
		if *includeBackups {
			targets = append(targets, backups...)
		} else {
			sum.SnapshotsExcluded = len(backups)
		}
	}
	sum.Total = len(targets)
	if len(targets) == 0 {
		logf("nothing to do: no *.events.jsonl under %s (filter %q)", *root, *only)
		sum.ElapsedMs = time.Since(started).Milliseconds()
		sum.FinishedAt = time.Now().Format(time.RFC3339)
		writeReport(sum)
		return 0
	}

	logf("tl1227 dag-migrate: mode=%s targets=%d root=%s", *mode, sum.Total, *root)
	if migrationMode == agent.SessionDAGMigrationApply {
		logf("rewriting logs; every migrated session keeps its schema-2 bytes at *.dagbak-tl1227")
	}

	prog := &printer{total: len(targets), every: *every, start: started, quiet: *quiet}
	interrupted := false

	for _, sessionPath := range targets {
		if ctx.Err() != nil {
			interrupted = true
			logf("")
			logf("INTERRUPTED by user after %d/%d sessions", prog.done, prog.total)
			break
		}
		sum.Scanned++
		label := sessionLabel(sessionPath)

		report, err := migrateOne(ctx, sessionPath, migrationMode)
		if err != nil {
			sum.Failed++
			sum.FailedSessions = append(sum.FailedSessions, sessionPath)
			writeReport(map[string]string{
				"kind":    "error",
				"session": sessionPath,
				"error":   err.Error(),
			})
			prog.note(true, fmt.Sprintf("FAILED       %s  %v", label, firstLine(err.Error())))
			continue
		}
		if report == nil {
			sum.Failed++
			sum.FailedSessions = append(sum.FailedSessions, sessionPath)
			prog.note(true, fmt.Sprintf("FAILED       %s  nil report and nil error", label))
			continue
		}

		switch report.Status {
		case agent.SessionDAGMigrated:
			sum.Migrated++
			if report.BackupPath != "" {
				sum.DagBackups = append(sum.DagBackups, report.BackupPath)
			}
		case agent.SessionDAGChecked:
			sum.Checked++
			sum.DAG++
			sum.DAGSessions = append(sum.DAGSessions, sessionPath)
		case agent.SessionDAGSkipNotDAG:
			// already flat: the normal case once the migration is done
		default:
			sum.Skipped++
			sum.DAG++
			sum.DAGSessions = append(sum.DAGSessions, sessionPath)
			sum.SkipReasons[string(report.Status)]++
		}

		// Contract carried over from the first version: an already-flat session
		// emits no JSONL line unless asked for explicitly (-single drill or -v),
		// so piped stdout stays proportional to real work.
		important, line := describe(report, label)
		if important || *single != "" || *verbose {
			writeReport(report)
		}
		prog.note(important || *verbose, line)
	}

	sum.Interrupted = interrupted
	sum.ElapsedMs = time.Since(started).Milliseconds()
	sum.FinishedAt = time.Now().Format(time.RFC3339)
	writeReport(sum)

	// --- human summary (stderr) -----------------------------------------
	logf("")
	logf("=== tl1227 dag-migrate %s ===", *mode)
	logf("root      : %s", *root)
	logf("targets   : %d (processed %d)", sum.Total, sum.Scanned)
	if migrationMode == agent.SessionDAGMigrationApply {
		logf("migrated  : %d", sum.Migrated)
	} else {
		logf("schema2   : %d", sum.Checked)
	}
	if reasons := formatReasons(sum.SkipReasons); reasons != "" {
		logf("skipped   : %d  %s", sum.Skipped, reasons)
	} else {
		logf("skipped   : %d", sum.Skipped)
	}
	logf("failed    : %d", sum.Failed)
	if sum.SnapshotsExcluded > 0 {
		logf("snapshots : %d pre-repair backup tree(s) left untouched (-include-backups to migrate them)", sum.SnapshotsExcluded)
	}
	for _, p := range sum.FailedSessions {
		logf("  failed  : %s", p)
	}
	logf("elapsed   : %s", time.Since(started).Round(time.Second))
	if interrupted {
		logf("NOTE      : interrupted; completed sessions above are final, re-run to continue")
	}
	if migrationMode == agent.SessionDAGMigrationApply {
		if sum.Migrated > 0 {
			logf("backups   : %d under *.dagbak-tl1227 (rollback = restore over the log)", sum.Migrated)
			logf("NEXT      : re-run `-mode check`; a clean state is `schema2 : 0, skipped : 0`")
		}
	}
	logf("")

	if interrupted {
		return 130
	}
	if sum.Failed > 0 {
		return 1
	}
	return 0
}

// migrateOne isolates one session: the shared API is careful, but a malformed
// log can still trip a panic deep inside replay, and one bad session must not
// take the whole batch down.
func migrateOne(ctx context.Context, sessionPath string, mode agent.SessionDAGMigrationMode) (report *agent.SessionDAGMigrationReport, err error) {
	defer func() {
		if r := recover(); r != nil {
			buf := make([]byte, 8192)
			n := runtime.Stack(buf, false)
			err = fmt.Errorf("panic: %v\n%s", r, strings.TrimSpace(string(buf[:n])))
		}
	}()
	return agent.MigrateSessionDAGLog(ctx, sessionPath, mode)
}

// collectTargets does a first pass so progress can report a real total (and an
// ETA). Per-entry errors are recorded and skipped instead of aborting: an
// unreadable subtree must not hide the sessions we can still fix. Self-heal
// "*.pre-repair-*" snapshot trees are returned separately so the caller can
// leave them alone by default.
func collectTargets(root, only string) (targets, backups, warnings []string, err error) {
	info, statErr := os.Stat(root)
	if statErr != nil {
		return nil, nil, nil, statErr
	}
	if !info.IsDir() {
		return nil, nil, nil, fmt.Errorf("%s is not a directory", root)
	}
	walkErr := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", path, walkErr))
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || !strings.HasSuffix(path, sessionEventsSuffix) {
			return nil
		}
		if only != "" && !strings.Contains(path, only) {
			return nil
		}
		if isPreRepairSnapshot(path) {
			backups = append(backups, sessionTranscriptFor(path))
			return nil
		}
		targets = append(targets, sessionTranscriptFor(path))
		return nil
	})
	return targets, backups, warnings, walkErr
}

// isPreRepairSnapshot reports whether any path segment is a self-heal snapshot
// tree ("<session>.pre-repair-<stamp>"). Those hold the bytes a repair is
// meant to be able to fall back to; they are never opened as sessions.
func isPreRepairSnapshot(path string) bool {
	for _, seg := range strings.Split(path, string(filepath.Separator)) {
		if strings.Contains(seg, ".pre-repair-") {
			return true
		}
	}
	return false
}

// printer owns the stderr progress line. Progress is throttled by count and by
// wall clock, so a 400-session sweep stays readable and a stalled session still
// shows liveness every few seconds.
type printer struct {
	total int
	every int
	done  int
	start time.Time
	last  time.Time
	quiet bool
}

func (p *printer) note(important bool, label string) {
	p.done++
	if p.quiet {
		return
	}
	due := important || p.done >= p.total || time.Since(p.last) >= 3*time.Second
	if !due && p.every > 0 {
		due = p.done%p.every == 0
	}
	if !due {
		return
	}
	p.last = time.Now()
	elapsed := time.Since(p.start)
	pct := 100.0
	if p.total > 0 {
		pct = float64(p.done) * 100 / float64(p.total)
	}
	eta := "--:--"
	if p.done > 0 && p.total > p.done {
		eta = (elapsed * time.Duration(p.total-p.done) / time.Duration(p.done)).Round(time.Second).String()
	} else if p.total <= p.done {
		eta = "done"
	}
	fmt.Fprintf(os.Stderr, "[%*d/%d %5.1f%% %5s eta %-6s] %s\n",
		len(fmt.Sprint(p.total)), p.done, p.total, pct, elapsed.Round(time.Second), eta, label)
}

// describe turns one report into a single human line and says whether it is
// worth printing unconditionally (migrations, skips and failures are; already
// flat sessions are only interesting in verbose mode).
func describe(rep *agent.SessionDAGMigrationReport, label string) (bool, string) {
	ok := func(b bool) string {
		if b {
			return "ok"
		}
		return "MISMATCH"
	}
	switch rep.Status {
	case agent.SessionDAGMigrated:
		return true, fmt.Sprintf("%-12s %s -> %s  msgs=%d roles=%s digest=%s  backup=%s",
			"MIGRATED", humanBytes(rep.LogBytesBefore), humanBytes(rep.LogBytesAfter),
			rep.MessageCount, ok(rep.RolesMatch), ok(rep.DigestMatch), filepath.Base(rep.BackupPath))
	case agent.SessionDAGChecked:
		return true, fmt.Sprintf("%-12s %s  msgs=%d heads=%d roles=%s digest=%s",
			"SCHEMA2", humanBytes(rep.LogBytesBefore), rep.MessageCount, rep.HeadCount,
			ok(rep.RolesMatch), ok(rep.DigestMatch))
	case agent.SessionDAGSkipLease:
		return true, fmt.Sprintf("%-12s %s  another runtime holds the lease", "SKIP", label)
	case agent.SessionDAGSkipDamaged:
		return true, fmt.Sprintf("%-12s %s  %s", "SKIP", label, damagedNote(rep))
	case agent.SessionDAGSkipForeignHead:
		return true, fmt.Sprintf("%-12s %s  %s", "SKIP", label, rep.Detail)
	case agent.SessionDAGSkipNotDAG:
		// Already flat: the boring majority, shown only with -v.
		return false, fmt.Sprintf("%-12s %s", "flat", label)
	default:
		return true, fmt.Sprintf("%-12s %s  %s", "SKIP", label, rep.Detail)
	}
}

func damagedNote(rep *agent.SessionDAGMigrationReport) string {
	if rep.Detail != "" {
		return "damaged source, left for the rescue path: " + rep.Detail
	}
	return "damaged source, left for the rescue path"
}

func formatReasons(m map[string]int) string {
	if len(m) == 0 {
		return ""
	}
	parts := make([]string, 0, len(m))
	for k, v := range m {
		parts = append(parts, fmt.Sprintf("%s=%d", k, v))
	}
	// stable order: sort by key without pulling in another import set
	for i := 1; i < len(parts); i++ {
		for j := i; j > 0 && parts[j] < parts[j-1]; j-- {
			parts[j], parts[j-1] = parts[j-1], parts[j]
		}
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func sessionLabel(sessionPath string) string {
	base := strings.TrimSuffix(filepath.Base(sessionPath), ".jsonl")
	if len(base) > 28 {
		base = base[:28] + "~"
	}
	return base
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%ciB", float64(n)/float64(div), "KMGTPE"[exp])
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
