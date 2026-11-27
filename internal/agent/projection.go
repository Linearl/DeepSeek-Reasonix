package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"reasonix/internal/fileutil"
	"reasonix/internal/provider"
	"reasonix/internal/store"
)

// Context-projection schema versions. Readers accept any known version;
// writers always emit the current schema.
const (
	compactionStateSchemaV1      = 1
	compactionStateSchemaV2      = 2
	compactionStateSchemaV3      = 3
	compactionStateSchemaV4      = 4
	compactionStateSchemaCurrent = compactionStateSchemaV4
)

// Cache state labels for resume/preflight telemetry. They never enter the
// provider-visible prompt.
const (
	CacheStateWarm    = "warm"
	CacheStateCold    = "cold"
	CacheStateUnknown = "unknown"
)

// Compaction trigger labels.
const (
	CompactionTriggerPressure = "pressure"
	CompactionTriggerManual   = "manual"
	CompactionTriggerOverflow = "overflow"
	CompactionTriggerSnip     = "snip"
	CompactionTriggerTool     = "tool"
	// tl1419:模型驱动的 ranges 小段折叠(compress_ranges 工具)——新增触发
	// 「发起源」,与阈值规则(压力/hard ceiling/overflow 安全网)叠加不替代;
	// 折叠事务与判据(foldEconomics/1396 红线)对所有触发源一视同仁。
	CompactionTriggerModelRanges = "model_ranges"
	// tl1719 压缩入口归一化：用户面手动家族的三个入口标签。五个压缩入口
	// （pressure 自动 / UI 按钮 / 斜杠 /compact / compress 工具 / serve
	// POST /compact）各带自己的标签进同一折叠核心，事件与 receipt 的
	// Trigger 字段可归因到具体入口；行为差异只允许来自 trigger 标签的
	// 显式分支。ui/slash/serve 与 manual 同属手动行为类（IsManualClassTrigger
	// 全量继承 manual 的折叠语义——裁段门/单飞预算/落地判据/失败救援全部
	// 一致），manual 本身保留给非五入口的手动道（guardian、SummarizeFrom
	// 等）。
	CompactionTriggerUI    = "ui"
	CompactionTriggerSlash = "slash"
	CompactionTriggerServe = "serve"
)

// IsManualClassTrigger reports whether t carries the manual behavior class.
// tl1719: ui/slash/serve are the entry-labelled forms of the manual family —
// every manual-class branch in the core reads this helper, never a bare
// == CompactionTriggerManual comparison, so a new entry label cannot silently
// fork behavior.
func IsManualClassTrigger(t string) bool {
	switch t {
	case CompactionTriggerManual, CompactionTriggerUI, CompactionTriggerSlash, CompactionTriggerServe:
		return true
	}
	return false
}

// Compaction mode labels.
const (
	CompactionModeNative     = "native"
	CompactionModeSummarized = "summarized"
	CompactionModeChunked    = "chunked"
	CompactionModeDegraded   = "degraded"
	CompactionModeSnip       = "snip"
	// CompactionModePointer (tl1691) is the zero-summary lane: the fold
	// installs a content-hash pointer + folded-work index instead of a digest,
	// so the fold spends no provider request at all. Verbatim content stays in
	// canonical and comes back through the recall tool.
	CompactionModePointer = "pointer"
)

const (
	SummaryInputCachePrefix        = "cache_prefix"
	SummaryInputExtensionRewritten = "extension_rewritten"
	SummaryInputNonPrefix          = "non_prefix"
	SummaryInputChunked            = "chunked"
	SummaryInputSlim               = "slim"
)

// ContextProjection is the model-visible view of a session. The canonical
// transcript in Session.Messages is never replaced by this structure.
type ContextProjection struct {
	Messages          []provider.Message `json:"messages"`
	TranscriptVersion uint64             `json:"transcript_version"`
	ProjectionVersion uint64             `json:"projection_version"`
	// CoveredCount is the canonical prefix represented by the frozen projection
	// body. Model-visible context is projection.Messages + canonical[CoveredCount:].
	CoveredCount int `json:"covered_count"`
	// CoveredPrefixHash fingerprints provider-visible canonical[:CoveredCount]
	// so append-only growth can be distinguished from prefix edits/rewrites.
	CoveredPrefixHash string `json:"covered_prefix_hash,omitempty"`
	// CoveredMessageHashes (tl1651 diagnostic, optional) fingerprints each
	// message of the sanitized covered prefix individually, in pipeline order,
	// so a covered_prefix_hash mismatch can be pinpointed to the FIRST edited
	// message on resume. Observability-only: no reader gates on it, and legacy
	// sidecars (field absent) degrade to the whole-prefix hash pair. The array
	// length is the SANITIZED pipeline length, which can differ from
	// CoveredCount when orphan tool messages are dropped or placeholders are
	// fabricated — that length delta is itself evidence of a structural rewrite.
	CoveredMessageHashes []string `json:"covered_message_hashes,omitempty"`
	// PinnedContextHash authenticates host-origin provenance inside the covered
	// canonical prefix. Provider bytes omit Origin, so CoveredPrefixHash alone
	// cannot detect provenance edits that change checkpoint reconstruction.
	PinnedContextHash string `json:"pinned_context_hash,omitempty"`
	SummaryHash       string `json:"summary_hash,omitempty"`
	SourceTokens      int    `json:"source_tokens,omitempty"`
	ProjectionTokens  int    `json:"projection_tokens,omitempty"`
	// ViewInputHash/ViewOutputHash make free maintenance idempotent across
	// retries and resume. They fingerprint the visible view, not canonical
	// storage, so a projection can evolve without rewriting the transcript.
	ViewInputHash  string    `json:"view_input_hash,omitempty"`
	ViewOutputHash string    `json:"view_output_hash,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

// ContextMaintenanceReceipt is the durable, provider-neutral outcome of one
// context maintenance transaction. Transcript content is intentionally not
// included; hashes and counts are sufficient for dedupe and diagnostics.
type ContextMaintenanceReceipt struct {
	OperationID         string `json:"operation_id,omitempty"`
	Status              string `json:"status,omitempty"` // planned|applied|noop|blocked|failed
	Action              string `json:"action,omitempty"` // snip|prune|summary|truncate|native_tool_clear|noop
	Trigger             string `json:"trigger,omitempty"`
	SourceProjection    uint64 `json:"source_projection,omitempty"`
	ProjectionVersion   uint64 `json:"projection_version,omitempty"`
	CoveredCount        int    `json:"covered_count,omitempty"`
	CoveredPrefixHash   string `json:"covered_prefix_hash,omitempty"`
	InputHash           string `json:"input_hash,omitempty"`
	OutputHash          string `json:"output_hash,omitempty"`
	InputTokens         int    `json:"input_tokens,omitempty"`
	ResultTokens        int    `json:"result_tokens,omitempty"`
	SavedTokens         int    `json:"saved_tokens,omitempty"`
	AffectedToolResults int    `json:"affected_tool_results,omitempty"`
	SummaryHash         string `json:"summary_hash,omitempty"`
	Archive             string `json:"archive,omitempty"`
	// FoldPointer (tl1691) is the zero-summary fold's content-hash pointer
	// (fold-<sha6>) naming the folded region the projection's pointer message
	// stands for. Distinct from Archive, which means the transcript was
	// archived to a NEW session; FoldPointer names an in-session fold whose
	// content stays verbatim in canonical.
	FoldPointer string `json:"fold_pointer,omitempty"`
	CacheBreak          bool   `json:"cache_break,omitempty"`
	Reason              string `json:"reason,omitempty"`
	BlockedInputHash    string `json:"blocked_input_hash,omitempty"`
	// FoldInstalled marks a failed/blocked summary refresh that follows an
	// already-installed fold in the same maintenance pass (task 303): the
	// compaction is kept and only the refresh is missing. The desktop renders
	// this state as "short view kept", not as the compaction failing (task 633).
	FoldInstalled bool      `json:"fold_installed,omitempty"`
	CreatedAt     time.Time `json:"created_at,omitempty"`
}

// CompactionOutcome reports whether compactToProjection installed a projection.
type CompactionOutcome int

const (
	// CompactionInstalled means a new (or replacement) projection was saved.
	CompactionInstalled CompactionOutcome = iota
	// CompactionNoop means no fold region / economics skip / empty fold after hooks.
	CompactionNoop
)

// RecallLedger is one generation's recall budget bookkeeping (tl1691): what
// the recall tool spent pulling folded content back into context. A new
// compaction generation starts a fresh ledger; the ledger rides the sidecar so
// a restart cannot hand the model a fresh allowance for the same generation.
type RecallLedger struct {
	Generation  uint64 `json:"generation,omitempty"`
	SpentTokens int    `json:"spent_tokens,omitempty"`
}

// CompactionState is the session context sidecar payload.
type CompactionState struct {
	SchemaVersion      int                        `json:"schema_version"`
	TranscriptVersion  uint64                     `json:"transcript_version"`
	Projection         ContextProjection          `json:"projection"`
	PromptCacheKey     string                     `json:"prompt_cache_key,omitempty"`
	LastCacheState     string                     `json:"last_cache_state,omitempty"`
	LastTrigger        string                     `json:"last_trigger,omitempty"`
	LastMode           string                     `json:"last_mode,omitempty"`
	LastSourceTokens   int                        `json:"last_source_tokens,omitempty"`
	LastResultTokens   int                        `json:"last_result_tokens,omitempty"`
	LastCompactionCost float64                    `json:"last_compaction_cost,omitempty"`
	Generation         uint64                     `json:"generation,omitempty"`
	LastReceipt        *ContextMaintenanceReceipt `json:"last_receipt,omitempty"`
	BlockedInputHash   string                     `json:"blocked_input_hash,omitempty"`
	BlockedReason      string                     `json:"blocked_reason,omitempty"`
	// Recall (tl1691) books the current generation's recall spend. A pointer
	// keeps the JSON empty for sessions that never recall, and needs no schema
	// bump: readers predating the field ignore it (the established
	// unknown-field tolerance on this sidecar).
	Recall *RecallLedger `json:"recall,omitempty"`
	// NativeContextEditingAccepted latches the first successful native request.
	// ContextEditingFallbackLocal persists the only allowed request-shape switch:
	// an explicit unsupported response before that latch was set.
	NativeContextEditingAccepted bool      `json:"native_context_editing_accepted,omitempty"`
	ContextEditingFallbackLocal  bool      `json:"context_editing_fallback_local,omitempty"`
	UpdatedAt                    time.Time `json:"updated_at"`
}

// CompactionTelemetry is the structured observability record for one
// compaction attempt. Sensitive transcript content is intentionally omitted.
type CompactionTelemetry struct {
	Trigger           string `json:"trigger"`
	CacheState        string `json:"cache_state"`
	Mode              string `json:"mode"`
	Native            bool   `json:"native"`
	SourceTokens      int    `json:"source_tokens"`
	FoldTokens        int    `json:"fold_tokens"` // summarizer input after any shortening
	Spans             int    `json:"spans"`       // summarizer calls the fold needed; 1 unless it was split
	ProjectionTokens  int    `json:"projection_tokens"`
	UserTurnsKept     int    `json:"user_turns_kept"`
	UserTurnsDropped  int    `json:"user_turns_dropped"` // past the retention budget, now summary-only
	InputTokens       int    `json:"input_tokens"`
	OutputTokens      int    `json:"output_tokens"`
	CacheHitTokens    int    `json:"cache_hit_tokens"`
	CacheMissTokens   int    `json:"cache_miss_tokens"`
	CacheWriteTokens  int    `json:"cache_write_tokens"`
	RequestCount      int    `json:"request_count"`
	ProviderRequestID string `json:"provider_request_id,omitempty"`
	SummaryInputMode  string `json:"summary_input_mode,omitempty"`
	Error             string `json:"error,omitempty"`
	// tl1397 ranges 观测面(实验档,全量路径恒零值):RangesFold 标记本次为
	// 小段增量折叠;RangesFoldIndex 是会话内序号(频次半边);PreservedTokens
	// 是保头前缀的计费尺读数;RangesCacheHit 是装前以 1396 态③口径实测的
	// 命中率(缓存半边)。「频次×缓存抖动」联合观测由此出数,迭代三洪水闸
	// 在此定参。
	RangesFold      bool    `json:"ranges_fold,omitempty"`
	RangesFoldIndex int     `json:"ranges_fold_index,omitempty"`
	PreservedTokens int     `json:"preserved_tokens,omitempty"`
	RangesCacheHit  float64 `json:"ranges_cache_hit,omitempty"`
	// tl1695: 压力道分割活跃轮较早轮次（splitActive）时置位。done 行与遥测
	// 消费——「压后仍过半/追着回合跑」修复面的观测半边；false = 折叠区未
	// 越过活跃轮边界（含 manual/tool 的钳制语义，属预期形态）。
	SplitActive bool `json:"split_active,omitempty"`
}

// ContextStatePath returns the projection sidecar path for a session transcript.
func ContextStatePath(sessionPath string) string {
	return store.SessionContext(sessionPath)
}

// LoadCompactionState reads the context sidecar. Missing files return ok=false.
// Corrupt or unsupported schema returns an error so callers can drop and rebuild.
func LoadCompactionState(sessionPath string) (CompactionState, bool, error) {
	path := ContextStatePath(sessionPath)
	if path == "" {
		return CompactionState{}, false, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return CompactionState{}, false, nil
		}
		return CompactionState{}, false, err
	}
	var st CompactionState
	if err := json.Unmarshal(b, &st); err != nil {
		return CompactionState{}, false, fmt.Errorf("decode context state %s: %w", path, err)
	}
	if st.SchemaVersion != 0 && st.SchemaVersion != compactionStateSchemaV1 && st.SchemaVersion != compactionStateSchemaV2 &&
		st.SchemaVersion != compactionStateSchemaV3 && st.SchemaVersion != compactionStateSchemaV4 {
		return CompactionState{}, false, fmt.Errorf("unsupported context schema version %d", st.SchemaVersion)
	}
	if st.SchemaVersion == 0 {
		st.SchemaVersion = compactionStateSchemaV1
	}
	// tl1651 打点 d (load-side anchor): the resume-time ordering — LoadSession
	// (with its transcript repair) → sidecar read → content validation — is
	// exactly the sequence the 20261116 restart incident needs to reconstruct.
	// Debug level: the caller-level restored/drop lines already carry the
	// verdict; this stamps the raw read with the on-disk sidecar's own state.
	// tl1651 主修①: the read path itself is stamped too — the 20261117
	// "sidecar_updated_at=2026-10-26 in a session created 2026-11-15" incident
	// could not name WHICH file served the stale reading. The load path is
	// deterministic (SessionContext of the bound transcript — backups/recovery
	// copies are never consulted; pinned by TestTL1651R1BackupSidecarNotRead),
	// so the logged path doubles as the source-of-truth audit trail.
	slog.Debug("agent: context sidecar loaded",
		"writer", SessionWriterID(),
		"path", path,
		"schema_version", st.SchemaVersion,
		"projection_version", st.Projection.ProjectionVersion,
		"covered_count", st.Projection.CoveredCount,
		"sidecar_updated_at", st.UpdatedAt.UTC().Format(time.RFC3339Nano),
		"sidecar_msg_count", len(st.Projection.Messages),
		"per_msg_hashes", len(st.Projection.CoveredMessageHashes))
	return st, true, nil
}

// SaveCompactionState writes the sidecar via strict atomic publish (temp +
// file fsync + rename + best-effort parent-dir fsync). Checkpoint sidecars are
// commit pointers: EXDEV/copy fallbacks that can tear an existing file are
// rejected so a failed write leaves the previous checkpoint intact. A returned
// error means the on-disk pointer was not published.
func SaveCompactionState(sessionPath string, st CompactionState) error {
	path := ContextStatePath(sessionPath)
	if path == "" {
		return fmt.Errorf("empty session path")
	}
	// V4 adds a canonical-coverage pinned-context checkpoint. Previous readers
	// fail closed on the unknown schema and replay canonical history.
	st.SchemaVersion = compactionStateSchemaCurrent
	if st.UpdatedAt.IsZero() {
		st.UpdatedAt = time.Now().UTC()
	}
	// LastReceipt is authoritative. Drop mirrored top-level last_*/blocked_*
	// writer fields so new sidecars do not re-emit the pre-v3 dual schema.
	// Old files with those keys still decode into the struct for readers.
	st.LastTrigger = ""
	st.LastMode = ""
	st.LastSourceTokens = 0
	st.LastResultTokens = 0
	st.LastCompactionCost = 0
	if st.LastReceipt != nil {
		st.BlockedInputHash = ""
		st.BlockedReason = ""
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	err = fileutil.AtomicWriteFileStrict(path, b, 0o644)
	// tl1651 打点 d (save-side anchor): every sidecar publish is stamped so the
	// shutdown/save ordering against the transcript snapshot can be read
	// straight off the log timeline (the 20261114→15 session's sidecar was
	// written ~14h before the restart; knowing WHICH publish produced it rules
	// the shutdown-flush hypothesis in or out directly). Low frequency (fold
	// commits, lineage rebinds): Info is proportionate.
	if err == nil {
		slog.Info("agent: context sidecar saved",
			"writer", SessionWriterID(),
			"projection_version", st.Projection.ProjectionVersion,
			"covered_count", st.Projection.CoveredCount,
			"transcript_version", st.TranscriptVersion,
			"sidecar_msg_count", len(st.Projection.Messages),
			"per_msg_hashes", len(st.Projection.CoveredMessageHashes),
			"bytes", len(b),
			"path", path)
	}
	return err
}

// RemoveCompactionState deletes a corrupt or invalidated projection sidecar.
func RemoveCompactionState(sessionPath string) error {
	path := ContextStatePath(sessionPath)
	if path == "" {
		return nil
	}
	err := os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// summaryContentHash fingerprints a compaction summary for projection metadata.
func summaryContentHash(summary string) string {
	if summary == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(summary))
	return hex.EncodeToString(sum[:16])
}

// transientStrippedView returns a request-side copy of msgs with host-injected
// transient blocks removed from user turns (tl1831r2). The blocks registered in
// TransientUserBlockTags (current-time, context-state, language pins, …) are
// runtime decoration the host prepends per turn — preview/title derivation,
// steer recognition, and the reasoning-replay anchor already strip them
// (StripTransientUserBlocks is the single source of truth). The covered-prefix
// fingerprint joins them: the hash exists to distinguish "conversation grew
// append-only" from "history was rewritten", and a rendering change to a clock
// anchor is neither. Everything else (roles, assistant/tool bytes, tool-call
// signatures) is passed through untouched.
func transientStrippedView(msgs []provider.Message) []provider.Message {
	out := make([]provider.Message, len(msgs))
	copy(out, msgs)
	for i := range out {
		if out[i].Role == provider.RoleUser {
			out[i].Content = StripTransientUserBlocks(out[i].Content)
		}
	}
	return out
}

// coveredPrefixHash fingerprints the current model-visible prefix of msgs[:n].
// Tool Content is the stable bounded provider representation; RawContent is
// local-only. SanitizeToolPairing applies the same deterministic repair used on
// the wire, keeping hashes stable when LoadSession repairs a transcript.
//
// tl1831r2: transient user-turn blocks are stripped before fingerprinting.
// The pre-fix recipe fingerprinted them verbatim, so any change to their
// rendering — the tl1831 context-state copy change being the incident — read
// as a prefix rewrite and discarded the whole fold (20261127 装机重启:
// covered_count 2371→0, 2.9M-token full-canonical fallback, 29-minute chunked
// compaction). The stripped recipe still fails closed on real edits: user
// prose, assistant/tool bytes, and tool-call signatures all keep hashing.
func coveredPrefixHash(msgs []provider.Message, n int) string {
	if n <= 0 || n > len(msgs) {
		return ""
	}
	visible := transientStrippedView(modelInputMessages(msgs[:n]))
	return providerVisibleFingerprint(provider.SanitizeToolPairing(visible))
}

// legacyTransientIncludedCoveredPrefixHash reproduces the pre-tl1831r2
// fingerprint (transient blocks included) exactly as every sidecar written
// before the recipe change sealed it. It exists only so
// migrateTransientStrippedPrefixHash can recognize those sidecars once; new
// checkpoints always use coveredPrefixHash.
func legacyTransientIncludedCoveredPrefixHash(msgs []provider.Message, n int) string {
	if n <= 0 || n > len(msgs) {
		return ""
	}
	visible := modelInputMessages(msgs[:n])
	return providerVisibleFingerprint(provider.SanitizeToolPairing(visible))
}

// coveredMessageFingerprints (tl1651) fingerprints each message of the
// sanitized covered prefix individually, through the exact pipeline
// coveredPrefixHash uses (transient-strip → modelInputMessages →
// SanitizeToolPairing), so a whole-prefix mismatch can be narrowed to the
// first edited message. The per-message hashes are read side by side against
// the sidecar's persisted CoveredMessageHashes; the index where they diverge
// is the rewrite point. tl1831r2: same recipe as coveredPrefixHash, so a
// transient-only rendering change never shows up as a per-message divergence.
func coveredMessageFingerprints(msgs []provider.Message, n int) []string {
	if n <= 0 || n > len(msgs) {
		return nil
	}
	visible := provider.SanitizeToolPairing(transientStrippedView(modelInputMessages(msgs[:n])))
	out := make([]string, len(visible))
	for i := range visible {
		out[i] = providerVisibleFingerprint(visible[i : i+1])
	}
	return out
}

// migrateTransientStrippedPrefixHash (tl1831r2) is the one-time lane for
// sidecars sealed under the pre-tl1831r2 recipe, which fingerprinted transient
// user-turn blocks verbatim. Without it, the first load after the upgrade
// recomputes every prefix hash under the stripped recipe, mismatches every
// old sidecar, and reproduces exactly the wholesale-drop fallback the recipe
// change was meant to prevent. Migration is re-anchoring only: the stored hash
// and the diagnostic per-message corpus are repointed at the current
// transcript under the new recipe; the projection body is untouched.
//
// Gates are deliberately strict; every non-passing shape stays invalid so
// real rewrites are never legalized:
//  1. Recipe gate — the stored hash must equal the LEGACY recipe recomputed
//     over the current covered prefix. A sidecar sealed under the new recipe
//     that mismatches has real content drift and is left for the drop path.
//  2. Load-repair gate (B-corpus) — when this load's normalization rewrote
//     provider-visible bytes (tl1651's repair family), the legacy recipe must
//     match the PRE-REPAIR disk prefix instead, and the stripped-recipe first
//     fingerprints of both views must agree (same shared-head proof
//     migrateLoadRepairedPrefixHash requires).
//
// No schema bump accompanies the recipe change: bumping would make readers
// reject every existing sidecar outright (fail into RemoveCompactionState and
// a full rebuild) — the catastrophe this lane exists to avoid. The
// dual-recipe re-anchor follows the migratePromotedCoveredPrefixHash precedent.
func migrateTransientStrippedPrefixHash(st *CompactionState, msgs, preRepair []provider.Message) bool {
	if st == nil {
		return false
	}
	n := st.Projection.CoveredCount
	if n <= 0 || n > len(msgs) {
		return false
	}
	stored := st.Projection.CoveredPrefixHash
	if stored == "" {
		return false
	}
	currentHash := coveredPrefixHash(msgs, n)
	if currentHash == "" || currentHash == stored {
		return false
	}
	matched := legacyTransientIncludedCoveredPrefixHash(msgs, n) == stored
	if !matched && len(preRepair) > 0 && n <= len(preRepair) &&
		legacyTransientIncludedCoveredPrefixHash(preRepair, n) == stored {
		preFP := coveredMessageFingerprints(preRepair, n)
		curFP := coveredMessageFingerprints(msgs, n)
		if len(preFP) == 0 || len(curFP) == 0 || preFP[0] != curFP[0] {
			return false
		}
		matched = true
	}
	if !matched {
		return false
	}
	st.Projection.CoveredPrefixHash = currentHash
	// tl1651: keep the diagnostic per-message corpus in step with the migrated
	// prefix hash, so later mismatches localize against the migrated form.
	st.Projection.CoveredMessageHashes = coveredMessageFingerprints(msgs, n)
	if st.LastReceipt != nil && st.LastReceipt.CoveredPrefixHash == stored {
		receipt := *st.LastReceipt
		receipt.CoveredPrefixHash = currentHash
		st.LastReceipt = &receipt
	}
	slog.Info("agent: transient-included covered prefix migrated",
		"writer", SessionWriterID(),
		"covered_count", n,
		"stored_prefix_hash", shortHash8(stored),
		"migrated_prefix_hash", shortHash8(currentHash))
	return true
}

// projectionMismatchEvidence is the tl1651 diagnosis of one hash_mismatch
// failure: which fail path fired, and — when a comparison corpus exists — the
// first diverging message with both sides' fingerprints. It is only ever
// logged, never persisted and never read back for decisions.
type projectionMismatchEvidence struct {
	StoredPrefixHash  string // sidecar's CoveredPrefixHash
	CurrentPrefixHash string // recomputed from the current transcript
	CoveredCount      int
	// Source names the corpus the per-message diff ran against:
	//   "load_repair"      — the pre-repair transcript (rawMessages) still
	//                        matches the stored hash, so the rewrite happened
	//                        during THIS process's LoadSession normalization.
	//   "persisted_hashes" — the sidecar's CoveredMessageHashes array.
	//   "none"             — no corpus; only the whole-prefix pair is known.
	Source         string
	DiffIndex      int    // first diverging index in the sanitized pipeline order; -1 unknown
	StoredMsgHash  string // stored fingerprint at DiffIndex ("" when unavailable)
	CurrentMsgHash string // current fingerprint at DiffIndex
	DiffRole       string // role of the current message at DiffIndex
	DiffOrigin     string // origin of the current message at DiffIndex
	StoredMsgLen   int    // sanitized pipeline length the stored side describes
	CurrentMsgLen  int    // sanitized pipeline length the current transcript produces
}

// diagnoseProjectionMismatch localizes a covered-prefix hash mismatch (tl1651
// 打点 b). It prefers the load-repair corpus (zero-cost, and it attributes the
// single most likely writer — LoadSession's NormalizeSession repair —
// directly), then the persisted per-message hashes, then degrades to the
// whole-prefix hash pair. Pure function: reads only its arguments.
func diagnoseProjectionMismatch(st CompactionState, msgs, preRepair []provider.Message) projectionMismatchEvidence {
	n := st.Projection.CoveredCount
	ev := projectionMismatchEvidence{
		StoredPrefixHash:  st.Projection.CoveredPrefixHash,
		CurrentPrefixHash: coveredPrefixHash(msgs, n),
		CoveredCount:      n,
		DiffIndex:         -1,
	}
	currentFP := coveredMessageFingerprints(msgs, n)
	ev.CurrentMsgLen = len(currentFP)

	// B1: load-repair attribution. If the pre-repair transcript still hashes to
	// the stored value, the covered prefix was rewritten by THIS load's
	// normalization, and the first diverging pipeline slot names the repaired
	// message.
	if len(preRepair) > 0 && n > 0 && n <= len(preRepair) &&
		coveredPrefixHash(preRepair, n) == st.Projection.CoveredPrefixHash {
		ev.Source = "load_repair"
		preFP := coveredMessageFingerprints(preRepair, n)
		ev.StoredMsgLen = len(preFP)
		ev.StoredMsgHash, ev.DiffIndex, ev.DiffRole, ev.DiffOrigin = firstFingerprintDivergence(preFP, currentFP, msgs)
		return ev
	}
	// B2: persisted per-message hashes from the sidecar.
	if stored := st.Projection.CoveredMessageHashes; len(stored) > 0 {
		ev.Source = "persisted_hashes"
		ev.StoredMsgLen = len(stored)
		ev.StoredMsgHash, ev.DiffIndex, ev.DiffRole, ev.DiffOrigin = firstFingerprintDivergence(stored, currentFP, msgs)
		return ev
	}
	// B3: no corpus — the whole-prefix pair is all the evidence available.
	ev.Source = "none"
	return ev
}

// firstFingerprintDivergence returns the first index where stored and current
// per-message fingerprints differ, with the stored fingerprint at that slot and
// a best-effort role/origin of the current message there. A length mismatch is
// itself the divergence (structural insert/delete inside the covered prefix):
// the slot is the shorter length, i.e. the first position where one side has no
// counterpart. Role/origin are read from the CURRENT canonical transcript at
// the pipeline index — approximate when sanitization dropped or fabricated
// earlier messages, exact when the pipeline is 1:1 with canonical (the common
// healthy-history case, since the fast path passes clean input through).
func firstFingerprintDivergence(stored, current []string, msgs []provider.Message) (storedHash string, idx int, role, origin string) {
	idx = -1
	limit := len(stored)
	if len(current) < limit {
		limit = len(current)
	}
	for i := 0; i < limit; i++ {
		if stored[i] != current[i] {
			storedHash, idx = stored[i], i
			break
		}
	}
	if idx < 0 && len(stored) != len(current) {
		idx = limit
	}
	if idx < 0 {
		return "", -1, "", ""
	}
	if idx < len(msgs) {
		role = string(msgs[idx].Role)
		origin = string(msgs[idx].Origin)
	}
	return storedHashOr(stored, idx), idx, role, origin
}

func storedHashOr(stored []string, idx int) string {
	if idx >= 0 && idx < len(stored) {
		return stored[idx]
	}
	return ""
}

// boundedCoveredPrefixHash is the v3 bounded provider fingerprint. Keep the
// named helper for old sidecar and load-repair compatibility tests.
func boundedCoveredPrefixHash(msgs []provider.Message, n int) string {
	if n <= 0 || n > len(msgs) {
		return ""
	}
	visible := provider.ModelMessages(msgs[:n])
	return providerVisibleFingerprint(provider.SanitizeToolPairing(visible))
}

// promotedCoveredPrefixHash reproduces the temporary v3 behavior that promoted
// full tool RawContent into every provider request.
func promotedCoveredPrefixHash(msgs []provider.Message, n int) string {
	if n <= 0 || n > len(msgs) {
		return ""
	}
	promoted := append([]provider.Message(nil), msgs[:n]...)
	for i := range promoted {
		if promoted[i].Role == provider.RoleTool && promoted[i].RawContent != "" {
			promoted[i].Content = promoted[i].RawContent
		}
	}
	return providerVisibleFingerprint(provider.SanitizeToolPairing(provider.ModelMessages(promoted)))
}

// normalizePromotedProjectionToolBodies converts the provider-visible tool
// bodies persisted by the temporary RawContent-promoting implementation back
// to canonical bounded Content. Every tool message must match a canonical tool
// result exactly by identity and old provider-visible body. Duplicate call IDs
// are safe only when every matching candidate maps to the same bounded body.
func normalizePromotedProjectionToolBodies(projection, canonical []provider.Message, n int) ([]provider.Message, bool) {
	if n <= 0 || n > len(canonical) {
		return nil, false
	}
	normalized := append([]provider.Message(nil), projection...)
	for i, projected := range normalized {
		if projected.Role != provider.RoleTool {
			continue
		}
		visibleBody := projected.Content
		if projected.ProviderContent != "" {
			visibleBody = projected.ProviderContent
		}
		boundedBody := ""
		matched := false
		for _, candidate := range canonical[:n] {
			if candidate.Role != provider.RoleTool || candidate.ToolCallID != projected.ToolCallID || candidate.Name != projected.Name {
				continue
			}
			matchesBounded := visibleBody == candidate.Content
			matchesPromoted := candidate.RawContent != "" && visibleBody == candidate.RawContent
			if !matchesBounded && !matchesPromoted {
				continue
			}
			if matched && boundedBody != candidate.Content {
				return nil, false
			}
			boundedBody = candidate.Content
			matched = true
		}
		if !matched {
			return nil, false
		}
		normalized[i].Content = boundedBody
		normalized[i].RawContent = ""
		normalized[i].ProviderContent = ""
	}
	return normalized, true
}

// migratePromotedCoveredPrefixHash normalizes a sidecar written while full tool
// RawContent was model-visible. Migration is exact and atomic: both its hash and
// retained tool bodies must match the historical form. Unrelated, stale, or
// ambiguous sidecars stay invalid so callers drop only their projection body.
func migratePromotedCoveredPrefixHash(st *CompactionState, msgs []provider.Message) bool {
	if st == nil {
		return false
	}
	n := st.Projection.CoveredCount
	stored := st.Projection.CoveredPrefixHash
	currentHash := coveredPrefixHash(msgs, n)
	if stored == "" || currentHash == "" || stored == currentHash ||
		stored != promotedCoveredPrefixHash(msgs, n) {
		return false
	}
	normalizedMessages, ok := normalizePromotedProjectionToolBodies(st.Projection.Messages, msgs, n)
	if !ok {
		return false
	}
	st.Projection.Messages = normalizedMessages
	st.Projection.CoveredPrefixHash = currentHash
	// tl1651: keep the diagnostic per-message corpus in step with the migrated
	// prefix hash, so later mismatches localize against the migrated form.
	st.Projection.CoveredMessageHashes = coveredMessageFingerprints(msgs, n)
	if st.LastReceipt != nil && st.LastReceipt.CoveredPrefixHash == stored {
		receipt := *st.LastReceipt
		receipt.CoveredPrefixHash = currentHash
		st.LastReceipt = &receipt
	}
	return true
}

// legacyCoveredPrefixHash reproduces the v1.25.2 fingerprint. It is used only
// to migrate a sidecar whose persisted pre-repair transcript is still available;
// new checkpoints always use coveredPrefixHash.
func legacyCoveredPrefixHash(msgs []provider.Message, n int) string {
	if n <= 0 || n > len(msgs) {
		return ""
	}
	return providerVisibleFingerprint(provider.ModelMessages(msgs[:n]))
}

// migrateLegacyCoveredPrefixHash upgrades a v1.25.2 sidecar after LoadSession
// performed a deterministic provider-visible repair. It is deliberately strict:
// the stored legacy hash must match the exact pre-repair disk prefix, and that
// prefix's wire-safe form must equal the current prefix. A real history or
// system-prompt change therefore remains invalid.
func migrateLegacyCoveredPrefixHash(st *CompactionState, current, preRepair []provider.Message) bool {
	if st == nil || len(preRepair) == 0 {
		return false
	}
	n := st.Projection.CoveredCount
	stored := st.Projection.CoveredPrefixHash
	if stored == "" || legacyCoveredPrefixHash(preRepair, n) != stored {
		return false
	}
	preRepairWireHash := boundedCoveredPrefixHash(preRepair, n)
	currentHash := coveredPrefixHash(current, n)
	if currentHash == "" || preRepairWireHash != boundedCoveredPrefixHash(current, n) {
		return false
	}
	st.Projection.CoveredPrefixHash = currentHash
	// tl1651: repopulate the diagnostic corpus against the current transcript.
	st.Projection.CoveredMessageHashes = coveredMessageFingerprints(current, n)
	if st.LastReceipt != nil && st.LastReceipt.CoveredPrefixHash == stored {
		receipt := *st.LastReceipt
		receipt.CoveredPrefixHash = currentHash
		st.LastReceipt = &receipt
	}
	return true
}

// migrateLoadRepairedPrefixHash (tl1651 修复③ / 候选 A1) gives the
// load-repair rewrite a legitimate migration lane, symmetric to
// migrateLegacyCoveredPrefixHash. On an auto-resume, LoadSession's
// NormalizeSession rewrites provider-visible bytes of an interrupted turn; a
// sidecar sealed against the pre-repair bytes then fails first-load
// verification (hash_mismatch) and used to be discarded wholesale — the
// 20261116 incident's 1.21M-token full-canonical fallback. When the diagnosis
// proves the rewrite was THIS load's deterministic repair, re-anchoring the
// stored hash to the repaired transcript is safe.
//
// The gates are deliberately strict:
//  1. B1 attribution gate — the stored hash must equal the PRE-REPAIR disk
//     prefix. A third-party prefix rewrite (the tl1644 silent-rewrite family)
//     changes the on-disk bytes themselves, so the stored hash no longer
//     matches what this load read and the gate fails closed: real rewrites are
//     never legalized into a migrated fold.
//  2. Shared-prefix gate — the FIRST sanitized message must fingerprint
//     identically on both sides. LoadSession repairs never touch the leading
//     identity header; a mismatch there means the "repair" reshaped the
//     conversation's head, and migration refuses.
//
// On success the sidecar hash, the per-message diagnostic corpus, and a
// matching last receipt are all repointed at the repaired transcript, and the
// migration is logged (it carries the covered count — the repair-migrated
// fold then serves the wire instead of forcing a rebuild).
func migrateLoadRepairedPrefixHash(st *CompactionState, current, preRepair []provider.Message) bool {
	if st == nil || len(preRepair) == 0 {
		return false
	}
	n := st.Projection.CoveredCount
	if n <= 0 || n > len(current) || n > len(preRepair) {
		return false
	}
	stored := st.Projection.CoveredPrefixHash
	if stored == "" || stored != coveredPrefixHash(preRepair, n) {
		slog.Debug("agent: load-repair migration gate closed",
			"writer", SessionWriterID(),
			"gate", "b1_stored_matches_pre_repair",
			"covered_count", n)
		return false
	}
	currentHash := coveredPrefixHash(current, n)
	if currentHash == "" || currentHash == stored {
		return false
	}
	preFP := coveredMessageFingerprints(preRepair, n)
	curFP := coveredMessageFingerprints(current, n)
	if len(preFP) == 0 || len(curFP) == 0 || preFP[0] != curFP[0] {
		slog.Debug("agent: load-repair migration gate closed",
			"writer", SessionWriterID(),
			"gate", "shared_first_fingerprint",
			"covered_count", n)
		return false
	}
	st.Projection.CoveredPrefixHash = currentHash
	st.Projection.CoveredMessageHashes = coveredMessageFingerprints(current, n)
	if st.LastReceipt != nil && st.LastReceipt.CoveredPrefixHash == stored {
		receipt := *st.LastReceipt
		receipt.CoveredPrefixHash = currentHash
		st.LastReceipt = &receipt
	}
	slog.Info("agent: load-repaired covered prefix migrated",
		"writer", SessionWriterID(),
		"covered_count", n,
		"msgs", len(current))
	return true
}

// projectionRepairDriftKeep (tl1785) decides whether a first-load
// hash_mismatch is attributable to the transcript-repair rewrite family —
// NormalizeSession repairs accumulated across loads, or an installer
// upgrade's changed normalization rules (the tl1116 recurrence family, e.g.
// the 20261123 装机重启 incident: stored prefix 6bc3bee8 vs current 8c6106d0,
// whole body dropped, 2,011,859-token full-canonical fallback) — in which case
// the projection body is kept and marked stale instead of being discarded.
//
// The gates are deliberately fail-closed; every non-passing shape keeps the
// pre-tl1785 wholesale drop:
//
//  1. Evidence gate — the sidecar must carry the tl1651 per-message corpus
//     (CoveredMessageHashes). Without it there is nothing to attribute with.
//  2. Structural gate — the current sanitized pipeline must have the same
//     length as the stored corpus AND be 1:1 with the covered canonical
//     prefix. An insert/delete inside the covered prefix (rewind, branch
//     splice, orphan-tool fabrication) is a structural rewrite, never repair
//     drift, and also breaks the pipeline-index → canonical-index mapping the
//     role gate relies on.
//  3. Role gate — every diverging slot must sit on an assistant or tool
//     message. The repair family only rewrites assistant wire bytes (tool-call
//     name backfill #4727, truncated-argument repair #3953, decision-receipt
//     folding) and tool bytes (placeholder pairing); a user or system edit is
//     real divergence and still drops.
//
// This lane NEVER re-anchors the stored hash and never republishes the
// sidecar: the kept body stays content-invalid, so it serves only through the
// tl1203 bounded stale splice while a forced rebuild replaces it — a
// deliberate contrast with migrateLoadRepairedPrefixHash, whose stronger
// proof (stored == this load's pre-repair disk prefix) justifies restoring the
// fold as fully valid. The single-slot empty-shell corpus migration remains
// the tl1770 乙案 candidate and is intentionally NOT implemented here.
//
// Returns the diverging pipeline indexes (non-empty when the keep applies) so
// the degrade log can name the rewritten slots; an empty slice means drop.
func projectionRepairDriftKeep(st CompactionState, msgs []provider.Message) []int {
	n := st.Projection.CoveredCount
	if n <= 0 || n > len(msgs) {
		return nil
	}
	corpus := st.Projection.CoveredMessageHashes
	if len(corpus) == 0 {
		return nil
	}
	current := coveredMessageFingerprints(msgs, n)
	// Structural gate: equal pipeline lengths, each 1:1 with the covered
	// canonical prefix (no sanitize drops or fabrications on either side).
	if len(current) != len(corpus) || len(current) != n {
		return nil
	}
	var diverging []int
	for i := 0; i < len(corpus); i++ {
		if corpus[i] == current[i] {
			continue
		}
		// Role gate: repair rewrites only ever land on assistant/tool slots.
		// Pipeline is 1:1 with canonical here, so msgs[i] is the rewritten
		// message itself.
		switch msgs[i].Role {
		case provider.RoleAssistant, provider.RoleTool:
		default:
			return nil
		}
		diverging = append(diverging, i)
	}
	if len(diverging) == 0 {
		// Whole-prefix hash mismatch with an all-equal per-message corpus is a
		// fingerprint-pipeline discrepancy — fail closed.
		return nil
	}
	return diverging
}

// providerVisibleFingerprint is the stable hash of fields that reach a provider.
func providerVisibleFingerprint(msgs []provider.Message) string {
	type wireCall struct {
		ID               string `json:"id,omitempty"`
		Name             string `json:"name,omitempty"`
		Arguments        string `json:"args,omitempty"`
		ThoughtSignature string `json:"ts,omitempty"`
	}
	type wireMsg struct {
		Role               string                      `json:"r"`
		Content            string                      `json:"c,omitempty"`
		Images             []string                    `json:"img,omitempty"`
		Audio              []string                    `json:"aud,omitempty"`
		Video              []string                    `json:"vid,omitempty"`
		ReasoningContent   string                      `json:"rc,omitempty"`
		ReasoningID        string                      `json:"rid,omitempty"`
		ReasoningStatus    string                      `json:"rst,omitempty"`
		ReasoningSignature string                      `json:"rsig,omitempty"`
		ToolCallID         string                      `json:"tid,omitempty"`
		Name               string                      `json:"n,omitempty"`
		ToolCalls          []wireCall                  `json:"tc,omitempty"`
		ThinkingBlocks     []provider.ThinkingBlock    `json:"tb,omitempty"`
		ResponsesItems     []json.RawMessage           `json:"ri,omitempty"`
		ServerSearch       []provider.ServerSearchCall `json:"ss,omitempty"`
	}
	wire := make([]wireMsg, 0, len(msgs))
	for _, m := range msgs {
		wm := wireMsg{
			Role:               string(m.Role),
			Content:            m.Content,
			Images:             append([]string(nil), m.Images...),
			Audio:              append([]string(nil), m.Audio...),
			Video:              append([]string(nil), m.Video...),
			ReasoningContent:   m.ReasoningContent,
			ReasoningID:        m.ReasoningID,
			ReasoningStatus:    m.ReasoningStatus,
			ReasoningSignature: m.ReasoningSignature,
			ThinkingBlocks:     m.ThinkingBlocks,
			ToolCallID:         m.ToolCallID,
			Name:               m.Name,
		}
		for _, tc := range m.ToolCalls {
			wm.ToolCalls = append(wm.ToolCalls, wireCall{
				ID: tc.ID, Name: tc.Name, Arguments: tc.Arguments, ThoughtSignature: tc.ThoughtSignature,
			})
		}
		if len(m.ResponsesItems) > 0 {
			wm.ResponsesItems = make([]json.RawMessage, len(m.ResponsesItems))
			for i, item := range m.ResponsesItems {
				wm.ResponsesItems[i] = append(json.RawMessage(nil), item...)
			}
		}
		if len(m.ServerSearch) > 0 {
			wm.ServerSearch = append([]provider.ServerSearchCall(nil), m.ServerSearch...)
		}
		wire = append(wire, wm)
	}
	b, err := json.Marshal(wire)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:16])
}

// projectionValid reports whether st can be reused for the current transcript.
// 任务638: validity is content-authoritative. The lineage key (workspace |
// branch | model ref) names a prompt-cache namespace, not a validity gate — a
// key change such as a model hot switch rebinds the namespace (see
// rebindProjectionLineage, the runtime twin of LoadProjectionSidecar's
// key-change fallback) instead of invalidating the fold, while real content
// drift still fails closed through the covered-prefix hash inside
// projectionContentValid.
func projectionValid(st CompactionState, msgs []provider.Message) bool {
	if len(st.Projection.Messages) == 0 {
		return false
	}
	return projectionContentValid(st, msgs)
}

// tl1651 fail-path labels for projectionContentValid. Before tl1651 every
// failure below was reported as the single opaque "projection_body_invalid",
// which made the 20261116 14:45 restart incident (projection dropped to a
// 1.21M-token full-canonical view) impossible to attribute. These strings are
// observability-only: they never gate behavior.
const (
	projectionFailNone            = "" // valid
	projectionFailBodyEmpty       = "projection_body_empty"
	projectionFailCoveredOverflow = "covered_count_overflow"
	projectionFailLegacyNoHash    = "legacy_no_hash"
	projectionFailPinned          = "pinned_provenance"
	projectionFailHashMismatch    = "hash_mismatch"
)

// projectionContentValid reports whether st's projection body still matches the
// canonical transcript, independent of provider/model lineage. The covered hash
// is authoritative, except for leading system messages: those live outside the
// folded region and may be refreshed independently after a compaction.
func projectionContentValid(st CompactionState, msgs []provider.Message) bool {
	return projectionInvalidReason(st, msgs) == projectionFailNone
}

// projectionInvalidReason is the tl1651 diagnostic twin of
// projectionContentValid: it returns why the projection body cannot serve the
// wire, with one distinct label per failure exit. The two functions share a
// single logic source — projectionContentValid is exactly "reason is empty" —
// so the labels can never drift from the actual gates. Zero behavior change:
// every branch below mirrors the pre-tl1651 projectionContentValid verbatim.
// tl1644 merge note: tl1644's parallel projectionContentInvalidReason
// (string-literal six-branch variant) was resolved INTO this function as the
// single logic source — tl1644's sidecar-drop WARN consumer reads these
// constants instead. Do not reintroduce a second verdict function.
func projectionInvalidReason(st CompactionState, msgs []provider.Message) string {
	if len(st.Projection.Messages) == 0 {
		return projectionFailBodyEmpty
	}
	n := st.Projection.CoveredCount
	if n <= 0 || n > len(msgs) {
		return projectionFailCoveredOverflow
	}
	// Prefix hash is required; legacy sidecars without it are rebuilt.
	if st.Projection.CoveredPrefixHash == "" {
		return projectionFailLegacyNoHash
	}
	// Readers before v4 did not authenticate pinned revision provenance. Their
	// projections may summarize or omit active pinned state, so fail closed and
	// rebuild a v4 checkpoint from the canonical transcript.
	if st.SchemaVersion < compactionStateSchemaV4 && containsPinnedContextRevision(msgs[:n]) {
		return projectionFailPinned
	}
	if st.SchemaVersion >= compactionStateSchemaV4 &&
		st.Projection.PinnedContextHash != pinnedContextCoverageHash(msgs, n) {
		return projectionFailPinned
	}
	if coveredPrefixHash(msgs, n) == st.Projection.CoveredPrefixHash {
		return projectionFailNone
	}
	if projectionMatchesAfterSystemRefresh(st, msgs, n) {
		return projectionFailNone
	}
	return projectionFailHashMismatch
}

// projectionMatchesAfterSystemRefresh verifies a covered-prefix mismatch by
// substituting the projection's previous leading system messages into the
// current canonical prefix. A match proves that only the dynamic system prompt
// changed; any user, assistant, tool, image, or signed-reasoning edit still
// fails closed.
func projectionMatchesAfterSystemRefresh(st CompactionState, msgs []provider.Message, n int) bool {
	if n <= 0 || n > len(msgs) || len(st.Projection.Messages) == 0 {
		return false
	}
	candidate := append([]provider.Message(nil), msgs[:n]...)
	systems := 0
	for systems < len(candidate) && candidate[systems].Role == provider.RoleSystem {
		if systems >= len(st.Projection.Messages) || st.Projection.Messages[systems].Role != provider.RoleSystem {
			return false
		}
		candidate[systems] = st.Projection.Messages[systems]
		systems++
	}
	if systems == 0 || (systems < len(st.Projection.Messages) && st.Projection.Messages[systems].Role == provider.RoleSystem) {
		return false
	}
	return coveredPrefixHash(candidate, len(candidate)) == st.Projection.CoveredPrefixHash
}

// modelVisibleFromProjection splices the projection with any messages appended
// after it was built. LocalOnly messages stay excluded via ModelMessages later.
func modelVisibleFromProjection(proj ContextProjection, canonical []provider.Message) []provider.Message {
	if len(proj.Messages) == 0 {
		return nil
	}
	out := append([]provider.Message(nil), proj.Messages...)
	// Leading system messages are outside every fold. Refresh them from canonical
	// so memory/tool/environment prompt updates do not serve a stale prefix or
	// force a full-history replay.
	for i := 0; i < len(canonical) && i < len(out) && canonical[i].Role == provider.RoleSystem && out[i].Role == provider.RoleSystem; i++ {
		out[i] = canonical[i]
	}
	if proj.CoveredCount >= 0 && proj.CoveredCount < len(canonical) {
		out = append(out, canonical[proj.CoveredCount:]...)
	}
	return out
}

// coalesceProjectionUserRuns keeps provider request copies compatible with
// providers that require strict user/assistant alternation. Projection
// sidecars retain logical user-turn boundaries; only the outbound copy is
// merged, leaving canonical history and range anchors untouched.
func coalesceProjectionUserRuns(msgs []provider.Message) []provider.Message {
	if len(msgs) < 2 {
		return msgs
	}
	out := make([]provider.Message, 0, len(msgs))
	for _, msg := range msgs {
		if len(out) == 0 || msg.Role != provider.RoleUser || out[len(out)-1].Role != provider.RoleUser {
			clone := msg
			clone.Images = append([]string(nil), msg.Images...)
			clone.Audio = append([]string(nil), msg.Audio...)
			clone.Video = append([]string(nil), msg.Video...)
			clone.ToolCalls = append([]provider.ToolCall(nil), msg.ToolCalls...)
			clone.ResponsesItems = append([]json.RawMessage(nil), msg.ResponsesItems...)
			clone.ServerSearch = append([]provider.ServerSearchCall(nil), msg.ServerSearch...)
			out = append(out, clone)
			continue
		}

		prev := &out[len(out)-1]
		if isCompactionSummary(msg) && !isCompactionSummary(*prev) {
			prev.Content = strings.TrimRight(msg.Content, "\n") + "\n\n" + prev.Content
		} else {
			prev.Content = strings.TrimRight(prev.Content, "\n") + "\n\n" + msg.Content
		}
		prev.Images = append(prev.Images, msg.Images...)
		prev.Audio = append(prev.Audio, msg.Audio...)
		prev.Video = append(prev.Video, msg.Video...)
		prev.ToolCalls = append(prev.ToolCalls, msg.ToolCalls...)
		prev.ResponsesItems = append(prev.ResponsesItems, msg.ResponsesItems...)
		prev.ServerSearch = append(prev.ServerSearch, msg.ServerSearch...)
	}
	return out
}

// formatSummaryMessage builds the stable user-turn wrapper around a digest.
func formatSummaryMessage(summary string) provider.Message {
	return provider.Message{
		Role: provider.RoleUser, Origin: provider.MessageOriginHost,
		Content: summaryTagOpen + "\n" +
			"Summary of earlier conversation (older messages were compacted to save context):\n" +
			summary + "\n" +
			summaryTagClose,
	}
}
