package boot

import (
	"testing"

	"reasonix/internal/config"
)

// Block2 M-a: consumption rights follow the switch — the two MailStore
// consumers must never coexist. ON registers drain_inbox (pump skips); OFF
// (default) keeps the host pump as the sole consumer and withholds the tool.
func TestCollabDrainInboxGatedByBackgroundDelivery(t *testing.T) {
	if collabDrainInboxEnabled(&config.AgentConfig{}) {
		t.Fatal("default (switch off) must NOT register drain_inbox: the host pump owns MailStore consumption")
	}
	if !collabDrainInboxEnabled(&config.AgentConfig{ExperimentalCollabBackgroundDelivery: true}) {
		t.Fatal("switch on must register drain_inbox: the pump skips and drain_inbox is the sole consumer")
	}
	if collabDrainInboxEnabled(nil) {
		t.Fatal("nil config must not register the tool")
	}
}
