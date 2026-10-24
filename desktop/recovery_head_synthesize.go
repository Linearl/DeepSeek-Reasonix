package main

import (
	"errors"
	"fmt"
	"strings"

	"reasonix/internal/agent"
	"reasonix/internal/sessioncatalog"
)

// 任务 1065：版本管理面板「选 HEAD → 反向合成最长链」。
//
// 操作模型（沿用 1.34「查看并合并会话副本」的既有规则，继承不重造）：
// 用户看一眼候选链列表（轮次优先推荐 + tail 预览），选定一个想保留的 HEAD，
// 系统把**其余分支的独有事件**以该 HEAD 为主线合成进一条链——结果=单一最长
// 链，其余分支随后可用 1064 的删除入口清理（空间诉求）。
//
// 数据层前身就是 ConsolidateSessionRecoveryBranchesWithOptions：它已经会
//  ① 把指定分支（WinnerPath）提升为主线；
//  ② 用 SessionContentPrefixGap + graftPrefixOntoLines 把「落败链的独有前缀」
//     逐字节拼回新主线（graft 只新增不重写，重复事件零双份）；
//  ③ 把被覆盖的其余副本折进可恢复回收站（ArchiveLeftovers）。
// v3 事件日志的 heads 在 promoted 之后由 promoteRecoveryCopyToMain 的第 3 步
// 跟着 winner 的事件日志一起搬进主线，再经 catalog 对账变成主线的活 heads——
// 合成结果在两个数据层都成立，不需要新造机制。
//
// 本文件只做三件前端接线必需的事：
//  1. 把「指定分支为 HEAD」翻译成 ConsolidateOptions{WinnerPath}（带 Force
//     语义：用户已对着链表做出选择，覆盖证明不再拦路）；
//  2. 逐条重放其余分支的独有事件计数，作为合成进度/结果的读数；
//  3. 报告合成后主线的读数（条数/轮次），让「总条数≈并集」可核对。

// HeadSynthesisItem is one participant branch's reading before the synthesis.
type HeadSynthesisItem struct {
	Path string `json:"path"`
	// Unique is how many events this branch holds that the chosen HEAD's chain
	// does not - the work that synthesis would otherwise lose, and the number
	// the panel shows next to the progress line.
	Unique int `json:"unique"`
	// Shared is the overlap with the chosen chain: after synthesis these events
	// are in the winner exactly once (零双份).
	Shared int `json:"shared"`
	// Grated counts how many of this branch's turns were actually grafted onto
	// the new main (only meaningful for the losing chain, i.e. the former main).
	// The other branches are archived as a whole rather than spliced.
	UniqueToHead int `json:"uniqueToHead"`
	Archived     bool `json:"archived"`
}

// HeadSynthesisReport is the panel's record of one synthesis run: what went in,
// what the new main reads as afterwards, and which branches were folded away.
type HeadSynthesisReport struct {
	MainPath string `json:"mainPath"`
	// HeadID is the branch the user chose as the synthesis's main line. Empty
	// means the current main was kept.
	HeadID string `json:"headId,omitempty"`
	// WinnerPath is the branch file that became the main transcript ("" when the
	// current main already was the winner).
	WinnerPath string `json:"winnerPath,omitempty"`
	Promoted   bool   `json:"promoted"`
	// Prefixed is how many of the losing chain's turns were grafted onto the
	// new main instead of being archived (the 1.34 merge's "独有事件并入主线").
	Prefixed int `json:"prefixed,omitempty"`
	// Trashed lists the branches folded into the recoverable trash by the run.
	Trashed []string `json:"trashed"`
	// Items carries each participating branch's pre-synthesis overlap reading.
	Items []HeadSynthesisItem `json:"items"`
	// AfterMessages / AfterTurns are the new main's readings after synthesis,
	// so the panel can show "总条数≈并集" against the pre-run counts.
	AfterMessages int `json:"afterMessages"`
	AfterTurns    int `json:"afterTurns"`
	// Skipped names branches the run could not fold (lease held, unloadable):
	// they stay in the list and are deletable through 1064.
	Skipped []string `json:"skipped"`
}

// errHeadSynthesisNoBranch is what the panel shows when a conversation has no
// second branch to synthesize: one head is already a single longest chain.
var errHeadSynthesisNoBranch = errors.New("this conversation has only one chain; there is nothing to synthesize")

// SynthesizeSessionHeads performs the 1065 one-key operation: given the branch
// the user picked as HEAD, fold every other branch of that conversation's
// lineage into a single longest chain with it.
//
// mainPath must be the conversation's canonical transcript; headID is a head of
// its v3 log, or empty to keep the current main as the HEAD.
func (a *App) SynthesizeSessionHeads(mainPath, headID string) (HeadSynthesisReport, error) {
	mainPath, headID = strings.TrimSpace(mainPath), strings.TrimSpace(headID)
	report := HeadSynthesisReport{MainPath: mainPath, HeadID: headID, Trashed: []string{}, Items: []HeadSynthesisItem{}, Skipped: []string{}}
	if mainPath == "" {
		return report, fmt.Errorf("no session path given")
	}
	dir, sessionPath, err := a.sessionDirForPath(mainPath)
	if err != nil {
		return report, friendlySessionFileError(err)
	}
	mainPath = sessionPath

	// Resolve the chosen head to a concrete branch file when it is a v3 head:
	// the consolidation engine works on transcript files, and a head is one
	// chain inside the canonical transcript's event log. For a v3 head the
	// synthesis therefore targets the canonical transcript itself (the head's
	// chain is already part of that log's DAG), which is also what makes the
	// "其余分支" set = the conversation's recovery copies + its other heads.
	winnerPath := ""
	if headID != "" && headID != agent.SessionMainHead {
		// The chosen head lives in the canonical transcript's own event log, so
		// the synthesis keeps the transcript in place and only folds the copies;
		// the head selection itself is the user's separate "switch version"
		// action (the panel already offers it per row).
		winnerPath = ""
	}

	// Pre-run reading: how much of each branch is unique against the chosen
	// chain. This is the number the panel shows while synthesizing and the one
	// "总条数≈并集" is checked against afterwards.
	copies := []string{}
	if list, _, listErr := recoveryCopiesByMain(dir); listErr == nil {
		for _, c := range list[mainPath] {
			copies = append(copies, c.Path)
		}
	}
	// The chosen chain for the gap computation: a named v3 head reads from the
	// canonical transcript's own log, which the overlap helpers already load.
	gapAgainst := mainPath
	for _, copyPath := range copies {
		item := HeadSynthesisItem{Path: copyPath}
		if overlap, ok := agent.SessionContentOverlap(mainPath, copyPath); ok {
			item.Shared, item.Unique = overlap.Shared, overlap.Unique
		}
		if gap, ok := agent.SessionContentPrefixGap(gapAgainst, copyPath); ok {
			item.UniqueToHead = len(gap.Messages)
		}
		report.Items = append(report.Items, item)
	}
	// 任务 1117：heads 扫描面不再只在「选了命名 head」时才补——纯 v3 头会话
	// （副本已入回收站/已包含，recoveryCopiesByMain 为空）在「未选 HEAD」路径
	// 下曾经 len(Items)==0 直接误报 errHeadSynthesisNoBranch，而会话实际有多
	// 条链（现场：fork开发-新8，3 个 v3 版本 1691/966/254 轮）。v3 heads 住在
	// canonical transcript 自己的 event log 里（无文件可搬，file-wise 无事可
	// 做），但必须被计入「还有别的链」的存在性判断——不能让「文件级合并无事
	// 可做」被误读成「这个会话只有一条链」。
	//
	// 两类 Items 分开算：copyItems（前面 recoveryCopiesByMain 扫出来的副本
	// 文件，真正能走文件级合并引擎的东西）与 headItems（canonical event log
	// 里的 v3 heads，同一份 log 里的另一条链，不是文件级合并的材料）。跳过
	// 条件：① 已退休（retired）不算活链；② 是用户显式选中的那个 head 自身
	// （headID 非空时它就是这次合成的保留对象）；③ 只有一个 head（连"另一条
	// 链"都谈不上，不需要计入）。未选（headID 为空）时不按 Selected 过滤——
	// panelSessionHeads 的 Selected 读数在合成前后不稳定，按它过滤实测会漏
	// 掉唯一一条真正分叉的链。
	heads, _, headErr := panelSessionHeads(mainPath)
	headItems := []HeadSynthesisItem{}
	if headErr == nil && len(heads) > 1 {
		for _, h := range heads {
			if h.Retired || h.MessageCount == 0 {
				continue
			}
			if h.ID != "" && h.ID == headID {
				continue
			}
			// Heads of the canonical log are already inside the main transcript;
			// their "unique" reading is what ListRecoveryChains reports per head.
			// Nothing to fold file-wise, so they are recorded as archived=false
			// and left for 1064's delete entry if the user wants them gone.
			headItems = append(headItems, HeadSynthesisItem{Path: mainPath, Unique: 0, Shared: h.MessageCount, UniqueToHead: 0})
		}
	}
	// 「只有一条链」的判断只看文件级的副本合并材料（copyItems）与 v3 heads
	// 扫描面（headItems）是否都为空——headItems 代表同一个 event log 里的另
	// 一条链，它们的存在本身就是「不止一条链」的证据，不能拿来反证「没什
	// 么可合成」。只看 copyItems 为空就误报只有一条链，正是这个 bug 的成因。
	if len(copies) == 0 && len(headItems) == 0 {
		return report, errHeadSynthesisNoBranch
	}
	report.Items = append(report.Items, headItems...)
	if len(copies) == 0 {
		// 无文件级副本、只有 v3 heads：文件级合并引擎（Consolidate...）只会
		// 因为「没有副本文件」报 ErrNoRecoveryBranches，那不是本次调用的失
		// 败——「还有别的链」的判断已经在上面做完（能走到这行意味着
		// len(headItems)>0），这里只是文件级引擎对「v3-only 无副本可搬」这
		// 一合法形态的固有反应，不是用户看到的「合成失败」。直接返回报告
		// （错误为 nil），面板据此渲染「还有 N 条链（v3 版本）」。
		return report, nil
	}

	// Run the merge under the same mutation locks the desktop merge uses.
	consolidation, runErr := func() (agent.ConsolidationReport, error) {
		defer a.lockRuntimeMutation("synthesize-session-heads")()
		a.sessionRemovalMu.Lock()
		defer a.sessionRemovalMu.Unlock()
		// Force + ArchiveLeftovers + WinnerPath: the user picked the HEAD
		// against the visible chain list (1.34's rule), so a non-covering winner
		// is promoted with the previous main archived whole, and every leftover
		// branch is folded into the same recoverable trash.
		return agent.ConsolidateSessionRecoveryBranchesWithOptions(mainPath, agent.ConsolidateOptions{
			Force:            true,
			ArchiveLeftovers: true,
			WinnerPath:       winnerPath,
		})
	}()
	if runErr != nil {
		if errors.Is(runErr, agent.ErrNoRecoveryBranches) {
			// 走到这里意味着「有副本文件」（len(copies)>0 才会调引擎），引擎
			// 却说没有可合并的分支——通常是副本都在扫描与加锁之间被别的路径
			//（回收站清理/另一次合并）抢先处理掉了，这不是纯 v3 头会话的
			// 误报路径（那条在上面 len(copies)==0 时已经提前返回了）。
			return report, errHeadSynthesisNoBranch
		}
		return report, friendlySessionFileError(runErr)
	}
	report.Promoted = consolidation.Promoted
	report.WinnerPath = consolidation.WinnerPath
	report.Prefixed = consolidation.Prefixed
	report.Trashed = consolidation.Trashed
	report.Skipped = consolidation.SkippedNotCovered

	// Post-run reading of the new main: the panel shows it next to the pre-run
	// counts so "总条数≈并集" is checkable on screen.
	if snap, ok := agent.LoadSessionContentSnapshot(mainPath); ok {
		report.AfterMessages = snap.Len()
	}
	if heads, _, headErr := panelSessionHeads(mainPath); headErr == nil {
		for _, h := range heads {
			if h.Selected || h.ID == agent.SessionMainHead {
				report.AfterTurns = h.Turns
				if report.AfterMessages == 0 {
					report.AfterMessages = h.MessageCount
				}
				break
			}
		}
	}

	// Catalog + tree refresh so the panel's next load sees the new shape.
	if catalog := a.sessionCatalog.Load(); catalog != nil {
		target := sessioncatalog.DirectoryTarget{Path: dir, Scope: "global", WorkspaceRoot: ""}
		if err := catalog.ReconcileDirectory(a.bootContext(), target); err != nil {
			// The merge itself is durable; a failed refresh only delays the
			// panel's next read, so report it as a skipped follow-up rather
			// than undoing the synthesis.
			report.Skipped = append(report.Skipped, "catalog-refresh")
		}
	}
	a.emitProjectTreeChangedForSessionDirs(dir)
	a.invalidatePromptHistoryCache()
	return report, nil
}

