package main

import (
	"testing"

	"reasonix/internal/unfiled"
)

// TestUnfiledWhitelistCoversDesktopHostPrompts keeps the reconciliation
// whitelist in sync with the desktop-owned injection constants. The unfiled
// package carries its own copies (internal cannot import desktop); when a
// prompt text changes here, update internal/unfiled too — this test fails
// first.
func TestUnfiledWhitelistCoversDesktopHostPrompts(t *testing.T) {
	for name, prompt := range map[string]string{
		"autopilotAbnormalResumePrompt": autopilotAbnormalResumePrompt,
		"autopilotResumePrompt":         autopilotResumePrompt,
		"autonomousUpdateResumePrompt":  autonomousUpdateResumePrompt,
	} {
		if !unfiled.IsSystemPrompt(prompt) {
			t.Errorf("%s is not whitelisted in internal/unfiled; an auto-resume turn would surface as a phantom unfiled input", name)
		}
	}
}

func TestUnfiledInputsForTabWithoutControllerIsEmpty(t *testing.T) {
	a := &App{}
	if got := a.UnfiledInputsForTab("no-such-tab"); got == nil || len(got) != 0 {
		t.Fatalf("expected empty (non-nil) list, got %+v", got)
	}
}
