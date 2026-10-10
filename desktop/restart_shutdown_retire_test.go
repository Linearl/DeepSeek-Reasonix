package main

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"testing"
)

// ── task 763: update-restart exit must not WARN on the retire-superseded check ─
// 21.5 装机实测（2026-10-11 03:08）：更新重启退出时 running(2015) ≠ active(0255)
// 是换版提交后的设计内状态，旧代码却在 shutdown 里 WARN「retire superseded
// update ... does not match running version」，落在用户更新后必看的日志窗口，
// 被读成「更新重启被误判为崩溃」。修法：更新家族退出点（与 461-P2 标记同一点
// 置位）豁免该检查，交由重启后的新版本进程在 completeFrontendStartup 重跑；
// 普通退出路径行为不变（含真异常的 WARN）。

func captureSlogFor763(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(old) })
	return buf
}

func stubShutdownRetireSuperseded(t *testing.T, calls *int, err error, archived bool) {
	t.Helper()
	old := shutdownRetireSuperseded
	shutdownRetireSuperseded = func() (bool, error) {
		*calls++
		return archived, err
	}
	t.Cleanup(func() { shutdownRetireSuperseded = old })
}

// An update-restart exit (flag armed at the 461-P2 marker commit point) must
// skip the check entirely and log the deferral — the mismatch is the design.
func TestRetireSupersededDeferredOnUpdateRestartExit(t *testing.T) {
	buf := captureSlogFor763(t)
	calls := 0
	stubShutdownRetireSuperseded(t, &calls, errors.New("active install version v2 does not match running version v1"), false)

	app := &App{}
	app.updateRestartExit.Store(true)
	app.retireSupersededDuringShutdown()

	if calls != 0 {
		t.Fatalf("retire check ran %d times on an update-restart exit, want 0 (deferred to the relaunched version)", calls)
	}
	out := buf.String()
	if !strings.Contains(out, "update-restart exit; retire-superseded check deferred") {
		t.Fatalf("deferral must be observable on the log, got: %s", out)
	}
	if strings.Contains(out, "retire superseded update during shutdown") {
		t.Fatalf("the misleading WARN must not fire on an update-restart exit, got: %s", out)
	}
}

// A normal exit keeps today's behavior: the check runs; a version mismatch
// still WARNs (e.g. a manual downgrade is a real anomaly worth naming).
func TestRetireSupersededStillRunsAndWarnsOnNormalExit(t *testing.T) {
	buf := captureSlogFor763(t)
	calls := 0
	stubShutdownRetireSuperseded(t, &calls, fmt.Errorf("active install version v2 does not match running version v1"), false)

	app := &App{}
	app.retireSupersededDuringShutdown()

	if calls != 1 {
		t.Fatalf("retire check ran %d times on a normal exit, want 1", calls)
	}
	out := buf.String()
	if !strings.Contains(out, "desktop: retire superseded update during shutdown") {
		t.Fatalf("normal-exit mismatch must keep the WARN face, got: %s", out)
	}
}

// Success and archive faces of the normal-exit path stay wired.
func TestRetireSupersededNormalExitFacesPreserved(t *testing.T) {
	captureSlogFor763(t)
	calls := 0
	stubShutdownRetireSuperseded(t, &calls, nil, true)

	app := &App{}
	app.retireSupersededDuringShutdown()
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}

	stubShutdownRetireSuperseded(t, &calls, nil, false)
	app.retireSupersededDuringShutdown()
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}

// Source contract: every 461-P2 marker site must arm updateRestartExit —
// a future update-family exit path that writes the marker but skips the flag
// would reintroduce the misleading WARN in its shutdown window.
func TestUpdateRestartExitArmedAtEveryMarkerSite(t *testing.T) {
	sites := map[string]string{
		"restart_update.go": "publish / restart tool",
		"version_switch.go": "switch",
		"updater_app.go":    "updater",
	}
	for file, face := range sites {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(string(body), "\n")
		for i, line := range lines {
			if !strings.Contains(line, "writeUpdateRestartMarker(") || strings.Contains(line, "func writeUpdateRestartMarker") {
				continue
			}
			armed := false
			for j := i + 1; j < len(lines) && j <= i+8; j++ {
				if strings.Contains(lines[j], "updateRestartExit.Store(true)") {
					armed = true
					break
				}
			}
			if !armed {
				t.Errorf("%s (%s face) line %d writes the update-restart marker without arming updateRestartExit within 8 lines", file, face, i+1)
			}
		}
	}
	// The plain-restart face must stay flag-free: a manual restart is not an
	// update restart (task 461-P2), and its retire check must keep running.
	restartBody, err := os.ReadFile("restart_update.go")
	if err != nil {
		t.Fatal(err)
	}
	if marker := "writeUpdateRestartMarker(\"\", version)"; strings.Contains(string(restartBody), marker) {
		t.Errorf("plain restart must never write a marker (%s found)", marker)
	}
}
