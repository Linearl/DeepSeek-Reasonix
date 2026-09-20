package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"reasonix/internal/provider"
	"reasonix/internal/store"
)

// sessionLoadResult is one transcript read from disk: the messages of the
// selected head (or the whole schema-1 log), per-message times where the
// source records them, and the head position for schema-2 sessions.
type sessionLoadResult struct {
	msgs       []provider.Message
	times      []time.Time
	fromEvents bool
	damaged    bool
	dag        bool
	head       HeadRef
	headCount  int
	state      *sessionDAGState
	openTurn   *sessionDAGTurn
	events     []HeadEvent
	// tailTruncated reports that msgs holds only the tail of the transcript
	// because the log was too large to replay in full for a first paint. Readers
	// that need the whole history (every write path) must not use this result;
	// readers that page older history in may show it immediately.
	tailTruncated bool
}

// sessionTranscriptTailThresholdBytes is the schema-2 log size past which a first
// paint replays only the trailing window. A 467 MiB log cost ~19s of pure decoding
// while materializing its 3880 messages took 5ms (task 187 follow-up, measured
// 2026-09-20), so the stall the user sees is the decode. Below the threshold the
// full replay stays, so small sessions cannot change behaviour.
const sessionTranscriptTailThresholdBytes = int64(32 << 20)

// sessionTranscriptTailWindowBytes is how much of a large log a first paint
// replays: roughly 1/25 of the 467 MiB case, which is the difference between a
// multi-second stall and a paint.
const sessionTranscriptTailWindowBytes = int64(20 << 20)

// loadSessionMessages returns the session transcript, preferring the event log
// when the native layer owns it and it holds at least one decodable record.
// Foreign files squatting the log path (legacy import leftovers) are ignored
// in favor of the .jsonl checkpoint. damaged reports that a native log could
// not be replayed to its end (torn tail or corrupt record); callers that write
// should rewrite-and-compact to heal it.
func loadSessionMessages(sessionPath string) (msgs []provider.Message, fromEvents, damaged bool, err error) {
	return loadSessionMessagesWithLimits(sessionPath, defaultSessionReplayLimits, nil)
}

func loadSessionMessagesWithLimits(sessionPath string, limits sessionReplayLimits, hasher *sessionTranscriptHasher) (msgs []provider.Message, fromEvents, damaged bool, err error) {
	// Size the byte budget to the file before deciding anything about it. The budget exists so a
	// damaged log cannot exhaust memory while decoding, and the size is known here - refusing an
	// oversize log instead of sizing to it left sessions that were fractions of a percent over
	// the default permanently unopenable (task 104). Applied at every entry that has a path, so
	// no caller has to remember; the record, message and collection caps are untouched and the
	// adaptive allowance keeps its own 1 GiB ceiling.
	limits = limitsForSessionLog(sessionPath, limits)
	return loadSessionMessagesWithContext(context.Background(), sessionPath, limits, hasher)
}

func loadSessionMessagesWithContext(ctx context.Context, sessionPath string, limits sessionReplayLimits, hasher *sessionTranscriptHasher) (msgs []provider.Message, fromEvents, damaged bool, err error) {
	res, err := loadSessionTranscript(ctx, sessionPath, limits, hasher)
	return res.msgs, res.fromEvents, res.damaged, err
}

// loadSessionTranscript dispatches on the log schema: a schema-2 log replays
// the DAG and materializes its selected head, a schema-1 log replays its
// records, and anything else falls back to the .jsonl checkpoint.
func loadSessionTranscript(ctx context.Context, sessionPath string, limits sessionReplayLimits, hasher *sessionTranscriptHasher) (sessionLoadResult, error) {
	if err := ctx.Err(); err != nil {
		return sessionLoadResult{}, err
	}
	// Adapt the byte budget here rather than at each caller. The budget exists so a damaged
	// log cannot exhaust memory while decoding into a larger graph, and the file size is known
	// before decoding - so it can be sized to the file instead of refusing it. The DAG replay
	// already adapted internally, but this entry point did not, so a log just over the default
	// budget was still refused on the non-DAG path (task 104). The record, message and
	// collection caps are untouched, and the adaptive allowance keeps its own 1 GiB ceiling.
	limits = limitsForSessionLog(sessionPath, limits)
	logPath := store.SessionEventLog(sessionPath)
	// A log that was refused before is refused again from memory: the refusal is a
	// property of the bytes on disk, and the LRU and the sidebar can ask for the
	// same session repeatedly, each time paying a full replay before giving up.
	if cached, ok := cachedSessionReplayRefusal(logPath); ok {
		return sessionLoadResult{fromEvents: true}, cached
	}
	probe, err := probeSessionEventLogWithLimits(sessionPath, limits)
	if err != nil {
		return sessionLoadResult{}, err
	}
	if probe.futureSchema {
		return sessionLoadResult{fromEvents: true}, fmt.Errorf("session event log for %s uses schema %d; this build supports up to %d", sessionPath, probe.schemaVersion, sessionDAGSchemaVersion)
	}
	if probe.dag {
		st, err := replaySessionDAG(ctx, logPath, limits)
		if err != nil {
			rememberSessionReplayRefusal(logPath, err)
			return sessionLoadResult{fromEvents: true, dag: true}, err
		}
		headID := st.selectedHead()
		msgs, times := st.materialize(headID)
		hasher.addAll(msgs)
		return sessionLoadResult{
			msgs: msgs, times: times, fromEvents: true, damaged: st.damaged, dag: true,
			head:      HeadRef{HeadID: headID, LeafID: st.heads[headID].leaf, LogGeneration: st.generation, LogOffset: st.lastGoodEnd},
			headCount: len(st.heads),
			state:     st,
			openTurn:  st.heads[headID].openTurn,
			events:    loadHeadEvents(st, headID),
		}, nil
	}
	if probe.native && probe.size > 0 {
		replay, replayErr := replaySessionEventLogWithContext(ctx, logPath, limits, hasher)
		if replayErr != nil {
			rememberSessionReplayRefusal(logPath, replayErr)
			return sessionLoadResult{fromEvents: true}, replayErr
		}
		if replay.records > 0 {
			return sessionLoadResult{msgs: replay.msgs, times: replay.times, fromEvents: true, damaged: replay.damaged}, nil
		}
		// Defensive: the probe saw a native head but nothing replayed; fall
		// back to the checkpoint and let the next save rebuild the log.
		msgs, err := loadSessionMessagesFromJSONLContext(ctx, sessionPath, hasher)
		return sessionLoadResult{msgs: msgs, damaged: true}, err
	}
	msgs, err := loadSessionMessagesFromJSONLContext(ctx, sessionPath, hasher)
	return sessionLoadResult{msgs: msgs}, err
}

// loadSessionTranscriptTail is the first-paint entry point for a session whose log
// may be too large to replay whole. For a schema-2 log above
// sessionTranscriptTailThresholdBytes it replays only the trailing window and
// reports tailTruncated so the reader can page earlier history in; for anything
// else - including every write path, which needs the complete transcript - it
// delegates to loadSessionTranscript unchanged.
//
// It is deliberately a separate entry point rather than a switch inside
// loadSessionTranscript: that function backs save, recovery and export, and a
// truncated transcript there would silently drop history on the next write.
func loadSessionTranscriptTail(ctx context.Context, sessionPath string, limits sessionReplayLimits, hasher *sessionTranscriptHasher) (sessionLoadResult, error) {
	if err := ctx.Err(); err != nil {
		return sessionLoadResult{}, err
	}
	limits = limitsForSessionLog(sessionPath, limits)
	probe, err := probeSessionEventLogWithLimits(sessionPath, limits)
	if err != nil || !probe.dag || probe.size <= sessionTranscriptTailThresholdBytes {
		return loadSessionTranscript(ctx, sessionPath, limits, hasher)
	}
	startedAt := time.Now()
	st, err := replaySessionDAGTail(ctx, store.SessionEventLog(sessionPath), sessionTranscriptTailWindowBytes, limits)
	if err != nil {
		// An unaligned or empty window is not an error the user should see: the
		// full replay is slow but correct, so fall back to it.
		return loadSessionTranscript(ctx, sessionPath, limits, hasher)
	}
	headID := st.selectedHead()
	msgs, times := st.materialize(headID)
	hasher.addAll(msgs)
	// Task 196: the first paint's own payload deserves a line. The window is capped in
	// bytes, not in messages, so a 20 MiB window can still carry an enormous page - and if
	// what the user waits on is the transfer rather than the decode, this is the line that
	// says so instead of leaving it to inference.
	slog.Info("session: first-paint tail transcript",
		"path", sessionPath,
		"log_bytes", probe.size,
		"window_bytes", sessionTranscriptTailWindowBytes,
		"window_from", st.windowStart,
		"messages", len(msgs),
		"tail_truncated", true,
		"replay_ms", time.Since(startedAt).Milliseconds())
	return sessionLoadResult{
		msgs: msgs, times: times, fromEvents: true, damaged: st.damaged, dag: true,
		tailTruncated: true,
		head:          HeadRef{HeadID: headID, LeafID: st.heads[headID].leaf, LogGeneration: st.generation, LogOffset: st.lastGoodEnd},
		headCount:     len(st.heads),
		state:         st,
		openTurn:      st.heads[headID].openTurn,
		events:        loadHeadEvents(st, headID),
	}, nil
}

func loadSessionMessagesFromJSONL(path string, hasher *sessionTranscriptHasher) ([]provider.Message, error) {
	return loadSessionMessagesFromJSONLContext(context.Background(), path, hasher)
}

func loadSessionMessagesFromJSONLContext(ctx context.Context, path string, hasher *sessionTranscriptHasher) ([]provider.Message, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var msgs []provider.Message
	dec := json.NewDecoder(&contextReader{ctx: ctx, reader: f})
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var m provider.Message
		if err := dec.Decode(&m); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("decode %s: %w", path, err)
		}
		msgs = append(msgs, hasher.add(m))
	}
	return msgs, nil
}
