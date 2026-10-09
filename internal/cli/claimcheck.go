package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"reasonix/internal/claimcheck"
)

// runClaimCheck implements `reasonix claim-check` (task 222 L1): the packaged,
// zero-dependency CLI half of claim hygiene. It turns an existence claim into
// a structured verdict — the same JSON shape the agent-native claim_check
// tool returns — so CI chains, delivery scripts, and humans all share one
// grammar. Exit codes: 0 = claim CONFIRMED, 1 = REFUTED, 2 = usage error.
func runClaimCheck(args []string) int {
	fs := flag.NewFlagSet("claim-check", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	claim := fs.String("claim", "", "claim to verify: \"missing\" or \"exists\" (required)")
	path := fs.String("path", "", "exact file/dir path to probe (mutually exclusive with --pattern)")
	pattern := fs.String("pattern", "", "base-name glob, case-sensitive (requires --base)")
	bases := multiFlag(fs, "base", "search root directory (repeatable)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := validateClaimCheckInput(*claim, *path, *pattern, *bases); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	result, err := claimcheck.Check(*claim, *path, *pattern, *bases)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	blob, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	fmt.Println(string(blob))
	// 任务 229 G1: the exit decision rides the shared verdict contract
	// (CONFIRMED -> 0, everything else -> 1) — same behaviour, one vocabulary.
	return verdictExitCode(string(result.Verdict))
}

// validateClaimCheckInput rejects obviously unusable invocations up front so
// the usage exit code (2) never competes with a real REFUTED (1).
func validateClaimCheckInput(claim, path, pattern string, bases []string) error {
	if claim != "missing" && claim != "exists" {
		return fmt.Errorf(`--claim must be "missing" or "exists"`)
	}
	if strings.TrimSpace(path) == "" && (strings.TrimSpace(pattern) == "" || len(bases) == 0) {
		return fmt.Errorf("provide --path, or --pattern with at least one --base")
	}
	if strings.TrimSpace(path) != "" && strings.TrimSpace(pattern) != "" {
		return fmt.Errorf("--path and --pattern are mutually exclusive")
	}
	return nil
}

// multiFlag is a repeatable string flag (like argparse's action="append").
func multiFlag(fs *flag.FlagSet, name, usage string) *[]string {
	values := &[]string{}
	fs.Var((*repeatableValue)(values), name, usage)
	return values
}

type repeatableValue []string

func (r *repeatableValue) String() string {
	return strings.Join(*r, ",")
}

func (r *repeatableValue) Set(s string) error {
	*r = append(*r, s)
	return nil
}
