package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"reasonix/internal/control"
)

// Task 806 (P2) 验收⑥: every user-facing tab mode switch writes one grep-able
// [tab-mode] line — which entry fired (the task-595 tier bar vs the legacy
// setter) and the before/after approval + autopilot posture. The 2026-10-12
// ask incident's only unresolved question was "which entry switched the mode
// to what"; this pin keeps that forensic capability from silently regressing.
func TestTabModeSwitchWritesAnchorLogs(t *testing.T) {
	isolateDesktopUserDirs(t)

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	app := NewApp()
	tab := testTab("a", t.TempDir())
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID
	defer tab.Ctrl.Close()

	// Approval axis via the legacy setter: entry=legacy with pre/post on both
	// the approval mode and the autopilot flag.
	buf.Reset()
	app.SetToolApprovalModeForTab(tab.ID, control.ToolApprovalYolo)
	line := buf.String()
	if !strings.Contains(line, "[tab-mode] approval mode switch") {
		t.Fatalf("legacy approval switch missing the [tab-mode] anchor; got:\n%s", line)
	}
	for _, field := range []string{"entry=legacy", "approval_pre=", "approval_post=yolo", "autopilot_pre=", "autopilot_post=", "tier_yolo_leave=false"} {
		if !strings.Contains(line, field) {
			t.Fatalf("legacy approval switch line missing %q; got:\n%s", field, line)
		}
	}

	// Approval axis via the task-595 tier bar: entry=tier — the entry
	// distinction is the whole point of the P2 line.
	buf.Reset()
	app.SetApprovalTierForTab(tab.ID, "yolo")
	line = buf.String()
	if !strings.Contains(line, "[tab-mode] approval mode switch") || !strings.Contains(line, "entry=tier") {
		t.Fatalf("tier approval switch missing the [tab-mode] anchor with entry=tier; got:\n%s", line)
	}

	// Collaboration axis: mode plus autopilot pre/post and the resulting
	// approval posture, joinable with the approval-axis line by the tag.
	buf.Reset()
	app.SetCollaborationModeForTab(tab.ID, "plan")
	line = buf.String()
	if !strings.Contains(line, "[tab-mode] collaboration mode switch") {
		t.Fatalf("collaboration switch missing the [tab-mode] anchor; got:\n%s", line)
	}
	for _, field := range []string{"mode=plan", "autopilot_pre=", "autopilot_post=", "approval_post="} {
		if !strings.Contains(line, field) {
			t.Fatalf("collaboration switch line missing %q; got:\n%s", field, line)
		}
	}
}
