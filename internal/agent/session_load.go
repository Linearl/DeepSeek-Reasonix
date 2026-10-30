package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

// tl1227: the schema-2-only first-paint tail window
// (sessionTranscriptTailThresholdBytes / sessionTranscriptTailWindowBytes /
// replaySessionDAGTail) retired with the read split; first paint and full
// reads share the single schema-1 path, whose trailing-replace replay is
// already bounded.

// sessionTranscriptTailThresholdBytes survives the tl1227 retirement as the
// large-log threshold reused by the tl1220 save-path observation counter
// (save_dag_observation.go): logs at or below this size are not counted as
// nil_cache+oversize replays.
const sessionTranscriptTailThresholdBytes = int64(32 << 20)

// SessionTailThresholdBytes reports the large-log first-paint threshold. The
// desktop cold-read gate (tl1281) shares it so "large enough to window" and
// "large enough to skip full cold decodes" stay one decision.
func SessionTailThresholdBytes() int64 {
	return sessionTranscriptTailThresholdBytes
}

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

// sessionCheckpointTailWindowBytes is the tl1281 first-paint tail window for
// non-native transcripts. tl1227 retired the schema-2 tail-window read, and
// since then every tail entry (LoadSessionTail: hydrate resume, 831③ first
// paint, cold tail window) silently degraded to a FULL checkpoint decode for
// sealed schema-2 logs — the decode state tl1279 measured as the literalStore
// baseline's dominant share (313.86MB at 20:27, four open sessions). This
// constant reintroduces the producer half of the task-187/831③ pair for the
// checkpoint branch: the .jsonl checkpoint is line-oriented, so the trailing
// window decodes standalone (no append-chain continuity to preserve) and the
// Session reports TailTruncated, which re-arms every guard the pair already
// ships (upgradeTruncatedTranscriptForWrite before writes, snapshot no-op
// exclusion, save-path graph-cache refusal). Native schema-1 replay is NOT
// windowed: append records carry message-index continuity against the replayed
// state, so only a trailing replace could bound it, and a stale replace would
// silently age the executor's context — that debt stays registered (tl1173).
const sessionCheckpointTailWindowBytes = int64(20 << 20)

// loadSessionTranscript dispatches on the log schema: a schema-2 log replays
// the DAG and materializes its selected head, a schema-1 log replays its
// records, and anything else falls back to the .jsonl checkpoint.
func loadSessionTranscript(ctx context.Context, sessionPath string, limits sessionReplayLimits, hasher *sessionTranscriptHasher) (sessionLoadResult, error) {
	return loadSessionTranscriptMode(ctx, sessionPath, limits, hasher, false)
}

// loadSessionTranscriptMode is the schema dispatcher; tail=true is the
// first-paint form (loadSessionTranscriptTail). Only the checkpoint fallback
// is windowed in tail mode — the schema-1 replay keeps its full form for the
// append-chain reasons documented on sessionCheckpointTailWindowBytes.
func loadSessionTranscriptMode(ctx context.Context, sessionPath string, limits sessionReplayLimits, hasher *sessionTranscriptHasher, tail bool) (sessionLoadResult, error) {
	if err := ctx.Err(); err != nil {
		return sessionLoadResult{}, err
	}
	// tl 1174：冷读透明的收口点（tl 1172 在 limitsForSessionLog 收口的同款
	// 选择——所有读会话的路径都从本函数或 Tail 变体过）。归档态转录在此
	// 物化回明文，后续探针/重放/预算按明文世界进行，上层读取链无感。开关
	// 关闭态只多一次存在性 stat（明文在 = 短路，零增量）。
	if _, err := ensureSessionTranscriptPlain(sessionPath); err != nil {
		return sessionLoadResult{}, fmt.Errorf("session transcript materialize: %w", err)
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
	// tl1227 读侧单路径化：schema-2 DAG 读分支已退役。存量 DAG 日志由
	// MigrateSessionDAGLog 一次性无损迁移为 schema-1（原字节存
	// .dagbak-tl1227 备份）；残余 schema-2 日志（v2 逃生门产物或迁移窗口
	// 遗漏）弃读封存——native 条件不中，落到下方 .jsonl checkpoint 兜底，
	// 不再有任何 DAG 重放读路径。
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
	if tail {
		msgs, truncated, tailErr := loadCheckpointTailWindowContext(ctx, sessionPath, hasher)
		if tailErr == nil {
			return sessionLoadResult{msgs: msgs, tailTruncated: truncated}, nil
		}
		// Undecodable or unreadable checkpoint tail: fall through to the full
		// decode, whose error semantics the tail entry has always carried.
	}
	msgs, err := loadSessionMessagesFromJSONLContext(ctx, sessionPath, hasher)
	return sessionLoadResult{msgs: msgs}, err
}

// loadCheckpointTailWindowContext decodes only the trailing
// sessionCheckpointTailWindowBytes of the .jsonl checkpoint. The checkpoint is
// one provider.Message per line (writeSessionMessages encodes per message), so
// the window snaps to the first line boundary inside the read span and every
// remaining line decodes standalone. truncated=false when the whole checkpoint
// fit the window (small sessions stay byte-identical to the full load);
// truncated=true reports the window via sessionLoadResult.tailTruncated, which
// re-arms the task-187 write-side guards. Any read or decode failure returns an
// error so the caller falls back to the full decode instead of inventing a
// shorter transcript.
func loadCheckpointTailWindowContext(ctx context.Context, sessionPath string, hasher *sessionTranscriptHasher) ([]provider.Message, bool, error) {
	info, err := os.Stat(sessionPath)
	if err != nil {
		return nil, false, err
	}
	if info.IsDir() || info.Size() <= sessionCheckpointTailWindowBytes {
		return nil, false, nil
	}
	window := sessionCheckpointTailWindowBytes
	f, err := os.Open(sessionPath)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	buf := make([]byte, window)
	if _, err := f.ReadAt(buf, info.Size()-window); err != nil {
		return nil, false, err
	}
	// Drop the partial first line: ReadAt starts mid-line unless the byte
	// boundary lands exactly on a newline. The window must only ever decode
	// whole messages — a truncated JSON value would fail the full decode too,
	// but here it would silently shrink the transcript.
	if nl := bytes.IndexByte(buf, '\n'); nl >= 0 {
		buf = buf[nl+1:]
	} else {
		// One line spans the whole window; nothing safely decodable.
		return nil, false, fmt.Errorf("checkpoint tail window holds no line boundary")
	}
	dec := json.NewDecoder(&contextReader{ctx: ctx, reader: bytes.NewReader(buf)})
	var msgs []provider.Message
	for {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		var m provider.Message
		if err := dec.Decode(&m); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, false, fmt.Errorf("decode checkpoint tail window %s: %w", sessionPath, err)
		}
		msgs = append(msgs, hasher.add(m))
	}
	return msgs, true, nil
}

// loadSessionTranscriptTail is the first-paint entry point for a session whose log
// may be too large to replay whole. tl1227 读侧单路径化后首画与全量读走同一条
// schema-1 路径（其内部自带 trailing-replace 有界重放），本入口保留签名供
// 首画/导出调用方使用，行为等价 loadSessionTranscript。
//
// It is deliberately a separate entry point rather than a switch inside
// loadSessionTranscript: that function backs save, recovery and export, and a
// truncated transcript there would silently drop history on the next write.
func loadSessionTranscriptTail(ctx context.Context, sessionPath string, limits sessionReplayLimits, hasher *sessionTranscriptHasher) (sessionLoadResult, error) {
	if err := ctx.Err(); err != nil {
		return sessionLoadResult{}, err
	}
	// tl 1174：与 loadSessionTranscript 同款冷读透明收口（首画入口）。
	if _, err := ensureSessionTranscriptPlain(sessionPath); err != nil {
		return sessionLoadResult{}, fmt.Errorf("session transcript materialize: %w", err)
	}
	// tl1281: the tail entry is a mode of the shared dispatcher again. Since
	// tl1227 retired the schema-2 window this entry had silently equaled the
	// full load; the checkpoint branch windows again (see
	// sessionCheckpointTailWindowBytes) and reports tailTruncated, re-arming
	// the task-187 guards (upgrade-before-write, snapshot no-op exclusion).
	return loadSessionTranscriptMode(ctx, sessionPath, limits, hasher, true)
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
