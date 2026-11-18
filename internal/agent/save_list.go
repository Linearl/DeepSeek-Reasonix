package agent

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"reasonix/internal/store"
)

// 会话列举域（tl1633 自 save.go 纯移动拆出）：SessionOrder/SessionInfo
// 列举投影（含 meta 缓存）。

// SessionInfo summarises a saved session for the --resume picker: where it is on
// disk, when it was created/last active, the first user message as a preview, and
// a rough turn count.
type SessionInfo struct {
	Path           string
	CreatedAt      time.Time
	LastActivityAt time.Time
	ModTime        time.Time // compatibility alias for LastActivityAt
	Preview        string
	Turns          int
	// RecentExcerpts carry the newest noise-filtered user messages (newest
	// first, tl 1177) for the session wall's hover overlay. Like Preview they
	// are a listing projection — stale/empty on sessions not yet re-projected.
	RecentExcerpts       []string
	CountsKnown          bool
	Scope                string
	WorkspaceRoot        string
	TopicID              string
	TopicTitle           string
	CustomTitle          string
	Recovered            bool
	RecoveryReason       string
	RecoveryDigest       string
	ParentID             string
	VersionKind          SessionVersionKind
	VersionState         SessionVersionState
	ParentConversationID string
	ParentVersionID      string
}

// sessionOrderMetaCache memoizes the parsed branch meta for one session sidecar.
//
// Directory reconciliation runs on tab switches and conversation-head queries, and
// it reads a branch-meta sidecar for every file the directory lists. Once recovery
// copies accumulate that directory is not small - a profile of this machine counted
// 510 files, 246 of them recovery copies - so unchanged files paid a full
// read-and-parse on every sweep.
//
// Two properties keep this safe rather than merely fast:
//
//   - The key is the *sidecar's* size and mtime, not the transcript's. LoadBranchMeta
//     reads BranchMetaPath(sessionPath), so keying on the transcript would miss a
//     sidecar rewrite that left the transcript untouched.
//   - Visibility does not go through it. IsCleanupPending only stats its own
//     sidecar, and hiding or clearing a session must take effect on the very next
//     listing - caching that decision is what made an earlier attempt at this fail
//     TestListSessionsSkipsCleanupPending.
var sessionOrderMetaCache sync.Map // metaPath -> sessionOrderMetaEntry

type sessionOrderMetaEntry struct {
	size  int64
	mtime time.Time
	meta  BranchMeta
	ok    bool
	err   error
}

// loadBranchMetaCached is LoadBranchMeta with the parse reused while the sidecar's
// size and mtime are unchanged. A missing sidecar is not cached: LoadBranchMeta
// already reports it cheaply as absent.
func loadBranchMetaCached(sessionPath string) (BranchMeta, bool, error) {
	metaPath := BranchMetaPath(sessionPath)
	if metaPath == "" {
		return BranchMeta{}, false, nil
	}
	info, statErr := os.Stat(metaPath)
	if statErr != nil {
		return LoadBranchMeta(sessionPath)
	}
	if raw, ok := sessionOrderMetaCache.Load(metaPath); ok {
		if entry, ok := raw.(sessionOrderMetaEntry); ok && entry.size == info.Size() && entry.mtime.Equal(info.ModTime()) {
			return entry.meta, entry.ok, entry.err
		}
	}
	meta, ok, err := LoadBranchMeta(sessionPath)
	sessionOrderMetaCache.Store(metaPath, sessionOrderMetaEntry{
		size:  info.Size(),
		mtime: info.ModTime(),
		meta:  meta,
		ok:    ok,
		err:   err,
	})
	return meta, ok, err
}

// ListSessionOrder returns every *.jsonl session under dir in the same
// most-recently-active order used by ListSessions, using only file metadata and
// branch sidecars. A missing directory is not an error.
func ListSessionOrder(dir string) ([]SessionOrderInfo, error) {
	return ListSessionOrderWithRecoveryPreferenceResolver(dir, RecoveryPreferenceCurrent)
}

// ListSessionOrderWithRecoveryPreferenceResolver lets catalog reconciliation
// reuse its wave-local transcript snapshot when validating explicit choices.
// Other callers keep the ordinary ListSessionOrder behavior above.
func ListSessionOrderWithRecoveryPreferenceResolver(dir string, resolve RecoveryPreferenceResolver) ([]SessionOrderInfo, error) {
	if resolve == nil {
		resolve = RecoveryPreferenceCurrent
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []SessionOrderInfo
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		// tl 1174：归档态主转录（<id>.jsonl.zst）照常被发现——发现锚从
		// 「明文名」扩展为「明文或归档名」。Path 归一为明文名：load/meta/
		// lease/删除全部按明文名工作（首次打开时冷读透明物化）。明文在
		// （并存残件态）时跳过归档——明文优先（1172 同款事实源规则）。
		if store.IsSessionTranscriptZstName(name) {
			plain := store.SessionTranscriptPlainName(name)
			if _, err := os.Stat(filepath.Join(dir, plain)); err == nil {
				continue
			}
			name = plain
		} else if !store.IsSessionTranscriptName(name) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		full := filepath.Join(dir, name)
		if !IsVisibleSession(full) {
			continue
		}
		contentMod := SessionContentModTime(full)
		if contentMod.IsZero() {
			contentMod = info.ModTime()
		}
		createdAt := info.ModTime()
		lastActivityAt := contentMod
		scope := "global"
		workspaceRoot := ""
		topicID := ""
		topicTitle := ""
		customTitle := ""
		recovered := false
		recoveryReason := ""
		recoveryDigest := ""
		parentID := ""
		versionKind := VersionNormal
		versionState := VersionActive
		parentConversationID := ""
		var headMirror BranchMeta
		parentVersionID := ""
		recoveryPreferred := false
		turns := 0
		preview := ""
		var recentExcerpts []string
		schemaVersion := 0
		revision := int64(0)
		contentDigest := ""
		listingRevision := int64(0)
		listingContentDigest := ""
		if meta, ok, err := loadBranchMetaCached(full); err == nil && ok {
			if !meta.CreatedAt.IsZero() {
				createdAt = meta.CreatedAt
			}
			if !meta.UpdatedAt.IsZero() {
				lastActivityAt = meta.UpdatedAt
			}
			scope = meta.DefaultScope()
			workspaceRoot = meta.WorkspaceRoot
			topicID = meta.TopicID
			topicTitle = meta.TopicTitle
			customTitle = meta.CustomTitle
			recovered = meta.Recovered
			recoveryReason = meta.RecoveryReason
			recoveryDigest = meta.RecoveryDigest
			parentID = meta.ParentID
			versionKind = meta.EffectiveVersionKind()
			versionState = meta.EffectiveVersionState()
			parentConversationID = meta.ParentConversationID
			parentVersionID = meta.ParentVersionID
			recoveryPreferred = resolve(full, meta)
			turns = meta.Turns
			preview = meta.Preview
			recentExcerpts = meta.RecentExcerpts
			schemaVersion = meta.SchemaVersion
			revision = meta.Revision
			contentDigest = meta.ContentDigest
			listingRevision = meta.ListingRevision
			listingContentDigest = meta.ListingContentDigest
			headMirror = meta
		}
		// Old recovery files may lack Recovered meta; filename still proves
		// automatic recovery lineage for catalog folding.
		if !recovered && LooksLikeRecoveryFilename(full) {
			recovered = true
			versionKind = VersionRecovery
			if parentID == "" {
				if parent, ok := RecoveryFilenameParentID(full); ok {
					parentID = parent
				}
			}
		}
		out = append(out, SessionOrderInfo{
			Path:                 full,
			CreatedAt:            createdAt,
			LastActivityAt:       lastActivityAt,
			ModTime:              lastActivityAt,
			Scope:                scope,
			WorkspaceRoot:        workspaceRoot,
			TopicID:              topicID,
			TopicTitle:           topicTitle,
			CustomTitle:          customTitle,
			Recovered:            recovered,
			RecoveryReason:       recoveryReason,
			RecoveryDigest:       recoveryDigest,
			ParentID:             parentID,
			VersionKind:          versionKind,
			VersionState:         versionState,
			ParentConversationID: parentConversationID,
			ParentVersionID:      parentVersionID,
			RecoveryPreferred:    recoveryPreferred,
			Turns:                turns,
			Preview:              preview,
			RecentExcerpts:       recentExcerpts,
			SchemaVersion:        schemaVersion,
			Revision:             revision,
			ContentDigest:        contentDigest,
			ListingRevision:      listingRevision,
			ListingContentDigest: listingContentDigest,
			HeadID:               headMirror.HeadID,
			HeadCount:            headMirror.HeadCount,
			LogSchema:            headMirror.LogSchema,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].LastActivityAt.Equal(out[j].LastActivityAt) {
			return out[i].Path < out[j].Path
		}
		return out[i].LastActivityAt.After(out[j].LastActivityAt)
	})
	return out, nil
}

// ListSessions returns every non-empty *.jsonl session under dir,
// most-recently-active first, each with a preview line so the picker can show
// something the user recognises. It never decodes a transcript: legacy counts
// remain explicitly unknown until the session catalog's single repair worker
// validates them. A missing directory is not an error.
func ListSessions(dir string) ([]SessionInfo, error) {
	ordered, err := ListSessionOrder(dir)
	if err != nil {
		return nil, err
	}
	return sessionsFromOrder(ordered), nil
}

// sessionsFromOrder applies the shared listing projection: the single source
// of the drop rules (stale projection without content → dropped; stale
// projection with content → counts unknown; fresh projection with zero user
// turns → dropped) and the "History is being indexed" placeholder preview.
// ListSessions and ListSessionsMetadataOnly both route through here so the
// two read modes can never drift apart (tl1474).
func sessionsFromOrder(ordered []SessionOrderInfo) []SessionInfo {
	var out []SessionInfo
	for _, session := range ordered {
		preview, turns := session.Preview, session.Turns
		if !sessionListingProjectionFresh(session.SchemaVersion, turns, session.Revision, session.ListingRevision, session.ContentDigest, session.ListingContentDigest) {
			if !sessionArtifactsHaveContent(session.Path) {
				continue
			}
			if strings.TrimSpace(preview) == "" {
				preview = "History is being indexed — " + filepath.Base(session.Path)
			}
			out = append(out, sessionInfoFromOrder(session, preview, turns, false))
			continue
		}
		if turns == 0 {
			// Never had user interaction — an empty conversation that should not
			// appear in the history panel or the resume picker.
			continue
		}
		out = append(out, sessionInfoFromOrder(session, preview, turns, true))
	}
	return out
}

// ListSessionsMetadataOnly is ListSessions with the recovery-preference proof
// skipped: it never replays a transcript, so a sidecar carrying
// RecoveryPreferred + digest cannot trigger a full LoadSession + digest cycle
// per record (tl1474: the trash listing paid that per recovery bucket, in the
// hundreds of milliseconds each on large transcripts). The listing projection
// (drop rules, ordering, preview, counts identity) is the exact shared
// sessionsFromOrder, so every display field matches ListSessions byte for
// byte; only SessionInfo.RecoveryPreferred differs — it stays false because
// the proof never ran. Callers must therefore not use this variant's
// RecoveryPreferred for lineage decisions; the trash cards (its first
// consumer) render title/preview/times only and classify recovery copies via
// the sidecar fields + filename shape.
func ListSessionsMetadataOnly(dir string) ([]SessionInfo, error) {
	ordered, err := ListSessionOrderWithRecoveryPreferenceResolver(dir, func(sessionPath string, meta BranchMeta) bool {
		return false // proof skipped by contract; see doc comment
	})
	if err != nil {
		return nil, err
	}
	return sessionsFromOrder(ordered), nil
}
