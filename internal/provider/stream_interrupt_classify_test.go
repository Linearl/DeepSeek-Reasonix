package provider

import (
	"errors"
	"fmt"
	"io"
	"syscall"
	"testing"
)

// 任务929 S1: the gateway's HTTP/2 RST_STREAM(INTERNAL_ERROR) must classify as
// its own reason (server_error), NOT flatten into premature_eof — that
// flattening made a deterministic gateway rejection read like a dropped proxy
// connection in the diagnostic payload. The RST often arrives wrapped in
// io.ErrUnexpectedEOF, so the case must win over the EOF one.
func TestClassifyStreamInterruptHTTP2ResetNotFlattened(t *testing.T) {
	rst := errors.New("stream error: stream ID 5; INTERNAL_ERROR; received from peer")
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"bare http2 rst", rst, StreamInterruptServerError},
		{"openai read-stream wrap", fmt.Errorf("mimo-token-plan: read stream: %w", rst), StreamInterruptServerError},
		{"rst wrapped in unexpected EOF (ordering guard)", fmt.Errorf("%w: %w", io.ErrUnexpectedEOF, rst), StreamInterruptServerError},
		{"other server reset code", errors.New("stream error: stream ID 7; REFUSED_STREAM; received from peer"), StreamInterruptServerError},
		{"idle stall text", errors.New("mimo-token-plan: stream stalled — no data for 5m0s, connection likely dropped: unexpected EOF"), StreamInterruptIdleTimeout},
		{"clean FIN cut", errors.New("mimo-token-plan: stream ended before completion: unexpected EOF"), StreamInterruptPrematureEOF},
		{"bare unexpected EOF", io.ErrUnexpectedEOF, StreamInterruptPrematureEOF},
		{"tcp reset", fmt.Errorf("read: %w", syscall.ECONNRESET), StreamInterruptConnectionReset},
	}
	for _, tc := range cases {
		if got := ClassifyStreamInterrupt(tc.err); got != tc.want {
			t.Errorf("ClassifyStreamInterrupt(%s) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// StreamInterruptReason must surface the server_error label through the
// StreamInterruptedError wrapper the emit sites use, and ClassifyRecovery must
// keep it a retryable stream failure — the same admission the flattened
// premature_eof got, so only the observability label changes.
func TestHTTP2ResetIsRetryableStreamFailure(t *testing.T) {
	inner := fmt.Errorf("mimo-token-plan: read stream: %w",
		errors.New("stream error: stream ID 5; INTERNAL_ERROR; received from peer"))
	err := StreamInterrupt(inner, ClassifyStreamInterrupt(inner))
	if got := StreamInterruptReason(err); got != StreamInterruptServerError {
		t.Fatalf("StreamInterruptReason = %q, want %q", got, StreamInterruptServerError)
	}
	f := ClassifyRecovery(err)
	if f.Phase != "stream" || !f.Retryable {
		t.Fatalf("ClassifyRecovery = %+v, want phase=stream retryable=true", f)
	}
}
