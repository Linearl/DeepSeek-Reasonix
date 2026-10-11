package cli

// Task 773: `reasonix sessions import-v5` — the CLI tier (first of the three
// entry shapes in the 766 design §7.1) over the read-only v5 importer kernel
// in internal/session. Without --apply it prints the dry-run plan only; with
// --apply it backs the source store up, builds everything in staging, and
// publishes after verification, printing a per-session line as it goes.

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/session"
)

// resolveV5ImportSource accepts the by-id root, its desktop-sessions-v5
// parent, or any directory that directly contains desktop-sessions-v5, and
// returns the by-id root.
func resolveV5ImportSource(from string) (string, error) {
	cleaned := filepath.Clean(strings.TrimSpace(from))
	if info, err := os.Stat(cleaned); err != nil || !info.IsDir() {
		return "", fmt.Errorf("source directory %s is not readable", from)
	}
	switch filepath.Base(cleaned) {
	case "by-id":
		if filepath.Base(filepath.Dir(cleaned)) != "desktop-sessions-v5" {
			return "", fmt.Errorf("%s is not inside desktop-sessions-v5", cleaned)
		}
		return cleaned, nil
	case "desktop-sessions-v5":
		return filepath.Join(cleaned, "by-id"), nil
	default:
		nested := filepath.Join(cleaned, "desktop-sessions-v5", "by-id")
		if info, err := os.Stat(nested); err == nil && info.IsDir() {
			return nested, nil
		}
		return "", fmt.Errorf("%s does not contain desktop-sessions-v5/by-id", cleaned)
	}
}

// v5ImportTargetFor maps a registry workspace root to the fork's legacy
// session directory for it: project roots land in projects/<slug>/sessions,
// everything else in the global sessions dir. An explicit --target-dir
// overrides routing entirely.
func v5ImportTargetFor(override string) func(string) (string, error) {
	return func(workspaceRoot string) (string, error) {
		if override != "" {
			return override, nil
		}
		if strings.TrimSpace(workspaceRoot) != "" {
			if dir := config.ProjectSessionDir(workspaceRoot); dir != "" {
				return dir, nil
			}
			return "", fmt.Errorf("cannot resolve the project session directory for workspace %q", workspaceRoot)
		}
		dir := config.SessionDir()
		if dir == "" {
			return "", fmt.Errorf("cannot resolve the global session directory (user state dir unavailable)")
		}
		return dir, nil
	}
}

type v5ImportFlags struct {
	from      string
	apply     bool
	jsonOut   bool
	noBackup  bool
	backupDir string
	targetDir string
}

func parseV5ImportFlags(args []string) (*v5ImportFlags, int, string) {
	opts := &v5ImportFlags{}
	fs := flag.NewFlagSet("sessions import-v5", flag.ContinueOnError)
	fs.StringVar(&opts.from, "from", "", "v5 source: .../desktop-sessions-v5/by-id, its parent, or the state root containing it (required)")
	fs.BoolVar(&opts.apply, "apply", false, "execute the import (default is a dry-run plan only)")
	fs.BoolVar(&opts.jsonOut, "json", false, "print the full report as JSON")
	fs.BoolVar(&opts.noBackup, "no-backup", false, "skip the pre-import full source backup (not recommended)")
	fs.StringVar(&opts.backupDir, "backup-dir", "", "where to create the timestamped backup directory (default: beside the v5 tree)")
	fs.StringVar(&opts.targetDir, "target-dir", "", "import every session into this one legacy session directory (default: per-workspace routing)")
	if code, ok := parseCommandFlags(fs, args); !ok {
		return nil, code, ""
	}
	if strings.TrimSpace(opts.from) == "" {
		fmt.Fprintln(os.Stderr, "usage: reasonix sessions import-v5 --from PATH [--apply] [--json] [--target-dir PATH] [--backup-dir PATH] [--no-backup]")
		return nil, 2, ""
	}
	return opts, 0, ""
}

func sessionsImportV5Command(args []string) int {
	opts, code, message := parseV5ImportFlags(args)
	if opts == nil {
		if message != "" {
			fmt.Fprintln(os.Stderr, "error:", message)
		}
		return code
	}
	source, err := resolveV5ImportSource(opts.from)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	if !opts.apply {
		plan, err := session.PlanV5Import(context.Background(), session.V5ImportOptions{
			SourceRoot:    source,
			ResolveTarget: v5ImportTargetFor(opts.targetDir),
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return 1
		}
		if opts.jsonOut {
			encoder := json.NewEncoder(os.Stdout)
			encoder.SetIndent("", "  ")
			if err := encoder.Encode(plan); err != nil {
				fmt.Fprintln(os.Stderr, "error:", err)
				return 1
			}
			return 0
		}
		fmt.Print("dry-run plan (nothing written; add --apply to execute)\n\n")
		fmt.Print(plan.Summarize())
		for _, entry := range plan.Entries {
			fmt.Printf("  %-8s rev%d %6d msgs  %s  %s\n", entry.Status, entry.StorageRevision, entry.MessageCount, entry.SessionID, entryReasonSuffix(entry))
		}
		for _, failure := range plan.Failures {
			fmt.Printf("  %-8s        %s  %s\n", "fail", failure.SessionID, failure.Reason)
		}
		return 0
	}

	report, err := session.RunV5Import(context.Background(), session.V5ImportRunOptions{
		V5ImportOptions: session.V5ImportOptions{
			SourceRoot:    source,
			ResolveTarget: v5ImportTargetFor(opts.targetDir),
		},
		NoBackup:   opts.noBackup,
		BackupRoot: opts.backupDir,
		Progress: func(format string, args ...any) {
			if !opts.jsonOut {
				fmt.Printf(format+"\n", args...)
			}
		},
	})
	if report != nil {
		reportPath := writeV5ImportReportFile(source, opts, report)
		if opts.jsonOut {
			encoder := json.NewEncoder(os.Stdout)
			encoder.SetIndent("", "  ")
			_ = encoder.Encode(report)
		} else {
			fmt.Printf("\nverified=%v %s\n", report.Verified, report.Accounting)
			for _, loss := range report.Losses {
				fmt.Printf("  loss: %s\n", loss)
			}
			if reportPath != "" {
				fmt.Printf("report: %s\n", reportPath)
			}
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	if report != nil && report.FailCount > 0 {
		return 1
	}
	return 0
}

// writeV5ImportReportFile persists the run receipt beside the v5 tree (the
// state root), keeping the ledger out of both the source store and the
// legacy session directories. Best effort: a report-file failure never fails
// an otherwise verified import.
func writeV5ImportReportFile(source string, opts *v5ImportFlags, report *session.V5ImportReport) string {
	base := strings.TrimSpace(opts.backupDir)
	if base == "" {
		base = filepath.Dir(filepath.Dir(source))
	}
	path := filepath.Join(base, "import-v5-report-"+time.Now().UTC().Format("20060102-150405")+".json")
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return ""
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return ""
	}
	return path
}

func entryReasonSuffix(entry session.V5PlanEntry) string {
	if entry.Reason == "" {
		return ""
	}
	return "— " + entry.Reason
}
