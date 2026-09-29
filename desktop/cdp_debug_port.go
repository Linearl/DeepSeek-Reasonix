package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"reasonix/internal/config"
)

// Task 342: WebView2 CDP (Chrome DevTools Protocol) debug endpoint.
//
// The WebView2 browser environment is created exactly once per process with a
// fixed argument list, so the endpoint cannot be toggled live: the [desktop]
// experimental_cdp_debug_port switch is read at startup and a flip applies on
// the next restart (the settings card communicates this and the lab pane
// raises the restart banner).
//
// Design (user ruling 2026-09-27, plan B "完整产品化"):
//   - ships OFF: with the zero value no browser argument is appended and the
//     process behaves byte-for-byte like a build without the feature;
//   - when ON the browser receives `--remote-debugging-port=0`: Chromium then
//     picks a free ephemeral port (random, so the classic fixed 9222 collision
//     is impossible) and binds it to loopback (127.0.0.1) only, which is not
//     reachable from other machines. A token/credential layer is not part of
//     the WebView2 CDP surface, so the security posture rests on the three
//     remaining gates: default off + loopback-only + random port (ruling
//     recorded in the task-342 body);
//   - the resolved endpoint is written to
//     <REASONIX_HOME>/logs/desktop/cdp-endpoint.txt so tooling can read the
//     actual port instead of guessing; the file is removed on shutdown.
//
// The remote web child windows (app.remoteWindowTicket) never enable the
// endpoint: they are single-purpose surfaces with no bindings.

const (
	// cdpEndpointFileEnv is the optional override for the endpoint file
	// location. Set by tests and the CDP verification harness; production
	// always uses the desktop log directory.
	cdpEndpointFileEnv = "REASONIX_CDP_ENDPOINT_FILE"

	// cdpEndpointFileName is the file written next to desktop.log when the
	// endpoint is live. `127.0.0.1:<port>` on a single line.
	cdpEndpointFileName = "cdp-endpoint.txt"
)

// cdpDebugPortBrowserArg returns the WebView2 browser argument that enables
// the CDP debug endpoint, or "" when the switch is off (zero behaviour). The
// extra environment channel (REASONIX_WEBVIEW2_EXTRA_ARGS) is appended
// separately by the vendored loader, so an operator can always widen the
// local debug surface by hand without this switch.
func cdpDebugPortBrowserArg(enabled bool) string {
	if !enabled {
		return ""
	}
	return "--remote-debugging-port=0"
}

// prepareCDPDebugEndpoint arms the task-342 endpoint for this boot:
//
//  1. decide from the user-global config (default off — a load failure keeps
//     every earlier behaviour);
//  2. when on, export REASONIX_WEBVIEW2_EXTRA_ARGS with
//     `--remote-debugging-port=0` so the vendored loader appends it to the
//     browser arguments (loopback-only random port, see NewChromium);
//  3. pre-write the pending marker (port 0 removes any stale endpoint line)
//     so a port from a previous boot can never be mistaken for a live one.
//
// The remote web child path never reaches here (preparePrimaryDesktopRuntime
// is skipped for remoteWindowTicket processes).
func prepareCDPDebugEndpoint() {
	enabled := cdpDebugEnabledForBoot()
	if !enabled {
		removeCDPDebugEndpointFile()
		return
	}
	arg := cdpDebugPortBrowserArg(true)
	if current := strings.TrimSpace(os.Getenv("REASONIX_WEBVIEW2_EXTRA_ARGS")); current != "" {
		arg = current + " " + arg
	}
	os.Setenv("REASONIX_WEBVIEW2_EXTRA_ARGS", arg)
	writeCDPDebugEndpointFile(0)
	slog.Info("desktop: CDP debug endpoint requested (loopback, random port)", "file", cdpDebugEndpointFile())
}

// cdpDebugEndpointFile returns the path the resolved endpoint is written to.
// Empty when the log directory cannot be resolved.
func cdpDebugEndpointFile() string {
	if custom := strings.TrimSpace(os.Getenv(cdpEndpointFileEnv)); custom != "" {
		return custom
	}
	if home := config.MemoryUserDir(); home != "" {
		return filepath.Join(home, desktopLogDirName, desktopLogSubDir, cdpEndpointFileName)
	}
	return ""
}

// writeCDPDebugEndpointFile records `127.0.0.1:<port>` (or "off") for tooling.
// Best effort: a failed write must never block startup.
func writeCDPDebugEndpointFile(port int) {
	path := cdpDebugEndpointFile()
	if path == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	if port <= 0 {
		// Pending: requested, but the browser child has not reported the
		// resolved port yet. A visible marker beats a missing file — tooling
		// can distinguish "endpoint coming up" from "nothing was armed".
		_ = os.WriteFile(path, []byte("pending\n"), 0o600)
		return
	}
	_ = os.WriteFile(path, []byte("127.0.0.1:"+strconv.Itoa(port)+"\n"), 0o600)
}

// removeCDPDebugEndpointFile clears the endpoint file on shutdown so a stale
// port can never be mistaken for a live one. Best effort.
func removeCDPDebugEndpointFile() {
	if path := cdpDebugEndpointFile(); path != "" {
		_ = os.Remove(path)
	}
}

// cdpDebugEnabledForBoot reports whether THIS process should enable the CDP
// debug endpoint. It reads the user-global config only (never project files:
// an untrusted checkout must not be able to open a debug port in a trusted
// desktop) and defaults to false on any load error.
func cdpDebugEnabledForBoot() bool {
	cfg, err := config.LoadUserConfigReadOnly()
	if err != nil || cfg == nil {
		return false
	}
	return cfg.Desktop.ExperimentalCDPDebugPort
}
