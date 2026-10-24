package agent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"reasonix/internal/provider"
	"reasonix/internal/store"
)

// 任务 1011：会话健康检查（只读）。打开会话、启动扫描、修复面板共用同一套
// 判据读数；自愈动作（唯一允许改历史数据的入口）在 session_selfheal.go。
//
// 本文件没有写入：所有函数只重放日志、统计水位、对比 head。健康会话跑一遍
// 的可观测副作用只有读缓存，磁盘字节零改动（验收硬条目「健康会话加载前后
// 文件逐字节零改动」在检查路径上必须天然成立）。

// 会话健康判据代码。前端与修复记录按原样引用，不翻译。
const (
	// HealthProjectionOverflow：选中链的投影估算 token ≥ 模型窗口的
	// projectionOverflowRatio。现场（20261019 ai-passport）：2.13M / 1M。
	HealthProjectionOverflow = "projection_overflow"
	// HealthWatermarkLag：events 日志最新链比物化 .jsonl 检查点多出超过
	// materializationGapNoticeThreshold 条（复用任务 1010 的阈值与语义）。
	HealthWatermarkLag = "watermark_lag"
	// HealthCompactFailStreak：运行中压缩连败次数（进程内读数，由调用方注入，
	// 磁盘上的历史检查无法得知本次进程的连败状态）。
	HealthCompactFailStreak = "compact_fail_streak"
	// HealthHeadLag：选中 head 被另一个存活 head 严格支配——对方消息更多且
	// 更新（复用任务 1010 的 dominatingLiveHead 判据，rotation 回归守卫同源）。
	HealthHeadLag = "head_lag"
	// HealthLoadTimeout：日志重放超过预算仍未完成——降级加载（tail 首绘 +
	// 后台重建）是它的自愈方向。
	HealthLoadTimeout = "load_timeout"
)

const (
	// projectionOverflowRatio is how close the estimated projection must sit
	// to the window before the session counts as overflowing. Below the ratio
	// a large-but-fitting transcript is the user's own choice, not a fault.
	projectionOverflowRatio = 0.95
	// defaultHealthWindowTokens backs callers that do not know the bound
	// model's window (startup scan before any provider round-trip). It matches
	// the 1M-class windows the self-heal was built against.
	defaultHealthWindowTokens = 1_000_000
	// DefaultHealthLoadTimeout bounds the read-only replay inside a health
	// check. A full replay of a healthy large session lands in seconds; a
	// timeout is itself the load_timeout reading.
	DefaultHealthLoadTimeout = 45 * time.Second
	// compactFailStreakThreshold is the consecutive-failure count at which the
	// running-session path asks for the projection diet (剥图+折叠).
	compactFailStreakThreshold = 2
)

// SessionHealthIssue is one failed criterion with its readings. Detail carries
// key=value pairs (the same convention task 1010 notices use) so the GUI and
// the repair log quote identical numbers.
type SessionHealthIssue struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
	// SuggestedAction names the self-heal step that fixes it: promote_head,
	// hydrate, diet (剥图+折叠), or degraded_load. Empty = observation only.
	SuggestedAction string `json:"suggested_action,omitempty"`
}

// SessionHealthReport is the full read-only verdict for one session file.
type SessionHealthReport struct {
	Path    string                `json:"path"`
	Healthy bool                  `json:"healthy"`
	Issues  []SessionHealthIssue  `json:"issues,omitempty"`
	Heads   []SessionHead         `json:"heads,omitempty"`
	// SuggestedHeadID is the "更长且更新" head the heal would promote, when the
	// selected head is strictly dominated. Empty otherwise.
	SuggestedHeadID string `json:"suggested_head_id,omitempty"`
	SelectedHeadID  string `json:"selected_head_id,omitempty"`
	// ProjectionTokens is the image-aware token estimate of the selected
	// chain; WindowTokens is the bound it is judged against.
	ProjectionTokens int   `json:"projection_tokens,omitempty"`
	WindowTokens     int   `json:"window_tokens,omitempty"`
	MaterializedMsgs int   `json:"materialized_messages,omitempty"`
	EventChainMsgs   int   `json:"event_chain_messages,omitempty"`
	LogSchema        int   `json:"log_schema,omitempty"`
	HeadCount        int   `json:"head_count,omitempty"`
	LoadTimeout      bool  `json:"load_timeout,omitempty"`
	LoadMillis       int64 `json:"load_millis,omitempty"`
}

// HealthInspectOptions carries the readings a disk-only check cannot derive.
type HealthInspectOptions struct {
	// CompactFailStreak is the live in-process consecutive compaction-failure
	// count; negative or zero skips the criterion.
	CompactFailStreak int
	// WindowTokens is the destination model's context window; <=0 uses
	// defaultHealthWindowTokens.
	WindowTokens int
	// LoadTimeout bounds the replay; <=0 uses DefaultHealthLoadTimeout.
	LoadTimeout time.Duration
}

// InspectSessionHealth runs the five read-only criteria against one session
// file. It never writes: the replay cache is the only shared state it touches.
// A schema-1 or bare-.jsonl session is healthy by definition here — the DAG
// criteria do not apply, and the v3 self-heal has nothing to act on.
func InspectSessionHealth(path string, opts HealthInspectOptions) SessionHealthReport {
	return InspectSessionHealthContext(context.Background(), path, opts)
}

// InspectSessionHealthContext is InspectSessionHealth with a caller-owned
// cancellation for the replay.
func InspectSessionHealthContext(ctx context.Context, path string, opts HealthInspectOptions) SessionHealthReport {
	report := SessionHealthReport{Path: path, Healthy: true}
	if path == "" {
		report.Issues = append(report.Issues, SessionHealthIssue{Code: HealthLoadTimeout, Detail: "path=empty"})
		report.Healthy = false
		return report
	}
	timeout := opts.LoadTimeout
	if timeout <= 0 {
		timeout = DefaultHealthLoadTimeout
	}
	window := opts.WindowTokens
	if window <= 0 {
		window = defaultHealthWindowTokens
	}
	report.WindowTokens = window

	probe, err := probeSessionEventLog(path)
	if err != nil || !probe.dag {
		// Schema-1 / bare transcript / unreadable head: nothing for the v3
		// heal to act on, and no criterion applies.
		return report
	}
	report.LogSchema = probe.schemaVersion

	replayCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	started := time.Now()
	st, replayErr := replaySessionDAG(replayCtx, store.SessionEventLog(path), limitsForSessionLog(path, defaultSessionReplayLimits))
	report.LoadMillis = time.Since(started).Milliseconds()
	if replayErr != nil {
		report.Healthy = false
		report.LoadTimeout = isDeadline(replayErr)
		report.Issues = append(report.Issues, SessionHealthIssue{
			Code:            HealthLoadTimeout,
			Detail:          fmt.Sprintf("replay_error=%v load_ms=%d timed_out=%t", replayErr, report.LoadMillis, report.LoadTimeout),
			SuggestedAction: "degraded_load",
		})
		return report
	}
	if st.damaged {
		// A torn tail settles itself on the next head op; a health check only
		// reports what it saw. Not one of the five criteria: damaged logs are
		// a write-path concern (save rewrites-and-compacts to heal).
		report.Issues = append(report.Issues, SessionHealthIssue{
			Code:   HealthLoadTimeout,
			Detail: "damaged=torn_tail load_ms=" + fmt.Sprint(report.LoadMillis),
		})
	}

	selected := st.selectedHead()
	report.SelectedHeadID = selected
	report.HeadCount = len(st.heads)
	report.Heads = st.headList()

	// 判据 4 head_lag：选中 head 被「更长且更新」者支配（1010 同源判据）。
	if domID, selLen, domLen, dominated := st.dominatingLiveHead(selected); dominated {
		dom := st.heads[domID]
		report.Healthy = false
		report.SuggestedHeadID = domID
		report.Issues = append(report.Issues, SessionHealthIssue{
			Code: HealthHeadLag,
			Detail: fmt.Sprintf("selected_messages=%d suggested_messages=%d selected_turns=%d suggested_turns=%d selected_activity=%s suggested_activity=%s",
				selLen, domLen, selectedTurns(st, selected), selectedTurns(st, domID),
				st.heads[selected].lastActivity.UTC().Format(time.RFC3339), dom.lastActivity.UTC().Format(time.RFC3339)),
			SuggestedAction: "promote_head",
		})
	}

	// 判据 2 watermark_lag：复用 1010 的只读检测（阈值 50 条、新鲜窗口）。
	if ev, ok := detectMaterializationGap(path, st); ok {
		report.Healthy = false
		report.MaterializedMsgs = ev.Messages
		report.EventChainMsgs = ev.OtherMessages
		report.Issues = append(report.Issues, SessionHealthIssue{
			Code:            HealthWatermarkLag,
			Detail:          fmt.Sprintf("gap=%d event_chain=%d materialized=%d", ev.OtherMessages-ev.Messages, ev.OtherMessages, ev.Messages),
			SuggestedAction: "hydrate",
		})
	}

	// 判据 1 projection_overflow：选中链的图片感知 token 估算 vs 窗口。
	msgs, _ := st.materialize(selected)
	tokens := estimateProjectionTokens(msgs)
	report.ProjectionTokens = tokens
	if float64(tokens) >= float64(window)*projectionOverflowRatio {
		report.Healthy = false
		report.Issues = append(report.Issues, SessionHealthIssue{
			Code:            HealthProjectionOverflow,
			Detail:          fmt.Sprintf("projection_tokens=%d window_tokens=%d ratio>=%.2f images_bytes=%d", tokens, window, projectionOverflowRatio, projectionImageBytes(msgs)),
			SuggestedAction: "diet",
		})
	}

	// 判据 3 compact_fail_streak：调用方注入的运行中读数。
	if opts.CompactFailStreak >= compactFailStreakThreshold {
		report.Healthy = false
		report.Issues = append(report.Issues, SessionHealthIssue{
			Code:            HealthCompactFailStreak,
			Detail:          fmt.Sprintf("streak=%d threshold=%d", opts.CompactFailStreak, compactFailStreakThreshold),
			SuggestedAction: "diet",
		})
	}
	return report
}

// estimateProjectionTokens adds the image plane to the text-only estimator:
// base64 vision payloads ride the wire even though estimateTextTokens cannot
// see them (the 1010 finding: images were the main bloat and the full-chain
// estimate was blind to them). bytes/4 keeps the same conservative shape.
//
// 任务 1116 件 3-5d（口径互指，勿"统一"）：这里是**判据口径**（Diet 触发、
// 健康检查用）——base64 长度 ÷4 的保守上界估计，宁高勿低（1010 教训：图片被
// 低估会让每个阈值失明）。与之相对，**计价口径**在 image_estimate.go（任务
// 1025/1040：按分辨率折算，喂给 ContextUsedTokens 主读数）。两套口径用途不
// 同、故意不同：判据要保守上界，计价要贴近 provider 实账。改任何一套前先读
// 另一套的注释，禁止把它们"统一"成一个数字——那会把 Diet 判据改盲（低估 ⇒
// 超窗不触发）或把显示读数改虚高。
func estimateProjectionTokens(msgs []provider.Message) int {
	tokens := estimateMessagesTokens(msgs)
	for _, m := range msgs {
		if m.LocalOnly {
			continue
		}
		for _, img := range m.Images {
			tokens += len(img) / 4
		}
	}
	return tokens
}

// projectionImageBytes reports how many wire bytes the selected chain's
// embedded images occupy — the number that decides whether stripping images
// alone can bring the projection back under the window.
func projectionImageBytes(msgs []provider.Message) int {
	n := 0
	for _, m := range msgs {
		if m.LocalOnly {
			continue
		}
		for _, img := range m.Images {
			n += len(img)
		}
	}
	return n
}

func selectedTurns(st *sessionDAGState, headID string) int {
	msgs, _ := st.materialize(headID)
	_, turns := SessionPreviewFromMessages(msgs)
	return turns
}

func isDeadline(err error) bool {
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)
}
