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
// fixed argument list, so the endpoint cannot be toggled live. tl1741（用户
// 20261121 三拍）：experimental_cdp_debug_port 实验室开关随「稳定档不再提供
// 开关」退役——endpoint 无条件武装（本机 config 实测该键此前已显式 true，
// 常开=本机行为零变化；新装用户从 ships-off 变为常开，按拍板执行）。
//
// Security posture (user ruling 2026-09-27, plan B "完整产品化"):
//   - the browser receives `--remote-debugging-port=0`: Chromium picks a free
//     ephemeral port (random, so the classic fixed 9222 collision is
//     impossible) and binds it to loopback (127.0.0.1) only, which is not
//     reachable from other machines. A token/credential layer is not part of
//     the WebView2 CDP surface, so the posture rests on loopback-only +
//     random port + the endpoint file being user-readable only;
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
// the CDP debug endpoint（tl1741：无条件启用）. The extra environment channel
// (REASONIX_WEBVIEW2_EXTRA_ARGS) is appended separately by the vendored
// loader, so an operator can always widen the local debug surface by hand.
func cdpDebugPortBrowserArg() string {
	return "--remote-debugging-port=0"
}

// prepareCDPDebugEndpoint arms the task-342 endpoint for this boot:
//
//  1. export REASONIX_WEBVIEW2_EXTRA_ARGS with `--remote-debugging-port=0`
//     so the vendored loader appends it to the browser arguments
//     (loopback-only random port, see NewChromium) — tl1741 起无条件；
//  2. pre-write the pending marker (port 0 removes any stale endpoint line)
//     so a port from a previous boot can never be mistaken for a live one.
//
// The remote web child path never reaches here (preparePrimaryDesktopRuntime
// is skipped for remoteWindowTicket processes).
func prepareCDPDebugEndpoint() {
	arg := cdpDebugPortBrowserArg()
	if current := strings.TrimSpace(os.Getenv("REASONIX_WEBVIEW2_EXTRA_ARGS")); current != "" {
		arg = current + " " + arg
	}
	os.Setenv("REASONIX_WEBVIEW2_EXTRA_ARGS", arg)
	writeCDPDebugEndpointFile(0)
	slog.Info("desktop: CDP debug endpoint armed (loopback, random port)", "file", cdpDebugEndpointFile())
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

// tl1741：cdpDebugEnabledForBoot 配置门随撤开关删除——endpoint 无条件武装。
