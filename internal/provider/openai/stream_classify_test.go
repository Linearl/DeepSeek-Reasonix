package openai

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"

	"reasonix/internal/provider"
)

// errBody fails every read with the given error, mimicking a transport cut.
type errBody struct{ err error }

func (b errBody) Read([]byte) (int, error) { return 0, b.err }
func (b errBody) Close() error             { return nil }

// 任务929 S1: the openai-compatible path must label the gateway's HTTP/2
// RST_STREAM(INTERNAL_ERROR) as server_error instead of dropping a bare error
// (old IsConnReset-only gate → text fallback → premature_eof). The recovery
// classifier must keep the same retryable stream admission it had before, so
// this is an observability fix, not a retry-policy change.
func TestStreamOnceClassifiesHTTP2RST(t *testing.T) {
	resp := &http.Response{Body: errBody{err: errors.New("stream error: stream ID 5; INTERNAL_ERROR; received from peer")}}
	out := make(chan provider.Chunk, 4)
	(&client{name: "mimo-token-plan"}).streamOnce(context.Background(), resp, out)
	chunk := errorChunk(t, out)
	if !provider.IsStreamInterrupted(chunk.Err) {
		t.Fatalf("err = %v, want StreamInterruptedError", chunk.Err)
	}
	if got := provider.StreamInterruptReason(chunk.Err); got != provider.StreamInterruptServerError {
		t.Fatalf("reason = %q, want %q", got, provider.StreamInterruptServerError)
	}
	f := provider.ClassifyRecovery(chunk.Err)
	if f.Phase != "stream" || !f.Retryable {
		t.Fatalf("ClassifyRecovery = %+v, want phase=stream retryable=true", f)
	}
}

// A proxy that closes with a clean FIN (no [DONE], no finish_reason) keeps its
// premature_eof label, now attached at the source instead of by fallback.
func TestStreamOnceClassifiesCleanFIN(t *testing.T) {
	resp := &http.Response{Body: io.NopCloser(eofReader{})}
	out := make(chan provider.Chunk, 4)
	(&client{name: "mimo-token-plan"}).streamOnce(context.Background(), resp, out)
	chunk := errorChunk(t, out)
	if got := provider.StreamInterruptReason(chunk.Err); got != provider.StreamInterruptPrematureEOF {
		t.Fatalf("reason = %q, want %q (err %v)", got, provider.StreamInterruptPrematureEOF, chunk.Err)
	}
}

type eofReader struct{}

func (eofReader) Read([]byte) (int, error) { return 0, io.EOF }

// A cancellation is the user's own action: it must keep its raw shape so
// ClassifyRecovery never turns it into a retryable stream failure.
func TestStreamOnceKeepsCancellationRaw(t *testing.T) {
	resp := &http.Response{Body: errBody{err: context.Canceled}}
	out := make(chan provider.Chunk, 4)
	(&client{name: "mimo-token-plan"}).streamOnce(context.Background(), resp, out)
	chunk := errorChunk(t, out)
	if provider.IsStreamInterrupted(chunk.Err) {
		t.Fatalf("cancellation was wrapped as a stream interruption: %v", chunk.Err)
	}
	if !errors.Is(chunk.Err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", chunk.Err)
	}
	if f := provider.ClassifyRecovery(chunk.Err); f.Retryable {
		t.Fatalf("ClassifyRecovery = %+v, cancellation must not be retryable", f)
	}
}

func errorChunk(t *testing.T, out <-chan provider.Chunk) provider.Chunk {
	t.Helper()
	for chunk := range out {
		if chunk.Type == provider.ChunkError {
			return chunk
		}
	}
	t.Fatal("stream closed without a ChunkError")
	return provider.Chunk{}
}
