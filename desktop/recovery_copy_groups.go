package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/store"
)

// RecoveryCopyView describes one recovery copy sitting beside a conversation.
//
// Listing and measuring are deliberately separate. Measuring means reading and
// comparing whole transcripts, and one copy on this machine is 128 MB, so doing it
// while building the list leaves the panel stuck on a spinner. The list therefore
// carries only what a directory read already knows - name, size, mtime - and the
// counts arrive when the user asks for them.
type RecoveryCopyView struct {
	Path string `json:"path"`
	// Label is what the user calls this conversation. The filename is an internal
	// id they never see, so the topic title from the meta sidecar is preferred and
	// the filename is only the fallback.
	Label string `json:"label"`
	// Bytes and Modified come from the directory entry, so they cost nothing.
	Bytes    int64  `json:"bytes"`
	Modified string `json:"modified"`
	// Messages, Shared and Unique are only meaningful once Scanned is true.
	// Shared and Unique are the numbers a user needs before merging: a copy that
	// differs only in its opening system prompt is duplication, while one holding
	// a few hundred events of its own is work that would be lost by ignoring it.
	Messages int  `json:"messages"`
	Shared   int  `json:"shared"`
	Unique   int  `json:"unique"`
	Scanned  bool `json:"scanned"`
	// Turns is the copy's user-turn count, measured in the same scan that fills
	// Messages (tl1415 点10: the panel's turns column showed "—" for every copy
	// because the scan never counted turns; the same snapshot replay already
	// walks every message, so the count rides along for free). Zero on an
	// unscanned copy is "not measured", not "no turns" — the panel says so.
	Turns int `json:"turns"`
	// Orphan marks a copy whose canonical transcript is not on disk under the
	// expected name. It is another line rather than a fork of this conversation,
	// so the UI has to say so instead of offering a merge that cannot work.
	Orphan bool `json:"orphan,omitempty"`
}

// RecoveryCopyGroupView is one conversation with recovery copies beside it.
type RecoveryCopyGroupView struct {
	MainPath  string `json:"mainPath"`
	MainLabel string `json:"mainLabel"`
	Directory string `json:"directory"`
	// MainExists reports whether the canonical transcript is on disk. A group whose
	// main is gone can only be described, not merged.
	MainExists bool `json:"mainExists"`
	// MainMessages is the canonical transcript's length, filled by scanning.
	MainMessages int `json:"mainMessages"`
	// MainTurns is the canonical transcript's user-turn count, measured in the
	// same scan (tl1415 点10: the summary row's turns column used to render the
	// message count under a turns header — the two readings agree only by
	// accident. With MainTurns measured the column finally shows what its
	// header says).
	MainTurns int `json:"mainTurns"`
	// The main's own size and mtime, so the table can show it on the same row the
	// copies are read against rather than leaving that line blank.
	MainBytes    int64              `json:"mainBytes"`
	MainModified string             `json:"mainModified"`
	Copies       []RecoveryCopyView `json:"copies"`
}

// ListRecoveryCopyGroups lists conversations that have recovery copies beside them.
//
// It scans the disk rather than asking the catalog on purpose. The point of the
// panel is reclaiming space, so it has to see what is actually on disk - including
// copies the catalog never indexed and copies whose canonical transcript is gone,
// which is precisely the state a cleanup has to be able to describe.
//
// This stays cheap: it reads names, sizes and the small meta sidecars only, never
// transcripts.
func (a *App) ListRecoveryCopyGroups() ([]RecoveryCopyGroupView, error) {
	groups := []RecoveryCopyGroupView{}
	for _, dir := range recoveryScanDirectories() {
		found, err := recoveryCopyGroupsIn(dir)
		if err != nil {
			// A directory that cannot be read must not fail the whole panel: the
			// other projects still have something to show.
			continue
		}
		groups = append(groups, found...)
	}
	sort.Slice(groups, func(i, j int) bool {
		if len(groups[i].Copies) != len(groups[j].Copies) {
			return len(groups[i].Copies) > len(groups[j].Copies)
		}
		return groups[i].MainLabel < groups[j].MainLabel
	})
	return groups, nil
}

// ScanRecoveryCopyGroup measures one conversation's copies: how many messages each
// holds and how much of that the canonical transcript already covers.
//
// Kept out of the listing because it opens every transcript, which is slow enough
// to be worth asking for.
func (a *App) ScanRecoveryCopyGroup(mainPath string) (RecoveryCopyGroupView, error) {
	dir := filepath.Dir(mainPath)
	if group, ok := recoveryCopyGroupFor(dir, mainPath); ok {
		return group, nil
	}
	// The copy may live beside a transcript under a different name than first
	// assumed; fall back to reporting the group as it currently reads.
	return RecoveryCopyGroupView{
		MainPath:  mainPath,
		MainLabel: recoveryDisplayName(mainPath),
		Directory: dir,
		Copies:    []RecoveryCopyView{},
	}, nil
}

// recoveryScanDirectories lists the session directories that can hold copies: the
// shared one plus one per project. A missing parent is not an error.
func recoveryScanDirectories() []string {
	seen := map[string]struct{}{}
	out := []string{}
	add := func(dir string) {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			return
		}
		if _, ok := seen[dir]; ok {
			return
		}
		seen[dir] = struct{}{}
		out = append(out, dir)
	}
	add(config.SessionDir())
	if root := strings.TrimSpace(config.MemoryUserDir()); root != "" {
		entries, err := os.ReadDir(filepath.Join(root, "projects"))
		if err == nil {
			for _, entry := range entries {
				if entry.IsDir() {
					add(filepath.Join(root, "projects", entry.Name(), "sessions"))
				}
			}
		}
	}
	return out
}

// recoveryCopiesByMain groups one directory's recovery copies under their
// canonical transcripts, keeping only names, sizes and labels.
func recoveryCopiesByMain(dir string) (map[string][]RecoveryCopyView, []string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, err
	}
	// tl1793: one titles read per directory — the display-name chain consults
	// the session-titles overrides, and reading that file per copy would
	// multiply the panel's listing cost by the copy count.
	titles := loadSessionTitles(dir)
	byMain := map[string][]RecoveryCopyView{}
	order := []string{}
	for _, entry := range entries {
		if entry.IsDir() || !store.IsSessionTranscriptName(entry.Name()) {
			continue
		}
		full := filepath.Join(dir, entry.Name())
		if !agent.LooksLikeRecoveryFilename(full) {
			continue
		}
		parentID, ok := agent.RecoveryFilenameParentID(full)
		if !ok || strings.TrimSpace(parentID) == "" {
			continue
		}
		mainPath := filepath.Join(dir, parentID+".jsonl")
		if _, seen := byMain[mainPath]; !seen {
			order = append(order, mainPath)
		}
		view := RecoveryCopyView{Path: full, Label: recoveryDisplayNameFor(full, titles)}
		if info, err := entry.Info(); err == nil {
			view.Bytes = info.Size()
			view.Modified = info.ModTime().Format(time.RFC3339)
		}
		byMain[mainPath] = append(byMain[mainPath], view)
	}
	return byMain, order, nil
}

// recoveryCopyGroupsIn lists one directory's groups without opening transcripts.
func recoveryCopyGroupsIn(dir string) ([]RecoveryCopyGroupView, error) {
	byMain, order, err := recoveryCopiesByMain(dir)
	if err != nil {
		return nil, err
	}
	// tl1793: same single titles read per directory as the copies above.
	titles := loadSessionTitles(dir)
	out := []RecoveryCopyGroupView{}
	for _, mainPath := range order {
		group := RecoveryCopyGroupView{
			MainPath:  mainPath,
			MainLabel: recoveryDisplayNameFor(mainPath, titles),
			Directory: dir,
			Copies:    byMain[mainPath],
		}
		if info, err := os.Stat(mainPath); err == nil {
			group.MainExists = true
			group.MainBytes = info.Size()
			group.MainModified = info.ModTime().Format(time.RFC3339)
		}
		// tl1793r3 组行反向继承: a mainless group's label chain bottoms out at
		// the stem (the main's sidecars are keyed by a gone file), so the
		// header showed the raw session id while its copies carried the real
		// title. Let the group borrow a copy's resolved title — only when the
		// main is missing, only from a real title (never a copy's stem).
		if !group.MainExists {
			if title := recoveryGroupLabelFromCopies(group.Copies, titles); title != "" {
				group.MainLabel = title
			}
		}
		out = append(out, group)
	}
	return out, nil
}

// recoveryCopyGroupFor measures one group: message counts plus how much of each
// copy the canonical transcript already holds.
func recoveryCopyGroupFor(dir, mainPath string) (RecoveryCopyGroupView, bool) {
	byMain, _, err := recoveryCopiesByMain(dir)
	if err != nil {
		return RecoveryCopyGroupView{}, false
	}
	copies, ok := byMain[mainPath]
	if !ok {
		return RecoveryCopyGroupView{}, false
	}
	titles := loadSessionTitles(dir)
	group := RecoveryCopyGroupView{
		MainPath:  mainPath,
		MainLabel: recoveryDisplayNameFor(mainPath, titles),
		Directory: dir,
		Copies:    make([]RecoveryCopyView, 0, len(copies)),
	}
	if info, err := os.Stat(mainPath); err == nil {
		group.MainExists = true
		group.MainBytes = info.Size()
		group.MainModified = info.ModTime().Format(time.RFC3339)
	}
	// The canonical transcript is measured too: without its length there is no
	// baseline to read the copies against, which is the whole point of scanning.
	// Turns ride the same replay (tl1415 点10): PreviewTurns counts user turns
	// while the snapshot is already in memory, so the turns column stops being a
	// per-column guess.
	if snapshot, ok := agent.LoadSessionContentSnapshot(mainPath); ok {
		group.MainMessages = snapshot.Len()
		_, group.MainTurns = snapshot.PreviewTurns()
	} else if count, turns, _, tok := agent.TolerantChainReadings(mainPath); tok {
		// Strict load refused (damaged/unnormalized): the tolerant loader still
		// counts, so the row shows real numbers instead of a misleading 0.
		group.MainMessages = count
		group.MainTurns = turns
	} else {
		group.MainMessages = agent.TolerantMessageCount(mainPath)
	}
	for _, copyView := range copies {
		copyView.Scanned = true
		if snapshot, ok := agent.LoadSessionContentSnapshot(copyView.Path); ok {
			copyView.Messages = snapshot.Len()
			_, copyView.Turns = snapshot.PreviewTurns()
		} else if count, turns, _, tok := agent.TolerantChainReadings(copyView.Path); tok {
			copyView.Messages = count
			copyView.Turns = turns
		} else {
			copyView.Messages = agent.TolerantMessageCount(copyView.Path)
		}
		if overlap, ok := agent.SessionContentOverlap(mainPath, copyView.Path); ok {
			copyView.Shared = overlap.Shared
			copyView.Unique = overlap.Unique
		} else {
			// No canonical transcript to compare against: the copy stands alone.
			copyView.Orphan = true
			copyView.Unique = copyView.Messages
		}
		group.Copies = append(group.Copies, copyView)
	}
	// tl1793r3 组行反向继承 (scan path): the scan response replaces the group
	// view in the panel, so the same reverse inheritance as the listing must
	// hold here too — otherwise the header flips back to the raw stem the
	// moment the user presses Scan on a mainless group.
	if !group.MainExists {
		if title := recoveryGroupLabelFromCopies(group.Copies, titles); title != "" {
			group.MainLabel = title
		}
	}
	return group, true
}

// recoveryDisplayName resolves the name the user knows this conversation by,
// walking the same title chain the sidebar and the trash listing render:
// the meta sidecar's custom_title (user rename) then topic_title (auto title),
// then the directory's session-titles overrides, and only then the file stem.
// tl1793: the panel used to read topic_title alone, so a conversation whose
// name lived in custom_title (user rename) or the titles sidecar fell through
// to the raw session-id stem — the internal string the user reported seeing on
// four panel rows while the trash listing rendered proper Chinese titles for
// the same files (the listing chain reads custom_title first).
func recoveryDisplayName(transcriptPath string) string {
	return recoveryDisplayNameFor(transcriptPath, nil)
}

// recoveryInheritDepthCap bounds the tl1793r2 inheritance walk. Nested copies
// are rare (stableRecoverySessionPath roots every lane at the peeled root
// stem), so 4 hops is far above the real depth; the cap only closes cycles and
// pathological chains.
const recoveryInheritDepthCap = 4

// recoveryDisplayNameFor is recoveryDisplayName with a caller-supplied
// session-titles map. The group scans read one directory's titles once and
// pass it down (a hundreds-of-conversations titles file read per row would
// multiply the panel's open cost); single-path callers pass nil and the map
// is loaded here.
func recoveryDisplayNameFor(transcriptPath string, titles map[string]string) string {
	return recoveryDisplayNameInherited(transcriptPath, titles, 0)
}

// recoveryDisplayNameInherited is the depth-carrying form of the display chain.
// tl1793r2 adds two inherited sources below the phase-1 chain (sidecar
// custom_title → topic_title → titles sidecar) and above the stem fallback:
// the copy's own meta.topic_id resolved against the topic registry (the same
// titles the sidebar and the trash family row render), then the original
// conversation found by the copy stem's prefix. The user's 20261125 scene is
// exactly the state these two cover: a restored copy whose every phase-1
// source is empty (its creation predated title inheritance), while the trash
// family rendering shows the Chinese title by resolving the family to the
// main conversation. Both lookups run only on stuck rows — titled rows never
// reach here — so the panel's steady-state cost is unchanged.
func recoveryDisplayNameInherited(transcriptPath string, titles map[string]string, depth int) string {
	if title := recoveryResolvedTitle(transcriptPath, titles, depth); title != "" {
		return title
	}
	return recoveryLabel(transcriptPath)
}

// recoveryResolvedTitle is the display chain without the stem fallback: the
// empty string when every title source is exhausted. tl1793r3 splits it out of
// recoveryDisplayNameInherited so a group row can run its copies through the
// same chain and inherit a real title (组行反向继承): when the canonical
// transcript is gone, every source for the main path reads empty — the sidecars
// and the titles map are keyed by a file that no longer exists — and MainLabel
// used to bottom out at the raw stem right beside copies that resolve fine.
// The stem stays the row-level last resort; only the group header borrows.
func recoveryResolvedTitle(transcriptPath string, titles map[string]string, depth int) string {
	if title := recoverySidecarTitle(transcriptPath); title != "" {
		return title
	}
	if title, ok := titles[filepath.Base(transcriptPath)]; ok && strings.TrimSpace(title) != "" {
		return strings.TrimSpace(title)
	}
	if title, ok := loadSessionTitles(filepath.Dir(transcriptPath))[filepath.Base(transcriptPath)]; ok && strings.TrimSpace(title) != "" {
		return strings.TrimSpace(title)
	}
	return recoveryInheritedTitle(transcriptPath, depth)
}

// recoveryGroupLabelFromCopies is the group row's reverse inheritance: the
// first copy whose own chain resolves a real title (sidecar or inherited —
// never its stem) lends that title to the group header. Used only when the
// canonical transcript is missing, so titled groups never pay the extra
// sidecar reads and a main's own name always outranks its copies'. The copies
// sit in directory order, so the pick is deterministic.
func recoveryGroupLabelFromCopies(copies []RecoveryCopyView, titles map[string]string) string {
	for _, copy := range copies {
		if title := recoveryResolvedTitle(copy.Path, titles, 0); title != "" {
			return title
		}
	}
	return ""
}

// recoveryInheritedTitle resolves a titleless transcript's name from the
// conversation it belongs to. Two routes, both reusing existing truth sources
// rather than minting a second registry:
//
//  1. Registry: the meta sidecar's topic_id — recovery copies inherit the
//     parent conversation's topic id at creation (tabSessionRecoveryMeta
//     carries tab.TopicID) — looked up in the topic registry, the same map
//     the sidebar renders. This works even while the original file itself is
//     still in the trash (the 20261125 scene: registry row topic_20261104-*
//     holds the Chinese title, the copy's legacy_ row holds none).
//  2. Stem prefix: a recovery filename is the original stem plus a
//     -recovery-<16 hex> suffix, so the peeled parent id names the original
//     transcript; when that file is absent (still trashed), the directory is
//     prefix-scanned for it (long legacy stems were truncated+hashed at copy
//     time, so exact-name lookup is not always possible).
//
// Empty return means nothing resolvable — caller falls back to the stem.
func recoveryInheritedTitle(transcriptPath string, depth int) string {
	if depth >= recoveryInheritDepthCap {
		return ""
	}
	dir := filepath.Dir(transcriptPath)
	if meta, ok, err := agent.LoadBranchMeta(transcriptPath); err == nil && ok {
		if topicID := strings.TrimSpace(meta.TopicID); topicID != "" {
			if _, _, topicRoot, ok := legacyMigrationTargetForDir(dir); ok {
				if title := strings.TrimSpace(loadTopicTitles(topicRoot)[topicID]); title != "" {
					return title
				}
			}
		}
	}
	parentID, ok := agent.RecoveryFilenameParentID(transcriptPath)
	if !ok || strings.TrimSpace(parentID) == "" {
		return ""
	}
	if parentPath := recoveryStemMatchIn(dir, parentID, transcriptPath); parentPath != "" {
		return recoveryDisplayNameInherited(parentPath, nil, depth+1)
	}
	return ""
}

// recoveryParentStemTruncatedAt mirrors the agent-side copy-stem truncation
// point (maxRecoveryParentStemBytes, unexported there): stems longer than this
// were hashed into the copy name, so the peeled parent id carries a 12-hex
// digest segment in place of the original tail.
const recoveryParentStemTruncatedAt = 80

// recoveryStemMatchIn finds the original transcript for a copy stem: the exact
// <stem>.jsonl when it exists, otherwise the directory's best prefix match.
// Candidates are live session transcripts that are not recovery-shaped (those
// are siblings, not the original) and not the excluded path itself. The
// shortest matching stem wins — closest to the copy's prefix — ties break
// lexicographically, so the pick is deterministic. For truncated (>80-byte)
// parent stems the digest segment is stripped and the shorter prefix is tried
// only when the full parent id matched nothing, so the hash-shaped-but-real
// stems common in this codebase (timestamp-hex ids) are never mis-stripped
// first.
func recoveryStemMatchIn(dir, stem, excludePath string) string {
	exact := filepath.Join(dir, stem+".jsonl")
	if _, err := os.Stat(exact); err == nil {
		return exact
	}
	if match := recoveryStemPrefixScanIn(dir, stem, excludePath); match != "" {
		return match
	}
	if trimmed, ok := trimTruncatedStemHash(stem); ok {
		return recoveryStemPrefixScanIn(dir, trimmed, excludePath)
	}
	return ""
}

// recoveryStemPrefixScanIn is one deterministic pass over dir's live session
// transcripts for stems carrying the given prefix.
func recoveryStemPrefixScanIn(dir, prefix, excludePath string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	best := ""
	bestStem := ""
	for _, entry := range entries {
		if entry.IsDir() || !store.IsSessionTranscriptName(entry.Name()) {
			continue
		}
		full := filepath.Join(dir, entry.Name())
		if sameDesktopPath(full, excludePath) || agent.LooksLikeRecoveryFilename(full) {
			continue
		}
		id := agent.BranchID(full)
		if !strings.HasPrefix(id, prefix) {
			continue
		}
		if best == "" || len(id) < len(bestStem) || (len(id) == len(bestStem) && id < bestStem) {
			best = full
			bestStem = id
		}
	}
	return best
}

// trimTruncatedStemHash recognizes the truncated copy-parent shape
// (<first 80 bytes>-<12 lower hex>) and returns the matchable prefix. The
// length gate keeps ordinary short stems ending in a hex segment untouched.
func trimTruncatedStemHash(stem string) (string, bool) {
	if len(stem) <= recoveryParentStemTruncatedAt {
		return stem, false
	}
	i := strings.LastIndexByte(stem, '-')
	if i <= 0 || len(stem)-i-1 != 12 {
		return stem, false
	}
	for _, r := range stem[i+1:] {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return stem, false
		}
	}
	return stem[:i], true
}

// recoverySidecarTitle reads the branch meta sidecar's two title fields in the
// sidebar's own precedence: custom_title first, then topic_title. Missing or
// unreadable sidecar is the ordinary fallback case and returns "".
func recoverySidecarTitle(transcriptPath string) string {
	if metaPath := store.SessionMeta(transcriptPath); metaPath != "" {
		if b, err := os.ReadFile(metaPath); err == nil {
			var meta struct {
				TopicTitle  string `json:"topic_title"`
				CustomTitle string `json:"custom_title"`
			}
			if json.Unmarshal(b, &meta) == nil {
				if title := strings.TrimSpace(meta.CustomTitle); title != "" {
					return title
				}
				if title := strings.TrimSpace(meta.TopicTitle); title != "" {
					return title
				}
			}
		}
	}
	return ""
}

// recoveryLabel renders a transcript path as the session id, dropping the
// directory and the extension. Used when no topic title is available.
func recoveryLabel(path string) string {
	name := filepath.Base(path)
	return strings.TrimSuffix(name, ".jsonl")
}
