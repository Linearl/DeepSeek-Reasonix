// Command sessionv4exp is a tiny experimental helper for the session-v4
// experiment branch: migrate a legacy JSONL into sessions-v4 or create an empty
// v4 session. It never deletes the legacy source.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "migrate":
		if len(os.Args) != 4 {
			usage()
			os.Exit(2)
		}
		migrate(os.Args[2], os.Args[3])
	case "create":
		if len(os.Args) != 3 {
			usage()
			os.Exit(2)
		}
		create(os.Args[2])
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage:")
	fmt.Fprintln(os.Stderr, "  sessionv4exp migrate <legacy.jsonl> <sessions-v4-root>")
	fmt.Fprintln(os.Stderr, "  sessionv4exp create <sessions-v4-root>")
}

func migrate(sourcePath, targetRoot string) {
	result, err := migrateLegacy(context.Background(), sourcePath, targetRoot)
	if err != nil {
		fmt.Fprintln(os.Stderr, "migrate:", err)
		os.Exit(1)
	}
	fmt.Printf("target=%s dir=%s reused=%v messages=%d\n", result.TargetID, result.TargetDir, result.Reused, result.MessageNum)
}

func create(targetRoot string) {
	id, dir, err := createEmpty(context.Background(), targetRoot)
	if err != nil {
		fmt.Fprintln(os.Stderr, "create:", err)
		os.Exit(1)
	}
	fmt.Printf("target=%s dir=%s\n", id, dir)
	_ = filepath.Separator
}
