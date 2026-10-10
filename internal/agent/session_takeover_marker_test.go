package agent

import (
	"strings"
	"testing"
	"time"
)

func TestParseTakeoverMarkerStates(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		kind    string
		target  string
		writer  string
		handoff string
	}{
		{"plain writer id", "serve-writer-1", TakeoverMarkerKindRequest, "serve-writer-1", "", ""},
		{"plain with whitespace", "  serve-writer-1\n", TakeoverMarkerKindRequest, "serve-writer-1", "", ""},
		{"pending", "pending:serve-writer-1", TakeoverMarkerKindPending, "serve-writer-1", "", ""},
		{"yielded", "yielded:desktop-w-7:desktop-w-7-takeover-42", TakeoverMarkerKindYielded, "", "desktop-w-7", "desktop-w-7-takeover-42"},
		{"yielded missing handoff", "yielded:desktop-w-7", TakeoverMarkerKindYielded, "", "desktop-w-7", ""},
		{"empty", "", TakeoverMarkerKindUnknown, "", "", ""},
		{"blank", "   \n", TakeoverMarkerKindUnknown, "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseTakeoverMarker(tc.raw)
			if got.Kind != tc.kind || got.TargetWriterID != tc.target || got.WriterID != tc.writer || got.HandoffID != tc.handoff {
				t.Fatalf("ParseTakeoverMarker(%q) = %+v, want kind=%q target=%q writer=%q handoff=%q",
					tc.raw, got, tc.kind, tc.target, tc.writer, tc.handoff)
			}
		})
	}
}

func TestFormatTakeoverMarkerRoundTrip(t *testing.T) {
	pending := FormatTakeoverMarkerPending("serve-writer-1")
	if !strings.HasPrefix(pending, TakeoverMarkerPendingPrefix) {
		t.Fatalf("pending format = %q", pending)
	}
	if got := ParseTakeoverMarker(pending); got.Kind != TakeoverMarkerKindPending || got.TargetWriterID != "serve-writer-1" {
		t.Fatalf("pending round trip = %+v", got)
	}
	yielded := FormatTakeoverMarkerYielded("desktop-w-7", "handoff-9")
	if got := ParseTakeoverMarker(yielded); got.Kind != TakeoverMarkerKindYielded || got.WriterID != "desktop-w-7" || got.HandoffID != "handoff-9" {
		t.Fatalf("yielded round trip = %+v", got)
	}
}

func TestTakeoverRequestMarkerPath(t *testing.T) {
	got := TakeoverRequestMarkerPath("/tmp/sessions/abc.jsonl")
	if got != "/tmp/sessions/abc.takeover-request" {
		t.Fatalf("marker path = %q", got)
	}
}

func TestSessionTakeoverYieldWindowAligned(t *testing.T) {
	// The 539 approval pins T2 to the handoff reservation window so the whole
	// yield chain fits one uniform 30s budget. If either changes, revisit the
	// desktop grace (SessionTakeoverYieldWindow + grace) deliberately.
	if SessionTakeoverYieldWindow != 30*time.Second || SessionLeaseHandoffWindow != 30*time.Second {
		t.Fatalf("yield window %v / handoff window %v, want both 30s", SessionTakeoverYieldWindow, SessionLeaseHandoffWindow)
	}
}
