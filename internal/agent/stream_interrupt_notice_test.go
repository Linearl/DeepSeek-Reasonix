package agent

import (
	"errors"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
)

// 任务929 S1: every stream-interrupt reason renders as its own notice with a
// code the fronts localize by. server_error (the gateway's HTTP/2
// RST_STREAM / SSE error frame) is the new face that used to flatten into
// premature_eof; a reason the enum does not know must render nothing instead
// of leaking raw transport text (#9560).
func TestStreamInterruptNoticeCoversServerReset(t *testing.T) {
	cases := []struct {
		name   string
		reason string
		code   string
	}{
		{"gateway reset", provider.StreamInterruptServerError, event.NoticeCodeStreamInterruptedServerError},
		{"idle stall", provider.StreamInterruptIdleTimeout, event.NoticeCodeStreamInterruptedIdleTimeout},
		{"clean fin", provider.StreamInterruptPrematureEOF, event.NoticeCodeStreamInterruptedPrematureEOF},
		{"tcp reset", provider.StreamInterruptConnectionReset, event.NoticeCodeStreamInterruptedConnectionReset},
	}
	for _, tc := range cases {
		code, text := streamInterruptNotice(provider.StreamInterrupt(errors.New("boom"), tc.reason))
		if code != tc.code || text == "" {
			t.Errorf("%s: streamInterruptNotice = (%q, %q), want code %q and non-empty text", tc.name, code, text, tc.code)
		}
	}
	if code, text := streamInterruptNotice(errors.New("raw transport text with https://secret")); code != "" || text != "" {
		t.Errorf("unclassified error rendered a notice: (%q, %q), want none", code, text)
	}
	if code, text := streamInterruptNotice(nil); code != "" || text != "" {
		t.Errorf("nil rendered a notice: (%q, %q), want none", code, text)
	}
}
