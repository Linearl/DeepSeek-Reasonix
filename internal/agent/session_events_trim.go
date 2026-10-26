package agent

import (
	"fmt"
	"log/slog"
	"strings"

	"reasonix/internal/provider"
)

// 任务 1172 L3 —— 超大 tool 结果的确定性裁剪（照 mimo prune 的 head+tail 保留）。
//
// 定位：三级降级的最后一级。L2 物理压缩（无损）后仍超 cap，或压缩不可用时，
// 对 tool 结果类消息里超过阈值的正文做「头尾各留一段 + 中间置明确标记」的
// 有损裁剪——保留头尾与标记是红线要求（可识别、可解释），丢弃的中间字节在
// 标记里带出原始尺寸。
//
// 判定与 mimo 同构：
//   - 只裁 role=tool 的消息（工具结果是膨胀主源，用户的对话正文永远不碰）；
//   - 单字段超过 sessionTrimFieldThreshold 才裁（小字段整条保留）；
//   - 头尾各保 sessionTrimKeepHead/Tail 字节，中间替换为带原始字节数的标记；
//   - 裁剪经由 SaveRewriteCompact 重写落盘（同一套 CAS/staged-write 机制），
//     失败不损原日志。
//
// 与 978（上下文压缩时机）的关系：L3 改的是 events 事实日志与转录里工具结果
// 的存储形态，不是给模型看的 token 视图、也不由压缩时机驱动；裁剪发生在空闲
// 维护窗口，L2 之后仍超限才会走到。
const (
	// sessionTrimFieldThreshold 单字段裁剪门槛：64KiB 以下的字段不动。
	sessionTrimFieldThreshold = 64 << 10
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
// bodies in the session's persisted transcript, rewriting the log via the
// maintenance-compact path. Reports the log sizes before/after and how many
// fields were trimmed. 调用方持有空闲判定职责（与 CompactSessionFile 相同的
// lease 纪律在这里执行：lease 取不到 = 有别的运行时在写，直接失败）。
func TrimSessionOversizeToolResults(sessionPath string) (before, after int64, trimmed int, err error) {
	if strings.TrimSpace(sessionPath) == "" {
		return 0, 0, 0, fmt.Errorf("empty session path")
	}
	// 归档态先物化：裁剪作用在明文世界上，重写发布明文（归档残件由物化清理）。
	if _, err := ensureSessionEventLogPlain(sessionPath); err != nil {
		return 0, 0, 0, err
	}
	before = SessionEventLogZstSize(sessionPath)
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
	after = SessionEventLogZstSize(sessionPath)
	slog.Info("session: oversized tool results trimmed (lossy, head+tail kept with explicit markers)",
		"path", sessionPath, "fieldsTrimmed", trimmed, "bytesBefore", before, "bytesAfter", after)
	return before, after, trimmed, nil
}

// trimOversizeToolResultFields walks the message list in place and trims the
// whitelisted body fields of tool results past the threshold. Returns how many
// fields were trimmed. 白名单刻意最小化：Content / RawContent / ReasoningContent
// 是工具结果正文的三个落点（Content 是有界正文、RawContent 是全量原文——两者
// 独立裁，各保各的头尾）；Images/Audio 是数据 URL（裁了就是损坏的引用，交由
// L2 无损压缩去管），ToolCalls.Arguments 属于助手侧入参不在本件范围。
func trimOversizeToolResultFields(msgs []provider.Message) int {
	trimmed := 0
	for i := range msgs {
		m := &msgs[i]
		if m.Role != provider.RoleTool {
			continue
		}
		trimField(&m.Content, &trimmed)
		trimField(&m.RawContent, &trimmed)
		trimField(&m.ReasoningContent, &trimmed)
	}
	return trimmed
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
