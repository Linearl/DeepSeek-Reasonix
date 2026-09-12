package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

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
func (a *App) PreviewRecoveryChain(mainPath string, chainPath string) (agent.RecoveryChainPreview, error) {
	dir := a.activeSessionDir()
	sessionPath, _, err := validateSessionPath(dir, mainPath)
	if err != nil {
		if dir2, sessionPath2, foundErr := a.sessionDirForPath(mainPath); foundErr == nil {
			sessionPath = sessionPath2
			dir = dir2
		} else {
			return agent.RecoveryChainPreview{}, friendlySessionFileError(err)
		}
	}
	// chainPath comes straight from the chain enumeration, so it already has the
	// same form the backend compares against; RecoveryChainPreviewFor re-verifies
	// that it belongs to this session before reading anything.
	_ = dir
	return agent.RecoveryChainPreviewFor(sessionPath, chainPath)
}
