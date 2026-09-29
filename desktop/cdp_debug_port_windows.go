//go:build windows

package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// publishCDPDebugEndpoint resolves the actual debug port of the WebView2
// browser and writes it to the endpoint file (task 342).
//
// With `--remote-debugging-port=0` Chromium picks a free ephemeral port,
// binds it to loopback, and records the resolved port in the
// `DevToolsActivePort` file at the root of its user-data folder (first line:
// port, second line: the browser target path). That file is the canonical,
// race-free discovery channel — scraping process command lines is useless
// here because child processes only echo the literal `=0` we passed in.
//
// DevToolsActivePort appears shortly after the browser environment finishes
// creating, so discovery polls for a bounded 20 seconds and then gives up:
// the endpoint file keeps saying "pending", which the verification flow
// treats as "endpoint did not come up". Best effort end to end — no failure
// here may block or crash the desktop.
func publishCDPDebugEndpoint() {
	go func() {
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			if port := findWebView2DebugPort(); port > 0 {
				writeCDPDebugEndpointFile(port)
				slog.Info("desktop: CDP debug endpoint ready", "endpoint", "127.0.0.1:"+strconv.Itoa(port))
				return
			}
			time.Sleep(500 * time.Millisecond)
		}
		slog.Warn("desktop: CDP debug endpoint did not report a port in time")
	}()
}

// findWebView2DebugPort reads the resolved port from the WebView2 browser's
// DevToolsActivePort file inside the user-data folder. Chromium's embedded
// (non-headless) remote debugging binds to loopback only, which together
// with the ephemeral port is the security posture of this switch. Returns 0
// when the file is not there (yet).
func findWebView2DebugPort() int {
	dir := webView2UserDataDir()
	if dir == "" {
		return 0
	}
	data, err := os.ReadFile(filepath.Join(dir, "DevToolsActivePort"))
	if err != nil {
		return 0
	}
	// First line = port; second = the browser target path.
	for i := 0; i < len(data); i++ {
		if data[i] == '\n' || data[i] == '\r' {
			data = data[:i]
			break
		}
	}
	port, err := strconv.Atoi(string(data))
	if err != nil || port <= 0 || port > 65535 {
		return 0
	}
	return port
}

// webView2UserDataDir mirrors the Wails/edge default: main.go leaves
// windows.Options.WebviewUserDataPath unset, so go-webview2 derives the
// folder as %APPDATA%\<exe-name>\EBWebView (pkg/edge/chromium.go — dataPath
// fallback via GetModuleFileName). Keep in sync with that fallback.
func webView2UserDataDir() string {
	appdata := os.Getenv("APPDATA")
	if appdata == "" {
		return ""
	}
	return filepath.Join(appdata, "reasonix-desktop.exe", "EBWebView")
}
