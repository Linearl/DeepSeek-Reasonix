package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"reasonix/internal/event"

	"reasonix/internal/evidence"

	"reasonix/internal/provider"
	"reasonix/internal/readcoord"
	"reasonix/internal/tool"
)

func deliveryFixture(t *testing.T) (*Agent, tool.ReadResultEnvelope, string) {
	t.Helper()
	body := "1→alpha\n2→beta\n"
	window, _ := tool.ParseReadWindow(body)
	end := 2
	env := tool.ReadResultEnvelope{ProtocolVersion: 2, Source: tool.ReadResultSource{CanonicalPath: "/w/a", Kind: tool.ReadSourceDisk, Identity: "bytes-a", Snapshot: "snapshot-a"}, Intent: tool.ReadIntentFull, DeliveredRanges: []tool.ReadRange{{Start: 0, End: 2}}, SourceEnd: &end, EOF: true, WindowDigest: tool.WindowDigest("/w/a", window)}
	a, _ := newEnvelopeTestAgent(t, envelopeReader{env: env})
	a.turn.readShadow = newReadShadowState(true)
	return a, env, body
}

func finalizeDelivery(t *testing.T, a *Agent, id, body string, env tool.ReadResultEnvelope) toolOutcome {
	t.Helper()
	call := provider.ToolCall{ID: id, Name: "read_file", Arguments: `{"path":"/w/a","intent":"full"}`}
	o := toolOutcome{output: body, readEnvelope: &env, readActiveMillis: 3}
	a.finalizeReadDelivery(context.Background(), call, &o)
	a.storeBatchToolResult(context.Background(), call, o)
	return o
}

func TestReadDeliveryReferencesOnlyFrozenOriginals(t *testing.T) {
	a, env, body := deliveryFixture(t)
	first := finalizeDelivery(t, a, "first", body, env)
	// Same-batch finalization cannot make a result model-visible.
	second := finalizeDelivery(t, a, "same-batch", body, env)
	if second.readReference != nil {
		t.Fatal("same-batch original was treated as visible")
	}
	a.freezeVisibleReads(provider.ModelMessages(a.Session().Snapshot()))
	repeat := finalizeDelivery(t, a, "repeat", body, env)
	if repeat.readReference == nil || len(repeat.finalReadEnvelope.DeliveredRanges) != 0 {
		t.Fatal("repeat did not use an original-only reference")
	}
	if first.finalReadEnvelope.ReadID != repeat.finalReadEnvelope.ReadID {
		t.Fatal("completed task identity changed")
	}
	ob, _ := a.turn.readShadow.coord.Get(first.finalReadEnvelope.ReadID)
	if ob.State != readcoord.StateSatisfied || ob.Pages != 3 || ob.ActiveTime != 9*time.Millisecond || ob.Stagnant != 0 {
		t.Fatalf("repeat changed completion or accounting: %+v", ob)
	}
	// A rewritten request, including compaction, invalidates raw references.
	a.freezeVisibleReads([]provider.Message{{Role: provider.RoleTool, ToolCallID: "first", Content: "summary only"}})
	after := finalizeDelivery(t, a, "after-compaction", body, env)
	if after.readReference != nil || after.output != body || after.finalReadEnvelope.ReadID != first.finalReadEnvelope.ReadID {
		t.Fatal("compaction lost completion or suppressed required raw text")
	}
	if len(a.turn.readShadow.coord.Snapshot()) != 1 {
		t.Fatal("repeat created another obligation")
	}
}

func TestReadDeliveryIdentityIsolation(t *testing.T) {
	for _, kind := range []string{"path", "workspace", "source", "snapshot", "unversioned"} {
		t.Run(kind, func(t *testing.T) {
			a, env, body := deliveryFixture(t)
			first := finalizeDelivery(t, a, "first", body, env)
			a.freezeVisibleReads(provider.ModelMessages(a.Session().Snapshot()))
			switch kind {
			case "path":
				env.Source.CanonicalPath = "/w/b"
				window, _ := tool.ParseReadWindow(body)
				env.WindowDigest = tool.WindowDigest("/w/b", window)
			case "workspace":
				a.workspaceID = "other"
			case "source":
				env.Source.Kind = tool.ReadSourceOverlay
			case "snapshot":
				env.Source.Snapshot = "changed"
				env.Source.Identity = "changed-bytes"
			case "unversioned":
				env.Source.Snapshot = ""
				env.Source.Identity = ""
			}
			next := finalizeDelivery(t, a, "next", body, env)
			if next.readReference != nil || next.finalReadEnvelope.ReadID == first.finalReadEnvelope.ReadID {
				t.Fatal("different source inherited a completion/reference")
			}
		})
	}
}

func TestReadDeliveryChangedByExtensionLosesIdentity(t *testing.T) {
	for _, body := range []string{"redacted", "1→changed\n2→beta\n"} {
		a, env, _ := deliveryFixture(t)
		out := finalizeDelivery(t, a, "modified", body, env)
		if out.finalReadEnvelope.Source.Snapshot != "" || len(out.finalReadEnvelope.DeliveredRanges) != 0 {
			t.Fatal("rewritten output inherited source evidence")
		}
	}
}

func TestReadDeliveryAfterRealContextProjection(t *testing.T) {
	path := makeShortPagedReadFixture(t, "projection.txt", 2105, 2051, "marker")
	read := func(id, args string) []provider.Chunk {
		return []provider.Chunk{toolCallChunk(id, "read_file", args), {Type: provider.ChunkDone}}
	}
	full := fmt.Sprintf(`{"path":%q,"intent":"full"}`, path)
	inner := &scriptedProvider{turns: [][]provider.Chunk{
		read("head", full), read("tail", fmt.Sprintf(`{"path":%q,"offset":2000,"limit":2000}`, path)), read("before-projection", full), read("after-projection", full), textTurn("Completed."),
	}}
	var a *Agent
	p := &inspectingProvider{inner: inner, before: func(round int, _ provider.Request) {
		if round != 2 {
			return
		}
		canonical := a.Session().Snapshot()
		projected := append([]provider.Message(nil), provider.ProjectionMessages(canonical)...)
		for i := range projected {
			if projected[i].Role == provider.RoleTool {
				projected[i].Content = "Original text archived; full read was completed."
			}
		}
		cacheKey := a.currentPromptCacheKey()
		a.sess.compactionMu.Lock()
		a.sess.compactionState = CompactionState{SchemaVersion: compactionStateSchemaCurrent, PromptCacheKey: cacheKey, Projection: ContextProjection{Messages: projected, CoveredCount: len(canonical), CoveredPrefixHash: coveredPrefixHash(canonical, len(canonical)), PinnedContextHash: pinnedContextCoverageHash(canonical, len(canonical))}}
		a.sess.compactionMu.Unlock()
	}}
	a = newIncompleteReadTestAgent(p, incompleteReadBuiltin(t), NewSession("system"), event.Discard)
	// This fixture specifically requires an unchanged first request body.
	// Isolate the process-wide latency median: other tests' microsecond model
	// calls can otherwise append a valid soft-budget nudge to that body.
	a.modelRef = t.Name()
	key := a.softBudgetHistoryKey()
	t.Cleanup(func() {
		readonlySoftBudgetHistory.Lock()
		delete(readonlySoftBudgetHistory.byKey, key)
		readonlySoftBudgetHistory.Unlock()
	})
	if err := a.Run(context.Background(), "Read the full file and verify it."); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(toolResultByID(a.Session(), "before-projection"), "Repeated read:") {
		t.Fatal("visible original was not reused")
	}
	if strings.Contains(toolResultByID(a.Session(), "after-projection"), "Repeated read:") {
		t.Fatal("projection removal did not force actual window delivery")
	}
	obs := a.turn.readShadow.coord.Snapshot()
	if len(obs) != 1 || obs[0].State != readcoord.StateSatisfied {
		t.Fatal("projection revoked historical completion")
	}
}

// finalizeDeliveryInBatch is finalizeDelivery with an explicit batch context:
// the real runner freezes an observation boundary into the batch context
// (execute_batch) and both the write gate and the read deduper must reason
// about the same value.
func finalizeDeliveryInBatch(t *testing.T, a *Agent, ctx context.Context, id, body string, env tool.ReadResultEnvelope) toolOutcome {
	t.Helper()
	call := provider.ToolCall{ID: id, Name: "read_file", Arguments: `{"path":"/w/a","intent":"full"}`}
	o := toolOutcome{output: body, readEnvelope: &env, readActiveMillis: 3}
	a.finalizeReadDelivery(ctx, call, &o)
	a.storeBatchToolResult(ctx, call, o)
	return o
}

// TestRepeatedReadAfterWriteCrossesBatchBoundary is the task-214 deadlock pin.
// The batch-two failure was: read → successful edit → gate demands a fresh
// read → the model retries [re-read, edit] in ONE batch → the deduper treated
// the re-read as current evidence the gate cannot see (its observation is
// sequenced after the frozen batch boundary) and suppressed it, so the edit
// stayed blocked forever. The deduper must reason about the same frozen
// boundary as the gate: inside the batch it re-delivers; across a boundary an
// identical window is a true repeat and still deduplicates.
func TestRepeatedReadAfterWriteCrossesBatchBoundary(t *testing.T) {
	a, env, body := deliveryFixture(t)
	if a.task.ledger == nil {
		a.task.ledger = evidence.NewLedger()
	}
	finalizeDelivery(t, a, "first", body, env)
	a.freezeVisibleReads(provider.ModelMessages(a.Session().Snapshot()))
	// A successful edit lands after the first read.
	a.task.ledger.Record(evidence.Receipt{ToolName: "edit_file", Success: true, Mutation: true, Write: true, Paths: []string{"/w/a"}})

	// Same-batch retry: the read pipeline records the re-read's observation
	// during execution, BEFORE finalize decides dedup (the same order the
	// real runner uses). The gate freezes the boundary at batch start, so that
	// fresh observation is invisible to the gate; deduplicating against it
	// would deadlock the retry edit.
	frozen := a.task.ledger.ObservationBoundary()
	window, _ := tool.ParseReadWindow(body)
	hashes := make([]string, 0, len(window.Lines))
	for _, line := range window.Lines {
		hashes = append(hashes, hashLine(line))
	}
	a.task.ledger.RecordTextObservation(evidence.TextObservation{Path: "/w/a", Snapshot: env.Source.Snapshot, StartLine: 1, LineHashes: hashes})
	ctx := withObservationBoundary(context.Background(), frozen)
	retry := finalizeDeliveryInBatch(t, a, ctx, "same-batch-retry", body, env)
	if retry.readReference != nil {
		t.Fatal("same-batch re-read after a write was deduplicated; the write gate would deadlock (task 214)")
	}

	// Cross-boundary repeat: the retry's observation has now reached a
	// provider boundary, so an identical window is a true repeat again.
	repeat := finalizeDelivery(t, a, "cross-boundary-repeat", body, env)
	if repeat.readReference == nil {
		t.Fatal("cross-boundary identical re-read stopped being deduplicated")
	}
}
