package anthropic

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"reasonix/internal/provider"
)

// TestSSEErrorEventSurfacesStreamInterrupted pins task 273's main fix: an SSE
// error frame must classify as StreamInterruptedError with reason
// server_error, so ClassifyRecovery marks it Phase=stream Retryable=true and
// the Agent's sampling-recovery budget applies instead of stalling the turn.
func TestSSEErrorEventSurfacesStreamInterrupted(t *testing.T) {
	sse := `event: message_start
data: {"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":5}}}

event: error
data: {"type":"error","error":{"type":"server_error","message":"Overloaded"}}
`
	c := &client{name: "mimo-api"}
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(sse))}
	ch := make(chan provider.Chunk)
	go c.readStream(context.Background(), resp, ch)

	var sawError, interrupted, sawDone bool
	var reason string
	for ck := range ch {
		switch ck.Type {
		case provider.ChunkDone:
			sawDone = true
		case provider.ChunkError:
			sawError = true
			if provider.IsStreamInterrupted(ck.Err) {
				interrupted = true
				reason = provider.StreamInterruptReason(ck.Err)
			}
		}
	}
	if sawDone {
		t.Fatal("error frame must not emit ChunkDone")
	}
	if !sawError {
		t.Fatal("error frame must surface ChunkError")
	}
	if !interrupted {
		t.Fatal("SSE error must classify as StreamInterruptedError (task 273)")
	}
	if reason != provider.StreamInterruptServerError {
		t.Fatalf("reason = %q, want %q", reason, provider.StreamInterruptServerError)
	}
}

// TestScanEndErrorHTTP2InternalClassified pins task 273's acceptance form:
// MiMo's observed "stream ID 3; INTERNAL_ERROR; received from peer" arrives
// through the scanner path, not an SSE error frame, and must classify too.
func TestScanEndErrorHTTP2InternalClassified(t *testing.T) {
	scanErr := errors.New("stream ID 3; INTERNAL_ERROR; received from peer")
	err := streamScanEndError("mimo-api", time.Second, false, scanErr, "")
	if err == nil {
		t.Fatal("scanner failure must produce an error")
	}
	if !provider.IsStreamInterrupted(err) {
		t.Fatalf("INTERNAL_ERROR scanner failure = %v, want StreamInterruptedError", err)
	}
	if !strings.Contains(err.Error(), "read stream") {
		t.Fatalf("error = %v, want read-stream wrapper preserved", err)
	}
	f := provider.ClassifyRecovery(err)
	if f.Phase != "stream" || !f.Retryable {
		t.Fatalf("recovery = %+v, want Phase=stream Retryable=true", f)
	}
}

// TestScanEndErrorContextCancelStaysUnclassified: user cancellation must not
// become a retryable stream interruption (task 273 carve-out).
func TestScanEndErrorContextCancelStaysUnclassified(t *testing.T) {
	err := streamScanEndError("mimo-api", time.Second, false, context.Canceled, "")
	if err == nil {
		t.Fatal("canceled scan must still surface the error")
	}
	if provider.IsStreamInterrupted(err) {
		t.Fatalf("context.Canceled = %v, must stay unclassified", err)
	}
}
