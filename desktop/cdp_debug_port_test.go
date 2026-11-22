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

// tl1741：开关退役——arg 无条件返回（loopback-only 随机端口契约不变）。
func TestCDPDebugPortBrowserArgAlwaysArmed(t *testing.T) {
	if got := cdpDebugPortBrowserArg(); got != "--remote-debugging-port=0" {
		t.Fatalf("armed endpoint must request the random loopback port, got %q", got)
	}
}

// tl1741：TestPrepareCDPDebugEndpointOffKeepsEnvClean 随撤开关删除——
// endpoint 无条件武装，off 态不存在（armed 态由其余断言覆盖）。
