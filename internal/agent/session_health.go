package agent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"reasonix/internal/provider"
	"reasonix/internal/store"
)

// 任务 1011：会话健康检查（只读）。tl1280：DAG 写路径慢车道删除后，本文件
// 收敛为「schema-1 会话的只读体检」——schema-2 DAG 判据（head_lag /
// watermark_lag / projection_overflow 的 DAG 重放分支）随慢车道一并退役，
// 剩下的判据仍服务于 self-heal 面板与 control 层的压缩连败提示。

// 会话健康判据代码。前端与修复记录按原样引用，不翻译。
const (
	// HealthProjectionOverflow：投影估算 token ≥ 模型窗口的
	// projectionOverflowRatio。现场（20261019 ai-passport）：2.13M / 1M。
	HealthProjectionOverflow = "projection_overflow"
	// HealthCompactFailStreak：运行中压缩连败次数（进程内读数，由调用方注入，
	// 磁盘上的历史检查无法得知本次进程的连败状态）。
	HealthCompactFailStreak = "compact_fail_streak"
	// HealthLoadTimeout：日志重放超过预算仍未完成——降级加载是它的自愈方向。
	HealthLoadTimeout = "load_timeout"
)

const (
	// projectionOverflowRatio is how close the estimated projection must sit
	// to the window before the session counts as overflowing. Below the ratio
	// a large-but-fitting transcript is the user's own choice, not a fault.
	projectionOverflowRatio = 0.95
	// defaultHealthWindowTokens backs callers that do not know the bound
	// model's window (startup scan before any provider round-trip).
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
	// SuggestedAction names the self-heal step that fixes it: hydrate or
	// diet (剥图+折叠). Empty = observation only.
	SuggestedAction string `json:"suggested_action,omitempty"`
}

// Token sources for the projection-overflow criterion (tl1506). The GUI and
// the repair log quote them alongside the reading so "which caliber decided"
// is always on the record.
const (
	// HealthTokenSourceUsageActual: the criterion read the trusted
	// provider-measured context size injected by the host (tl1183 anchor).
	HealthTokenSourceUsageActual = "usage_actual"
	// HealthTokenSourceProjectionEstimate: no trusted actual exists (fresh
	// process, closed session); the criterion fell back to the local
	// pricing-caliber projection estimate.
	HealthTokenSourceProjectionEstimate = "projection_estimate"
)

// SessionHealthReport is the full read-only verdict for one session file.
type SessionHealthReport struct {
	Path    string               `json:"path"`
	Healthy bool                 `json:"healthy"`
	Issues  []SessionHealthIssue `json:"issues,omitempty"`
	// ProjectionTokens is the criterion reading the overflow verdict used:
	// the trusted usage actual when the host injected one, otherwise the
	// pricing-caliber projection estimate (tl1506). WindowTokens is the
	// bound it is judged against.
	ProjectionTokens int `json:"projection_tokens,omitempty"`
	WindowTokens     int `json:"window_tokens,omitempty"`
	// TokenSource names which caliber produced ProjectionTokens
	// (HealthTokenSourceUsageActual / HealthTokenSourceProjectionEstimate).
	TokenSource string `json:"token_source,omitempty"`
	// CompactFailStreak echoes the injected reading so callers can quote it.
	CompactFailStreak int `json:"compact_fail_streak,omitempty"`
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
	// ActualContextTokens is the trusted provider-measured context size of the
	// current view (任务 1183 anchor: last sampling request's real prompt plus
	// completion plus calibrated delta; injected by hosts holding a live agent
	// via Agent.TrustedObservedContextTokens). tl1506 用户终裁（20261109）：
	// 超窗判据有实账按实账判；<=0 = 无实账，判据退回计价口径估算
	// （estimateProjectionTokens，已按 1025 分辨率折算图片）。注入方必须保证
	// 只传可信实账（非 Estimated/Unknown 重建值）——信任闸在
	// observedContextTokens，本层不再复检。
	ActualContextTokens int
}

// InspectSessionHealth runs the read-only criteria against one session file.
// It never writes. tl1280: a schema-1 / bare-.jsonl session is the only shape
// with criteria anymore — the DAG head/watermark criteria left with the slow
// lane, so a sealed schema-2 log now reports healthy with no criterion
// (the migration tool owns its remediation).
//
// tl1506: the projection-overflow criterion reads the trusted usage actual
// (opts.ActualContextTokens) when the host injected one, and only otherwise
// falls back to the local pricing-caliber estimate — see
// healthProjectionCriterionTokens.
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
	window := opts.WindowTokens
	if window <= 0 {
		window = defaultHealthWindowTokens
	}
	report.WindowTokens = window
	report.CompactFailStreak = opts.CompactFailStreak

	probe, err := probeSessionEventLog(path)
	if err != nil {
		report.Healthy = false
		report.Issues = append(report.Issues, SessionHealthIssue{
			Code:   HealthLoadTimeout,
			Detail: fmt.Sprintf("probe_error=%v", err),
		})
		return report
	}
	if probe.dag {
		// tl1280: sealed schema-2 logs no longer carry a health criterion —
		// the migration tool owns their remediation.
		return report
	}

	msgs, _, _, err := loadSessionMessages(path)
	if err != nil {
		report.Healthy = false
		report.Issues = append(report.Issues, SessionHealthIssue{
			Code:            HealthLoadTimeout,
			Detail:          fmt.Sprintf("load_error=%v", err),
			SuggestedAction: "degraded_load",
		})
		return report
	}

	tokens, tokenSource := healthProjectionCriterionTokens(msgs, opts)
	report.ProjectionTokens = tokens
	report.TokenSource = tokenSource
	if float64(tokens) >= float64(window)*projectionOverflowRatio {
		report.Healthy = false
		report.Issues = append(report.Issues, SessionHealthIssue{
			Code: HealthProjectionOverflow,
			Detail: fmt.Sprintf("projection_tokens=%d window_tokens=%d ratio>=%.2f images_bytes=%d tokens_source=%s",
				tokens, window, projectionOverflowRatio, projectionImageBytes(msgs), tokenSource),
			SuggestedAction: "diet",
		})
	}

	if opts.CompactFailStreak >= compactFailStreakThreshold {
		report.Healthy = false
		report.Issues = append(report.Issues, SessionHealthIssue{
			Code:            HealthCompactFailStreak,
			Detail:          fmt.Sprintf("streak=%d threshold=%d", opts.CompactFailStreak, compactFailStreakThreshold),
			SuggestedAction: "diet",
		})
	}
	_ = ctx
	_ = store.SessionEventLog
	return report
}

// healthProjectionCriterionTokens is the tl1506 three-segment criterion
// selector: a trusted provider-measured actual wins outright; only its absence
// falls back to the local projection estimate. The source label rides along so
// the issue detail, the report and the repair log quote the same caliber.
func healthProjectionCriterionTokens(msgs []provider.Message, opts HealthInspectOptions) (int, string) {
	if opts.ActualContextTokens > 0 {
		return opts.ActualContextTokens, HealthTokenSourceUsageActual
	}
	return estimateProjectionTokens(msgs), HealthTokenSourceProjectionEstimate
}

// estimateProjectionTokens prices the transcript's provider-visible plane in
// window-comparable tokens: the static text estimator plus each image at its
// resolution-derived price (imageEstimateTokens, 任务 1025).
//
// 任务 1506（用户终裁 20261109 08:49，**推翻任务 1116 件 3-5d 的「判据宁高勿
// 低勿统一」裁决，记档**）：本函数此前把图片按 base64 长度 ÷4 计（1116 口径：
// 判据要保守上界），但该上界把大截图高估数倍——右栏显示 265.8K/1M 的含图会话
// 被判成超窗、打开即触发自愈 Diet（strip_images+fold），把可用的历史图片剥掉。
// 超窗判据现为三段口径：
//  1. 有实账（最后一次大模型调用的 usage 实账，任务 1183 锚
//     observedContextTokens，经 control 层注入 ActualContextTokens）⇒ 按实账判，
//     ≥ 窗口×projectionOverflowRatio 才触发 Diet；
//  2. 无实账 ⇒ 退回本估算，且图片改为 1025 分辨率计价口径（废 base64÷4），
//     与显示读数 ContextUsedTokens（context_usage.go）同族；
//  3. image_estimate.go 的不可读图兜底（byteCap/4 封顶价）保留——它是带上限
//     的计价兜底，不是本件废除的无界 len/4 判据（详见该常量注释）。
//
// 1010 的教训仍然有效：图片不得计零——分辨率计价下每个可读图片仍有
// floor 256、不可读图片有封顶 2048，超窗不会因图片"失明"而漏判；误判方向
// 由实账段兜住（有 provider 实账时估算器不再参与判定）。
func estimateProjectionTokens(msgs []provider.Message) int {
	tokens := estimateMessagesTokens(msgs)
	for _, m := range msgs {
		if m.LocalOnly {
			continue
		}
		for _, img := range m.Images {
			tokens += imageEstimateTokens(img)
		}
	}
	return tokens
}

// projectionImageBytes reports how many wire bytes the transcript's embedded
// images occupy — the number that decides whether stripping images alone can
// bring the projection back under the window.
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

func isDeadline(err error) bool {
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)
}
