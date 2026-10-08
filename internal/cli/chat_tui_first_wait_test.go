package cli

import (
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/i18n"
)

func TestWorkingLineShowsFirstWaitHint(t *testing.T) {
	// Task 370: before the first model output lands, silence and a hang look
	// identical. Past the soft threshold the working line must say what the
	// silence is; before the threshold and after output arrives it must not.
	m := newTestChatTUIWithMessages(t, "")
	m.state = tuiRunning

	m.elapsed = 5
	m.gotOutput = false
	if line := m.runningWorkingLine(false, false); strings.Contains(line, i18n.M.ChatStatusFirstWaitHint) {
		t.Fatalf("hint shown before the threshold: %s", line)
	}

	m.elapsed = firstResponseHintSecs
	if line := m.runningWorkingLine(false, false); !strings.Contains(line, i18n.M.ChatStatusFirstWaitHint) {
		t.Fatalf("hint missing past %ds with no output: %s", firstResponseHintSecs, line)
	}

	m.gotOutput = true
	if line := m.runningWorkingLine(false, false); strings.Contains(line, i18n.M.ChatStatusFirstWaitHint) {
		t.Fatalf("hint shown after output arrived: %s", line)
	}
}

func TestGotOutputSetByFirstModelEvents(t *testing.T) {
	// Reasoning, text and full tool dispatch each flip the flag.
	for _, tc := range []struct {
		name string
		make func() event.Event
	}{
		{"reasoning", func() event.Event { return event.Event{Kind: event.Reasoning, Text: "…"} }},
		{"text", func() event.Event { return event.Event{Kind: event.Text, Text: "answer"} }},
		{"tool_dispatch", func() event.Event {
			return event.Event{Kind: event.ToolDispatch, Tool: event.Tool{ID: "t1", Name: "bash"}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestChatTUIWithMessages(t, "")
			m.state = tuiRunning
			m.ingestEvent(tc.make())
			if !m.gotOutput {
				t.Fatalf("%s did not set gotOutput", tc.name)
			}
		})
	}

	// A partial dispatch (streaming args preview) must not flip the flag —
	// the full dispatch follows and sets it.
	m := newTestChatTUIWithMessages(t, "")
	m.state = tuiRunning
	m.ingestEvent(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{ID: "t1", Name: "bash", Partial: true}})
	if m.gotOutput {
		t.Fatalf("partial dispatch set gotOutput")
	}
}
