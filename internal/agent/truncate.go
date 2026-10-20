package agent

import (
	"fmt"
	"strings"

	"reasonix/internal/provider"
)

// Truncation is the lossy last rung of overflow recovery, taken only when no
// summary can form: tool results outside the protected tail are elided
// oldest-first, then whole replay units are dropped, until the view fits under
// the target. It is a projection; canonical storage keeps every byte.
const (
	maintenanceActionTruncate = "truncate"
	elidedToolResultPrefix    = "[tool result elided to fit the context window"
	truncatedHistoryMarker    = "[earlier conversation truncated to fit the context window: %d messages removed]"
	// truncateProtectShare bounds the verbatim tail to this fraction of the
	// target so a rescue can always reclaim enough.
	truncateProtectShare = 4
	// 任务1029②: 内容级省略参数。truncate 的前两级（tool 结果 stub、整 replay
	// unit 删除）都以消息为最小粒度，两类巨消息构成死角：落在保护尾里的巨
	// unit（实测样本：单条 587K 字符的 tool 结果）和 drop 阶段刻意保留的
	// session-context carrier（实测样本：7 条各 ~150K 字符）。 rescue 截到
	// 1066351 >= hard 1048320 只差 18K 却永远失败，就是它们顶住的。本阶段把
	// 单条超过 target/contentElideOversizedDiv 的消息中部省略、头尾保留；
	// 阈值取 target/16，正常体量的消息（含当前轮输入）永远够不到。
	contentElideOversizedDiv = 16
	contentElideHeadChars    = 4000
	contentElideTailChars    = 2000
	contentElideMarkerPrefix = "[content elided to fit the context window"
)

func (a *Agent) truncateToProjectionLocked(trigger string, target int) (bool, error) {
	canonical, transcriptVersion := a.sess.conversation.snapshotMessagesVersion()
	a.sess.compactionMu.Lock()
	stateSnapshot := a.sess.compactionState
	a.sess.compactionMu.Unlock()
	visible, _ := a.visibleInputForFold(stateSnapshot, canonical, transcriptVersion)
	projected, affected := a.truncateView(visible, target)
	if affected == 0 {
		return false, nil
	}
	return a.installMaintenanceProjection(maintenanceInstall{
		trigger: trigger, action: maintenanceActionTruncate, state: stateSnapshot,
		canonical: canonical, transcriptVersion: transcriptVersion,
		visible: visible, projected: projected, affected: affected,
	})
}

// truncateView returns the truncated copy of visible and how many messages it
// changed; zero means the view already fits or nothing could be cut.
func (a *Agent) truncateView(visible []provider.Message, target int) ([]provider.Message, int) {
	total := a.estimatedVisibleRequestTokens(visible)
	if target <= 0 || total < target || len(visible) == 0 {
		return nil, 0
	}
	head := a.pinnedPrefixLen(visible)
	budget := max(1, min(a.recentTailBudget(), target/truncateProtectShare))
	protect := tailStart(visible, head, budget, a.tokPerChar(), minRecentKeep)
	projected := append([]provider.Message(nil), visible...)
	remaining, affected := total, 0
	for i := head; i < protect && remaining >= target; i++ {
		elided, ok := elideToolResult(projected[i])
		if !ok {
			continue
		}
		remaining -= a.messageTokens(projected[i]) - a.messageTokens(elided)
		projected[i] = elided
		affected++
	}
	if remaining >= target {
		var dropped int
		projected, dropped = a.dropOldestUnits(projected, head, protect, target)
		affected += dropped
	}
	// 任务1029②: 前两级截不动时的消息级/内容级兜底——巨消息中部省略留头尾，
	// 覆盖保护尾内的巨 unit 与 drop 保护的 carrier 死区。
	if a.estimatedVisibleRequestTokens(projected) >= target {
		var elided int
		projected, elided = a.elideOversizedContents(projected, head, target)
		affected += elided
	}
	if affected == 0 || a.estimatedVisibleRequestTokens(projected) >= total {
		return nil, 0
	}
	return projected, affected
}

func (a *Agent) messageTokens(m provider.Message) int {
	return a.estimatedPromptTokens([]provider.Message{m})
}

func elideToolResult(m provider.Message) (provider.Message, bool) {
	if m.Role != provider.RoleTool || m.LocalOnly || m.Content == "" || strings.HasPrefix(m.Content, elidedToolResultPrefix) {
		return m, false
	}
	out := m
	out.Content = fmt.Sprintf("%s: %d bytes]", elidedToolResultPrefix, len(m.Content))
	out.RawContent = ""
	out.ProviderContent = ""
	out.Images = nil
	return out, true
}

// elideOversizedContents is the last rung of truncateView (任务1029②): any
// message still above target/contentElideOversizedDiv tokens — typically a
// giant tool result inside the protected tail or a drop-protected
// session-context carrier — has its middle removed while head and tail stay
// verbatim. RawContent keeps the full local original; ProviderContent is
// cleared so it cannot re-introduce the elided bytes at the provider boundary
// (projection.go swaps it over Content when set). Already-elided messages are
// skipped so repeated rescues stay idempotent.
func (a *Agent) elideOversizedContents(msgs []provider.Message, head, target int) ([]provider.Message, int) {
	threshold := max(1, target/contentElideOversizedDiv)
	affected := 0
	for i := head; i < len(msgs); i++ {
		m := msgs[i]
		if m.LocalOnly || m.Content == "" ||
			strings.HasPrefix(m.Content, contentElideMarkerPrefix) ||
			strings.HasPrefix(m.Content, elidedToolResultPrefix) ||
			isCompactionSummary(m) || IsPinnedContextRevision(m) {
			continue
		}
		if a.messageTokens(m) <= threshold {
			continue
		}
		// 短于"头+尾"保留量的消息切了没有净收益（省下的不够标记本身），
		// 直接跳过——也避免极小窗口下 tail 区间越界。
		if len(m.Content) <= contentElideHeadChars+contentElideTailChars {
			continue
		}
		headPart := snapToRuneBoundary(m.Content, 0, contentElideHeadChars)
		tailPart := snapToRuneBoundary(m.Content, len(m.Content)-contentElideTailChars, len(m.Content))
		removed := len(m.Content) - len(headPart) - len(tailPart)
		out := m
		out.Content = fmt.Sprintf("%s: %d of %d bytes removed from the middle]\n%s\n[...]\n%s",
			contentElideMarkerPrefix, removed, len(m.Content), headPart, tailPart)
		out.ProviderContent = ""
		msgs[i] = out
		affected++
	}
	return msgs, affected
}

// dropOldestUnits removes whole replay units from the oldest end of the
// foldable region until the estimate fits. The session-context section
// carriers, compaction digests, and pinned revisions survive behind one
// marker.
func (a *Agent) dropOldestUnits(msgs []provider.Message, head, protect, target int) ([]provider.Message, int) {
	if protect <= head {
		return msgs, 0
	}
	remaining := a.estimatedVisibleRequestTokens(msgs)
	carriers := sessionContextCarrierSet(msgs)
	var kept []provider.Message
	dropped, end := 0, head
	for _, u := range extractMessageUnits(msgs[head:protect]) {
		if remaining < target {
			break
		}
		for i := head + u.lo; i < head+u.hi; i++ {
			if carriers[i] || isCompactionSummary(msgs[i]) || IsPinnedContextRevision(msgs[i]) {
				kept = append(kept, msgs[i])
				continue
			}
			remaining -= a.messageTokens(msgs[i])
			dropped++
		}
		end = head + u.hi
	}
	if dropped == 0 {
		return msgs, 0
	}
	out := make([]provider.Message, 0, len(msgs)-dropped+1)
	out = append(out, msgs[:head]...)
	out = append(out, HostGeneratedUserMessage(fmt.Sprintf(truncatedHistoryMarker, dropped)))
	out = append(out, kept...)
	out = append(out, msgs[end:]...)
	return out, dropped
}
