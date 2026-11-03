package control

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/sessioninbox"
)

// tl1347: the steer loader reads the durable blob at consume time. When the
// reference layer fails but the body itself was readable, the loader must wrap
// the body in agent.SteerBodyError so the unapplied-steer notice shows the
// user's original guidance ("原文可见可复制") instead of a placeholder.
func TestInboxSteerLoaderCarriesBodyOnReferenceFailure(t *testing.T) {
	st, err := sessioninbox.Open(filepath.Join(t.TempDir(), "session.jsonl"), sessioninbox.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := st.Enqueue(sessioninbox.EnqueueRequest{
		Intent: sessioninbox.IntentSteer,
		Envelope: sessioninbox.PromptEnvelope{
			DisplayText:     "先看这张图再继续",
			SubmitText:      "先看这张图再继续 @missing.png",
			ReferenceErrors: []string{"@missing.png — file not found"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = inboxSteerLoader(st, rec.ItemID)()
	if err == nil {
		t.Fatal("loader succeeded, want a reference failure")
	}
	var bodyErr *agent.SteerBodyError
	if !errors.As(err, &bodyErr) {
		t.Fatalf("err = %v (%T), want *agent.SteerBodyError", err, err)
	}
	if bodyErr.Body != "先看这张图再继续 @missing.png" {
		t.Fatalf("body = %q, want the original submit text", bodyErr.Body)
	}
	if !strings.Contains(err.Error(), "frozen reference unavailable") {
		t.Fatalf("err text = %q, want the frozen-reference reason preserved", err.Error())
	}
}
