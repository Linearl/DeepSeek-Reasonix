package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
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
		if tailErr == nil && truncated {
			return sessionLoadResult{msgs: msgs, tailTruncated: truncated}, nil
		}
		// Window refused (checkpoint fits it) or undecodable: fall through to
		// the full decode, whose error semantics the tail entry has always
		// carried. Small sessions keep byte-identical full loads this way.
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

// LoadSession reads a saved session into a fresh Session value. New sessions
// replay the append-only event log; legacy sessions without an event log fall
// back to the compatibility .jsonl checkpoint. A damaged log is replayed to its
// last clean record (or the checkpoint when nothing decodes) and flagged so the
// next save heals it with a rewrite-and-compact.
// In-process loads share the save path mutex so they cannot observe a local
// SaveSnapshot between appending an event-log record and refreshing the index.
// The mutex wait is bounded (任务 946): a wedged holder surfaces as
// ErrSessionFileLockHeld instead of hanging the hydrate until the process
// watchdog kills the app.
// Missing files surface as os.IsNotExist so callers can fall through to a
// new session.
func LoadSession(path string) (*Session, error) {
	// 任务 1071：LoadSession 是所有「读入→重写」链路（保存/自愈/投影修复）的
	// 共同入口。侧车文件（如 .repairlog.jsonl，修复日志审计记录）不得被当成
	// 会话加载——一旦加载，后续任何一次 Save 都会把它重写成只剩 role 字段的
	// 消息行（writeSessionMessages 原子覆盖），审计内容即被清空。分类闸门在
	// 锁之前：不给侧车文件留任何被「顺手保存」的机会。
	// tl1424：闸门走「可加载」口径（主转录 + guardian 伴随转录），不再直接用
	// IsSessionTranscriptName。guardian 伴随转录是会话形态（同一会话层存取，
	// 1071 闸门误拒后 guardian Load 自 2026-10-22 起静默失败：跨重启丢对话、
	// 增量游标不恢复）。repairlog 等审计/派生侧车仍在拒绝集内——钉：
	// Test1071RepairLogLoadSessionRefuses / TestLoadSessionGateSidecarsRefused；
	// 会话列表发现仍用 IsSessionTranscriptName，guardian 伴随不进列表。
	if path != "" && !store.IsLoadableSessionTranscriptName(filepath.Base(path)) {
		return nil, fmt.Errorf("load session: %s is not a session transcript", filepath.Base(path))
	}
	unlock, err := lockSessionSavePath(path)
	if err != nil {
		return nil, err
	}
	defer unlock()
	return loadSessionUnlocked(path)
}

// LoadSessionTail is the first-paint form of LoadSession: for a very large
// schema-2 log it returns a Session whose transcript is the trailing window and
// reports that fact through TailTruncated. It exists for the hydrate path, where
// the reader pages older history in anyway; every other caller (save, recovery,
// migration, export) keeps using LoadSession, whose transcript is complete.
// 946: this is the hydrate entry that sat behind a wedged save-path holder
// until the 20s process watchdog killed the app — the wait is bounded now.
func LoadSessionTail(path string) (*Session, error) {
	unlock, err := lockSessionSavePath(path)
	if err != nil {
		return nil, err
	}
	defer unlock()
	return loadSessionTailUnlocked(path)
}

func loadSessionUnlocked(path string) (*Session, error) {
	return loadSessionWithReader(path, func(hasher *sessionTranscriptHasher) (sessionLoadResult, error) {
		// P18-R1: the save-path lock is held by LoadSession here, so a cached
		// fully replayed graph can serve the load directly (guards in
		// cachedSessionLoadResult mirror dagStateForSave's reuse checks); every
		// guard failure falls through to the ordinary full replay.
		if res, ok := cachedSessionLoadResult(path, hasher); ok {
			return res, nil
		}
		return loadSessionTranscript(context.Background(), path, defaultSessionReplayLimits, hasher)
	})
}

func loadSessionTailUnlocked(path string) (*Session, error) {
	return loadSessionWithReader(path, func(hasher *sessionTranscriptHasher) (sessionLoadResult, error) {
		return loadSessionTranscriptTail(context.Background(), path, defaultSessionReplayLimits, hasher)
	})
}

// loadSessionWithReader is the shared body of both loaders: they differ only in
// which transcript they ask for, so the normalization, digest and persistence
// baseline below stay identical for the tail and the full form.
func loadSessionWithReader(path string, read func(*sessionTranscriptHasher) (sessionLoadResult, error)) (*Session, error) {
	hasher := newSessionTranscriptHasher()
	res, err := read(hasher)
	if err != nil {
		return nil, err
	}
	msgs := res.msgs
	if res.state != nil {
		sessionGraphCachePut(store.SessionEventLog(path), res.state)
	}
	s := &Session{Messages: msgs, eventLogDamaged: res.damaged, tailTruncated: res.tailTruncated, head: sessionHeadState{ref: res.head, dag: res.dag, headCount: res.headCount, state: nil, openTurn: res.openTurn, events: res.events}}
	// Repair persisted-history-safe issues before anything reads the session.
	// Old sessions (pre adde2d3e) and interrupted turns can carry empty tool-call
	// names, dangling tool_calls, or half-streamed argument JSON that DeepSeek
	// rejects with a 400 on replay. Wire-only cleanup, such as dropping orphan
	// tool messages, stays in the provider send path so Save/LoadSession keeps
	// its round-trip contract. The fast path returns the input slice unchanged
	// for a well-formed history, so we detect an actual repair by comparing
	// slice headers: when NormalizeSession allocated a new backing array, the
	// session is marked dirty so the next Save persists the fix.
	normalized := NormalizeSession(s.Messages)
	normalized = migrateLegacyProviderContent(normalized)
	if len(normalized) != len(s.Messages) || (len(s.Messages) > 0 && &normalized[0] != &s.Messages[0]) {
		s.normalizedDirty = true
		// Keep the pre-repair transcript: checkSnapshotWrite must be able to
		// recognize a snapshot that extends the bytes actually on disk, which
		// the repaired view no longer represents (an interrupted tool turn
		// gets a placeholder result fabricated here that the live session
		// answered for real).
		s.rawMessages = msgs
		// tl1651 打点 c 恢复半边: this repair rewrites provider-visible
		// transcript bytes at resume time WITHOUT passing through
		// Rewrite/Replace, so the rewrite probe never sees it — yet it is the
		// prime suspect for a resume-time covered-prefix hash mismatch (the
		// sidecar was written against the pre-repair bytes). The log line makes
		// the repair visible on the same timeline the projection validation
		// reads; diagnoseProjectionMismatch's "load_repair" corpus check closes
		// the attribution loop.
		slog.Info("agent: transcript repaired on load",
			"writer", SessionWriterID(),
			"path", path,
			"pre_repair_msgs", len(msgs),
			"post_repair_msgs", len(normalized))
	}
	s.Messages = normalized
	if s.tailTruncated {
		// tl1281: the window base, captured after load-time normalization so
		// the upgrade merge knows exactly where the appended turns start.
		s.tailWindowLen = len(s.Messages)
	}
	assignLegacyMessageIDs(path, s.Messages)
	// Decode already hashed the transcript; when the repairs above returned it
	// unchanged (the common case) reuse that digest, else re-hash the repair.
	digest, digestOK := hasher.sum()
	if s.normalizedDirty || !digestOK {
		if d, derr := digestSessionMessages(s.Messages); derr == nil {
			digest, digestOK = d, true
		} else {
			digestOK = false
		}
	}
	if digestOK {
		// Pair the raw pre-repair transcript when normalization changed it.
		diskView := s.Messages
		if s.normalizedDirty {
			diskView = s.rawMessages
		}
		if meta, ok, metaErr := loadBranchMetaRetry(path); metaErr != nil {
			// The sidecar exists but is unreadable even after retries (torn or
			// corrupt). The session must still open, but revision 0 must not
			// pose as a real baseline: the next save would misread the honest
			// on-disk revision as another runtime's write and fork a recovery
			// branch. Anchor the baseline on digest+version only until a
			// successful save re-learns the revision.
			s.markPersistedRevisionUnknown(path, digest, s.version, s.rewriteVersion, diskView)
		} else {
			revision := int64(0)
			if ok {
				revision = meta.Revision
			}
			// 任务 831②: the clean-load snapshot no-op additionally requires
			// the sidecar to agree with the decoded transcript. A missing
			// sidecar, or one whose digest lags the bytes (interrupted save),
			// keeps the first save on the full path that heals the ledger.
			ledgerClean := ok && strings.TrimSpace(meta.ContentDigest) == digestString(digest)
			s.markPersistedFromLoad(path, digest, s.version, revision, s.rewriteVersion, diskView, ledgerClean)
		}
	}
	return s, nil
}

// upgradeTruncatedTranscriptForWrite replaces a first-paint transcript with the
// complete one before it can reach disk.
//
// A tail Session is safe to show and must never be saved: saves are full
// rewrites, so persisting the window would drop everything the window left out.
// This is the second of two guards (the first being that only the hydrate path
// asks for a tail at all), and it runs before any save lock is taken, so the
// extra full replay happens on the rare transition from "opened for display" to
// "written" and never inside the save critical section.
//
// tl1281: turns appended after hydration (executor appends on a windowed
// session — the turn loop does not wait for an upgrade) are merged onto the
// reloaded full transcript via tailWindowLen. tl1227 had made truncation
// unreachable, so the outright replacement below never faced an appended turn;
// with the checkpoint tail window producer back, replacement would silently
// drop the user's turns.
func (s *Session) upgradeTruncatedTranscriptForWrite(path string) error {
	if s == nil || path == "" {
		return nil
	}
	s.mu.RLock()
	truncated := s.tailTruncated
	windowLen := s.tailWindowLen
	// The window base the reloaded full transcript must be spliced against:
	// the pre-repair window byte count when load-time normalization changed
	// the view (rawMessages), else the normalized window length. Everything
	// from that base on — repaired window messages plus appended turns —
	// survives the upgrade.
	windowRawLen := 0
	if truncated {
		windowRawLen = windowLen
		if s.normalizedDirty && len(s.rawMessages) > 0 {
			windowRawLen = len(s.rawMessages)
		}
	}
	s.mu.RUnlock()
	if !truncated {
		return nil
	}
	res, err := loadSessionTranscript(context.Background(), path, defaultSessionReplayLimits, newSessionTranscriptHasher())
	if err != nil {
		return fmt.Errorf("session: complete transcript required before saving a first-paint session: %w", err)
	}
	s.mu.Lock()
	merged := res.msgs
	if windowRawLen > 0 && windowRawLen <= len(res.msgs) {
		prefix := res.msgs[:len(res.msgs)-windowRawLen]
		merged = make([]provider.Message, 0, len(res.msgs)+len(s.Messages))
		merged = append(merged, prefix...)
		merged = append(merged, s.Messages...)
	}
	s.Messages = merged
	s.tailTruncated = false
	s.tailWindowLen = 0
	if res.dag {
		// Task 239 B1-2: release DAG to shared cache; do not hold res.state on Session.
		if res.state != nil {
			sessionGraphCachePut(store.SessionEventLog(path), res.state)
		}
		s.head = sessionHeadState{ref: res.head, dag: res.dag, headCount: res.headCount, state: nil, openTurn: res.openTurn, events: res.events}
	}
	s.mu.Unlock()
	slog.Info("session: upgraded a first-paint transcript to the full one before saving",
		"path", path, "messages", len(merged))
	return nil
}
