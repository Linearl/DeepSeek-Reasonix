package agent

// 任务912 P1-d: a rejected provider key (HTTP 401) must announce itself as a
// global, throttled banner — not as a log line inside one failing turn.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
)

// 验收③: the compaction that hits the dead key raises exactly one banner (the
// throttle holds the retries back), and the banner carries the user's next
// step plus the provider's own actionable message.
func TestTask912ExpiredKeyRaisesGlobalAlertWithThrottle(t *testing.T) {
	shrinkSummaryBackoff(t)

	sess := foldableSessionOverForce(6)
	a := agentOverForce(t, &fakeProvider{streamErr: errors.New(
		`authentication failed for provider "MiMo TP · Chat Completions" (HTTP 401): REASONIX_CONNECTION_ABC_KEY from Reasonix credentials (.env) is invalid or expired — update it (in .env or your environment) and retry, or run ` + "`reasonix setup`")}, sess)
	var notices []event.Event
	a.svc.sink = event.FuncSink(func(e event.Event) {
		if e.Kind == event.Notice && e.Code == event.NoticeCodeCredentialExpired {
			notices = append(notices, e)
		}
	})

	_ = prepareContext(context.Background(), a, CompactionTriggerPressure)

	if len(notices) != 1 {
		t.Fatalf("credential alerts = %d, want exactly 1 (throttled per credential)", len(notices))
	}
	if notices[0].Level != event.LevelWarn {
		t.Fatalf("alert level = %v, want LevelWarn", notices[0].Level)
	}
	if notices[0].Text == "" {
		t.Fatal("alert text is empty — the user-facing next step must be present")
	}
	if !strings.Contains(notices[0].Detail, "401") {
		t.Fatalf("alert detail = %q, want the provider's 401 message", notices[0].Detail)
	}

	// A second rejection of the SAME credential inside the cooldown stays
	// quiet; a different credential announces itself immediately.
	a.alertCredentialExpired(errors.New(`authentication failed (HTTP 401): the key is invalid or expired`))
	if len(notices) != 1 {
		t.Fatalf("alerts after an in-cooldown repeat = %d, want still 1", len(notices))
	}
	a.alertCredentialExpired(&provider.AuthError{Provider: "deepseek", Status: 401})
	if len(notices) != 2 {
		t.Fatalf("alerts after a different credential = %d, want 2", len(notices))
	}
}

// The alert is narrow: only a key rejection raises it, and a nil sink agent
// (or a non-401 failure) is a no-op, so call sites never classify first.
func TestTask912AlertIgnoresNonAuthFailures(t *testing.T) {
	sess := foldableSessionOverForce(3)
	a := agentOverForce(t, &fakeProvider{reply: "ok"}, sess)
	var notices []event.Event
	a.svc.sink = event.FuncSink(func(e event.Event) {
		if e.Kind == event.Notice && e.Code == event.NoticeCodeCredentialExpired {
			notices = append(notices, e)
		}
	})

	for _, err := range []error{
		nil,
		errors.New("boom"),
		errors.New("context canceled"),
		errors.New("mimo-token-plan: Request rate limited"),
		&provider.AuthError{Provider: "mimo", Status: 403},
	} {
		a.alertCredentialExpired(err)
	}
	if len(notices) != 0 {
		t.Fatalf("credential alerts = %d for non-401 failures, want 0", len(notices))
	}
}
