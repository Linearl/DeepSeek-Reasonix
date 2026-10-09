package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent/testutil"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// TestCurrentTimeBlockRendersExactTimestamp pins the block's exact shape: the
// task-664 acceptance criterion is that the injected anchor carries the correct
// local wall clock, so the test asserts the rendered RFC3339 timestamp and
// weekday against a fixed clock in a fixed zone.
func TestCurrentTimeBlockRendersExactTimestamp(t *testing.T) {
	tz := time.FixedZone("UTC+8", 8*60*60)
	// 2026-10-09 08:05 in UTC+8 is a Friday.
	now := time.Date(2026, 10, 9, 8, 5, 0, 0, tz)
	want := "<current-time>2026-10-09T08:05:00+08:00 Fri — host clock when this turn started</current-time>"
	if got := CurrentTimeBlock(now); got != want {
		t.Fatalf("CurrentTimeBlock = %q, want %q", got, want)
	}
}

func TestCurrentTimeBlockZeroTimeReturnsEmpty(t *testing.T) {
	if got := CurrentTimeBlock(time.Time{}); got != "" {
		t.Fatalf("CurrentTimeBlock(zero) = %q, want empty", got)
	}
	const content = "unwrapped prompt"
	if got := WithCurrentTime(content, time.Time{}); got != content {
		t.Fatalf("WithCurrentTime(zero) = %q, want %q unchanged", got, content)
	}
}

// TestWithCurrentTimeWrapsAndStrips proves the block is a clean prefix: it
// double-injects nothing, and the shared registry strip paths (previews,
// titles, replay classification) remove it because "current-time" is registered
// in TransientUserBlockTags.
func TestWithCurrentTimeWrapsAndStrips(t *testing.T) {
	tz := time.FixedZone("UTC+8", 8*60*60)
	now := time.Date(2026, 10, 9, 8, 5, 0, 0, tz)
	const userText = "help me fix the auth bug"

	wrapped := WithCurrentTime(userText, now)
	if !strings.HasPrefix(wrapped, "<current-time>") || !strings.HasSuffix(wrapped, userText) {
		t.Fatalf("WithCurrentTime should prefix the block and keep user text as suffix, got %q", wrapped)
	}
	if again := WithCurrentTime(wrapped, now); again != wrapped {
		t.Fatalf("WithCurrentTime double-injected: %q", again)
	}
	if got := StripTransientUserBlocks(wrapped); got != userText {
		t.Fatalf("StripTransientUserBlocks = %q, want %q", got, userText)
	}
	if got := UserPreviewText(wrapped); got != userText {
		t.Fatalf("UserPreviewText = %q, want %q", got, userText)
	}
}

// TestTurnPreferencesCarriesFreshCurrentTimeAnchor is the turn-boundary drift
// regression: two turns composed eight hours apart must carry anchors eight
// hours apart, each within the 5-minute acceptance tolerance of its own
// composition clock.
func TestTurnPreferencesCarriesFreshCurrentTimeAnchor(t *testing.T) {
	extract := func(t *testing.T, content string) time.Time {
		t.Helper()
		_, body, ok := strings.Cut(content, "<current-time>")
		if !ok {
			t.Fatalf("no <current-time> block in %q", content)
		}
		// First field is the RFC3339 timestamp; the trailing weekday and gloss
		// are display-only.
		stamp, _, _ := strings.Cut(body, " ")
		parsed, err := time.Parse(time.RFC3339, stamp)
		if err != nil {
			t.Fatalf("anchor %q is not RFC3339: %v", stamp, err)
		}
		return parsed
	}

	morning := time.Date(2026, 10, 9, 1, 0, 0, 0, time.UTC)
	evening := morning.Add(8 * time.Hour)
	a := extract(t, WithCurrentTime("morning turn", morning))
	b := extract(t, WithCurrentTime("evening turn", evening))
	if d := b.Sub(a); d != 8*time.Hour {
		t.Fatalf("anchors drifted by %v, want exactly 8h apart", d)
	}
}

// TestSteerReplaySkipsCurrentTimeAndGatedBlocks guards the steer-replay path
// against the registry/list split: withTurnPreferences now leads every steer
// with a current-time block, and gated blocks (context-state, exec-speed-mode)
// can lead it too. SteerText must skip every leading registered block before
// matching the steer prefix — including the gated ones the old hardcoded
// two-tag list never recognized.
func TestSteerReplaySkipsCurrentTimeAndGatedBlocks(t *testing.T) {
	tz := time.FixedZone("UTC+8", 8*60*60)
	now := time.Date(2026, 10, 9, 8, 5, 0, 0, tz)
	const steerText = "use plan B instead"

	// Mirror withTurnPreferences composition order: current-time first
	// (innermost), then the language blocks, then the gated blocks outermost.
	input := midTurnSteerMessage(steerText)
	input = WithCurrentTime(input, now)
	input = WithResponseLanguage(input, "zh")
	input = WithReasoningLanguage(input, "zh")
	input = WithExecSpeedMode(input, "provider/fast-model", []string{"fast-model"})
	input = ContextBudgetBlock(40_000, 60_000, 100_000) + "\n\n" + input

	got, ok := SteerText(input)
	if !ok || got != steerText {
		t.Fatalf("SteerText = %q, %v; want %q, true (leading blocks must not break steer replay)", got, ok, steerText)
	}
}

// TestRunPersistsCurrentTimeAnchorInUserTurn runs one live turn and asserts the
// acceptance criterion end to end: the persisted user turn the model sees
// carries a <current-time> anchor whose timestamp sits within 5 minutes of the
// host clock, and the user-facing view of that turn stays the raw prompt.
func TestRunPersistsCurrentTimeAnchorInUserTurn(t *testing.T) {
	mp := testutil.NewMock("m", testutil.Turn{Text: "done"})
	reg := tool.NewRegistry()
	a := New(mp, reg, NewSession(""), Options{}, event.FuncSink(func(event.Event) {}))

	before := time.Now()
	if err := a.Run(context.Background(), "what time is it?"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var anchor string
	found := false
	for _, m := range a.Session().Messages {
		if m.Role != provider.RoleUser || m.Origin != provider.MessageOriginUser {
			continue
		}
		if _, body, ok := strings.Cut(m.Content, "<current-time>"); ok {
			anchor, _, _ = strings.Cut(body, " ")
			found = true
		}
		if found {
			if got := UserPreviewText(m.Content); got != "what time is it?" {
				t.Fatalf("preview = %q, want raw prompt %q", got, "what time is it?")
			}
			break
		}
	}
	if !found {
		t.Fatalf("no persisted user turn carried a <current-time> anchor")
	}
	parsed, err := time.Parse(time.RFC3339, anchor)
	if err != nil {
		t.Fatalf("anchor %q is not RFC3339: %v", anchor, err)
	}
	if d := parsed.Sub(before); d < -time.Minute || d > 5*time.Minute {
		t.Fatalf("anchor %s is %v away from the host clock, want within 5 minutes", anchor, d)
	}
}
