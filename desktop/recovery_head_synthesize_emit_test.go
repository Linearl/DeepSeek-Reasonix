package main

// tl1402: the synthesis button ("合成最长链") rewrites the main transcript in
// place while an open tab still holds its pre-synthesis snapshot — the user
// reads the stale pane as lost history (20261105 实测：合成成功落盘 380→438
// 行，打开的对话却不刷新). The merge path's emission is pinned in
// recovery_consolidate_emit_test.go (tl1401); this pins the synthesis path:
// a successful SynthesizeSessionHeads must emit the same session:consolidated
// runtime event carrying the main path, so the frontend keeps exactly one
// subscription and can reload the transcript projection in place
// (reason=synthesize-done).

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/provider"
)

func TestSynthesizeSessionHeadsEmitsSessionConsolidatedEvent(t *testing.T) {
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	mainPath := filepath.Join(dir, "heads-synth-emit-1.jsonl")
	copyPath := filepath.Join(dir, "heads-synth-emit-1-recovery-0123456789abcdef.jsonl")
	t.Cleanup(func() {
		_ = os.Remove(mainPath)
		_ = os.Remove(copyPath)
		_ = os.Remove(copyPath + ".trashed")
	})
	// Main: sys + q1 + a1; the copy adds one unique turn so the synthesis has
	// real work (the same fixture shape the data-layer test uses).
	s := agent.NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "q1"})
	s.Add(provider.Message{Role: provider.RoleAssistant, Content: "a1"})
	if err := s.Save(mainPath); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatal(err)
	}
	copyBody := string(b) +
		`{"role":"user","content":"q2-unique-to-copy"}` + "\n" +
		`{"role":"assistant","content":"a2-unique-to-copy"}` + "\n"
	if err := os.WriteFile(copyPath, []byte(copyBody), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := agent.EnsureBranchMeta(copyPath); err != nil {
		t.Fatalf("ensure copy meta: %v", err)
	}

	a := newBranchDeleteApp(t)
	a.ctx = context.Background()
	emitted := make(chan runtimeEventEnvelope, 4)
	a.runtimeEvents.emit = func(_ context.Context, name string, payload ...any) {
		emitted <- runtimeEventEnvelope{name: name, payload: append([]any(nil), payload...)}
	}

	report, err := a.SynthesizeSessionHeads(mainPath, "")
	if err != nil {
		t.Fatalf("synthesize: %v", err)
	}
	// The copy becomes the main identity (promoted) — the merge engine's own
	// contract (the data-layer test pins the file half); Trashed stays empty
	// here because the winner is promoted, not archived.
	if !report.Promoted {
		t.Fatalf("the synthesis must promote the fullest branch, got %+v", report)
	}

	// The runtime emitter drains asynchronously (a goroutine behind the queue),
	// so the test must block on the channel instead of sampling it once.
	var ev *runtimeEventEnvelope
	deadline := time.After(2 * time.Second)
collect:
	for {
		select {
		case item := <-emitted:
			if item.name == "session:consolidated" {
				ev = &item
				break collect
			}
			continue
		case <-deadline:
			break collect
		}
	}
	if ev == nil {
		t.Fatal("a successful synthesis must emit session:consolidated so open tabs reload")
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
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.MainPath != mainPath {
		t.Fatalf("event mainPath must name the synthesized session, got %q want %q", payload.MainPath, mainPath)
	}
	if !payload.Promoted {
		t.Fatalf("event must report the promotion, got %+v", payload)
	}
}

func TestSynthesizeSessionHeadsRefusalEmitsNothing(t *testing.T) {
	// The single-chain refusal must stay silent: the failure is reported
	// through the call's error (the panel's outcome row + toast), and an event
	// on top would make open tabs reload a transcript that never changed.
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	mainPath := filepath.Join(dir, "heads-synth-emit-2.jsonl")
	t.Cleanup(func() { _ = os.Remove(mainPath) })
	s := agent.NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "only turn"})
	if err := s.Save(mainPath); err != nil {
		t.Fatal(err)
	}

	a := newBranchDeleteApp(t)
	a.ctx = context.Background()
	emitted := make(chan runtimeEventEnvelope, 4)
	a.runtimeEvents.emit = func(_ context.Context, name string, payload ...any) {
		emitted <- runtimeEventEnvelope{name: name, payload: append([]any(nil), payload...)}
	}

	if _, err := a.SynthesizeSessionHeads(mainPath, ""); err == nil {
		t.Fatal("a single-chain session must refuse synthesis")
	}
	// Give the async emitter drain a beat to surface anything wrongly emitted,
	// then assert silence.
	time.Sleep(100 * time.Millisecond)
	select {
	case item := <-emitted:
		if item.name == "session:consolidated" {
			t.Fatalf("a refused synthesis must not emit session:consolidated, got %+v", item)
		}
	default:
	}
}
