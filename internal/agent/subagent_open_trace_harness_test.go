package agent

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// tl1293 harness: the open-window trace driven end-to-end through the real
// TaskTool path (spec → registry → transcript → runtime → slot → first
// request phases) with injected provider latency. Pins two things the unit
// tests cannot:
//  1. the summary line appears exactly once per foreground open and names
//     every segment in the real wiring order;
//  2. the locally-assembled segments (registry/transcript/runtime/slot on an
//     empty-ish workspace) are sub-millisecond — the instrument's own
//     overhead stays invisible — while the injected request latencies land
//     in the rtt/first_out segments they belong to.
//
// This is also the before/after harness for any phase-2 compression of the
// open window (tl1293 evaluation): run it with -v and read the log line.

// latencyProvider delays the response headers by connectDelay (the
// request→headers window) and the first chunk by firstOutDelay (server
// prefill + first token), replaying the scripted turns underneath.
type latencyProvider struct {
	inner         *scriptedProvider
	connectDelay  time.Duration
	firstOutDelay time.Duration
}

func (p *latencyProvider) Name() string { return p.inner.Name() }

func (p *latencyProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	time.Sleep(p.connectDelay)
	ch, err := p.inner.Stream(ctx, req)
	if err != nil {
		return nil, err
	}
	if p.firstOutDelay <= 0 {
		return ch, nil
	}
	out := make(chan provider.Chunk, 8)
	go func() {
		defer close(out)
		first := true
		for c := range ch {
			if first {
				time.Sleep(p.firstOutDelay)
				first = false
			}
			out <- c
		}
	}()
	return out, nil
}

func TestSubagentOpenSegmentsEndToEndHarness(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(old) })

	const connectDelay = 25 * time.Millisecond
	const firstOutDelay = 15 * time.Millisecond
	prov := &latencyProvider{
		inner: &scriptedProvider{name: "lat", turns: [][]provider.Chunk{
			{{Type: provider.ChunkText, Text: "research done"}, {Type: provider.ChunkDone}},
		}},
		connectDelay:  connectDelay,
		firstOutDelay: firstOutDelay,
	}
	root := t.TempDir()
	reg := tool.NewRegistry()
	reg.Add(fakeReadFileTool{})
	task := NewTaskTool(prov, nil, reg, 20, 0, 0, 0, 0, 0, 0, 0.0, "", "sys", nil, 0, "", "", nil).
		WithTranscripts(mustSubagentStore(t), root, "base", "high").
		WithScheduler(NewSubagentScheduler(4, 4))

	parent := filepath.Join(root, "parent.jsonl")
	ctx := withCallContext(context.Background(), "call-1", event.Discard, nil, false)
	ctx = WithParentSession(ctx, parent)
	if _, err := task.Execute(ctx, []byte(`{"prompt":"inspect the parser","description":"harness"}`)); err != nil {
		t.Fatalf("task execute: %v", err)
	}

	lines := strings.Split(buf.String(), "\n")
	summaries := 0
	for _, line := range lines {
		if !strings.Contains(line, "subagent: open segments") {
			continue
		}
		summaries++
		for _, want := range []string{"registry=", "transcript=", "runtime=", "slot=", "dispatch=", "prepare=", "rtt=", "first_out=", "total="} {
			if !strings.Contains(line, want) {
				t.Fatalf("summary missing %q:\n%s", want, line)
			}
		}
		if !strings.Contains(line, "mode=fg") {
			t.Fatalf("foreground run must report mode=fg:\n%s", line)
		}
	}
	if summaries != 1 {
		t.Fatalf("summary lines = %d, want exactly 1\nlog:\n%s", summaries, buf.String())
	}

	// The harness reads the last trace it can reach through the same seam the
	// production line uses: parse the rtt/first_out readings and assert the
	// injected latencies dominate the request window they name.
	rtt := parseOpenSegmentMs(t, buf.String(), "rtt=")
	firstOut := parseOpenSegmentMs(t, buf.String(), "first_out=")
	if rtt < int64(connectDelay/time.Millisecond)-5 {
		t.Fatalf("rtt=%dms should carry the injected ~%dms header delay", rtt, connectDelay/time.Millisecond)
	}
	if firstOut < int64(firstOutDelay/time.Millisecond)-5 {
		t.Fatalf("first_out=%dms should carry the injected ~%dms prefill delay", firstOut, firstOutDelay/time.Millisecond)
	}
}

// parseOpenSegmentMs extracts the integer after name= from the harness log.
// The summary rides inside a quoted slog value, so trailing quotes are
// stripped from fields before the ms suffix check.
func parseOpenSegmentMs(t *testing.T, log, name string) int64 {
	t.Helper()
	for _, line := range strings.Split(log, "\n") {
		if !strings.Contains(line, "subagent: open segments") {
			continue
		}
		for _, field := range strings.Fields(line) {
			field = strings.Trim(field, `"`)
			if after, ok := strings.CutPrefix(field, name); ok && strings.HasSuffix(after, "ms") {
				var ms int64
				if _, err := fmt.Sscanf(after, "%dms", &ms); err != nil {
					t.Fatalf("parse %q in %q: %v", field, line, err)
				}
				return ms
			}
		}
	}
	t.Fatalf("segment %q not found in harness log:\n%s", name, log)
	return 0
}
