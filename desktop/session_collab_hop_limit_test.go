package main

import (
	"strings"
	"testing"

	"reasonix/internal/config"
	"reasonix/internal/sessioncollab"
)

// TestSessionCollabRefusedHopTextReportsConfiguredLimit: the refusal a sender sees must
// name the ceiling actually in force (task 204), not the built-in default.
func TestSessionCollabRefusedHopTextReportsConfiguredLimit(t *testing.T) {
	isolateDesktopUserDirs(t)
	cfg := config.Default()
	if err := cfg.SetSessionCollabHopLimit(7); err != nil {
		t.Fatal(err)
	}
	cfg.Agent.ExperimentalSessionCollab = true
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	text := sessionCollabRefusedHopText(sessioncollab.MailMessage{ID: "msg_test"})
	if !strings.Contains(text, "7") {
		t.Fatalf("refusal must report the configured ceiling: %q", text)
	}
	if !strings.Contains(text, "msg_test") {
		t.Fatalf("refusal must keep the message id: %q", text)
	}
}
