package jobs

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/event"
)

// 任务1033: the capsule hover card was truncated because the job Label is a
// display-sized preview (the bash tool's 48-rune commandPreview). The Detail
// field carries the untruncated source text alongside it; these tests pin the
// channel end to end within the package: Start variant → running View →
// persisted meta → recovered Job.

func testLongCommand(t *testing.T) string {
	t.Helper()
	segments := make([]string, 0, 12)
	for i := 0; i < 12; i++ {
		segments = append(segments, "npm run build --silent && node scripts/check-bundle-budget.mjs")
	}
	command := strings.Join(segments, " && ")
	if len([]rune(command)) <= 48 {
		t.Fatalf("fixture is shorter than the 48-rune preview cap: %d", len([]rune(command)))
	}
	return command
}

func TestStartForSessionDetailCarriesFullText(t *testing.T) {
	m := NewManager(event.Discard)
	defer m.Close()
	command := testLongCommand(t)
	preview := "npm run build --silent &&…"

	j := m.StartForSessionDetail("session-a", "bash", preview, command, func(context.Context, io.Writer) (string, error) {
		time.Sleep(50 * time.Millisecond)
		return "", nil
	})
	views := m.RunningForSession("session-a")
	if len(views) != 1 || views[0].ID != j.ID {
		t.Fatalf("running views = %+v, want exactly job %s", views, j.ID)
	}
	if views[0].Detail != command {
		t.Fatalf("View.Detail = %q, want the full %d-char command", views[0].Detail, len(command))
	}
	if views[0].Label != preview {
		t.Fatalf("View.Label = %q, want the untouched preview %q", views[0].Label, preview)
	}

	// The legacy Start/StartForSession path keeps Detail empty — task-kind jobs
	// and any other caller opt in explicitly.
	legacy := m.StartForSession("session-a", "task", "legacy", func(context.Context, io.Writer) (string, error) {
		return "done", nil
	})
	waitFor(t, func() bool {
		for _, view := range m.RunningForSession("session-a") {
			if view.ID == legacy.ID {
				return false
			}
		}
		return true
	})
	for _, view := range m.RunningForSession("session-a") {
		if view.ID == legacy.ID {
			t.Fatalf("legacy job still listed as running: %+v", view)
		}
	}
}

func TestStartForSessionDetailEmptyWithoutDetail(t *testing.T) {
	m := NewManager(event.Discard)
	defer m.Close()
	j := m.StartForSession("session-a", "bash", "preview only", func(context.Context, io.Writer) (string, error) {
		return "", nil
	})
	views := m.RunningForSession("session-a")
	if len(views) != 1 || views[0].ID != j.ID {
		t.Fatalf("running views = %+v, want exactly job %s", views, j.ID)
	}
	if views[0].Detail != "" {
		t.Fatalf("Detail = %q, want empty for the legacy start path", views[0].Detail)
	}
}

func TestJobDetailSurvivesArtifactMetaRoundTrip(t *testing.T) {
	m := NewManager(event.Discard)
	command := testLongCommand(t)
	j := m.StartForSessionDetail("session-meta", "bash", "preview", command, func(context.Context, io.Writer) (string, error) {
		return "", nil
	})

	// The start-time meta write (writeJobMetaLocked from StartForSessionDetail)
	// must already persist Detail so a crash right after start still recovers
	// the full command.
	j.mu.Lock()
	metaPath := j.artifactMetaPath
	j.mu.Unlock()
	raw, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatalf("read start-time meta %s: %v", metaPath, err)
	}
	var persisted artifactMeta
	if err := json.Unmarshal(raw, &persisted); err != nil {
		t.Fatalf("unmarshal meta: %v", err)
	}
	if persisted.Detail != command {
		t.Fatalf("persisted meta Detail = %q, want the full command", persisted.Detail)
	}
	m.Close()

	// Restore path: a meta on disk (as recovery loads it) must rebuild a Job
	// whose Detail reaches consumers again.
	root := t.TempDir()
	transcript := filepath.Join(root, "s.jsonl")
	artifactDir := ArtifactDir(transcript)
	if err := os.MkdirAll(artifactDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeArtifactMetaFile(t, artifactDir, "bash-9.json", artifactMeta{
		ID:               "bash-9",
		Kind:             "bash",
		Label:            "truncated preview…",
		Detail:           command,
		Status:           Done,
		StartedAt:        1,
		FinishedAt:       2,
		ArtifactComplete: true,
	})
	restored := NewManager(event.Discard)
	defer restored.Close()
	restored.SetActiveSessionPath("sess", transcript)
	job := restored.get("sess", "bash-9")
	if job == nil {
		t.Fatal("recovery did not register bash-9")
	}
	if job.Detail != command {
		t.Fatalf("recovered Job.Detail = %q, want the full command", job.Detail)
	}
}
