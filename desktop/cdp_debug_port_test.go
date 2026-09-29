package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Task 342: the CDP endpoint file is the machine-readable contract between
// the desktop and the verification flow, so its three states are pinned:
// pending (port 0), resolved (positive port), and removed (switch off or
// shutdown). Constructed failures must abort the test — a skipped assert is
// an assert that never ran.
func TestCDPEndpointFileLifecycle(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cdp-endpoint.txt")
	t.Setenv(cdpEndpointFileEnv, path)

	// Pending: requested but not yet resolved — a visible "pending" marker so
	// tooling can tell "coming up" from "nothing armed".
	writeCDPDebugEndpointFile(0)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("pending write must create the file: %v", err)
	}
	if got := strings.TrimSpace(string(data)); got != "pending" {
		t.Fatalf("pending marker = %q, want pending", got)
	}

	// Resolved: exactly one loopback:port line.
	writeCDPDebugEndpointFile(41234)
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatalf("resolved write must replace the pending file: %v", err)
	}
	if got := strings.TrimSpace(string(data)); got != "127.0.0.1:41234" {
		t.Fatalf("endpoint file = %q, want 127.0.0.1:41234", got)
	}

	// Removed: switch off / shutdown must clear the file.
	removeCDPDebugEndpointFile()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("endpoint file must be removed, stat err = %v", err)
	}
}

// The disabled arm is the zero-behaviour contract (铁律 2): no argument.
func TestCDPDebugPortBrowserArgDisabled(t *testing.T) {
	if got := cdpDebugPortBrowserArg(false); got != "" {
		t.Fatalf("disabled switch must return no browser argument, got %q", got)
	}
	if got := cdpDebugPortBrowserArg(true); got != "--remote-debugging-port=0" {
		t.Fatalf("enabled switch must request the random loopback port, got %q", got)
	}
}

// prepareCDPDebugEndpoint with the switch off must not touch the env channel:
// the process environment is part of the zero-behaviour surface.
func TestPrepareCDPDebugEndpointOffKeepsEnvClean(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(cdpEndpointFileEnv, filepath.Join(dir, "cdp-endpoint.txt"))
	t.Setenv("REASONIX_WEBVIEW2_EXTRA_ARGS", "")
	t.Setenv("REASONIX_HOME", filepath.Join(dir, "home"))

	prepareCDPDebugEndpoint()
	if got := os.Getenv("REASONIX_WEBVIEW2_EXTRA_ARGS"); got != "" {
		t.Fatalf("switch off must not set REASONIX_WEBVIEW2_EXTRA_ARGS, got %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "cdp-endpoint.txt")); !os.IsNotExist(err) {
		t.Fatalf("switch off must leave no endpoint file, stat err = %v", err)
	}
}
