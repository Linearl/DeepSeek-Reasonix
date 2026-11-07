package permission

import (
	"strings"
	"testing"
)

// TestUserDeclinedFeedbackTl1480 pins the tl1480 deny-feedback refusal shape:
// empty feedback stays byte-identical to the legacy sentence (纯拒绝零变化),
// non-empty feedback keeps the sentence as prefix and quotes the user text.
func TestUserDeclinedFeedbackTl1480(t *testing.T) {
	if UserDeclinedFeedback("") != UserDeclinedReason {
		t.Fatalf("empty feedback drifted from the legacy sentence: %q", UserDeclinedFeedback(""))
	}
	if UserDeclinedFeedback("   ") != UserDeclinedReason {
		t.Fatalf("whitespace-only feedback must behave as pure deny: %q", UserDeclinedFeedback("   "))
	}
	if UserFeedbackSuffix("") != "" {
		t.Fatalf("empty suffix must be empty: %q", UserFeedbackSuffix(""))
	}
	got := UserDeclinedFeedback("不要动 main 分支")
	if !strings.HasPrefix(got, UserDeclinedReason) {
		t.Fatalf("feedback refusal lost the declined sentence prefix: %q", got)
	}
	if !strings.Contains(got, `"不要动 main 分支"`) {
		t.Fatalf("feedback refusal does not quote the user text: %q", got)
	}
}
