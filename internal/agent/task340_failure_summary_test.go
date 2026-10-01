package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"reasonix/internal/agent/testutil"
	"reasonix/internal/event"
	"reasonix/internal/provider"
)

// Task 340 (upstream #10778, fixes #10254): an interrupted turn that carries a
// real provider error must keep that error visible across a history reload.
// The durable interrupted-turn record gains a bounded, credential-scrubbed
// summary of the terminal error - the diagnostic alone classifies the failure
// but carries no message, so a reload used to show only "failed (HTTP 402)"
// while the actual cause stayed invisible.

func TestInterruptedRecoveryKeepsRedactedFailureSummary(t *testing.T) {
	body := `{"error":{"message":"insufficient balance, key sk-live-abcdef123456 registered"}}`
	apiErr := &provider.APIError{Provider: "relay", ProviderDisplayName: "Relay", Protocol: "openai", Status: 402, Body: body}
	mp := testutil.NewMock("m", testutil.ErrorTurn(apiErr))
	a := New(mp, echoRegistry(), NewSession(""), Options{}, event.Discard)
	if err := a.Run(withNoClosedLoop(context.Background()), "check bug"); !errors.Is(err, apiErr) {
		t.Fatalf("Run error = %v", err)
	}
	last := a.Session().Messages[len(a.Session().Messages)-1]
	recovery := last.InterruptedTurn
	if recovery == nil || recovery.TerminalStatus != "failed" {
		t.Fatalf("failed recovery = %+v", recovery)
	}
	summary := recovery.FailureSummary
	if summary == "" {
		t.Fatal("failed turn must persist a failure summary")
	}
	// The real cause survives: the status and the body snippet.
	if !strings.Contains(summary, "402") || !strings.Contains(summary, "insufficient balance") {
		t.Fatalf("failure summary lost the real cause: %q", summary)
	}
	// The credential does not (418 RedactError discipline, applied before the
	// record becomes durable).
	if strings.Contains(summary, "sk-live-abcdef123456") {
		t.Fatalf("failure summary leaked the credential: %q", summary)
	}
}

func TestInterruptedRecoverySummaryAbsentOnCancel(t *testing.T) {
	a := New(testutil.NewMock("m"), echoRegistry(), NewSession(""), Options{}, event.Discard)
	a.recordInterruptedDisplay("partial", "", nil, true, context.Canceled, 0)
	last := a.Session().Messages[len(a.Session().Messages)-1]
	if last.InterruptedTurn == nil || last.InterruptedTurn.TerminalStatus != "interrupted" {
		t.Fatalf("cancelled recovery = %+v", last.InterruptedTurn)
	}
	if last.InterruptedTurn.FailureSummary != "" {
		t.Fatalf("a user stop must not persist a failure summary: %q", last.InterruptedTurn.FailureSummary)
	}
}

func TestInterruptedFailureSummaryBoundsAndKeepsUTF8(t *testing.T) {
	if got := interruptedFailureSummary(nil); got != "" {
		t.Fatalf("nil error summary = %q", got)
	}
	// Multibyte text cut at the byte bound stays valid UTF-8.
	long := strings.Repeat("余额不足，", 400) // 5 bytes per repetition, 2000 bytes
	got := interruptedFailureSummary(errors.New(long))
	if len(got) > 515 { // 512-byte cut + the 3-byte ellipsis
		t.Fatalf("summary not bounded: %d bytes", len(got))
	}
	if !utf8.ValidString(got) {
		t.Fatalf("summary cut mid-rune: %q", got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("bounded summary must mark the cut: %q", got)
	}
}
