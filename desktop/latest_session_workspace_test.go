package main

import (
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/agent"
)

// Task 546: the Wails payload mirrors the shared agent resolution one-to-one —
// usable verdict, stale path preservation, and a best-effort display title.
func TestLastSessionWorkspaceInfoMapsLatest(t *testing.T) {
	project := t.TempDir()
	at := time.Now().Add(-time.Hour)
	latest := agent.LatestSessionWorkspace{
		SessionPath:    filepath.Join("store", "20261006_100000_alpha.jsonl"),
		WorkspaceRoot:  filepath.Clean(project),
		Usable:         true,
		LastActivityAt: at,
		CustomTitle:    "Alpha work",
		TopicTitle:     "topic title",
	}
	info := lastSessionWorkspaceInfo(latest, true)
	if info.Path != filepath.Clean(project) {
		t.Fatalf("Path = %q, want %q", info.Path, filepath.Clean(project))
	}
	if !info.Usable {
		t.Fatal("Usable must carry over")
	}
	if info.SessionTitle != "Alpha work" {
		t.Fatalf("SessionTitle = %q, want the custom title", info.SessionTitle)
	}
	if info.LastActivityAt != at.UnixMilli() {
		t.Fatalf("LastActivityAt = %d, want %d", info.LastActivityAt, at.UnixMilli())
	}
}

// A stale root must reach the frontend unchanged so the fallback notice can
// name the dead path; the title falls back to the topic title.
func TestLastSessionWorkspaceInfoKeepsStalePathAndTitleFallback(t *testing.T) {
	latest := agent.LatestSessionWorkspace{
		SessionPath:   filepath.Join("store", "20261006_100000_beta.jsonl"),
		WorkspaceRoot: filepath.Clean(filepath.Join(t.TempDir(), "gone")),
		Usable:        false,
		TopicTitle:    "Beta topic",
	}
	info := lastSessionWorkspaceInfo(latest, true)
	if info.Usable {
		t.Fatal("stale root must stay not-usable")
	}
	if info.SessionTitle != "Beta topic" {
		t.Fatalf("SessionTitle = %q, want the topic title fallback", info.SessionTitle)
	}
	if info.Path == "" {
		t.Fatal("stale path must not be blanked out")
	}
}

// No candidate anywhere → empty payload (frontend hides the option).
func TestLastSessionWorkspaceInfoEmptyWhenNoCandidate(t *testing.T) {
	if info := lastSessionWorkspaceInfo(agent.LatestSessionWorkspace{}, false); info != (LastSessionWorkspaceInfo{}) {
		t.Fatalf("expected zero payload, got %+v", info)
	}
}

// Title fallback ends at the transcript file name.
func TestLatestSessionDisplayTitleFallsBackToFileName(t *testing.T) {
	got := latestSessionDisplayTitle("", "", filepath.Join("store", "20261006_100000_gamma.jsonl"))
	if got != "20261006_100000_gamma" {
		t.Fatalf("title = %q, want the transcript file stem", got)
	}
	if latestSessionDisplayTitle("  ", "", "x.jsonl") != "x" {
		t.Fatal("blank custom/topic titles must fall through to the file stem")
	}
}
