package agent

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"os"
	"strings"
	"time"

	"reasonix/internal/store"
)

// tl1837：压缩/折叠关键读数持久化。
//
// 病灶（20261127 用户判定立项）：压缩/折叠的关键读数——fold 计划 vs folded
// 实际（案B 主疑点：计划 1384 实际 329）、buckets 分项（transient /
// session_context / image / assistant / tool）、source→projection 压缩比、
// 触发源与轮次——只写 desktop.log。日志轮转/进程重建后即不可得：10:58 中途
// 快照（672.8K 分项）的细节因 desktop.log 重建+轮转永久失去数据源，多次想
// 复核折叠构成均无历史可查。
//
// 修复：每次压缩事件同步追加一行 JSON 到会话兄弟侧车
// <stem>.compactmetrics.jsonl（store.SessionCompactMetricsLog，与
// rewritelog/repairlog 同惯例：追加式审计侧车，不进会话列表，删除会话时
// 一并清）。字段与 desktop.log 现有锚（agent: fold plan / compaction done /
// compaction source buckets / compaction projection buckets / ranges fold
// observation）逐字段同源，事后分析可与日志互相对账。
//
// 选型（实施申报）：每会话一份 sidecar，非全局 metrics 文件。理由：读侧是
// 事后按会话取历史（案B 即「这一个会话的折叠史」），写侧每压缩事件一行
// （低频）；会话侧车随 trash/还原/身份换位全链走（tl1841 三链同 commit），
// 全局文件则要另建轮转/清理/按会话过滤三套机制——侧车路线复用现有机制，
// 符合「先用现有机制」纪律。
//
// 上界：compactMetricsMaxFileBytes / compactMetricsTailLines 双界——追加后
// 超字节上限即重写为尾部 N 行（滚动覆盖），永不无限增长。
//
// 时序：写入点全部位于 compactionRunMu 串行面内（与 tl1652 的 s.mu 外
// flush 不同，这里不持任何会话锁），追加为一次 O_APPEND 小写，不阻塞折叠
// 主路径；写失败只 slog.Warn，不影响压缩结果。
//
// non-goals：不改折叠算法与触发阈值；不做读数可视化面板；不动 desktop.log
// 现有日志行；ranges 增量折叠路径沿用其现有观测面（无 buckets 分桶计算，
// 记录沿用 ranges fold observation 字段）。

const (
	// compactMetricsMaxFileBytes 是侧车字节上界：追加后超过即截尾。
	compactMetricsMaxFileBytes = 512 << 10 // 512 KiB
	// compactMetricsTailLines 是截尾保留的行数（最近的压缩事件）。
	compactMetricsTailLines = 256
)

// CompactMetricsRecord is one line of <stem>.compactmetrics.jsonl. The shape
// is fixed per kind (no omitempty on numeric fields) so readers can rely on
// every key of the declared kind being present; fields not belonging to the
// kind carry zero values. Bucket objects are nil outside kind=fold_done.
type CompactMetricsRecord struct {
	// At is the UTC RFC3339Nano timestamp of the moment the event landed.
	At string `json:"at"`
	// Kind discriminates the record shape: "fold_plan" (full-path fold plan),
	// "fold_done" (full-path install with both bucket readouts),
	// "ranges_fold" (small-segment incremental fold observation),
	// "fold_declined" (gate refused before spending a summary request),
	// "fold_failed" (spent attempt errored).
	Kind string `json:"kind"`
	// Session is the same ref the desktop.log lines carry (sessionLogRef).
	Session string `json:"session"`
	// Trigger names the compaction trigger source ("pressure" / "manual" /
	// "overflow" / model ranges …) — same values as the log lines.
	Trigger string `json:"trigger"`
	// SplitActive (tl1695) — plan: whether the fold crosses into the active
	// turn; done: same semantics as the done line. Always present.
	SplitActive bool `json:"split_active"`

	// --- fold_plan fields (计划半边; 案B「计划 1384」读这里) ---
	Head         int  `json:"head"`
	Start        int  `json:"start"`
	ViewMessages int  `json:"view_messages"`
	OnProjection bool `json:"on_projection"`
	ActiveTurn   int  `json:"active_turn"` // -1 = no active turn boundary
	KeptMessages int  `json:"kept_messages"`
	FoldMessages int  `json:"fold_messages"`

	// --- fold_done / ranges_fold execution fields (执行半边; 案B「实际 329」
	// 与压缩比读这里) ---
	FoldedMessages   int `json:"folded_messages"`
	SourceTokens     int `json:"source_tokens"`
	FoldTokens       int `json:"fold_tokens"`
	ProjectionTokens int `json:"projection_tokens"`
	// ProjectionSourceRatio is projection_tokens / source_tokens (<1 =
	// shrunk; 0 when source is 0). 压缩族 tl1713 验收「压后 projection<100K」
	// 的历史读数来源。
	ProjectionSourceRatio float64 `json:"projection_source_ratio"`
	InputTokens           int     `json:"input_tokens"`
	OutputTokens          int     `json:"output_tokens"`
	CacheHitTokens        int     `json:"cache_hit_tokens"`
	CacheMissTokens       int     `json:"cache_miss_tokens"`
	RequestCount          int     `json:"request_count"`
	// CompactionIntervalMin (tl1695) — minutes since the previous successful
	// compaction; -1 = first of the session. fold_done only.
	CompactionIntervalMin float64 `json:"compaction_interval_min"`

	// --- ranges_fold fields (增量折叠频次×缓存观测面) ---
	RangesFoldIndex int64   `json:"ranges_fold_index"`
	SincePrevMs     int64   `json:"since_prev_ranges_fold_ms"`
	PreservedTokens int     `json:"preserved_tokens"`
	SegmentTokens   int     `json:"segment_tokens"`
	NoteTokens      int     `json:"note_tokens"`
	RangesCacheHit  float64 `json:"ranges_cache_hit"`

	// --- fold_declined / fold_failed ---
	Reason string `json:"reason,omitempty"` // declined gate reason
	Err    string `json:"err,omitempty"`    // failed attempt error

	// --- fold_done bucket readouts (tl1770 口径: 桶和=total_tokens,
	// transient ⊆ real_user, image ⊆ 宿主桶交叉读数) ---
	BucketsPre  *CompactMetricsBuckets `json:"buckets_pre"`
	BucketsPost *CompactMetricsBuckets `json:"buckets_post"`
}

// CompactMetricsBuckets prices one compaction view's composition — the same
// ruler and classification as the buckets_pre / buckets_post log lines.
type CompactMetricsBuckets struct {
	Messages              int `json:"messages"`
	TotalTokens           int `json:"total_tokens"`
	System                int `json:"bucket_system_tokens"`
	RealUser              int `json:"bucket_real_user_tokens"`
	SessionContextCarrier int `json:"bucket_session_context_tokens"`
	Assistant             int `json:"bucket_assistant_tokens"`
	Tool                  int `json:"bucket_tool_tokens"`
	Summary               int `json:"bucket_summary_tokens"`
	Other                 int `json:"bucket_other_tokens"`
	TransientBlocks       int `json:"transient_blocks_tokens"`
	ImageTokens           int `json:"image_tokens"`
	ImageCount            int `json:"image_count"`
}

// compactMetrics record kinds.
const (
	compactMetricsKindFoldPlan   = "fold_plan"
	compactMetricsKindFoldDone   = "fold_done"
	compactMetricsKindRangesFold = "ranges_fold"
	compactMetricsKindDeclined   = "fold_declined"
	compactMetricsKindFailed     = "fold_failed"
)

// recordCompactMetrics stamps the common fields and appends one line to the
// session's compactmetrics sidecar. No persistence baseline (session never
// saved) means no on-disk session form to analyse afterwards — the record is
// dropped with a debug line, not buffered (metrics are observability, not
// evidence; the sessions worth investigating have always been saved).
func (a *Agent) recordCompactMetrics(rec CompactMetricsRecord) {
	if a == nil || a.sess.path == "" {
		return
	}
	rec.At = time.Now().UTC().Format(time.RFC3339Nano)
	if rec.Session == "" {
		rec.Session = a.sessionLogRef()
	}
	path := store.SessionCompactMetricsLog(a.sess.path)
	if err := appendCompactMetricsRecord(path, rec); err != nil {
		slog.Warn("agent: compact metrics sidecar write failed",
			"path", path, "kind", rec.Kind, "trigger", rec.Trigger, "err", err)
	}
}

// appendCompactMetricsRecord appends one JSON line and enforces the size cap
// (rolling tail). A failed append aborts before touching existing content.
func appendCompactMetricsRecord(path string, rec CompactMetricsRecord) error {
	blob, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.Write(append(blob, '\n'))
	cerr := f.Close()
	if werr != nil {
		return werr
	}
	if cerr != nil {
		return cerr
	}
	return truncateCompactMetricsTail(path)
}

// truncateCompactMetricsTail caps the sidecar: over the byte limit the file
// is rewritten to its newest lines under BOTH bounds — at most
// compactMetricsTailLines lines AND at most compactMetricsMaxFileBytes bytes
// (fat records shrink the line count; the byte bound always holds). Upper
// bound overall = compactMetricsMaxFileBytes + one appended line.
func truncateCompactMetricsTail(path string) error {
	st, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if st.Size() <= compactMetricsMaxFileBytes {
		return nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(string(b), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	kept := 0
	used := 0
	for i := len(lines) - 1; i >= 0; i-- {
		size := len(lines[i]) + 1
		if kept >= compactMetricsTailLines || used+size > compactMetricsMaxFileBytes {
			break
		}
		used += size
		kept++
	}
	if kept == 0 {
		return os.Remove(path)
	}
	return os.WriteFile(path, []byte(strings.Join(lines[len(lines)-kept:], "\n")+"\n"), 0o600)
}

// ListSessionCompactMetrics reads a session's compaction metrics journal
// oldest first. A missing log is an empty list, not an error — a session with
// no compaction history never grew one.
func ListSessionCompactMetrics(sessionPath string) ([]CompactMetricsRecord, error) {
	logPath := store.SessionCompactMetricsLog(sessionPath)
	if logPath == "" {
		return nil, nil
	}
	f, err := os.Open(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []CompactMetricsRecord
	dec := json.NewDecoder(f)
	for {
		var rec CompactMetricsRecord
		if err := dec.Decode(&rec); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return out, err
		}
		out = append(out, rec)
	}
	return out, nil
}

// compactMetricsBucketsFrom converts the tl1770 view-bucket readout into its
// sidecar shape. total is the view's own ruler total (source_tokens /
// projection_tokens on the same line).
func compactMetricsBucketsFrom(b compactionViewBuckets, total int) *CompactMetricsBuckets {
	return &CompactMetricsBuckets{
		Messages:              b.messages,
		TotalTokens:           total,
		System:                b.system,
		RealUser:              b.realUser,
		SessionContextCarrier: b.carrier,
		Assistant:             b.assistant,
		Tool:                  b.tool,
		Summary:               b.summary,
		Other:                 b.other,
		TransientBlocks:       b.transient,
		ImageTokens:           b.images,
		ImageCount:            b.imageCount,
	}
}

// foldPlanMetricsRecord builds the 计划半边 record (the "agent: fold plan"
// log line's fields). plan/actual correlation = adjacent records in one
// append-only file.
func foldPlanMetricsRecord(trigger string, head, start, viewMessages int, onProjection bool, activeTurn int, splitActive bool, kept, fold int) CompactMetricsRecord {
	return CompactMetricsRecord{
		Kind:         compactMetricsKindFoldPlan,
		Trigger:      trigger,
		SplitActive:  splitActive,
		Head:         head,
		Start:        start,
		ViewMessages: viewMessages,
		OnProjection: onProjection,
		ActiveTurn:   activeTurn,
		KeptMessages: kept,
		FoldMessages: fold,
	}
}

// foldDoneMetricsRecord builds the 执行半边 record — the done line plus both
// bucket readouts in one line, so 压后构成可完整复原. sourceTokens is the
// committed source (in.sourceTokens — the same read the done line carries).
func foldDoneMetricsRecord(trigger string, tele CompactionTelemetry, sourceTokens, foldedMessages, projTokens int, intervalMin float64, pre, post compactionViewBuckets) CompactMetricsRecord {
	ratio := 0.0
	if sourceTokens > 0 {
		ratio = math.Round(float64(projTokens)/float64(sourceTokens)*10000) / 10000
	}
	return CompactMetricsRecord{
		Kind:                  compactMetricsKindFoldDone,
		Trigger:               trigger,
		SplitActive:           tele.SplitActive,
		FoldedMessages:        foldedMessages,
		SourceTokens:          sourceTokens,
		FoldTokens:            tele.FoldTokens,
		ProjectionTokens:      projTokens,
		ProjectionSourceRatio: ratio,
		InputTokens:           tele.InputTokens,
		OutputTokens:          tele.OutputTokens,
		CacheHitTokens:        tele.CacheHitTokens,
		CacheMissTokens:       tele.CacheMissTokens,
		RequestCount:          tele.RequestCount,
		CompactionIntervalMin: intervalMin,
		BucketsPre:            compactMetricsBucketsFrom(pre, sourceTokens),
		BucketsPost:           compactMetricsBucketsFrom(post, projTokens),
	}
}

// rangesFoldMetricsRecord builds the incremental-fold observation record —
// the "agent: ranges fold observation" line's fields.
func rangesFoldMetricsRecord(trigger string, idx int64, sincePrevMs int64, preservedTokens, segTokens, noteTokens int, cacheHit float64, projTokens, sourceTokens int) CompactMetricsRecord {
	ratio := 0.0
	if sourceTokens > 0 {
		ratio = math.Round(float64(projTokens)/float64(sourceTokens)*10000) / 10000
	}
	return CompactMetricsRecord{
		Kind:                  compactMetricsKindRangesFold,
		Trigger:               trigger,
		RangesFoldIndex:       idx,
		SincePrevMs:           sincePrevMs,
		PreservedTokens:       preservedTokens,
		SegmentTokens:         segTokens,
		NoteTokens:            noteTokens,
		RangesCacheHit:        cacheHit,
		FoldedMessages:        0,
		SourceTokens:          sourceTokens,
		ProjectionTokens:      projTokens,
		ProjectionSourceRatio: ratio,
	}
}

// foldDeclinedMetricsRecord builds the refusal record — a gate refused before
// (or instead of) spending a summary request.
func foldDeclinedMetricsRecord(trigger, reason string) CompactMetricsRecord {
	return CompactMetricsRecord{
		Kind:    compactMetricsKindDeclined,
		Trigger: trigger,
		Reason:  reason,
	}
}

// foldFailedMetricsRecord builds the failure record — a spent attempt errored.
func foldFailedMetricsRecord(trigger, errMsg string) CompactMetricsRecord {
	return CompactMetricsRecord{
		Kind:    compactMetricsKindFailed,
		Trigger: trigger,
		Err:     errMsg,
	}
}
