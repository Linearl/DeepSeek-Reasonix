package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"reasonix/internal/config"
	"reasonix/internal/heartbeatcfg"
)

// 任务 823 (①+④): `reasonix heartbeat validate` checks heartbeat-tasks.json
// and reports every problem per field, before the desktop engine sees it. The
// 2026-10-12 incident: a hand-edited config with one wrong-typed field made
// the whole file fail to load and every task silently vanish from the UI.
// This command is the pre-flight check a human or an agent runs after editing;
// it never repairs, it only reports (the engine's fail-open degradation is the
// desktop half of the fix).
func heartbeatCommand(args []string) int {
	if len(args) == 0 {
		heartbeatUsage()
		return verdictExitUsage
	}
	switch args[0] {
	case "validate":
		return runHeartbeatValidate(args[1:])
	case "help", "--help", "-h":
		heartbeatUsage()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown heartbeat subcommand %q\n\n", args[0])
		heartbeatUsage()
		return verdictExitUsage
	}
}

func heartbeatUsage() {
	fmt.Fprint(os.Stderr, `Usage:
  reasonix heartbeat validate [--file PATH] [--json]

  Validate a heartbeat-tasks.json config and report every problem per field:
  type errors (with line numbers), duplicate task ids, schedules the engine
  can never fire, and a schemaVersion newer than this binary. Exit code is
  0 when the config is clean, 1 when problems were found, 2 on usage errors.
`)
}

func runHeartbeatValidate(args []string) int {
	fs := flag.NewFlagSet("heartbeat validate", flag.ContinueOnError)
	file := fs.String("file", "", "config path (default: <user state dir>/heartbeat-tasks.json)")
	jsonOut := fs.Bool("json", false, "emit the problem list as JSON instead of human-readable text")
	if code, ok := parseCommandFlags(fs, args); !ok {
		return code
	}
	path := strings.TrimSpace(*file)
	if path == "" {
		base := config.MemoryUserDir()
		if base == "" {
			base = "."
		}
		path = filepath.Join(base, "heartbeat-tasks.json")
	}
	problems, err := heartbeatcfg.ValidateFile(path)
	if err != nil {
		// Missing/unreadable file: usage-class failure, not a config problem.
		fmt.Fprintln(os.Stderr, "error:", err)
		return verdictExitUsage
	}
	if len(problems) == 0 {
		fmt.Printf("OK %s: no problems found\n", path)
		return verdictExitConfirmed
	}
	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(problems); err != nil {
			fmt.Fprintln(os.Stderr, "error: encode:", err)
			return verdictExitRefuted
		}
		return verdictExitRefuted
	}
	heartbeatcfg.FormatProblems(os.Stdout, path, problems)
	return verdictExitRefuted
}
