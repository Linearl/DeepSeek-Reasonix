package main

import (
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/sessioncatalog"
	"reasonix/internal/store"
)

// Fork forks the requested source tab into a new tab, keeping code intact,
// preserving the current tab, and switching to the new tab.
func (a *App) Fork(turn int) (TabMeta, error) {
	return a.ForkForTab("", turn)
}

// ForkForTab forks the requested source tab even if focus changes before the
// backend begins processing the request. The fork becomes active only while the
// source tab still owns focus, so a later tab selection remains authoritative.
func (a *App) ForkForTab(tabID string, turn int) (TabMeta, error) {
	result, err := a.forkForTabWithOptions(tabID, turn, false)
	return result.Tab, err
}

// ForkWorktreeForTab forks the requested source tab into an isolated Git worktree.
func (a *App) ForkWorktreeForTab(tabID string, turn int) (ForkWorktreeResultView, error) {
	return a.forkForTabWithOptions(tabID, turn, true)
}

// SummarizeFrom / SummarizeUpTo compress model context after / before the start
// of a selected turn. Visible history and checkpoints remain unchanged.
func (a *App) SummarizeFrom(turn int) error {
	return a.SummarizeFromForTab("", turn)
}

func (a *App) SummarizeFromForTab(tabID string, turn int) error {
	tab, ctrl := a.tabAndCtrlByID(tabID)
	if a.tabIsReadOnly(tab) {
		return readOnlyChannelErr()
	}
	if ctrl == nil {
		return nil
	}
	return ctrl.SummarizeFrom(a.ctx, turn)
}

func (a *App) SummarizeUpTo(turn int) error {
	return a.SummarizeUpToForTab("", turn)
}

func (a *App) SummarizeUpToForTab(tabID string, turn int) error {
	tab, ctrl := a.tabAndCtrlByID(tabID)
	if a.tabIsReadOnly(tab) {
		return readOnlyChannelErr()
	}
	if ctrl == nil {
		return nil
	}
	return ctrl.SummarizeUpTo(a.ctx, turn)
}

type channelSessionRoute struct {
	channel       string
	channelLabel  string
	remoteID      string
	chatType      string
	userID        string
	threadID      string
	sessionSource string
}

type WorkspaceMeta struct {
	Path    string `json:"path"`
	Name    string `json:"name"`
	Current bool   `json:"current"`
}

func controllerSessionDir(ctrl control.SessionAPI) string {
	if ctrl != nil {
		if dir := ctrl.SessionDir(); dir != "" {
			return dir
		}
	}
	return desktopSessionDir("")
}

func tabSessionDir(tab *WorkspaceTab) string {
	if tab != nil {
		if tab.WorkspaceRoot != "" {
			return desktopSessionDir(tab.WorkspaceRoot)
		}
		if tab.Ctrl != nil {
			if dir := tab.Ctrl.SessionDir(); dir != "" {
				return dir
			}
		}
	}
	return desktopSessionDir("")
}

func tabRuntimeSessionDir(tab *WorkspaceTab) string {
	if tab != nil && tab.Ctrl != nil {
		if dir, ok := safeControllerSessionDir(tab.Ctrl); ok && strings.TrimSpace(dir) != "" {
			if path := strings.TrimSpace(tab.currentSessionPath()); path != "" {
				if _, _, err := validateSessionPath(dir, path); err == nil {
					return dir
				}
			} else {
				return dir
			}
		}
	}
	return tabSessionDir(tab)
}

func (a *App) activeSessionDir() string {
	tab := a.activeTab()
	if path, ok := a.reconcileTabWithPinnedSessionMeta(tab); ok && strings.TrimSpace(path) != "" {
		return filepath.Dir(path)
	}
	if tab != nil && tab.Ctrl != nil {
		return tabRuntimeSessionDir(tab)
	}
	return tabSessionDir(tab)
}

// ListSessions returns the saved sessions newest-first for the history panel,
// marking the one the current conversation is writing to and attaching any
// user-chosen titles.
func (a *App) ListSessions() []SessionMeta {
	dir := a.activeSessionDir()
	return a.listSessionsFromDir(dir, a.activeSessionPath(dir))
}

// SessionTailExcerpts returns the newest noise-filtered user messages of one
// session, computed on demand from the tail of its transcript (tl-1177b 方案
// B). The session wall's hover overlay calls this per card, so sessions saved
// before any excerpts sidecar existed still answer on the first hover instead
// of falling back to the conversation name. Read-only: an unreadable or
// non-transcript path just yields no excerpts.
func (a *App) SessionTailExcerpts(sessionPath string) []string {
	sessionPath = strings.TrimSpace(sessionPath)
	if sessionPath == "" || !store.IsSessionTranscriptName(filepath.Base(sessionPath)) {
		return nil
	}
	return agent.SessionTailUserExcerpts(sessionPath, agent.RecentExcerptCount)
}

// SessionTailAgentReply returns the closing assistant reply of one session for
// the wall card's hover overlay (tl-1188 返修: excerpts alone showed only the
// user's questions). Same read-only tail scan contract as SessionTailExcerpts;
// "" means no displayable reply in the tail.
func (a *App) SessionTailAgentReply(sessionPath string) string {
	sessionPath = strings.TrimSpace(sessionPath)
	if sessionPath == "" || !store.IsSessionTranscriptName(filepath.Base(sessionPath)) {
		return ""
	}
	return agent.SessionTailAgentReply(sessionPath)
}

// ListSessionsForTab returns sessions from the directory owned by tabID. Task
// Monitor uses this stable target after asynchronous control lookups so a tab
// switch cannot redirect the eventual session lookup to another workspace.
func (a *App) ListSessionsForTab(tabID string) []SessionMeta {
	target, err := a.taskMonitorTargetForTab(tabID)
	if err != nil {
		return []SessionMeta{}
	}
	return a.listSessionsFromDir(target.sessionDir, target.sessionPath)
}

func (a *App) listSessionsFromDir(dir, active string) []SessionMeta {
	catalog := a.sessionCatalog.Load()
	if catalog == nil {
		return []SessionMeta{}
	}
	target := sessioncatalog.DirectoryTarget{Path: dir, Scope: "global"}
	for _, candidate := range a.sessionCatalogTargets() {
		if sameProjectRoot(candidate.Path, dir) {
			target = candidate
			break
		}
	}
	records, err := listCatalogSessionsForDirectory(a.bootContext(), catalog, target, dir)
	if err != nil {
		return []SessionMeta{}
	}
	open := a.openSessionPaths(dir)
	channelRoutes := channelSessionRoutesForDir(dir)
	out := make([]SessionMeta, 0, len(records))
	for _, record := range records {
		_, isOpen := open[record.Path]
		// tl1352 D：侧栏过滤 recovery 副本条目。副本以主会话的文件名前缀+继承
		// title 出现在侧栏，与 tab 标题错位（现场被用户判为对话泄露）。排除
		// 非 open 的副本条目——已打开的 tab（正写在副本上）必须保留自己的条目；
		// 副本管理走既有的 recovery-copy groups 面板。fork 副本转正（tl1352 A）
		// 落地后副本通常很快消失，这里的过滤只是侧栏的兜底净化。
		if !isOpen && record.Path != active && agent.LooksLikeRecoveryFilename(record.Path) {
			continue
		}
		meta := sessionMetaFromCatalog(record, record.Path == active, isOpen)
		if route, ok := channelRoutes[sessionRuntimeKey(record.Path)]; ok {
			applyChannelSessionRoute(&meta, route)
		}
		out = append(out, meta)
	}
	return out
}

// tl1474TrashListWorkers bounds the parallel per-bucket assembly. Bucket reads
// are small-syscall IO (dir walks, sidecar loads, transcript stats) plus the
// recovery-coverage proof on first proof per content identity — IO-bound work
// that parallelizes cleanly. 8 covers the reporting machine's trash (93
// buckets, cold open) without a goroutine per bucket on huge trash dirs; the
// per-bucket order is re-derived by the deterministic sort below, so assembly
// order never leaks into the output.
const tl1474TrashListWorkers = 8

// ListTrashedSessions returns sessions that were moved to the local trash,
// newest-deleted first. These can be previewed, restored, or permanently purged.
//
// tl1474 load chain: the old shape walked the listing sequentially and paid
// per bucket a full agent.ListSessions (whose recovery-preference proof can
// replay a transcript) plus a duplicate trash-meta read, and the
// RecoveryCopy classification re-proved coverage per record with zero caching
// — measured 14.0s of coverage proofs alone across 25 recovery buckets on the
// 2026-11-07 reporting machine. The observable semantics (record per bucket,
// empty-shell synthesis, family grouping fields, RecoveryCopy contract) are
// pinned by the tl1429b/1429d2/1449 suites and the tl1474 equivalence pin;
// what changed is how each bucket is read: metadata-only listing reads, one
// trash-meta read per bucket, coverage proofs memoized per content identity
// (internal/agent), and the per-bucket assembly running on a bounded worker
// pool.
func (a *App) ListTrashedSessions() []SessionMeta {
	type trashBucketWork struct {
		dir    string
		titles map[string]string
		bucket trashedBucketRecord
	}
	var work []trashBucketWork
	for _, dir := range a.knownSessionDirs() {
		buckets, err := listTrashedSessionBuckets(dir)
		if err != nil {
			continue
		}
		titles := loadSessionTitles(dir)
		for _, bucket := range buckets {
			work = append(work, trashBucketWork{dir: dir, titles: titles, bucket: bucket})
		}
	}

	out := make([]SessionMeta, len(work))
	ok := make([]bool, len(work))
	var wg sync.WaitGroup
	sem := make(chan struct{}, tl1474TrashListWorkers)
	for i, item := range work {
		wg.Add(1)
		go func(i int, item trashBucketWork) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			meta, assembled := a.assembleTrashedSessionMeta(item.dir, item.titles, item.bucket)
			out[i] = meta
			ok[i] = assembled
		}(i, item)
	}
	wg.Wait()

	assembled := make([]SessionMeta, 0, len(work))
	for i := range work {
		if ok[i] {
			assembled = append(assembled, out[i])
		}
	}
	sort.Slice(assembled, func(i, j int) bool {
		if assembled[i].DeletedAt == assembled[j].DeletedAt {
			return assembled[i].LastActivityAt > assembled[j].LastActivityAt
		}
		return assembled[i].DeletedAt > assembled[j].DeletedAt
	})
	return assembled
}

// assembleTrashedSessionMeta builds one trash record from one bucket. This is
// the per-bucket logic from the pre-tl1474 sequential loop, kept verbatim —
// including the tl1449 empty-shell synthesis and the tl1429d2 family grouping
// fields — with two read-mode swaps: agent.ListSessionsMetadataOnly instead of
// agent.ListSessions (display fields identical, no transcript replay), and
// deletedAt folded into the bucket record (no second trash-meta read).
func (a *App) assembleTrashedSessionMeta(dir string, titles map[string]string, bucket trashedBucketRecord) (SessionMeta, bool) {
	path := bucket.Path
	infos, err := agent.ListSessionsMetadataOnly(filepath.Dir(path))
	if err != nil {
		return SessionMeta{}, false
	}
	deletedAt := bucket.DeletedAt
	if len(infos) == 0 {
		// tl1449: 空壳桶(结构性记录在、transcript 已消失)不再静默漏——
		// 漏掉的桶不可见也不可清,清空后永远躺在磁盘上(症状②③)。合成
		// 一条最小记录让清空够得到它;标题留空,前端 emptySession 兜底。
		// tl1429d2: 空壳同样带同族归组字段——副本桶的空壳仍归到主线的
		// 族里,不因合成路径漏掉归组线索。
		key, isCopy := trashedFamilyKey(filepath.Base(path))
		return SessionMeta{
			Path:           path,
			TurnsState:     "unknown",
			DeletedAt:      deletedAt,
			LastActivityAt: deletedAt,
			ModTime:        deletedAt,
			TrashFamilyKey:  key,
			TrashFamilyCopy: isCopy,
		}, true
	}
	title := strings.TrimSpace(infos[0].CustomTitle)
	if title == "" {
		title = titles[filepath.Base(path)]
	}
	meta := sessionMetaFromInfo(infos[0], title, false, false, deletedAt, dir)
	// tl1429d2: 同族归组改走只读字段(tl1429d 的标题后缀标识被用户打回——
	// 打错靶:同族副本桶不该在列表占独立多行,渲染层合并才是修法,标题
	// 污染不是)。桶仍逐条出记录(桶=还原/清除的单桶语义),归组键/副本
	// 形状落在 TrashFamilyKey/TrashFamilyCopy,由前端合并渲染。刻意不动
	// RecoveryCopy——契约是「已证明未被续写且被母本覆盖,可安全清理」
	// (22b334f22,TestTrashMarksLegacyRecoverySessionsAsRecovered 钉死);
	// 副本桶内没有母本,coverage 证明必败,形状推断不够格。
	meta.TrashFamilyKey, meta.TrashFamilyCopy = trashedFamilyKey(filepath.Base(path))
	return meta, true
}

// trashedFamilyKey 返回桶 transcript 名的同族归组键(tl1429d2):主线桶=
// stem 本身;recovery 副本桶=剥掉 -recovery-<16hex> 尾号(嵌套则剥到底)后
// 的根 stem,恰为对应主线桶的键。第二个返回值=是否副本形状(纯命名判据,
// 与 agent.LooksLikeRecoveryFilename 的家族判据同源,不依赖 meta 与
// coverage 证明)。
func trashedFamilyKey(key string) (string, bool) {
	if root, ok := agent.RecoveryFilenameRootID(key); ok {
		return root, true
	}
	return strings.TrimSuffix(key, store.SessionTranscriptSuffix), false
}

func (a *App) trashedSessionDir(path string) (string, error) {
	var lastErr error
	for _, dir := range a.knownSessionDirs() {
		if _, _, _, err := validateTrashedSessionPath(dir, path); err == nil {
			return dir, nil
		} else {
			lastErr = err
		}
	}
	if lastErr != nil {
		// tl1429b-2: carry the strict-validation reason so a stuck record is
		// attributable from desktop.log alone (the package-79 "outside known
		// session dirs" toast carried no WHY).
		slog.Warn("desktop: trashed session path rejected by every known session dir",
			"reason", lastErr.Error())
	}
	return "", fmt.Errorf("trashed session path outside known session dirs: %s", path)
}

func (a *App) sessionDirForPath(path string) (string, string, error) {
	for _, dir := range a.knownSessionDirs() {
		sessionPath, _, err := validateSessionPath(dir, path)
		if err == nil {
			return dir, sessionPath, nil
		}
	}
	return "", "", fmt.Errorf("session path outside known session dirs: %s", path)
}

func applyChannelSessionRoute(meta *SessionMeta, route channelSessionRoute) {
	if meta == nil {
		return
	}
	meta.Kind = "channel"
	meta.Channel = route.channel
	meta.ChannelLabel = route.channelLabel
	meta.RemoteID = route.remoteID
	meta.ChatType = route.chatType
	meta.UserID = route.userID
	meta.ThreadID = route.threadID
	meta.SessionSource = route.sessionSource
}

func channelSessionRoutesForDir(dir string) map[string]channelSessionRoute {
	userPath := config.UserConfigPath()
	if strings.TrimSpace(userPath) == "" {
		return nil
	}
	cfg := config.LoadForEdit(userPath)
	out := map[string]channelSessionRoute{}
	for _, conn := range cfg.Bot.Connections {
		channel := strings.TrimSpace(conn.Provider)
		if channel == "" {
			continue
		}
		channelLabel := strings.TrimSpace(conn.Label)
		if channelLabel == "" {
			channelLabel = channelDisplayName(channel, conn.Domain)
		}
		for _, mapping := range conn.SessionMappings {
			if strings.TrimSpace(mapping.SessionSource) != "auto" {
				continue
			}
			sessionPath := botSessionPathTarget(mapping.SessionID)
			if sessionPath == "" {
				continue
			}
			validPath, _, err := validateSessionPath(dir, sessionPath)
			if err != nil {
				continue
			}
			key := sessionRuntimeKey(validPath)
			if key == "" {
				continue
			}
			out[key] = channelSessionRoute{
				channel:       channel,
				channelLabel:  channelLabel,
				remoteID:      strings.TrimSpace(mapping.RemoteID),
				chatType:      strings.TrimSpace(mapping.ChatType),
				userID:        strings.TrimSpace(mapping.UserID),
				threadID:      strings.TrimSpace(mapping.ThreadID),
				sessionSource: strings.TrimSpace(mapping.SessionSource),
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func botSessionPathTarget(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(sessionID), "path:") {
		return strings.TrimSpace(sessionID[5:])
	}
	if strings.HasSuffix(sessionID, ".jsonl") || strings.Contains(sessionID, "/") || strings.Contains(sessionID, `\`) || strings.HasPrefix(sessionID, "~") {
		return sessionID
	}
	return ""
}

func channelDisplayName(provider, domain string) string {
	provider = strings.TrimSpace(provider)
	domain = strings.TrimSpace(domain)
	switch provider {
	case "feishu":
		if strings.EqualFold(domain, "lark") {
			return "Lark"
		}
		return "Feishu"
	case "weixin":
		return "WeChat"
	case "qq":
		return "QQ"
	case "dingtalk":
		return "DingTalk"
	default:
		return provider
	}
}

// RestoreSession moves a trashed session back into the saved-session list.
func (a *App) RestoreSession(path string) error {
	return friendlySessionFileError(a.restoreSession(path))
}

func (a *App) restoreSession(path string) error {
	dir, err := a.trashedSessionDir(path)
	if err != nil {
		return err
	}
	_, key, _, err := validateTrashedSessionPath(dir, path)
	if err != nil {
		return err
	}
	// The destroying/open checks and the trash-entry move must not interleave
	// with DeleteSession/TrashTopic trashing the same entry.
	a.sessionRemovalMu.Lock()
	defer a.sessionRemovalMu.Unlock()
	target := filepath.Join(dir, key)
	if a.sessionDestroying(dir, target) {
		return fmt.Errorf("session cleanup is still in progress: %s", key)
	}
	// A committed archive may have moved the transcript into trash while a
	// Windows handle kept a sidecar at the live path; the durable cleanup
	// marker makes that recoverable — finish it before restore preflights.
	if agent.IsCleanupPending(target) {
		_ = reconcileDesktopCleanupPendingLocked(dir)
		if agent.IsCleanupPending(target) {
			return fmt.Errorf("session cleanup is still in progress: %s", key)
		}
	}
	if a.sessionOpen(dir, target) {
		return fmt.Errorf("session is open: %s", key)
	}
	if err := restoreTrashedSessionFile(dir, path); err != nil {
		return err
	}
	healed, err := restoreSessionTopicIndex(dir, target)
	if err != nil {
		return err
	}
	if healed != target {
		// tl1331 (G11): the empty shell now lives in its owning scope's
		// session dir — retire the global-side catalog rows immediately
		// (RemoveSession deletes the session row; recomputeTopic then drops
		// the shielded topic row) instead of waiting for a missing-grace
		// reconcile of the global directory.
		a.removeSessionCatalogPath(target, "tl1331: empty shell moved to owning scope")
	}
	a.requestSessionCatalogPath("", "", healed)
	a.emitProjectTreeChangedForSessionDirs(dir)
	a.invalidatePromptHistoryCache()
	return nil
}

func (a *App) sessionDestroying(dir, sessionPath string) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, tab := range a.runtimeTabsLocked() {
		if tab == nil || tab.Ctrl == nil || tabRuntimeSessionDir(tab) != dir {
			continue
		}
		if tab.Ctrl.IsDestroyingSession(sessionPath) {
			return true
		}
	}
	return false
}

func (a *App) sessionOpen(dir, sessionPath string) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, tab := range a.runtimeTabsLocked() {
		if tabMatchesSession(tab, dir, sessionPath) {
			return true
		}
	}
	return false
}

// PurgeTrashedSession permanently removes a trashed session and its title/display
// sidecars.
func (a *App) PurgeTrashedSession(path string) error {
	return friendlySessionFileError(a.purgeTrashedSession(path, false))
}

// PurgeRecoveryCopy is the guarded permanent-cleanup path. A trashed branch is
// rechecked against its live parent; missing, stale, or divergent data is kept.
func (a *App) PurgeRecoveryCopy(path string) error {
	return friendlySessionFileError(a.purgeTrashedSession(path, true))
}

func (a *App) purgeTrashedSession(path string, requireRedundantRecovery bool) error {
	dir, err := a.trashedSessionDir(path)
	if err != nil {
		return err
	}
	a.sessionRemovalMu.Lock()
	defer a.sessionRemovalMu.Unlock()
	var parentGuard *agent.SessionRemovalGuard
	if requireRedundantRecovery {
		parentGuard, err = agent.TryAcquireRecoveryParentGuard(path, dir)
		if err != nil {
			switch {
			case errors.Is(err, agent.ErrRecoveryBranchNotCovered):
				return errRecoveryCopyNotRedundant
			case errors.Is(err, agent.ErrSessionLeaseHeld):
				return errSessionBusyElsewhere
			default:
				return err
			}
		}
		defer parentGuard.Release()
	}
	if err := purgeTrashedSessionFile(dir, path); err != nil {
		return err
	}
	a.invalidatePromptHistoryCache()
	return nil
}

// RenameSession sets a custom display name for a session (empty clears it back to
// the preview). The transcript file is unchanged; the canonical name lives in
// the branch meta sidecar, with the legacy .titles.json map kept as a
// compatibility write-through for older desktop data paths.
func (a *App) RenameSession(path, title string) error {
	dir := a.activeSessionDir()
	if _, _, err := validateSessionPath(dir, path); err != nil {
		resolvedDir, _, resolveErr := a.sessionDirForPath(path)
		if resolveErr != nil {
			return errors.New("session version is unavailable")
		}
		dir = resolvedDir
	}
	return friendlySessionFileError(a.renameSessionInDir(dir, path, title))
}

func (a *App) renameSessionInDir(dir, path, title string) error {
	sessionPath, _, err := validateSessionPath(dir, path)
	if err != nil {
		return err
	}
	if err := agent.RenameSession(sessionPath, title); err != nil {
		return err
	}
	return a.onSessionTitleChanged(dir, sessionPath, title)
}

func (a *App) renameSessionInDirIfTitleUnchanged(dir, path, expectedTitle, title string) error {
	sessionPath, _, err := validateSessionPath(dir, path)
	if err != nil {
		return err
	}
	if err := agent.RenameSessionIfTitleUnchanged(sessionPath, expectedTitle, title); err != nil {
		return err
	}
	return a.onSessionTitleChanged(dir, sessionPath, title)
}

// onSessionTitleChanged projects the canonical BranchMeta custom title into
// the legacy desktop map and live catalog/UI indexes. The session directory is
// supplied by the owning boot so background tabs never route through whichever
// tab happens to be active when the tool finishes.
func (a *App) onSessionTitleChanged(dir, sessionPath, title string) error {
	validated, _, err := validateSessionPath(dir, sessionPath)
	if err != nil {
		return err
	}
	if err := syncSessionTitleFromBranchMeta(dir, validated); err != nil {
		return err
	}
	// Fork (#8280 residual): the project tree's authoritative label comes from
	// the Topic-level titles store, which RenameSession never updated (the
	// "rename flash-back"); project the manual title, best-effort on miss.
	if trimmed := strings.TrimSpace(title); trimmed != "" {
		if topicID, workspaceRoot, ok := topicIDForSessionPath(dir, validated); ok {
			_ = setTopicTitleWithSource(workspaceRoot, topicID, trimmed, topicTitleSourceManual)
			a.updateOpenTopicTitle(topicID, trimmed, topicTitleSourceManual)
		}
	}
	a.requestSessionCatalogPath("", "", validated)
	a.invalidatePromptHistoryCache()
	a.emitProjectTreeChangedForSessionDirs(dir)
	return nil
}

// ResumeSession snapshots the current conversation, then loads the session at
// path and continues it on the active tab. The model and working folder are
// unchanged; only the transcript is swapped. Returns the resumed messages for
// the frontend to render.
func (a *App) ResumeSession(path string) ([]HistoryMessage, error) {
	return a.ResumeSessionForTab("", path)
}

func (a *App) ResumeSessionPage(path string, limit int) (HistoryPage, error) {
	return a.ResumeSessionPageForTab("", path, limit)
}

func (a *App) ResumeSessionPageForTab(tabID, path string, limit int) (HistoryPage, error) {
	return a.resumeSessionPageForTab(tabID, path, limit)
}

// ResumeSessionForTab is the tab-scoped form of ResumeSession. A saved session
// path is a runtime identity, so changing to a different path must replace the
// tab's controller binding rather than mutating the current controller in place.
// ResumeSessionForTab resumes a tab onto a session path, following the
// ordinary-list continuation (an open of the parent follows its unique linear
// leaf — open/restore semantics).
func (a *App) ResumeSessionForTab(tabID, path string) ([]HistoryMessage, error) {
	return a.resumeSessionForTab(tabID, path, true)
}

// resumeSessionForTab with followContinuation=false is the exact form (tl1415c):
// an explicit version switch names the exact transcript the user clicked. The
// heuristic redirected a switch BACK to the main file onto the unique linear
// leaf, so with the tab sitting on a recovery copy the "switch to main"
// hot-switch silently kept the tab on the copy while the preference and
// head_switch marker landed on the main file — a silent wrong switch, exactly
// the shape tl1430 forbids. Ordinary open/restore keeps the heuristic through
// ResumeSessionForTab. Unexported on purpose: only the switch path needs it, and
// an exported App method becomes a Wails binding that bridge.ts must declare.
func (a *App) resumeSessionForTab(tabID, path string, followContinuation bool) ([]HistoryMessage, error) {
	tab, ctrl := a.tabAndCtrlByID(tabID)
	if tab == nil || ctrl == nil {
		return []HistoryMessage{}, fmt.Errorf("tab is not ready")
	}
	if followContinuation {
		if continued := a.continuePathForOpen(path); continued != "" {
			path = continued
		}
	}
	sessionPath, _, err := validateSessionPath(controllerSessionDir(ctrl), path)
	if err != nil {
		return nil, err
	}
	if sessionRuntimeKey(tab.currentSessionPath()) == sessionRuntimeKey(sessionPath) {
		a.mu.RLock()
		takeoverSpectator := a.tabs[tab.ID] == tab && tab.Takeover.Spectator
		a.mu.RUnlock()
		if takeoverSpectator {
			return nil, fmt.Errorf("session is held by the remote side; use TakeoverSession to reclaim it")
		}
		a.setTabReadOnly(tab.ID, false)
		// A read-only transcript explicitly reopened for writing re-announces
		// itself so a resident Serve can mirror and later reclaim it.
		a.attachTakeoverMirror(tab.ID, sessionPath)
		go a.adoptSessionFromLocalServe(tab.ID, sessionPath)
		return a.HistoryForTab(tabID), nil
	}
	// Task 196 second round: switching tabs also resumes through here, and the
	// user reports slow switching *out of* a long session - so both directions
	// need the same decomposition as resumeSessionPageForTab.
	resumeStart := time.Now()
	loaded, err := loadResumableSession(sessionPath)
	loadMs := time.Since(resumeStart).Milliseconds()
	if err != nil {
		slog.Info("desktop: resume session stages", "tab", tabID, "path", sessionPath,
			"load_ms", loadMs, "rebind_ms", int64(0), "history_ms", int64(0),
			"total_ms", time.Since(resumeStart).Milliseconds(), "error", err.Error())
		return nil, err
	}

	rebindStart := time.Now()
	if err := a.rebindTabToLoadedSessionPath(tab, sessionPath, loaded); err != nil {
		slog.Info("desktop: resume session stages", "tab", tabID, "path", sessionPath,
			"load_ms", loadMs, "rebind_ms", time.Since(rebindStart).Milliseconds(), "history_ms", int64(0),
			"total_ms", time.Since(resumeStart).Milliseconds(), "error", err.Error())
		return nil, err
	}
	rebindMs := time.Since(rebindStart).Milliseconds()
	a.setTabReadOnly(tab.ID, false)
	a.attachTakeoverMirror(tab.ID, sessionPath)
	go a.adoptSessionFromLocalServe(tab.ID, sessionPath)
	historyStart := time.Now()
	messages := a.HistoryForTab(tabID)
	slog.Info("desktop: resume session stages", "tab", tabID, "path", sessionPath,
		"load_ms", loadMs, "rebind_ms", rebindMs, "history_ms", time.Since(historyStart).Milliseconds(),
		"total_ms", time.Since(resumeStart).Milliseconds())
	return messages, nil
}

// validateChannelSessionPath 校验 bot/channel 会话路径：channel 会话可能位于
// 当前 controller 的 session dir（project scope）或全局 session dir
// （global scope），单 tab 无法同时覆盖两者，因此都放行。
func validateChannelSessionPath(ctrlDir, path string) (string, string, error) {
	if p, b, err := validateSessionPath(ctrlDir, path); err == nil {
		return p, b, nil
	}
	if globalDir := config.SessionDir(); globalDir != "" && globalDir != ctrlDir {
		if p, b, err := validateSessionPath(globalDir, path); err == nil {
			return p, b, nil
		}
	}
	return validateSessionPath(ctrlDir, path)
}

func (a *App) OpenChannelSessionForTab(tabID, path string) ([]HistoryMessage, error) {
	tab, ctrl := a.tabAndCtrlByID(tabID)
	if tab == nil || ctrl == nil {
		return []HistoryMessage{}, fmt.Errorf("tab is not ready")
	}
	sessionPath, _, err := validateChannelSessionPath(controllerSessionDir(ctrl), path)
	if err != nil {
		return nil, err
	}
	loaded, err := loadResumableSession(sessionPath)
	if err != nil {
		return nil, err
	}
	if sessionRuntimeKey(tab.currentSessionPath()) != sessionRuntimeKey(sessionPath) {
		if err := a.rebindTabToLoadedSessionPath(tab, sessionPath, loaded); err != nil {
			return nil, err
		}
	}
	a.setTabReadOnly(tab.ID, true)
	return a.HistoryForTab(tab.ID), nil
}

func (a *App) OpenChannelSessionPageForTab(tabID, path string, limit int) (HistoryPage, error) {
	tab, ctrl := a.tabAndCtrlByID(tabID)
	if tab == nil || ctrl == nil {
		return HistoryPage{}, fmt.Errorf("tab is not ready")
	}
	sessionPath, _, err := validateChannelSessionPath(controllerSessionDir(ctrl), path)
	if err != nil {
		return HistoryPage{}, err
	}
	loaded, err := loadResumableSession(sessionPath)
	if err != nil {
		return HistoryPage{}, err
	}
	if sessionRuntimeKey(tab.currentSessionPath()) != sessionRuntimeKey(sessionPath) {
		if err := a.rebindTabToLoadedSessionPath(tab, sessionPath, loaded); err != nil {
			return HistoryPage{}, err
		}
	}
	a.setTabReadOnly(tab.ID, true)
	return a.HistoryPageForTab(tab.ID, 0, limit), nil
}

// PreviewSession reads a saved session for display only. It does not snapshot or
// swap the active controller, so the history drawer can call it while a turn runs.
func (a *App) PreviewSession(path string) ([]HistoryMessage, error) {
	sessionDir, sessionPath, err := a.sessionDirForPath(path)
	if err != nil {
		return nil, err
	}
	return previewSessionMessages(sessionDir, sessionPath)
}
