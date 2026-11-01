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

// SessionHealthReport is the full read-only verdict for one session file.
type SessionHealthReport struct {
	Path    string               `json:"path"`
	Healthy bool                 `json:"healthy"`
	Issues  []SessionHealthIssue `json:"issues,omitempty"`
	// ProjectionTokens is the image-aware token estimate of the transcript;
	// WindowTokens is the bound it is judged against.
	ProjectionTokens int `json:"projection_tokens,omitempty"`
	WindowTokens     int `json:"window_tokens,omitempty"`
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
}

// InspectSessionHealth runs the read-only criteria against one session file.
// It never writes. tl1280: a schema-1 / bare-.jsonl session is the only shape
// with criteria anymore — the DAG head/watermark criteria left with the slow
// lane, so a sealed schema-2 log now reports healthy with no criterion
// (the migration tool owns its remediation).
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
