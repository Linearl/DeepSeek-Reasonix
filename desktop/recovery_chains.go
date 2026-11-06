package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"reasonix/internal/agent"
)

// RecoveryChainView is one candidate chain a merge could promote: a session
// transcript as seen from one of its heads.
//
// tl1355：schema-2 头重放退役（tl1227/tl1280）后，一个候选 = 一个转录文件
// （schema-1 单链形态，HeadID 恒空、行身份即文件）；主转录与各恢复副本各占
// 一行，每行自带完整历史，选它不需要跨文件拼接。读数由会话文件内容扫描合
// 成（agent.ScanSessionChainViews），链间覆盖关系由 digest/前缀覆盖判定标
// 出（Covered）。
//
// MessageCount and Turns are what make "longest" a fact rather than a guess, and
// LastActivity is what makes "newest" one.
type RecoveryChainView struct {
	Path         string `json:"path"`
	HeadID       string `json:"headId"`
	Label        string `json:"label"`
	IsMain       bool   `json:"isMain"`
	Selected     bool   `json:"selected"`
	Covered      bool   `json:"covered"`
	MessageCount int    `json:"messageCount"`
	Turns        int    `json:"turns"`
	LastActivity string `json:"lastActivity"`
	Bytes        int64  `json:"bytes"`
	Preview      string `json:"preview"`
	// Degraded marks a row whose readings are not full-content verified
	// (tl1355)：读数来自 tolerant 兜底（残缺日志的可读前缀，计数真实但未经
	// 严格校验），或整文件解析不可得（超字节/时间预算——条数由纯行计数填真
	// 实值，轮次/预览缺席）。行照常展示、标灰，不推为「更长/更新」赢家。
	Degraded bool `json:"degraded,omitempty"`
}

// RecoveryChainSet is the answer to "which chains exist for this conversation",
// with each chain's own strengths marked separately so the panel can recommend
// without overclaiming (任务 1123).
//
// 任务 1123（用户 20261024 反馈）：旧形态只有一个 LongestPath/LongestHead，
// 前端据此打「建议（更长且更新）」这一条合并文案——但实际被推荐的长链
// （6800条/246轮，最后活动 2026-09-28）并不比落选短链（468条/6轮，
// 2026-10-22 折叠）更新，「且更新」这一半是失实的：长短（条数/轮次）与新旧
// （最后活动时间）是两个独立的事实，任何一条链都可能只占其一。改成两条独立
// 读数——轮次优先的「更长」与最后活动优先的「更新」各自成立、各自标注理由
// （含实际值），不再合成一条可能只对一半成立的表述让用户自行判断哪条更合
// 用，而不是替用户把两个独立事实打包成一个「且」字。
type RecoveryChainSet struct {
	MainPath  string              `json:"mainPath"`
	MainLabel string              `json:"mainLabel"`
	Chains    []RecoveryChainView `json:"chains"`
	// Longest is the path+head of the chain with the most turns (raw message
	// count only breaks ties - see the sort below for why). Ties prefer the main
	// transcript, then the most recent activity, so the recommendation is stable
	// rather than dependent on directory order.
	LongestPath string `json:"longestPath"`
	LongestHead string `json:"longestHead"`
	// Newest is the path+head of the chain with the most recent LastActivity,
	// computed independently of Longest: a chain can be "longer" without being
	// "newer" (or the other way around), and the panel shows each fact on its own
	// row rather than folding them into one label (任务 1123). Ties prefer the
	// main transcript, then message count, for the same stability reason.
	NewestPath string `json:"newestPath"`
	NewestHead string `json:"newestHead"`
}

// ListRecoveryChains enumerates every candidate chain for one conversation: the
// main transcript plus each recovery copy, and for each of those every live head.
//
// This is a read-only inventory. It replays each log once, which is why the panel
// asks for it when the user opens a conversation's detail rather than for the whole
// list at once.
func (a *App) ListRecoveryChains(mainPath string) (RecoveryChainSet, error) {
	mainPath = strings.TrimSpace(mainPath)
	// 任务 1039r2：Chains 以空切片起底——全部候选（主转录+副本）的探测/重放
	// 都以非限额错误失败时，nil 切片会序列化成 JSON null，前端展开行的
	// .length 读数直接渲染期崩溃（实测 GC开发专员会话分区横幅报
	// "Cannot read properties of null (reading 'length')"）。空数组才是
	// 「没有候选链」的合法形状。
	set := RecoveryChainSet{MainPath: mainPath, Chains: []RecoveryChainView{}}
	if mainPath == "" {
		return set, fmt.Errorf("no session path given")
	}
	// Accept the transcript path even when the picker handed us the .meta id.
	if _, err := os.Stat(mainPath); err != nil {
		if resolved, ok := resolveTranscriptSibling(mainPath); ok {
			mainPath = resolved
			set.MainPath = mainPath
		}
	}
	set.MainLabel = recoveryDisplayName(mainPath)

	candidates := []string{mainPath}
	if copies, _, err := recoveryCopiesByMain(filepath.Dir(mainPath)); err == nil {
		for _, copy := range copies[mainPath] {
			candidates = append(candidates, copy.Path)
		}
	} else {
		return set, err
	}

	// tl1355：链读数改为会话文件内容扫描合成。panelSessionHeads 的多头枚举
	// 自 tl1227/tl1280 起是恒空垫片（schema-2 头重放退役），原代码走到
	// len(heads)==0 分支时每候选只带 snapshot 条数——0·0+无预览正是那次退
	// 化的现场。现在每个候选文件本身就是一条完整的链（schema-1 单链形态），
	// 条数/轮次/预览/最后活动直接从内容扫描来，链间覆盖关系由 digest/前缀
	// 覆盖判定（ScanSessionChainViews，与合并引擎同一套 Covers 规则）标出。
	// 扫描成本（整文件读取）比读索引高，任务书明示可接受；快照/重叠缓存让
	// 反复展开同一会话零重付。
	scans := agent.ScanSessionChainViews(candidates)
	scanByPath := make(map[string]agent.ChainScanView, len(scans))
	for _, scan := range scans {
		scanByPath[scan.Path] = scan
	}
	// tl1430b 环②：「当前主线」行必须跟着显式切换走。Selected 原先恒等于
	// isMain（扫描起点=面板展开的主线文件），用户采纳副本后表内主线行纹丝
	// 不动——回执闪了一下、表行照旧正是包 78 现场。单一真相源=meta 的
	// RecoveryPreferred（与重开读取的 promote 判定同源）；无任何显式选择时
	// 退回 isMain（单主线场景零回归）。
	preferredRows := make(map[string]bool, len(candidates))
	anyPreferred := false
	for _, path := range candidates {
		if meta, ok, err := agent.LoadBranchMeta(path); err == nil && ok && meta.RecoveryPreferred {
			preferredRows[path] = true
			anyPreferred = true
		}
	}
	// tl1415c A：Selected 升级为权威源同源——优先取 recoveryAuthorityMainPath
	// 的裁决（打开的 tab 当前转录 → 显式偏好，与 already-main 闸门读的同一对
	// 权威），meta 直读降为其后的回退。此前两条线各判各的（这里=目录内 meta
	// preferred + 扫描起点回退；切换闸=tab 路径 + catalog 偏好）：tab 停在副
	// 本文件上而目录里无显式偏好时，闸门认为副本是主线、面板却把扫描起点标
	// 成主线——用户实测「下面那个是主线为何还能切换到此版本」的分裂现场。
	// catalog 拿不到（路径未入册/目录未建组）时保留旧判定，不为入不了册的
	// 路径发明裁决。
	authorityPath := ""
	if catalog := a.sessionCatalog.Load(); catalog != nil {
		authorityPath = a.recoveryAuthorityMainPath(catalog, mainPath)
	}
	for _, path := range candidates {
		info, statErr := os.Stat(path)
		var bytes int64
		if statErr == nil {
			bytes = info.Size()
		}
		isMain := samePath(path, mainPath)
		// tl1415c A：selected 与切换闸同源——权威路径命中即选中；权威缺席时
		// 才落到 tl1430b 的旧判定（meta preferred → 扫描起点回退）。
		selected := samePath(path, authorityPath) ||
			(authorityPath == "" && (preferredRows[path] || (!anyPreferred && isMain)))
		scan := scanByPath[path]
		if !scan.Readable {
			// 降级行：整文件读不出（超字节/时间预算、tolerant 也拒、文件消
			// 失）。与任务 1039 的降级同语义——保留行、其他候选照旧枚举，
			// 面板看得见哪一份读不了；条数由纯行计数填真实值（协调线输入①：
			// 不解析 JSON，~0.1s/86MB），轮次/预览不可得。
			set.Chains = append(set.Chains, RecoveryChainView{
				Path: path, Label: recoveryDisplayName(path), IsMain: isMain,
				Selected:     selected,
				MessageCount: scan.MessageCount,
				LastActivity: scan.LastActivity.UTC().Format("2006-01-02T15:04:05Z"),
				Bytes:        bytes, Degraded: true,
			})
			continue
		}
		set.Chains = append(set.Chains, RecoveryChainView{
			Path:    path,
			Label:   recoveryDisplayName(path),
			IsMain:  isMain,
			// tl1430b：Selected 跟随显式采纳（preferredRows）；副本行的激活
			// 按钮（headId 为空）走既有的副本采纳路径（SetActiveSessionVersion
			// 的 catalog lineage 校验），与恒空 heads 时代副本行的行为一致。
			Selected:     selected,
			Covered:      scan.CoveredBy != "",
			MessageCount: scan.MessageCount,
			Turns:        scan.Turns,
			LastActivity: scan.LastActivity.UTC().Format("2006-01-02T15:04:05Z"),
			Bytes:        bytes,
			Preview:      scan.Preview,
			// Degraded 语义收窄为「读数来自 tolerant 兜底」：计数真实但未经
			// 严格校验（残缺日志的可读前缀），面板标灰但不隐藏。降级身份行
			// （!Readable）在上方单独成行。
			Degraded: !scan.Loadable,
		})
	}

	sort.SliceStable(set.Chains, func(i, j int) bool {
		// User turns outrank raw message count: a tool-only transcript can pile
		// up thousands of assistant rows without a single user turn, and by raw
		// count it would top the list (and be "recommended") over the real
		// conversation - exactly what the merge would then promote.
		if set.Chains[i].Turns != set.Chains[j].Turns {
			return set.Chains[i].Turns > set.Chains[j].Turns
		}
		if set.Chains[i].MessageCount != set.Chains[j].MessageCount {
			return set.Chains[i].MessageCount > set.Chains[j].MessageCount
		}
		if set.Chains[i].IsMain != set.Chains[j].IsMain {
			return set.Chains[i].IsMain
		}
		return set.Chains[i].LastActivity > set.Chains[j].LastActivity
	})
	// 任务 1019：longest（更长）与 newest（更新）只在带**完整内容读数**的链
	// 里选——零读数（真空会话）与纯计数降级行（时间/字节预算外，条数来自
	// 行计数、轮次/预览不可得）不推为赢家，全部不可得时留空，前端因此不标
	// 推荐。tl1355 起降级行里有一档 tolerant 兜底行（计数真实但未经严格校
	// 验）：它们按真实读数参选——「更长」的判据是内容事实，不是行的校验等
	// 级；Chains 已按轮次优先排好序，第一个带完整读数的就是「更长」。
	for _, chain := range set.Chains {
		scan := scanByPath[chain.Path]
		if chain.MessageCount > 0 && (scan.Loadable || scan.Readable) {
			set.LongestPath = chain.Path
			set.LongestHead = chain.HeadID
			break
		}
	}
	// 任务 1123：「更新」独立于「更长」计算——LastActivity 是字符串比较
	// （RFC3339 UTC 恒定宽度，字典序即时间序），直接扫一遍取最大值即可，
	// 不需要额外排序。同样只认带完整内容读数的行（零读数与纯计数行不是
	// 「最新」）。平手时按当前排序序（轮次优先）先出现者胜，与 longest 的
	// 平手规则一致。
	newestIndex := -1
	for i, chain := range set.Chains {
		scan := scanByPath[chain.Path]
		if chain.MessageCount == 0 || !(scan.Loadable || scan.Readable) {
			continue
		}
		if newestIndex == -1 || chain.LastActivity > set.Chains[newestIndex].LastActivity {
			newestIndex = i
		}
	}
	if newestIndex >= 0 {
		set.NewestPath = set.Chains[newestIndex].Path
		set.NewestHead = set.Chains[newestIndex].HeadID
	}
	return set, nil
}

// resolveTranscriptSibling maps a session id or .meta path onto the transcript
// beside it, so callers may pass whichever they hold.
func resolveTranscriptSibling(path string) (string, bool) {
	base := strings.TrimSuffix(path, ".meta")
	for _, ext := range []string{".jsonl", ""} {
		candidate := base + ext
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, true
		}
	}
	return "", false
}

// PreviewRecoveryChain returns an in-memory preview of one candidate chain for
// the merge picker's dialog: sizes, how the branch starts and ends, and what
// picking it would keep or drop relative to the current main. Nothing is
// written - a preview can be repeated freely and cancels cleanly.
//
// mainPath reaches us straight from the chain enumeration (ListRecoveryChains),
// which scans every project's session directory - not just the one the active
// tab lives in. Anchoring the preview on activeSessionDir used to fail the
// whole dialog whenever the panel was opened from any other tab: the path was
// "outside" that tab's directory and the registered-project fallback did not
// know auto-discovered session dirs either. RecoveryChainPreviewFor itself
// re-verifies that chainPath belongs to mainPath before reading anything, so
// the security boundary lives there, not in this resolution dance.
func (a *App) PreviewRecoveryChain(mainPath string, chainPath string) (agent.RecoveryChainPreview, error) {
	mainPath = strings.TrimSpace(mainPath)
	if mainPath == "" {
		return agent.RecoveryChainPreview{}, fmt.Errorf("no session path given")
	}
	return agent.RecoveryChainPreviewFor(mainPath, chainPath)
}

// PreviewRecoveryChainMessages returns the trailing slice of one chain's
// messages for the conversation-style preview dialog. Read-only, tolerant
// loader: unnormalized copies preview the same way the summary showed them.
// previewFeedCache remembers the fully-flattened feed of a chain between dialog
// opens. Building the feed replays the whole event log - on a 37 MB copy that is
// the slow part, and re-paying it on every preview click is what made the dialog
// feel stuck. The key carries size and mtime so an edited file re-parses.
var previewFeedCache = struct {
	sync.Mutex
	entries map[string][]agent.RecoveryChainPreviewMessage
	order   []string
}{entries: map[string][]agent.RecoveryChainPreviewMessage{}}

func previewFeedCacheKey(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return path + "|?"
	}
	return fmt.Sprintf("%s|%d|%d", path, info.Size(), info.ModTime().UnixNano())
}

func previewFeedCached(key string) []agent.RecoveryChainPreviewMessage {
	previewFeedCache.Lock()
	defer previewFeedCache.Unlock()
	return previewFeedCache.entries[key]
}

func previewFeedStore(key string, feed []agent.RecoveryChainPreviewMessage) {
	previewFeedCache.Lock()
	defer previewFeedCache.Unlock()
	if _, ok := previewFeedCache.entries[key]; !ok {
		previewFeedCache.order = append(previewFeedCache.order, key)
		for len(previewFeedCache.order) > 4 {
			oldest := previewFeedCache.order[0]
			previewFeedCache.order = previewFeedCache.order[1:]
			delete(previewFeedCache.entries, oldest)
		}
	}
	previewFeedCache.entries[key] = feed
}

// PreviewRecoveryChainMessages returns the trailing slice of one chain's
// messages for the conversation-style preview dialog. Read-only, tolerant
// loader: unnormalized copies preview the same way the summary showed them.
//
// mainPath is passed through to the agent layer unchanged - the summary preview
// above documents why anchoring on the active tab's directory broke previews
// from any other tab. RecoveryChainPreviewMessagesFor re-verifies chain
// membership before reading.
func (a *App) PreviewRecoveryChainMessages(mainPath string, chainPath string, limit int) ([]agent.RecoveryChainPreviewMessage, error) {
	mainPath = strings.TrimSpace(mainPath)
	if mainPath == "" {
		return nil, fmt.Errorf("no session path given")
	}
	cacheKey := previewFeedCacheKey(chainPath)
	if cached := previewFeedCached(cacheKey); cached != nil {
		return cached, nil
	}
	feed, err := agent.RecoveryChainPreviewMessagesFor(mainPath, chainPath, limit)
	if err != nil {
		return nil, err
	}
	previewFeedStore(cacheKey, feed)
	return feed, nil
}
