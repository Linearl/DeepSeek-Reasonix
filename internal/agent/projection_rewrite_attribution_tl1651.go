package agent

import (
	"log/slog"

	"reasonix/internal/provider"
)

// tl1651 打点 c：前缀改写归因留痕。
//
// 20261116 现场回放：装机重启后 mimo 会话（20261115-095114）的投影被判定
// projection_body_invalid，模型工作上下文从投影回退到全量 121 万 token。
// 用户判定 covered_prefix_hash 锚定前 154 条前缀、尾部变化不动前缀——真凶
// 是「前缀被改写」但当时无任何改写现场日志，无法归因。
//
// 本文件在改写落地的那一刻留痕：谁（Rewrite reason / Replace）、何时、
// 哪段（第一条差异的转录 index 相对投影 covered 前缀区的位置）。只观测，
// 不改任何判定分支；probe 为 nil 时零开销（Session 侧判 nil 直跳）。
//
// 两半互补：
//   - 本文件捕捉「本进程内」的改写（rewind_truncate / rewind_restore /
//     guardian_merge / compact / fork Replace …… 全部经 Session.Rewrite /
//     Session.Replace 入口）；
//   - LoadSession 的转录修复（normalizedDirty，save.go）不经过这两个入口，
//     由 loadSessionWithReader 内的直接日志留痕（恢复半边）。
//   - 跨进程且非 load-repair 的改写由打点 b 的 sidecar 逐条指纹定位
//     （diagnoseProjectionMismatch，projection.go）。

// installPrefixRewriteProbe binds the covered-prefix rewrite observer to the
// agent's conversation. Called once from NewAgent; a conversation without a
// bound path behaves identically to before (probe reads only memory state).
func (a *Agent) installPrefixRewriteProbe() {
	if a == nil || a.sess.conversation == nil {
		return
	}
	a.sess.conversation.prefixRewriteProbe = a.noteCoveredPrefixRewrite
}

// noteCoveredPrefixRewrite is the agent-side probe body. It reads the covered
// count under compactionMu (never s.mu — the Session calls the probe after
// releasing s.mu, so taking compactionMu here keeps the existing lock order
// compactionMu→s.mu that the rest of the codebase already uses), diffs the
// pre/post rewrite logs, and files the attribution.
func (a *Agent) noteCoveredPrefixRewrite(reason string, before, after []provider.Message) {
	if a == nil {
		return
	}
	a.sess.compactionMu.Lock()
	covered := a.sess.compactionState.Projection.CoveredCount
	hasProjection := len(a.sess.compactionState.Projection.Messages) > 0
	a.sess.compactionMu.Unlock()
	if !hasProjection || covered <= 0 {
		// No covered region to protect — a rewrite cannot invalidate a prefix
		// that no projection claims. Silent: the invalidate/rebuild trail owns
		// this state transition already (task 549 / tl1203 lines).
		return
	}
	if reason == "" {
		reason = "replace" // Session.Replace carries no reason of its own
	}
	idx := firstTranscriptDivergence(before, after)
	base := []any{
		"writer", SessionWriterID(),
		"rewrite_reason", reason,
		"covered_count", covered,
		"before_msgs", len(before),
		"after_msgs", len(after),
	}
	if idx < 0 {
		// Same length, no provider-visible byte difference (local-only
		// metadata round-trip): worth a Debug trail, never a warning.
		base = append(base, "diff_index", -1)
		slog.Debug("agent: history rewritten without provider-visible change", base...)
		return
	}
	base = append(base, "diff_index", idx)
	if idx < len(after) {
		base = append(base,
			"diff_role", string(after[idx].Role),
			"diff_origin", string(after[idx].Origin))
	}
	if idx < covered {
		// THE signal: a rewrite landed inside the covered prefix region. If a
		// later resume logs hash_mismatch, this line is its witness — reason,
		// timestamp, and the exact message slot.
		base = append(base, "in_covered_prefix", true)
		slog.Warn("agent: covered prefix rewritten", base...)
		return
	}
	// Tail-only rewrite: evidence for the "尾部变化不动前缀" boundary the
	// user pinned for tl1651. Debug keeps the trail without noise.
	base = append(base, "in_covered_prefix", false)
	slog.Debug("agent: history rewritten past covered prefix", base...)
}

// firstTranscriptDivergence returns the first index where before and after
// differ in provider-visible content, or -1 when neither a content change nor
// a length change exists. Comparison runs on the raw transcript order (no
// sanitize pipeline) so diff_index maps 1:1 onto the transcript jsonl line a
// human would inspect.
func firstTranscriptDivergence(before, after []provider.Message) int {
	limit := len(before)
	if len(after) < limit {
		limit = len(after)
	}
	for i := 0; i < limit; i++ {
		if transcriptMessageFingerprint(before[i]) != transcriptMessageFingerprint(after[i]) {
			return i
		}
	}
	if len(before) != len(after) {
		return limit
	}
	return -1
}

// transcriptMessageFingerprint is the per-message wire-visible fingerprint used
// by the rewrite probe. providerVisibleFingerprint already ignores every
// non-wire field; the single-message slice keeps it allocation-cheap enough
// for rewrite-time probing (rewrites are user-frequency events).
func transcriptMessageFingerprint(m provider.Message) string {
	return providerVisibleFingerprint([]provider.Message{m})
}
