package agent

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

// Task 551: the task-244 B9 rejection gate is gone (it judged the turn-level
// attachment candidates instead of the subagent's own inputs). What must keep
// working is the opt-in forwarding of those candidates to the child, and —
// because the criterion is inherited ctx state — the structured log line that
// records who received which attachments (its absence cost two misdiagnoses).

func captureForwardLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(old) })
	return &buf
}

func TestForwardTurnAttachmentsOptInForwardsAndLogs(t *testing.T) {
	buf := captureForwardLogs(t)
	parent := WithSubagentImageCandidates(context.Background(),
		[]string{"data:image/png;base64,AAAA", "data:image/png;base64,BBBB"})

	child := forwardTurnAttachments(parent, "text-model", "sess-1")

	images := userImages(child)
	if len(images) != 2 || images[0] != "data:image/png;base64,AAAA" {
		t.Fatalf("child images = %v, want both parent candidates forwarded", images)
	}
	line := buf.String()
	for _, want := range []string{
		"subagent: forwarding turn attachments",
		"modelRef=text-model",
		"count=2",
		"session=sess-1",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("forward log must contain %q, got: %s", want, line)
		}
	}
}

func TestForwardTurnAttachmentsImagelessIsSilentNoop(t *testing.T) {
	buf := captureForwardLogs(t)
	parent := context.Background()

	child := forwardTurnAttachments(parent, "text-model", "sess-1")

	if images := userImages(child); len(images) != 0 {
		t.Fatalf("child images = %v, want none on an imageless turn", images)
	}
	if line := buf.String(); line != "" {
		t.Fatalf("imageless turn must not log, got: %s", line)
	}
}

func TestSubagentSessionNamePrefersCacheSessionID(t *testing.T) {
	sess := &Session{cacheSessionID: "cache-abc"}
	if got := subagentSessionName(sess, "subagent:ref-1"); got != "cache-abc" {
		t.Fatalf("subagentSessionName = %q, want the cache session id", got)
	}
	if got := subagentSessionName(nil, "subagent:ref-1"); got != "subagent:ref-1" {
		t.Fatalf("subagentSessionName = %q, want the recovery task ref fallback", got)
	}
	if got := subagentSessionName(&Session{}, "subagent:ref-1"); got != "subagent:ref-1" {
		t.Fatalf("subagentSessionName = %q, want the fallback when the cache id is empty", got)
	}
}
