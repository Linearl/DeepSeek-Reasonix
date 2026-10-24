package main

import (
	"errors"
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
// A session log is a DAG, and every head is the tip of one chain back to the
// root - chainIDs walks node.parent from the head's leaf, so a head *is* a chain.
// Copies of a session are separate files that forked from the main one, and each
// carries the whole history it forked from, so a candidate chain is always
// complete on its own: choosing one needs no splicing across files.
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
	// Degraded marks a chain listed without a full replay (任务 1019): the
	// events log exceeded the panel replay budget, so the row carries the file
	// identity only and every count stays zero rather than the panel hanging
	// the settings page on the replay.
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

	for _, path := range candidates {
		// 任务 1019：面板路径走限流缓存入口——命中零重放；超预算的病态大
		// 日志降级为一行 Degraded 标记而不是把设置页挂在全量重放上。
		info, statErr := os.Stat(path)
		var bytes int64
		if statErr == nil {
			bytes = info.Size()
		}
		isMain := samePath(path, mainPath)
		heads, degraded, err := panelSessionHeads(path)
		if err != nil {
			// A copy that cannot be replayed is reported as an empty chain rather
			// than failing the whole inventory: the other candidates are still
			// usable, and the panel shows which one is unreadable.
			//
			// 任务 1039：重放超预算不再是「跳过该候选」——主转录被跳过时整个
			// 集合为空，前端弹窗只有一句「设置加载失败」。与降级行同语义：
			// 保留行、计数全零、不重放；其他候选（副本/正常会话）照旧枚举。
			if errors.Is(err, agent.ErrSessionReplayLimitExceeded) {
				set.Chains = append(set.Chains, RecoveryChainView{
					Path: path, Label: recoveryDisplayName(path), IsMain: isMain,
					Bytes: bytes, Degraded: true,
				})
				continue
			}
			continue
		}
		if degraded {
			set.Chains = append(set.Chains, RecoveryChainView{
				Path: path, Label: recoveryDisplayName(path), IsMain: isMain,
				Bytes: bytes, Degraded: true,
			})
			continue
		}
		if len(heads) == 0 {
			// A schema-1 log has no heads. It is still a candidate chain - the
			// whole file - so it is listed with the numbers the snapshot reports
			// instead of being hidden.
			view := RecoveryChainView{
				Path: path, Label: recoveryDisplayName(path), IsMain: isMain, Bytes: bytes,
			}
			if snap, ok := agent.LoadSessionContentSnapshot(path); ok {
				view.MessageCount = snap.Len()
			}
			set.Chains = append(set.Chains, view)
			continue
		}
		for _, head := range heads {
			set.Chains = append(set.Chains, RecoveryChainView{
				Path:         path,
				HeadID:       head.ID,
				Label:        recoveryDisplayName(path),
				IsMain:       isMain,
				Selected:     head.Selected,
				Covered:      head.Covered,
				MessageCount: head.MessageCount,
				Turns:        head.Turns,
				LastActivity: head.LastActivity.UTC().Format("2006-01-02T15:04:05Z"),
				Bytes:        bytes,
				Preview:      head.Preview,
			})
		}
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
	// 任务 1019：longest（更长）与 newest（更新）都只在真正重放过的链里选——
	// 全部降级时留空，前端因此不标推荐，而不是把零读数的降级行推为赢家。
	// Chains 已按轮次优先排好序，第一个非降级的就是「更长」（longest）。
	for _, chain := range set.Chains {
		if !chain.Degraded {
			set.LongestPath = chain.Path
			set.LongestHead = chain.HeadID
			break
		}
	}
	// 任务 1123：「更新」独立于「更长」计算——LastActivity 是字符串比较
	// （RFC3339 UTC 恒定宽度，字典序即时间序），直接扫一遍取最大值即可，
	// 不需要额外排序。同样跳过降级行（零读数不是「最新」）。平手时按当前
	// 排序序（轮次优先）先出现者胜，与 longest 的平手规则一致。
	newestIndex := -1
	for i, chain := range set.Chains {
		if chain.Degraded {
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
