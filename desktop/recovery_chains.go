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
}

// RecoveryChainSet is the answer to "which chains exist for this conversation",
// with the longest one marked so the panel can recommend it.
type RecoveryChainSet struct {
	MainPath string              `json:"mainPath"`
	MainLabel string             `json:"mainLabel"`
	Chains   []RecoveryChainView `json:"chains"`
	// Longest is the path+head of the chain with the most messages. Ties prefer
	// the main transcript, then the most recent activity, so the recommendation
	// is stable rather than dependent on directory order.
	LongestPath string `json:"longestPath"`
	LongestHead string `json:"longestHead"`
}

// ListRecoveryChains enumerates every candidate chain for one conversation: the
// main transcript plus each recovery copy, and for each of those every live head.
//
// This is a read-only inventory. It replays each log once, which is why the panel
// asks for it when the user opens a conversation's detail rather than for the whole
// list at once.
func (a *App) ListRecoveryChains(mainPath string) (RecoveryChainSet, error) {
	mainPath = strings.TrimSpace(mainPath)
	set := RecoveryChainSet{MainPath: mainPath}
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
		heads, err := agent.ListSessionHeads(path)
		if err != nil {
			// A copy that cannot be replayed is reported as an empty chain rather
			// than failing the whole inventory: the other candidates are still
			// usable, and the panel shows which one is unreadable.
			continue
		}
		info, statErr := os.Stat(path)
		var bytes int64
		if statErr == nil {
			bytes = info.Size()
		}
		isMain := samePath(path, mainPath)
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
	if len(set.Chains) > 0 {
		set.LongestPath = set.Chains[0].Path
		set.LongestHead = set.Chains[0].HeadID
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
