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
// Deleted version names are printed one per line ("pruned: <name>"); a no-op
// run prints nothing and exits 0.
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
	pruned, err := installlayout.PruneVersionTrees(*root, *keep)
	if err != nil {
		fmt.Fprintf(os.Stderr, "prune-versions: %v\n", err)
		os.Exit(1)
	}
	for _, name := range pruned {
		fmt.Printf("pruned: %s\n", name)
	}
}
