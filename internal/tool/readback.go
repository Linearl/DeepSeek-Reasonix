package tool

import (
	"context"
)

// ReadBackObservation is the host-only record of the window an edit tool read
// back to the model after a successful write (task 603). It files exactly the
// lines the readBack section rendered — no source text is retained, only the
// canonical path, 1-based start line, and SHA-256 line digests — so the write
// gate can treat the returned window as fresh read evidence. Snapshot binds
// the window to the post-write content version, computed by the writer from
// the bytes it actually wrote; a later change anywhere else still fails the
// coverage match, so stale detection keeps working.
type ReadBackObservation struct {
	Path       string
	StartLine  int
	LineHashes []string
	Snapshot   string
}

type readBackKey struct{}

// WithReadBackCollector attaches an observation collector the executing edit
// tool fills in. Contexts are immutable, so the callee cannot hand a value
// back by deriving a new ctx; the collector is a shared cell the caller reads
// after the call. Returns the collector alongside the derived context
// (WithMCPAppCollector pattern). A nil collector means nobody records: tools
// skip the filing and keep their plain result.
func WithReadBackCollector(ctx context.Context) (context.Context, *ReadBackObservation) {
	if ctx == nil {
		ctx = context.Background()
	}
	sink := &ReadBackObservation{}
	return context.WithValue(ctx, readBackKey{}, sink), sink
}

// CollectReadBackObservation fills the call's collector. Only a fully
// observed window is accepted: an empty path or no line hashes (a failed or
// skipped read-back) never reaches the gate as evidence.
func CollectReadBackObservation(ctx context.Context, o *ReadBackObservation) {
	if ctx == nil || o == nil || o.Path == "" || o.StartLine < 1 || len(o.LineHashes) == 0 {
		return
	}
	if sink, ok := ctx.Value(readBackKey{}).(*ReadBackObservation); ok && sink != nil {
		*sink = *o
	}
}

// ReadBackCollected returns the observation the executed call filed, or nil
// when the call carried no read-back (default path, switch off, or a failed
// write).
func ReadBackCollected(ctx context.Context) *ReadBackObservation {
	if ctx == nil {
		return nil
	}
	if sink, ok := ctx.Value(readBackKey{}).(*ReadBackObservation); ok {
		return sink
	}
	return nil
}
