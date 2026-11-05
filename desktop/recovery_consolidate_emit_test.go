package main

// tl1401: the consolidation rewrites the main transcript on disk while an open
// tab may still be showing its in-memory snapshot — the user reads the stale
// pane as lost history (20261105 实测：合成成功落盘 380→438 行，打开的对话
// 却不刷新). The sidebar refreshes via project-tree:changed-v2; the open tab
// needs its own signal. This pins the backend half: a successful merge (the
// same Promoted/Trashed gate that drives the catalog cleanup) must emit the
// session:consolidated runtime event carrying the main path, so the frontend
// can reload the transcript projection in place (reason=synthesize-done).

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
)

func TestConsolidateEmitsSessionConsolidatedEvent(t *testing.T) {
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// The copy must COVER the main transcript or the merge is refused (the
	// coverage proof): main holds "hi", the copy holds "hi" plus one extra turn,
	// so promoting the copy grafts the extra turn onto the main identity.
	mainPath := filepath.Join(dir, "sess-consolidate-emit-1.jsonl")
	copyPath := filepath.Join(dir, "sess-consolidate-emit-1-recovery-0123456789abcdef.jsonl")
	if err := os.WriteFile(mainPath, []byte(`{"id":"m1","role":"user","content":"hi"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(copyPath, []byte(`{"id":"c1","role":"user","content":"hi"}`+"\n"+`{"id":"c2","role":"assistant","content":"extra answer"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The consolidation loads branch meta for both sides (the coverage proof
	// reads it), so the sidecars must exist for the fixture to be mergeable.
	for _, p := range []string{mainPath, copyPath} {
		if err := agent.SaveBranchMeta(p, agent.BranchMeta{ID: filepath.Base(p)}); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_ = os.Remove(mainPath)
		_ = os.Remove(copyPath)
	})
	t.Cleanup(func() {
		_ = os.Remove(mainPath)
		_ = os.Remove(copyPath)
		_ = os.Remove(filepath.Join(dir, "sess-consolidate-emit-1-recovery-0123456789abcdef.jsonl.trashed"))
	})
	if !agent.LooksLikeRecoveryFilename(copyPath) {
		t.Fatalf("fixture must look like a recovery copy: %s", copyPath)
	}

	a := newBranchDeleteApp(t)
	a.ctx = context.Background()
	emitted := make(chan runtimeEventEnvelope, 4)
	a.runtimeEvents.emit = func(_ context.Context, name string, payload ...any) {
		emitted <- runtimeEventEnvelope{name: name, payload: append([]any(nil), payload...)}
	}

	report, err := a.ConsolidateSessionRecoveryCopies(mainPath, copyPath)
	if err != nil {
		t.Fatalf("consolidate: %v", err)
	}
	if !report.Promoted {
		t.Fatalf("the copy must be promoted onto the main identity, got %+v", report)
	}

	var ev *runtimeEventEnvelope
	// Emit is an async queue: the producer goroutine may lag the call, so wait
	// with a deadline instead of draining once.
	deadline := time.Now().Add(2 * time.Second)
	for ev == nil && time.Now().Before(deadline) {
		select {
		case item := <-emitted:
			if item.name == "session:consolidated" {
				ev = &item
			}
			continue
		case <-time.After(20 * time.Millisecond):
		}
	}
	if ev == nil {
		t.Fatal("a promoted consolidation must emit session:consolidated")
	}
	if len(ev.payload) != 1 {
		t.Fatalf("event must carry exactly one payload, got %d", len(ev.payload))
	}
	raw, err := json.Marshal(ev.payload[0])
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		MainPath string `json:"mainPath"`
		Promoted bool   `json:"promoted"`
		Trashed  []any  `json:"trashed"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.MainPath != mainPath {
		t.Fatalf("event mainPath must name the consolidated session, got %q want %q", payload.MainPath, mainPath)
	}
	if !payload.Promoted {
		t.Fatalf("event must report the promotion, got %+v", payload)
	}
}

func TestConsolidateWithoutCopiesEmitsNothing(t *testing.T) {
	// The refusal path (no recovery copies beside main) must stay silent: the
	// failure is reported through the call's error (the frontend toasts it),
	// and an event on top would make open tabs reload a transcript that was
	// never changed.
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	mainPath := filepath.Join(dir, "sess-consolidate-emit-2.jsonl")
	if err := os.WriteFile(mainPath, []byte(`{"id":"m1","role":"user","content":"hi"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(mainPath) })

	a := newBranchDeleteApp(t)
	a.ctx = context.Background()
	emitted := make(chan runtimeEventEnvelope, 4)
	a.runtimeEvents.emit = func(_ context.Context, name string, payload ...any) {
		emitted <- runtimeEventEnvelope{name: name, payload: append([]any(nil), payload...)}
	}

	_, err := a.ConsolidateSessionRecoveryCopies(mainPath, "")
	if err == nil || !strings.Contains(err.Error(), "no recovery copies") {
		t.Fatalf("a copy-less session must be refused, got %v", err)
	}
	select {
	case item := <-emitted:
		if item.name == "session:consolidated" {
			t.Fatalf("a refused consolidation must not emit session:consolidated, got %+v", item)
		}
	default:
	}
}
