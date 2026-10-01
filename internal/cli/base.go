package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"

	"reasonix/internal/baseproc"
)

// baseCommand is the resident-base-subprocess entry (design D4): the same
// reasonix binary re-run as `reasonix base serve --stdio`. In this mode the
// process registers no GUI, touches no session locks, takes no part in the
// file-lease system (the base holds no sessions), and speaks the S1 base
// protocol on stdin/stdout until EOF. It is plumbing, not a user command —
// intentionally absent from usage() — so its stdout stays protocol-pure.
func baseCommand(args []string, version string) int {
	if len(args) == 0 || args[0] != "serve" {
		fmt.Fprintln(os.Stderr, "usage: reasonix base serve --stdio")
		return 2
	}
	fs := flag.NewFlagSet("base serve", flag.ContinueOnError)
	stdio := fs.Bool("stdio", false, "serve the base protocol on stdin/stdout (the only v1 transport)")
	if err := fs.Parse(args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "usage: reasonix base serve --stdio")
		return 2
	}
	if !*stdio {
		fmt.Fprintln(os.Stderr, "reasonix base serve: --stdio is required (the only v1 transport)")
		return 2
	}
	// Ctrl-C maps to a clean serve return; the orphan path is stdin EOF
	// (parent death), which RunStdioServer handles inside Serve.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return baseproc.RunStdioServer(ctx, version, os.Stdin, os.Stdout, os.Stderr)
}
