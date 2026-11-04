package agent

import (
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

func TestReviewHintDueThresholds(t *testing.T) {
	cases := []struct {
		name                    string
		batches, watermark, est int64
		want                    bool
	}{
		{"fresh session below both", 1, 0, 5_000, false},
		{"token delta crosses from zero", 1, 0, 20_000, true},
		{"token delta crosses from watermark", 3, 80_000, 100_000, true},
		{"token delta below", 3, 80_000, 99_999, false},
		{"batch interval crosses", 25, 0, 100, true},
		{"batch below interval", 24, 0, 100, false},
		{"resumed long session hints once", 1, 0, 150_000, true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := reviewHintDue(tt.batches, tt.watermark, tt.est); got != tt.want {
				t.Fatalf("reviewHintDue(%d,%d,%d) = %v, want %v", tt.batches, tt.watermark, tt.est, got, tt.want)
			}
		})
	}
}

func TestReviewHintLineOneLine(t *testing.T) {
	line := reviewHintLine(123_456)
	if strings.Contains(line, "\n") || strings.Contains(line, "\r") {
		t.Fatalf("hint must be one line: %q", line)
	}
	if !strings.Contains(line, "123456") || !strings.Contains(line, "advisory") {
		t.Fatalf("hint missing token count or advisory marker: %q", line)
	}
}

// TestMaybeApplyReviewHintGating pins the finalize hook's face: the hint lands
// only on the batch's last, clean outcome, resets the counters, and the
// off-by-default switch appends nothing.
func TestMaybeApplyReviewHintGating(t *testing.T) {
	sess := &Session{Messages: []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "work"},
	}}
	// Switch on: reviewHint rides the immutable agent config via Options.
	a := New(&fakeProvider{reply: "unused"}, tool.NewRegistry(), sess, Options{ReviewHint: true}, event.Discard)
	// Force the due condition (batch interval crossed).
	a.reviewHintBatches.Store(reviewHintBatchInterval)

	last := &toolOutcome{output: "ok result"}
	a.maybeApplyReviewHint(last, true)
	if !strings.Contains(last.output, "[context review hint]") {
		t.Fatalf("hint not appended to the last outcome: %q", last.output)
	}
	if got := a.reviewHintBatches.Load(); got != 0 {
		t.Fatalf("batches counter not reset, got %d", got)
	}
	if a.reviewHintTokenWatermark.Load() <= 0 {
		t.Fatal("token watermark not stamped")
	}

	// Not the last index: never appended.
	other := &toolOutcome{output: "mid-batch result"}
	a.reviewHintBatches.Store(reviewHintBatchInterval)
	a.maybeApplyReviewHint(other, false)
	if strings.Contains(other.output, "[context review hint]") {
		t.Fatal("hint appended to a non-last outcome")
	}

	// Blocked / error stubs: advisory lines do not ride error spam.
	blocked := &toolOutcome{output: "blocked: x", blocked: true, errMsg: "blocked: x"}
	a.maybeApplyReviewHint(blocked, true)
	if strings.Contains(blocked.output, "[context review hint]") {
		t.Fatal("hint appended to a blocked stub")
	}

	// Below both thresholds: nothing appended.
	quiet := &toolOutcome{output: "small result"}
	a.reviewHintBatches.Store(1)
	a.reviewHintTokenWatermark.Store(1 << 30) // huge watermark → negative delta
	a.maybeApplyReviewHint(quiet, true)
	if strings.Contains(quiet.output, "[context review hint]") {
		t.Fatal("hint appended below both thresholds")
	}

	// Switch off (the default): the layer is fully inert even when due.
	off := New(&fakeProvider{reply: "unused"}, tool.NewRegistry(), &Session{Messages: []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "work"},
	}}, Options{}, event.Discard)
	off.reviewHintBatches.Store(reviewHintBatchInterval)
	dark := &toolOutcome{output: "untouched"}
	off.maybeApplyReviewHint(dark, true)
	if strings.Contains(dark.output, "[context review hint]") {
		t.Fatal("switch-off agent appended a hint — iron rule 2 regression")
	}
}
