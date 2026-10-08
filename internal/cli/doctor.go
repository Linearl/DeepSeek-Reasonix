package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/doctor"
	"reasonix/internal/repair"
	"reasonix/internal/sessioncatalog"
)

func doctorBillingCommand(args []string) int {
	fs := flag.NewFlagSet("doctor billing", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "print billing diagnostics as JSON")
	root := fs.String("root", ".", "project root for config resolution")
	if code, ok := parseCommandFlags(fs, args); !ok {
		return code
	}
	cfg, err := config.LoadForRoot(*root)
	if err != nil {
		// Still report with defaults so doctor stays useful offline.
		cfg = config.Default()
	}
	report := doctor.CollectBilling(cfg)
	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return 0
	}
	fmt.Print(doctor.RenderBillingText(report))
	return 0
}

func doctorCommand(args []string, version string) int {
	if len(args) > 0 && args[0] == "catalogs" {
		return doctorCatalogsCommand(args[1:])
	}
	if len(args) > 0 && args[0] == "sessions" {
		return doctorSessionsCommand(args[1:])
	}
	if len(args) > 0 && args[0] == "quality" {
		return doctorQualityCommand(args[1:], version)
	}
	if len(args) > 0 && args[0] == "session" {
		return doctorSessionCommand(args[1:], version)
	}
	if len(args) > 0 && args[0] == "redact-sessions" {
		return doctorRedactSessionsCommand(args[1:])
	}
	if len(args) > 0 && args[0] == "capabilities" {
		return doctorCapabilitiesCommand(args[1:])
	}
	if len(args) > 0 && args[0] == "runtime" {
		return doctorRuntimeCommand(args[1:])
	}
	if len(args) > 0 && args[0] == "billing" {
		return doctorBillingCommand(args[1:])
	}
	if len(args) > 0 && args[0] == "responsiveness" {
		return doctorResponsivenessCommand(args[1:])
	}
	if len(args) > 0 && args[0] == "repair" {
		return doctorRepairCommand(args[1:])
	}
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "print diagnostics as JSON")
	if code, ok := parseCommandFlags(fs, args); !ok {
		return code
	}

	report := doctor.Collect(doctor.Options{Version: version})
	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return 0
	}
	fmt.Print(doctor.RenderText(report))
	return 0
}

func doctorSessionsCommand(args []string) int {
	fs := flag.NewFlagSet("doctor sessions", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "print session catalog diagnostics as JSON")
	if code, ok := parseCommandFlags(fs, args); !ok {
		return code
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: reasonix doctor sessions [--json]")
		return 2
	}
	status, err := sessioncatalog.Inspect(context.Background(), sessioncatalog.DefaultPath())
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(status); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return 0
	}
	fmt.Println("Reasonix session catalog")
	fmt.Printf("  state: %s\n", status.State)
	fmt.Printf("  mode: %s\n", status.Mode)
	fmt.Printf("  revision: %d\n", status.Revision)
	fmt.Printf("  indexed: %d\n", status.Indexed)
	fmt.Printf("  physical sessions: %d\n", status.PhysicalSessions)
	fmt.Printf("  logical sessions: %d\n", status.LogicalSessions)
	fmt.Printf("  repair pending: %d\n", status.RepairPending)
	fmt.Printf("  repair: %d active, %d deferred, %d blocked\n",
		status.RepairActive, status.RepairDeferred, status.RepairBlocked)
	if len(status.RepairErrorKinds) > 0 {
		fmt.Printf("  repair error kinds: %v\n", status.RepairErrorKinds)
	}
	if status.LastRepairDurationMS > 0 {
		fmt.Printf("  last repair wave: %dms\n", status.LastRepairDurationMS)
	}
	fmt.Printf("  recovery: %d groups, %d branches, %d diverged, %d safe cleanup\n",
		status.RecoveryGroups, status.RecoveryBranches, status.RecoveryDiverged, status.CleanupEligible)
	if status.LastError != "" {
		fmt.Printf("  note: %s\n", status.LastError)
	}
	return 0
}

func doctorRepairCommand(args []string) int {
	fs := flag.NewFlagSet("doctor repair", flag.ContinueOnError)
	root := fs.String("root", ".", "project root to inspect")
	apply := fs.Bool("apply", false, "quarantine invalid config and restore the last-known-good global snapshot")
	includeProject := fs.Bool("project", false, "allow --apply to quarantine an invalid project reasonix.toml")
	jsonOut := fs.Bool("json", false, "print result as JSON")
	if code, ok := parseCommandFlags(fs, args); !ok {
		return code
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: reasonix doctor repair [--root PATH] [--apply] [--project] [--json]")
		return 2
	}
	report, err := repair.InspectAndRepairConfig(repair.ConfigOptions{
		Root:           *root,
		Apply:          *apply,
		IncludeProject: *includeProject,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	} else {
		fmt.Println("Reasonix repair report")
		for _, check := range report.Checks {
			status := "ok"
			if !check.Exists {
				status = "missing (defaults apply)"
			} else if !check.Valid {
				status = "invalid: " + check.Error
			}
			fmt.Printf("  %-8s %s\n             %s\n", check.Scope, status, check.Path)
		}
		for _, action := range report.Applied {
			fmt.Println("  applied:", action)
		}
		if !*apply {
			fmt.Println("  dry run; pass --apply to repair the global config")
		}
	}
	for _, check := range report.Checks {
		if check.Exists && !check.Valid {
			return 1
		}
	}
	return 0
}

func doctorQualityCommand(args []string, version string) int {
	ref := ""
	jsonOut := false
	for _, arg := range args {
		switch arg {
		case "-h", "--help":
			fmt.Fprintln(os.Stdout, "usage: reasonix doctor quality <branch-id-or-path> [--json]")
			fmt.Fprintln(os.Stdout, "Prints a public-safe, content-free coding-quality summary for one session.")
			return 0
		case "--json":
			jsonOut = true
		default:
			if strings.HasPrefix(arg, "-") || ref != "" {
				fmt.Fprintln(os.Stderr, "usage: reasonix doctor quality <branch-id-or-path> [--json]")
				return 2
			}
			ref = arg
		}
	}
	if ref == "" {
		fmt.Fprintln(os.Stderr, "usage: reasonix doctor quality <branch-id-or-path> [--json]")
		return 2
	}
	report, err := doctor.CollectQuality(doctor.QualityOptions{Version: version, SessionRef: ref})
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return 0
	}
	fmt.Print(doctor.RenderQualityText(report))
	return 0
}

type stringListFlag []string

func (f *stringListFlag) String() string {
	if f == nil {
		return ""
	}
	return strings.Join(*f, string(os.PathListSeparator))
}

func (f *stringListFlag) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("empty path")
	}
	*f = append(*f, value)
	return nil
}

func doctorRedactSessionsCommand(args []string) int {
	fs := flag.NewFlagSet("doctor redact-sessions", flag.ContinueOnError)
	var dirs stringListFlag
	dryRun := fs.Bool("dry-run", false, "show how many session files would be redacted without writing")
	jsonOut := fs.Bool("json", false, "print result as JSON")
	fs.Var(&dirs, "dir", "session directory to scan; repeat to scan multiple directories")
	if code, ok := parseCommandFlags(fs, args); !ok {
		return code
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: reasonix doctor redact-sessions [--dry-run] [--json] [--dir PATH]")
		return 2
	}
	res := doctor.RedactSessions(doctor.RedactSessionsOptions{
		Dirs:   []string(dirs),
		DryRun: *dryRun,
	})
	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(res); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	} else {
		action := "redacted"
		if *dryRun {
			action = "would redact"
		}
		fmt.Fprintf(os.Stdout, "session secret cleanup %s %d/%d files", action, res.FilesChanged, res.FilesScanned)
		if res.FilesSkipped > 0 {
			fmt.Fprintf(os.Stdout, " (%d skipped: active lease held)", res.FilesSkipped)
		}
		fmt.Fprintln(os.Stdout)
	}
	for _, msg := range res.Errors {
		fmt.Fprintln(os.Stderr, "warning:", msg)
	}
	if len(res.Errors) > 0 {
		return 1
	}
	return 0
}

// doctorResponsivenessCommand answers "stuck or slow?" for one session from
// durable files only (task 370). Read-only: it never repairs the ledger it
// reads — the runtime repairs on next open. --watch re-samples so the third
// criterion (does a silent session recover?) is one command instead of a
// manual re-run later.
func doctorResponsivenessCommand(args []string) int {
	fs := flag.NewFlagSet("doctor responsiveness", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "print the responsiveness report as JSON")
	watch := fs.Duration("watch", 0, "re-sample for this long (e.g. 2m) and report whether the ledger advances")
	if code, ok := parseCommandFlags(fs, args); !ok {
		return code
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: reasonix doctor responsiveness <branch-id-or-path> [--watch DURATION] [--json]")
		return 2
	}
	report, err := doctor.CollectResponsiveness(fs.Arg(0), time.Now())
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	if *watch > 0 {
		interval := *watch / 12
		if interval > 15*time.Second {
			interval = 15 * time.Second
		}
		if interval < time.Second {
			interval = time.Second
		}
		baseSeq, baseSize := report.LatestSequence, report.LedgerSize
		baseVerdict := report.Verdict
		deadline := time.Now().Add(*watch)
		for time.Now().Before(deadline) {
			time.Sleep(interval)
			report, err = doctor.CollectResponsiveness(fs.Arg(0), time.Now())
			if err != nil {
				fmt.Fprintln(os.Stderr, "error:", err)
				return 1
			}
			if report.LatestSequence != baseSeq || report.LedgerSize != baseSize {
				break
			}
		}
		advanced := report.LatestSequence != baseSeq || report.LedgerSize != baseSize
		if !*jsonOut {
			progress := "no progress"
			if advanced {
				progress = "advanced"
			}
			fmt.Printf("watch: sequence %d -> %d, ledger %d -> %d bytes (%s)\n",
				baseSeq, report.LatestSequence, baseSize, report.LedgerSize, progress)
			fmt.Printf("watch: verdict %s -> %s\n", baseVerdict, report.Verdict)
		}
	}
	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return 0
	}
	fmt.Print(doctor.RenderResponsivenessText(report, time.Now()))
	// exit 1 only for states a human asked about and should act on now;
	// "slow but alive" is not a failure.
	return 0
}

func doctorSessionCommand(args []string, version string) int {
	ref := ""
	outPath := ""
	exportPath := ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "-h", "--help":
			fmt.Fprintln(os.Stdout, "usage: reasonix doctor session <branch-id-or-path> [--zip] [--out PATH] [--export-v1 PATH.jsonl]")
			fmt.Fprintln(os.Stdout, "")
			fmt.Fprintln(os.Stdout, "Bundles the session transcript, persistence sidecars, conflict diagnostics,")
			fmt.Fprintln(os.Stdout, "and the recovery parent chain into a zip for support. Unlike `reasonix doctor`,")
			fmt.Fprintln(os.Stdout, "bundled transcripts are NOT redacted; share only with a trusted support channel.")
			fmt.Fprintln(os.Stdout, "--export-v1 instead writes the session's current version as a schema-1 session")
			fmt.Fprintln(os.Stdout, "that releases before v1.39.0 can open.")
			return 0
		case "--export-v1":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "error: --export-v1 requires a destination .jsonl path")
				return 2
			}
			exportPath = args[i]
		case "--zip":
			// The subcommand currently writes a zip by default. Keep --zip as an
			// explicit, script-friendly marker so support replies can say exactly
			// what to run.
		case "--out":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "error: --out requires a path")
				return 2
			}
			outPath = args[i]
		default:
			if v, ok := strings.CutPrefix(arg, "--out="); ok {
				if v == "" {
					fmt.Fprintln(os.Stderr, "error: --out requires a path")
					return 2
				}
				outPath = v
				continue
			}
			if strings.HasPrefix(arg, "-") {
				fmt.Fprintf(os.Stderr, "error: unknown doctor session flag %s\n", arg)
				return 2
			}
			if ref != "" {
				fmt.Fprintln(os.Stderr, "usage: reasonix doctor session <branch-id-or-path> [--zip] [--out PATH]")
				return 2
			}
			ref = arg
		}
	}
	if ref == "" {
		fmt.Fprintln(os.Stderr, "usage: reasonix doctor session <branch-id-or-path> [--zip] [--out PATH] [--export-v1 PATH.jsonl]")
		return 2
	}
	if exportPath != "" {
		src, err := doctor.ResolveSessionRef(ref)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return 1
		}
		if err := agent.ExportSessionSchemaOne(src, exportPath); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return 1
		}
		fmt.Println(exportPath)
		return 0
	}
	result, err := doctor.WriteSessionBundle(doctor.SessionBundleOptions{
		Version:    version,
		SessionRef: ref,
		OutputPath: outPath,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	fmt.Println(result.Path)
	fmt.Fprintln(os.Stderr, "note: the bundle contains full session transcripts without redaction; share it only with a trusted support channel")
	return 0
}
