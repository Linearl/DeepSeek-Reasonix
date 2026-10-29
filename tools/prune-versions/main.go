// prune-versions applies the task-411 retention rule to a versioned install:
// keep the newest N trees under <root>/versions plus whatever current.json
// points at, delete the rest. It exists so scripts/build-local-installer.sh
// can prune automatically after every build without embedding retention logic
// in bash — the rule itself lives in internal/installlayout.PruneVersionTrees
// and is unit-tested there (prune_test.go).
//
// Usage:
//
//	go run ./tools/prune-versions -root <installRoot> -keep 5
//
// Deleted version names are printed one per line ("pruned: <name>"); trees a
// live process still executes from are kept and reported ("kept-running:",
// task 1259), escalating to an explicit user-action line once the same tree
// has been occupied for installlayout.OccupiedEscalateAfterRounds consecutive
// runs. A no-op run prints nothing and exits 0.
package main

import (
	"flag"
	"fmt"
	"os"

	"reasonix/internal/installlayout"
)

func main() {
	root := flag.String("root", "", "versioned install root (the directory holding current.json and versions/)")
	keep := flag.Int("keep", 5, "how many newest version trees to keep (current.json target is always kept)")
	flag.Parse()
	if *root == "" {
		fmt.Fprintln(os.Stderr, "prune-versions: -root is required")
		os.Exit(2)
	}
	pruned, retained, err := installlayout.PruneVersionTrees(*root, *keep)
	if err != nil {
		fmt.Fprintf(os.Stderr, "prune-versions: %v\n", err)
		os.Exit(1)
	}
	for _, name := range pruned {
		fmt.Printf("pruned: %s\n", name)
	}
	for _, occ := range retained {
		fmt.Printf("kept-running: %s\n", occ.Describe())
		if occ.Escalated {
			fmt.Printf("needs-user-action: %s has been occupied for %d consecutive prune rounds (pid %v); %s\n",
				occ.Version, occ.Rounds, occ.PIDs, installlayout.OccupiedEscalateHint)
		}
	}
}
