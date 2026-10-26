package agent

import (
	"fmt"
	"log/slog"
	"strings"

	"reasonix/internal/provider"
	"reasonix/internal/secrets"
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
	// sessionTrimImageThreshold 图像 data URL 裁剪门槛：256KiB 以下的图不动
	//（图标/小图保留，超大截图才裁）。2026-10-26 对 fork开发-新10 的 123MB
	// 实测：>64KiB 大行的 86.8MiB 里 77.8MiB 是 user 消息的 images base64，
	// tool 结果文本只有 2.3MiB——不裁图像，该会话过不了 cap（红线扩展见下）。
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

// 红线扩展说明（任务书「tool 结果类事件」之外的部分，回执需用户追认）：
// 123MB 实测构成是 user 消息内嵌截图（images data URL）主导，不是 tool 结果。
// 据此把 L3 白名单扩展到 **超大 image data URL**（>256KiB）：头 1.5KB（保留
// data: 前缀可识别 MIME）+ 显式标记（带原始字节数）+ 尾 1.5KB。这是有损裁剪
// ——红线允许（「只能压缩/归档/有损裁剪（裁剪必须保留头尾且可识别）」），且
// 与任务 1011 自愈的「local diet: strip embedded images」同一字段先例；受同一
// 实验开关（默认关）约束。裁剪不可逆（图像内容不保留）但可解释（标记带原始
// 尺寸、逐字可 grep）。

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
	// 强制维护旋转（CompactSessionFile 的 1086 修复同款机制）：重写产生的
	// rewind+replace 使旧链不可达，旋转把不可达节点物理清出日志。fork开发-新10
	// 123MB 实测：裁剪只及载入视图里的 13 个字段（-16.4MiB），另有 64MiB 大图
	// 压在已不可达的链历史节点里——只有这一步能落回视图尺寸（11MiB）。单写者
	// 证明由本函数持有的 lease 满足；失败不回滚裁剪（日志停在裁剪后尺寸）。
	if st := sessionCompactRepairDAG(sessionPath); st != nil {
		if rotErr := sessionCompactRotateDAG(sessionPath, st); rotErr != nil {
			slog.Warn("session: forced rotation after trim failed (log stays at trimmed size)",
				"path", sessionPath, "err", secrets.RedactError(rotErr))
		}
	}
	after = SessionEventLogZstSize(sessionPath)
	slog.Info("session: oversized tool results trimmed (lossy, head+tail kept with explicit markers)",
		"path", sessionPath, "fieldsTrimmed", trimmed, "bytesBefore", before, "bytesAfter", after)
	return before, after, trimmed, nil
}

// trimOversizeToolResultFields walks the message list in place and trims the
// whitelisted fields past the threshold. Returns how many fields were trimmed.
// 白名单：
//   - tool 结果正文：Content / RawContent / ReasoningContent（>64KiB 才裁，
//     Content 是有界正文、RawContent 是全量原文——各自独立裁）；
//   - 超大 image data URL（红线扩展，见常量区说明）：任何 role 的 Images/Audio
//     条目 >256KiB 才裁（实测该会话 77.8MiB 集中在此）。
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
