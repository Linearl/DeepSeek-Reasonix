package agent

import "testing"

// #6 P0-c: the model announcing the next step instead of taking it.
func TestShouldNudgeStalledIntent(t *testing.T) {
	for _, text := range []string{
		"I'll now run the tests to confirm.",
		"Next, I will check the failing package.",
		"Let me now verify the build.",
		"About to patch the handler.",
	} {
		if !shouldNudgeStalledIntent(text) {
			t.Errorf("%q announces the next step and must be nudged", text)
		}
	}
	for _, text := range []string{
		"",
		"Done: the tests pass and the build is clean.",
		"The fix is applied; here is the diff.",
	} {
		if shouldNudgeStalledIntent(text) {
			t.Errorf("%q is a final answer and must not be nudged", text)
		}
	}
}
