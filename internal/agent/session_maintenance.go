package agent

import (
	"fmt"
	"log/slog"
	"strings"

	"reasonix/internal/provider"
)

// tl1280 —— DAG 写路径慢车道删除后的维护入口收敛。
//
// CompactSessionFile 与 TrimSessionOversizeToolResults 的主体本来就是
// schema-1 通路（lease → LoadSession → SaveRewriteCompact）；只有附着在它们
// 上的 schema-2 强制旋转（sessionCompactRepairDAG / sessionCompactRotateDAG）
// 属于慢车道，随 tl1280 一并退役。本文件把两条维护通路按 schema-1 形态
// 重新落地，供 CLI `session compact`、desktop 修复面板与空闲 sweep 复用。

// CompactSessionFile is the manual "repair / slim this session" entry (task
// 275): acquire the lease (the idleness check), load the session, and rewrite
// it through the production SaveRewriteCompact path, reporting the event-log
// bytes before and after. tl1280: the schema-2 forced-rotation branch is gone
// with the slow lane; a sealed schema-2 log refuses at LoadSession and the
// caller surfaces that error.
func CompactSessionFile(sessionPath string) (before, after int64, err error) {
	if sessionPath == "" {
		return 0, 0, fmt.Errorf("empty session path")
	}
	lease, leaseErr := TryAcquireSessionLease(sessionPath)
	if leaseErr != nil {
		return 0, 0, fmt.Errorf("session is not idle (lease busy): %w", leaseErr)
	}
	defer lease.Release()
	before = sessionEventLogZstSize(sessionPath)
	s, err := LoadSession(sessionPath)
	if err != nil {
		return before, 0, err
	}
	if err := s.SaveRewriteCompact(sessionPath); err != nil {
		return before, 0, err
	}
	after = sessionEventLogZstSize(sessionPath)
	return before, after, nil
}

// sessionEventLogZstSize is the at-rest footprint of a session's event log
// (zst archive when archived, plaintext otherwise) — the number the user-facing
// before/after readings refer to (task 275: 300MB+ .events.jsonl files).
func sessionEventLogZstSize(sessionPath string) int64 {
	return SessionEventLogZstSize(sessionPath)
}

// ─── tl1172 L3: deterministic oversized-tool-result trim ────────────────
//
// 定位：三级降级的最后一级。L2 物理压缩（无损）后仍超 cap，或压缩不可用时，
// 对 tool 结果类消息里超过阈值的正文做「头尾各留一段 + 中间置明确标记」的
// 有损裁剪——保留头尾与标记是红线要求（可识别、可解释），丢弃的中间字节在
// 标记里带出原始尺寸。

const (
	// sessionTrimFieldThreshold 单字段裁剪门槛：64KiB 以下的字段不动。
	sessionTrimFieldThreshold = 64 << 10
	// sessionTrimImageThreshold 图像 data URL 裁剪门槛：256KiB 以下的图不动
	//（图标/小图保留，超大截图才裁）。
	sessionTrimImageThreshold = 256 << 10
	// sessionTrimKeepHead/Tail 头尾保留字节数（任务正文口径：头尾各 1.5KB）。
	sessionTrimKeepHead = 1536
	sessionTrimKeepTail = 1536
	// sessionTrimMarkerHead/Tail 标记定界符，逐字可 grep。
	sessionTrimMarkerHead = "«TL1172-TRIMMED"
	sessionTrimMarkerTail = "»"
	// sessionTrimMinLogBytes 直接调用方的防御性地板：小日志没有可裁体量。
	sessionTrimMinLogBytes = 8 << 20
)

// TrimSessionOversizeToolResults deterministically trims oversized tool-result
// bodies (and oversized image data URLs — the tl1172 红线扩展) in the
// session's persisted transcript, rewriting it via the maintenance-compact
// path. Reports the log sizes before/after and how many fields were trimmed.
// The caller holds the idleness responsibility; the lease discipline here
// matches CompactSessionFile. tl1280: the schema-2 forced-rotation tail left
// with the slow lane.
func TrimSessionOversizeToolResults(sessionPath string) (before, after int64, trimmed int, err error) {
	if strings.TrimSpace(sessionPath) == "" {
		return 0, 0, 0, fmt.Errorf("empty session path")
	}
	// 归档态先物化：裁剪作用在明文世界上，重写发布明文（归档残件由物化清理）。
	if _, err := ensureSessionEventLogPlain(sessionPath); err != nil {
		return 0, 0, 0, err
	}
	before = sessionEventLogZstSize(sessionPath)
	if before < sessionTrimMinLogBytes {
		return before, before, 0, fmt.Errorf("session event log under trim floor (%d < %d bytes)", before, int64(sessionTrimMinLogBytes))
	}
	lease, leaseErr := TryAcquireSessionLease(sessionPath)
	if leaseErr != nil {
		return before, 0, 0, fmt.Errorf("session is not idle (lease busy): %w", leaseErr)
	}
	defer lease.Release()
	s, err := LoadSession(sessionPath)
	if err != nil {
		return before, 0, 0, err
	}
	// lease 在手 = 单写者已证明；新鲜 Load 的 Messages 无并发访问者，直接改。
	trimmed = trimOversizeToolResultFields(s.Messages)
	if trimmed == 0 {
		return before, before, 0, nil
	}
	if err := s.SaveRewriteCompact(sessionPath); err != nil {
		return before, 0, trimmed, err
	}
	after = sessionEventLogZstSize(sessionPath)
	slog.Info("session: oversized tool results trimmed (lossy, head+tail kept with explicit markers)",
		"path", sessionPath, "fieldsTrimmed", trimmed, "bytesBefore", before, "bytesAfter", after)
	return before, after, trimmed, nil
}

// trimOversizeToolResultFields walks the message list in place and trims the
// whitelisted fields past the threshold. Returns how many fields were trimmed.
// 白名单：
//   - tool 结果正文：Content / RawContent / ReasoningContent（>64KiB 才裁）；
//   - 超大 image data URL（红线扩展）：任何 role 的 Images/Audio 条目 >256KiB
//     才裁。
// ToolCalls.Arguments 属于助手侧入参不在本件范围。
func trimOversizeToolResultFields(msgs []provider.Message) int {
	trimmed := 0
	for i := range msgs {
		m := &msgs[i]
		if m.Role == provider.RoleTool {
			trimField(&m.Content, &trimmed)
			trimField(&m.RawContent, &trimmed)
			trimField(&m.ReasoningContent, &trimmed)
		}
		trimDataURLs(&m.Images, &trimmed)
		trimDataURLs(&m.Audio, &trimmed)
	}
	return trimmed
}

// trimDataURLs trims oversized data-URL entries in place (head+marker+tail),
// shrinking the slice entry itself. Non-URL strings (http(s) refs, Files API
// ids) never touch the threshold path — 只裁内嵌 base64，外部引用不动。
func trimDataURLs(urls *[]string, counter *int) {
	if urls == nil || len(*urls) == 0 {
		return
	}
	for i, u := range *urls {
		if len(u) <= sessionTrimImageThreshold || !strings.HasPrefix(u, "data:") {
			continue
		}
		marker := fmt.Sprintf("%s data-url %d bytes removed by tl1172 L3 deterministic trim %s",
			sessionTrimMarkerHead, len(u)-sessionTrimKeepHead-sessionTrimKeepTail, sessionTrimMarkerTail)
		var b strings.Builder
		b.Grow(sessionTrimKeepHead + len(marker) + sessionTrimKeepTail)
		b.WriteString(u[:sessionTrimKeepHead])
		b.WriteString(marker)
		b.WriteString(u[len(u)-sessionTrimKeepTail:])
		(*urls)[i] = b.String()
		*counter++
	}
}

// trimField replaces *body with head+marker+tail when it exceeds the threshold.
// 同一原始正文可能同时落在 Content 与 RawContent：各自裁剪会让标记出现两次，
// 这是可接受的冗余（两个存储位各自保持「头尾+标记」的可读形态），不为此引入
// 字节级对账。
func trimField(body *string, counter *int) bool {
	if len(*body) <= sessionTrimFieldThreshold {
		return false
	}
	original := len(*body)
	marker := fmt.Sprintf("%s %d bytes removed by tl1172 L3 deterministic trim %s",
		sessionTrimMarkerHead, original-sessionTrimKeepHead-sessionTrimKeepTail, sessionTrimMarkerTail)
	var b strings.Builder
	b.Grow(sessionTrimKeepHead + len(marker) + sessionTrimKeepTail)
	b.WriteString((*body)[:sessionTrimKeepHead])
	b.WriteString(marker)
	b.WriteString((*body)[original-sessionTrimKeepTail:])
	*body = b.String()
	*counter++
	return true
}

// sessionTrimMarkerPresent reports whether a body carries the trim marker —
// the grep contract tests and log triage key on.
func sessionTrimMarkerPresent(body string) bool {
	return strings.Contains(body, sessionTrimMarkerHead)
}
