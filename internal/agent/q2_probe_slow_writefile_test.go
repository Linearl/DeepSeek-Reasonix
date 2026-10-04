package agent

// Q2 probe (task wt-zcode-q2, 2026-10-04): decomposed measurement for the
// "write_file shows 200+s in the UI" report. Zero product-code changes; every
// number this file prints comes from running this repo's own code at the
// incident scale (session 20260927-015341: 9318 messages / ~54MB).
//
// Measurements:
//  1. write_file.Execute on a 5561-byte payload (cold / overwrite / no-op).
//  2. Raw OS create+write+close+delete distribution (Windows Defender tail).
//  3. Session.Save at incident scale, sequential and under a saturated
//     save-path lock queue (the shape desktop.log showed: wait_ms 19-22s).
//  4. Session.Snapshot cost at scale, contended vs idle (what executeOne's
//     session-lock operations pay while a save holds s.mu).
//
// Scale: default 2000 messages so the suite stays fast; set
// Q2_PROBE_MESSAGES=9318 (and optionally Q2_PROBE_ARG_BYTES=5561) to replay the
// incident scale exactly. All output on stdout via t.Log; assertions only guard
// sanity, never wall-clock thresholds.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

func q2EnvInt(t *testing.T, key string, def int) int {
	t.Helper()
	if v := os.Getenv(key); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			t.Fatalf("bad %s: %v", key, err)
		}
		return n
	}
	return def
}

func q2Percentiles(xs []float64) (p50, p95, p99, max float64) {
	sort.Float64s(xs)
	pick := func(q float64) float64 {
		i := int(q * float64(len(xs)-1))
		return xs[i]
	}
	return pick(0.50), pick(0.95), pick(0.99), xs[len(xs)-1]
}

// q2IncidentSession builds a session shaped like 20260927-015341: most rounds
// are user → assistant(text + one write-sized tool call) → tool result, with
// the tail carrying a few-KB reasoning blob. Average message size lands near
// the real ~5.8KB.
func q2IncidentSession(t *testing.T, rounds int, argBytes int) *Session {
	t.Helper()
	s := NewSession("You are Reasonix, a coding agent.")
	args := fmt.Sprintf(`{"path":"docs/probe-%d.md","content":"%s"}`, rounds, string(make([]byte, argBytes)))
	for i := 0; i < rounds; i++ {
		s.Add(provider.Message{Role: provider.RoleUser, Content: fmt.Sprintf("probe prompt %d with some body text to reach realistic sizes", i)})
		s.Add(provider.Message{
			Role:            provider.RoleAssistant,
			Content:         fmt.Sprintf("round %d answer", i),
			ReasoningContent: fmt.Sprintf("reasoning blob %d: %s", i, string(make([]byte, 700))),
			ToolCalls: []provider.ToolCall{{
				ID:        fmt.Sprintf("call_%08d", i),
				Name:      "write_file",
				Arguments: args,
			}},
		})
		s.Add(provider.Message{Role: provider.RoleTool, ToolCallID: fmt.Sprintf("call_%08d", i), Name: "write_file", Content: fmt.Sprintf("wrote %d bytes to docs/probe-%d.md", argBytes, i)})
	}
	return s
}

func TestProbeQ2WriteFileExecuteLatency(t *testing.T) {
	argBytes := q2EnvInt(t, "Q2_PROBE_ARG_BYTES", 5561)
	content := make([]byte, argBytes)
	for i := range content {
		content[i] = byte('a' + i%26)
	}
	args, err := json.Marshal(map[string]string{"path": filepath.Join(t.TempDir(), "probe.md"), "content": string(content)})
	if err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(args)
	tv, ok := tool.LookupBuiltin("write_file")
	if !ok {
		t.Fatal("write_file not registered")
	}
	ctx := context.Background()

	// Cold (first Execute, new file).
	start := time.Now()
	out, err := tv.Execute(ctx, raw)
	cold := time.Since(start)
	if err != nil {
		t.Fatalf("cold execute: %v", err)
	}
	t.Logf("write_file cold create: %s (%s)", cold, out)

	// Overwrite (different content each round: full write path).
	var samples []float64
	for i := 0; i < 30; i++ {
		payload, _ := json.Marshal(map[string]string{"path": filepath.Join(t.TempDir(), fmt.Sprintf("ow-%d.md", i%4)), "content": string(content) + fmt.Sprint(i)})
		start = time.Now()
		if _, err := tv.Execute(ctx, payload); err != nil {
			t.Fatalf("overwrite %d: %v", i, err)
		}
		samples = append(samples, float64(time.Since(start).Microseconds())/1000)
	}
	p50, p95, p99, max := q2Percentiles(samples)
	t.Logf("write_file overwrite x30 ms: p50=%.3f p95=%.3f p99=%.3f max=%.3f", p50, p95, p99, max)

	// No-op (identical content → early return).
	noargs, _ := json.Marshal(map[string]string{"path": args2path(t, args), "content": string(content)})
	start = time.Now()
	if _, err := tv.Execute(ctx, noargs); err != nil {
		t.Fatalf("noop: %v", err)
	}
	t.Logf("write_file no-op path: %s", time.Since(start))
}

func args2path(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m["path"]
}

func TestProbeQ2OSFileCreateDistribution(t *testing.T) {
	dir := t.TempDir()
	var samples []float64
	payload := make([]byte, 5561)
	for i := range payload {
		payload[i] = byte('a' + i%26)
	}
	for i := 0; i < 200; i++ {
		p := filepath.Join(dir, fmt.Sprintf("f-%d.tmp", i))
		start := time.Now()
		if err := os.WriteFile(p, payload, 0o644); err != nil {
			t.Fatal(err)
		}
		samples = append(samples, float64(time.Since(start).Microseconds())/1000)
		os.Remove(p)
	}
	p50, p95, p99, max := q2Percentiles(samples)
	t.Logf("os create+write 5561B x200 ms: p50=%.3f p95=%.3f p99=%.3f max=%.3f", p50, p95, p99, max)
}

func TestProbeQ2SessionSaveAtIncidentScale(t *testing.T) {
	rounds := q2EnvInt(t, "Q2_PROBE_MESSAGES", 2000) / 3
	s := q2IncidentSession(t, rounds, q2EnvInt(t, "Q2_PROBE_ARG_BYTES", 5561))
	dir := t.TempDir()
	path := filepath.Join(dir, "probe-session.jsonl")

	// First save creates the store files.
	if err := s.Save(path); err != nil {
		t.Fatalf("initial save: %v", err)
	}
	t.Logf("session scale: messages=%d approxBytes=%.1fMB", s.Len(), float64(len(q2MeasureJSON(t, s)))/1e6)

	// Sequential saves: one clean message between each so nothing is skipped.
	var seq []float64
	for i := 0; i < 5; i++ {
		s.Add(provider.Message{Role: provider.RoleUser, Content: fmt.Sprintf("tick %d", i)})
		start := time.Now()
		if err := s.Save(path); err != nil {
			t.Fatalf("save %d: %v", i, err)
		}
		seq = append(seq, float64(time.Since(start).Milliseconds()))
	}
	t.Logf("Save() sequential ms: %v", seq)

	// Saturated queue: mimic the incident shape — a save holds the path lock
	// while the agent's own save queues behind it. The holder releases from a
	// separate goroutine (holding and waiting on the same goroutine would
	// deadlock on purpose — do NOT "fix" that by releasing in-line).
	release := q2HoldSavePath(t, path)
	go func() {
		time.Sleep(3 * time.Second)
		release()
	}()
	start := time.Now()
	if err := s.Save(path); err != nil {
		t.Fatalf("contended save: %v", err)
	}
	t.Logf("Save() queued behind a 3s in-flight save holder: total=%s (wait ≈ total - own locked work)", time.Since(start))
}

func q2MeasureJSON(t *testing.T, s *Session) []byte {
	t.Helper()
	msgs := s.Snapshot()
	b, err := json.Marshal(msgs)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func q2HoldSavePath(t *testing.T, path string) func() {
	t.Helper()
	// lockSessionSavePath is the same per-path mutex every saver takes.
	unlock := lockSessionSavePath(path)
	return unlock
}

func TestProbeQ2SessionSnapshotContention(t *testing.T) {
	rounds := q2EnvInt(t, "Q2_PROBE_MESSAGES", 2000) / 3
	s := q2IncidentSession(t, rounds, q2EnvInt(t, "Q2_PROBE_ARG_BYTES", 5561))
	path := filepath.Join(t.TempDir(), "probe-session.jsonl")
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}

	// Idle Snapshot latency.
	var idle []float64
	for i := 0; i < 10; i++ {
		start := time.Now()
		_ = s.Snapshot()
		idle = append(idle, float64(time.Since(start).Microseconds())/1000)
	}
	p50, p95, _, max := q2Percentiles(idle)
	t.Logf("Snapshot() idle ms: p50=%.3f p95=%.3f max=%.3f", p50, p95, max)

	// Contended: a save loop runs in the background (each save copies the
	// session under s.mu), while the foreground measures Snapshot() — the
	// shape of executeOne's recovery-record/intent writes waiting behind a save.
	// The loop is bounded: it caps its own save count and then idles on the
	// stop channel so the probe cannot run away at incident scale.
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		const maxSaves = 3
		for i := 0; i < maxSaves; i++ {
			select {
			case <-stop:
				return
			default:
			}
			s.Add(provider.Message{Role: provider.RoleUser, Content: fmt.Sprintf("bg %d", i)})
			_ = s.Save(path)
		}
		<-stop
	}()
	time.Sleep(100 * time.Millisecond)
	var busy []float64
	for i := 0; i < 40; i++ {
		start := time.Now()
		_ = s.Snapshot()
		busy = append(busy, float64(time.Since(start).Microseconds())/1000)
		time.Sleep(20 * time.Millisecond)
	}
	close(stop)
	wg.Wait()
	p50, p95, p99, max := q2Percentiles(busy)
	t.Logf("Snapshot() under save loop ms: p50=%.3f p95=%.3f p99=%.3f max=%.3f", p50, p95, p99, max)
}
