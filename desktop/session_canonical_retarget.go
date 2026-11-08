package main

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/sessioncatalog"
	"reasonix/internal/topicregistry"
)

func (a *App) resolveOpenTopicSessionPath(scope, workspaceRoot, sessionPath string) (string, string) {
	actualRoot := workspaceRoot
	if scope == "global" {
		actualRoot = globalWorkspaceRoot()
	}
	// tl1415h 刀①：打开漏斗的裁决日志行。取证报告（tl1415四轮取证-20261108）
	// 定案「打开漏斗正常路径零日志」是首开错切当场实例无法百分百归因的唯一
	// 缺口（open_session_trace 只记入参 sessionPath，不记闸后裁决；正常路径
	// 零日志）。本行只在打开动作（树行点击）落一次：选了哪个权威/组员几条/
	// 窗口未就绪标记/最终裁决全在字段里。resume 翻页与启动 retarget 仍走
	// continuePathForOpen 的无日志包装，日志量不随热路径增长。
	continued, verdict := a.continuePathForOpenVerdict(sessionPath)
	verdict.LiveController = continued != "" && a.sessionHasLiveController(sessionPath)
	slog.Info("desktop: open session authority verdict", verdict.logFields(scope, workspaceRoot)...)
	// Keep a live controller on this path (including paused). Opening a
	// different ordinary session of the same topic must still switch.
	if continued != "" {
		if verdict.LiveController {
			return actualRoot, sessionPath
		}
		sessionPath = continued
	}
	return actualRoot, sessionPath
}

// openSessionAuthorityVerdict 是打开漏斗一次裁决的完整读数（tl1415h 刀①）。
// 字段口径对齐取证报告 Q1/Q3 的逐门走查：filed=投影行在册；groupReady=组界定
// 成立（recoveryTopicHasGroup——它被目录 reconcile 建立，是权威#3 的时序前
// 提）；authority/authorityKind=权威#2（catalog filing）或权威#3（meta 旗标）
// 的命中；windowReady=false 即取证报告定义的冷机窗口形态（首开落在窗口内的
// 观测标记）。
type openSessionAuthorityVerdict struct {
	Requested      string
	TopicID        string
	Filed          bool
	GroupID        string
	GroupMembers   int
	GroupReady     bool
	Authority      string
	AuthorityKind  string // catalog_preferred | meta_flag
	Continued      string
	Redirected     bool
	WindowReady    bool
	LiveController bool
	// tl1415h 刀②：窗口收口读数——点击打开在窗口形态时对目录做的按需
	// reconcile 是否执行/是否失败（超时兜底按既有降级走并落日志）。
	WindowReconciled   bool
	WindowReconcileErr string
}

func (v openSessionAuthorityVerdict) logFields(scope, workspaceRoot string) []any {
	fields := []any{
		"scope", scope,
		"workspaceRoot", workspaceRoot,
		"sessionPath", v.Requested,
		"topicId", v.TopicID,
		"filed", v.Filed,
		"windowReady", v.WindowReady,
		"redirected", v.Redirected,
		"continued", v.Continued,
		"liveController", v.LiveController,
	}
	if v.GroupID != "" {
		fields = append(fields, "groupId", v.GroupID, "groupMembers", v.GroupMembers, "groupReady", v.GroupReady)
	}
	if v.Authority != "" {
		fields = append(fields, "authority", v.Authority, "authorityKind", v.AuthorityKind)
	}
	if v.WindowReconciled {
		fields = append(fields, "windowReconciled", true)
		if v.WindowReconcileErr != "" {
			fields = append(fields, "windowReconcileErr", v.WindowReconcileErr)
		}
	}
	return fields
}

// findTopicSessionAcrossScopes resolves a topic id to its best session file
// across EVERY known session directory, regardless of scope (tl1197, third
// shape of the 1074 family). Two entry shapes:
//   - sessionPath empty (openGlobalTab / findTopicSessionForTarget missed):
//     any existing session of the topic wins — the open follows its binding
//     instead of minting a same-topic shell under the global directory.
//   - sessionPath present but content-free (a leftover global shell that the
//     scope-filtered resolver itself picked, because the project-bound owner
//     was filtered out): a content-bearing owner outranks the shell — the
//     #7305 rule that an empty session must not hijack the conversation the
//     user actually had, applied across scopes.
//
// A concrete content-bearing currentPath is always respected (the caller
// already resolved the owner; task 754 corrections stay authoritative).
func (a *App) findTopicSessionAcrossScopes(topicID, currentPath string) (string, string, string, bool) {
	topicID = strings.TrimSpace(topicID)
	if topicID == "" {
		return "", "", "", false
	}
	currentPath = strings.TrimSpace(currentPath)
	if currentPath != "" && sessionFileHasConversationContent(currentPath) {
		// Fast path: the caller already holds a content-bearing session of
		// this topic — always respected. Skips the cross-directory scan on
		// the common open path.
		return "", "", "", false
	}
	var best topicSessionMatch
	var bestContent bool
	found := false
	// tl1208: 注册表唯一归属直查——候选同为内容承载/同为空壳时，落在归属
	// scope 的会话文件优先于 UpdatedAt 启发式；注册表无条目（headless 冷机
	// /索引未建）时保持 1197 启发式不变。
	owner, ownerOK := topicRegistryOwnerOf(topicID)
	matchBetter := func(match topicSessionMatch, content bool) bool {
		if !found {
			return true
		}
		if content != bestContent {
			return content // 内容承载者优先（1197 规则不变）
		}
		if ownerOK {
			mo := topicMatchOwnedByOwner(match, owner)
			bo := topicMatchOwnedByOwner(best, owner)
			if mo != bo {
				return mo
			}
		}
		return match.updatedAt.After(best.updatedAt)
	}
	for _, dir := range a.knownSessionDirs() {
		for _, match := range topicSessionMatches(dir, topicID) {
			content := sessionFileHasConversationContent(match.path)
			if matchBetter(match, content) {
				best = match
				bestContent = content
				found = true
			}
		}
	}
	if !found || best.path == currentPath {
		return "", "", "", false
	}
	if currentPath != "" && !bestContent {
		// The caller pinned a content-free session and no owner exists
		// anywhere — keep the caller's pick rather than churning bindings.
		return "", "", "", false
	}
	binding, ok := a.resolveSessionBinding(best.path)
	if !ok {
		return "", "", "", false
	}
	scope := strings.TrimSpace(binding.scope)
	if scope != "project" {
		return best.path, "global", "", true
	}
	root := normalizeProjectRoot(binding.workspaceRoot)
	if root == "" {
		return "", "", "", false
	}
	return best.path, "project", root, true
}

// topicMatchOwnedByOwner 报告一个候选会话文件是否落在注册表唯一归属的
// scope 里（tl1208）。归属为 global 时只认 global 目录候选；归属为项目时
// 认同根项目候选。
func topicMatchOwnedByOwner(match topicSessionMatch, owner topicregistry.Entry) bool {
	if owner.Scope == topicregistry.ScopeGlobal {
		return match.scope != "project"
	}
	return match.scope == "project" && sameProjectRoot(match.workspaceRoot, owner.Root)
}

func (a *App) sessionHasLiveController(path string) bool {
	key := sessionRuntimeKey(path)
	if a == nil || key == "" {
		return false
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, tabs := range []map[string]*WorkspaceTab{a.tabs, a.detachedSessions} {
		for _, tab := range tabs {
			if tab != nil && tab.Ctrl != nil && sessionRuntimeKey(tab.currentSessionPath()) == key {
				return true
			}
		}
	}
	return false
}

func (a *App) skipContinuationRebind(tab *WorkspaceTab, target string) bool {
	if tab == nil || tab.Ctrl == nil {
		return false
	}
	next := a.continuePathForOpen(tab.currentSessionPath())
	return next != "" && sessionRuntimeKey(next) == sessionRuntimeKey(target)
}

func (a *App) continuePathForOpen(path string) string {
	continued, _ := a.continuePathForOpenVerdict(path)
	return continued
}

// continuePathForOpenVerdict 是 continuePathForOpen 的裁决读数版（tl1415h 刀①）：
// 打开动作（resolveOpenTopicSessionPath）经它落裁决日志；resume/retarget 走
// 无日志包装，行为与日志面都保持原样。
func (a *App) continuePathForOpenVerdict(path string) (string, openSessionAuthorityVerdict) {
	path = strings.TrimSpace(path)
	verdict := openSessionAuthorityVerdict{Requested: path, WindowReady: true}
	if path == "" {
		return "", verdict
	}
	catalog := a.sessionCatalog.Load()
	if catalog == nil {
		verdict.WindowReady = false
		return "", verdict
	}
	ctx := context.Background()
	rec, ok, err := catalog.GetSession(ctx, path)
	if err != nil || !ok {
		// tl1415h 刀②：投影行不在册——冷机窗口的第一形态（目录 reconcile
		// 未跑）。与启动恢复路径的排序语义对齐：判权威前先对该目录做一次有
		// 界 reconcile，再重试一次入册读；超时/失败按既有降级路径走（落
		// reconcileOpenWindow 的 Warn）。
		verdict.WindowReady = false
		if a.reconcileOpenWindow(catalog, filepath.Dir(path), &verdict) {
			if rec, ok, err = catalog.GetSession(ctx, path); err == nil && ok {
				verdict.WindowReady = true
			} else {
				return a.continuePathForMissingParent(ctx, catalog, path), verdict
			}
		} else {
			return a.continuePathForMissingParent(ctx, catalog, path), verdict
		}
	}
	verdict.Filed = true
	if rec.TopicID == "" {
		return "", verdict
	}
	topic, ok, err := catalog.GetTopic(ctx, sessioncatalog.TopicKey{Scope: rec.Scope, WorkspaceRoot: rec.WorkspaceRoot, TopicID: rec.TopicID})
	if err != nil || !ok {
		return "", verdict
	}
	continued := sessioncatalog.OrdinaryContinuePath(topic.Sessions, path)
	verdict.Continued = continued
	// tl1415e: open/restore reconciles against the panel's main-line authority
	// (recoveryAuthorityMainPath — the same resolver Selected and the
	// already-main gate read). tl1415c unified the panel verdict but left the
	// ordinary open on the linear-leaf heuristic, so the tab landed on a copy
	// file while the panel marked the adopted recovery line as Selected (the
	// user's retest: panel marks recovery-7fa1f7e5, tab renders the 10-18
	// collab copy). Reconciliation rules:
	//   ① only a catalog-filed NON-recovered request redirects — explicitly
	//     opening a recovery member stays inspection (OrdinaryContinuePath
	//     already keeps those put, and so does this guard);
	//   ② an absent authority (unfiled path, no preference, no tab) keeps the
	//     heuristic untouched — conversations without recovery groups keep
	//     their exact open/restore semantics (the resolver returns "" for
	//     unfiled paths, so the no-op is structural, not incidental);
	//   ③ an authority that names a different member wins — the open target
	//     and the panel's Selected row now read one resolver and cannot split
	//     by construction.
	if !rec.Recovered {
		authority, kind, detail, resolved := a.recoveryAuthorityForOpenVerdict(catalog, rec)
		if resolved && !detail.GroupReady {
			// tl1415h 刀②：有行但组未界定——冷机窗口的第二形态（副本行未被
			// 组分类，recoveryTopicHasGroup 落空，权威#1/#2/#3 全空）。同窗口
			// 第一形态：有界 reconcile 后重判一次权威；仍落空才按既有降级走。
			verdict.WindowReady = false
			if a.reconcileOpenWindow(catalog, filepath.Dir(path), &verdict) {
				if authority2, kind2, detail2, resolved2 := a.recoveryAuthorityForOpenVerdict(catalog, rec); resolved2 {
					authority, kind, detail = authority2, kind2, detail2
					if detail2.GroupReady {
						verdict.WindowReady = true
					}
				}
			}
		}
		verdict.TopicID = detail.TopicID
		verdict.GroupID = detail.GroupID
		verdict.GroupMembers = detail.GroupMembers
		verdict.GroupReady = detail.GroupReady
		verdict.Authority = authority
		verdict.AuthorityKind = kind
		if authority != "" && !sameRecoveryLineagePath(authority, rec.Path) {
			verdict.Redirected = true
			return authority, verdict
		}
	}
	return continued, verdict
}

// recoveryAuthorityMainPathForOpen is the open/restore funnel's view of the
// panel's main-line authority (tl1415f package-82 field reversal). It differs
// from recoveryAuthorityMainPath — the resolver the switch gate and the panel
// Selected read — in exactly two ways, both dictated by what an open request
// is:
//
//   - The open tab's current transcript (recoveryAuthorityMainPath authority
//     #1) is skipped here. That authority answers "which line is the user on
//     right now" for an already-main refusal; consulted from the open funnel
//     it self-answers: a restored or scan-handed tab sitting on a demoted copy
//     names that copy as its own authority and the reconciliation can never
//     fire (the restart-restore half of the user's field scene).
//   - A third authority is appended: the single recovery group member whose
//     file meta carries RecoveryPreferred. The catalog's filed preference
//     (authority #2) is digest-validated at reconcile time
//     (agent.RecoveryPreferenceCurrent), so a system rewrite of the adopted
//     file — repair, tolerant replay, damaged-tail re-save — voids the filing
//     while the panel (recovery_chains.go preferredRows, tl1430b) keeps
//     rendering the same flag as Selected. When the two disagree, the panel is
//     the product truth the open must follow: the user's field scene had the
//     panel on recovery-7fa1f7e5 and the first open on the 10-18 collab copy,
//     with the linear-leaf heuristic dead too (turns regression). Multiple
//     flagged members fail closed, mirroring promoteCanonicalLeaves.
//
// Authority #2 keeps one shared predicate with recoveryAuthorityMainPath
// (preferredGroupMemberPathOfGroup) so the two resolvers cannot drift apart
// again.
func (a *App) recoveryAuthorityMainPathForOpen(catalog *sessioncatalog.Catalog, rec sessioncatalog.SessionRecord) string {
	authority, _, _, _ := a.recoveryAuthorityForOpenVerdict(catalog, rec)
	return authority
}

// recoveryAuthorityForOpenVerdict 是 recoveryAuthorityMainPathForOpen 的裁决
// 读数版（tl1415h 刀①）：返回值在权威路径与命中类别（catalog_preferred=权威
// #2 / meta_flag=权威 #3）之外，带出组界定读数（组 ID/组员数/组是否已建立）
// 供打开裁决日志与窗口判定使用。逐门语义与原函数逐字节一致——纯观测改造，
// 权威裁决行为零变化。
func (a *App) recoveryAuthorityForOpenVerdict(catalog *sessioncatalog.Catalog, rec sessioncatalog.SessionRecord) (string, string, openSessionAuthorityVerdict, bool) {
	detail := openSessionAuthorityVerdict{Requested: rec.Path, WindowReady: true}
	resolveCtx, cancel := a.catalogReadContext()
	record, found, err := catalog.GetSession(resolveCtx, rec.Path)
	cancel()
	if err != nil || !found || strings.TrimSpace(record.LogicalTopicID) == "" {
		detail.WindowReady = false
		return "", "", detail, false
	}
	detail.Filed = true
	detail.TopicID = strings.TrimSpace(record.LogicalTopicID)
	scope, workspaceRoot := normalizeDesktopTopicScope(record.Scope, record.WorkspaceRoot)
	topic, ok, err := catalog.GetTopic(a.bootContext(), sessioncatalog.TopicKey{Scope: scope, WorkspaceRoot: workspaceRoot, TopicID: detail.TopicID})
	if err != nil || !ok {
		return "", "", detail, false
	}
	groupID, _, ok := recoveryLineageSelection(topic, record.Path)
	if !ok || groupID == "" {
		detail.WindowReady = false
		return "", "", detail, true
	}
	detail.GroupID = groupID
	detail.GroupReady = true
	for _, member := range topic.Sessions {
		if recoveryRecordBelongsToGroup(member, groupID) {
			detail.GroupMembers++
		}
	}
	if path := preferredGroupMemberPathOfGroup(topic, groupID); path != "" {
		detail.Authority, detail.AuthorityKind = path, "catalog_preferred"
		return path, detail.AuthorityKind, detail, true
	}
	path := a.preferredMetaFlagPathOfGroup(topic, groupID)
	if path != "" {
		detail.Authority, detail.AuthorityKind = path, "meta_flag"
	}
	return path, detail.AuthorityKind, detail, true
}

// preferredMetaFlagPathOfGroup resolves authority #3: the one member of the
// recovery group whose file meta still carries the explicit RecoveryPreferred
// flag — the same undigested source the panel's Selected fallback reads
// (recovery_chains.go preferredRows). Exactly one flag wins; zero or several
// return "" (ambiguous authority must not invent a verdict).
func (a *App) preferredMetaFlagPathOfGroup(topic sessioncatalog.TopicRecord, groupID string) string {
	flagged := ""
	for _, member := range topic.Sessions {
		if !recoveryRecordBelongsToGroup(member, groupID) {
			continue
		}
		meta, ok, err := agent.LoadBranchMeta(member.Path)
		if err != nil || !ok || !meta.RecoveryPreferred {
			continue
		}
		if flagged != "" {
			return ""
		}
		flagged = member.Path
	}
	return flagged
}

// openSessionWindowReconcileBudget 是点击打开路径对未就绪目录做按需 reconcile
// 的超时兜底（tl1415h 刀②，取证报告修复建议②）。var 仅为测试可注入；生产代
// 码不得改写。
var openSessionWindowReconcileBudget = 2 * time.Second

// reconcileDirectoryForOpenWindow 在冷机窗口（路径未入册/组未建）时对该目录
// 做一次有界目录 reconcile。启动路径（session_catalog_lifecycle.go）把
// retargetOpenTabsToContinuations 显式排在目录 reconcile 之后，而用户点击打
// 开路径没有这个排序保证——树行来自 topic 注册表，先于 reconcile 可见可点，
// 窗口内首开会因组未建落错主线（取证报告 Q3 定案）。本函数把同一条排序语义
// 补到点击路径上：只做目录级对账（ReconcileDirectory 自带 directorySignature
// 未变跳过与目录锁），不做全量重扫。
//
// target 取自 sessionCatalogTargets（目录归属的 scope/root 与启动 reconcile
// 同源）；不在已知目录集里的路径不碰（与 sessionDirForPath 的 known-dirs 门
// 同一保守边界）。返回 attempted=是否发起了 reconcile；超时/失败按既有降级
// 路径走（attempted 仍为 true，错误由调用方落日志）。
func (a *App) reconcileDirectoryForOpenWindow(catalog *sessioncatalog.Catalog, dir string) (bool, error) {
	dir = filepath.Clean(strings.TrimSpace(dir))
	if dir == "" || catalog == nil {
		return false, nil
	}
	target := sessioncatalog.DirectoryTarget{}
	known := false
	for _, candidate := range a.sessionCatalogTargets() {
		if sameOpenWindowDir(candidate.Path, dir) {
			target = candidate
			known = true
			break
		}
	}
	if !known {
		return false, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), openSessionWindowReconcileBudget)
	defer cancel()
	err := catalog.ReconcileDirectory(ctx, target)
	return true, err
}

// reconcileOpenWindow 执行窗口 reconcile 并把读数写进裁决（tl1415h 刀②）。
// 返回是否真的发起了 reconcile；失败落一行 Warn（超时兜底可见），随后调用方
// 按既有降级路径走。
func (a *App) reconcileOpenWindow(catalog *sessioncatalog.Catalog, dir string, verdict *openSessionAuthorityVerdict) bool {
	attempted, err := a.reconcileDirectoryForOpenWindow(catalog, dir)
	verdict.WindowReconciled = verdict.WindowReconciled || attempted
	if err != nil {
		verdict.WindowReconcileErr = err.Error()
		slog.Warn("desktop: open session window reconcile", "dir", dir, "err", err.Error())
	}
	return attempted
}

// sameOpenWindowDir compares two cleaned directory spellings as the same
// session directory. Windows folds case (the disk real name and the catalog
// spelling legitimately differ — tl1415 四轮取证附记一), other platforms
// compare bytes.
func sameOpenWindowDir(left, right string) bool {
	if left == right {
		return true
	}
	return runtime.GOOS == "windows" && strings.EqualFold(left, right)
}

func (a *App) continuePathForMissingParent(ctx context.Context, catalog *sessioncatalog.Catalog, path string) string {
	parentID := agent.BranchID(path)
	if parentID == "" {
		return ""
	}
	// desktop-tabs.json may still name a parent that lineage folded off the
	// ordinary row. Look up the topic by the filename id.
	for _, target := range a.sessionCatalogTargets() {
		page, err := catalog.ListTopics(ctx, sessioncatalog.TopicPageRequest{
			Scope: target.Scope, WorkspaceRoot: target.WorkspaceRoot, Limit: sessioncatalog.MaxLimit,
		})
		if err != nil {
			continue
		}
		for _, topic := range page.Items {
			for _, session := range topic.Sessions {
				if session.ParentID == parentID || strings.TrimSpace(session.RecoveryGroupID) == parentID {
					if next := sessioncatalog.OrdinaryContinuePath(topic.Sessions, path); next != "" {
						return next
					}
				}
			}
		}
	}
	return ""
}

func (a *App) resumeSessionPageForTab(tabID, path string, limit int) (HistoryPage, error) {
	tab, ctrl := a.tabAndCtrlByID(tabID)
	if tab == nil || ctrl == nil {
		return HistoryPage{}, fmt.Errorf("tab is not ready")
	}
	if continued := a.continuePathForOpen(path); continued != "" {
		current := tab.currentSessionPath()
		if !tab.hasActiveRuntimeWork() || sessionRuntimeKey(current) != sessionRuntimeKey(path) {
			path = continued
		}
	}
	sessionPath, _, err := validateSessionPath(controllerSessionDir(ctrl), path)
	if err != nil {
		return HistoryPage{}, err
	}
	// Task 196 second round: the stall sits between "takeover watcher started"
	// and "resume cache state", and neither end is itself slow (one starts a
	// 3s poller, the other reads a sidecar). The cost is this chain, which had no
	// timing at all. Split it into load (agent.LoadSessionTail: lock + tail
	// replay), rebind (controller swap + projection bind + cold/warm prune) and
	// page (history assembly), so the next capture names the culprit directly.
	resumeStart := time.Now()
	loaded, err := loadResumableSession(sessionPath)
	loadMs := time.Since(resumeStart).Milliseconds()
	if err != nil {
		slog.Info("desktop: resume session page stages", "tab", tabID, "path", sessionPath,
			"load_ms", loadMs, "rebind_ms", int64(0), "page_ms", int64(0),
			"total_ms", time.Since(resumeStart).Milliseconds(), "error", err.Error())
		return HistoryPage{}, err
	}
	rebindMs := int64(0)
	if sessionRuntimeKey(tab.currentSessionPath()) != sessionRuntimeKey(sessionPath) {
		rebindStart := time.Now()
		if err := a.rebindTabToLoadedSessionPath(tab, sessionPath, loaded); err != nil {
			slog.Info("desktop: resume session page stages", "tab", tabID, "path", sessionPath,
				"load_ms", loadMs, "rebind_ms", time.Since(rebindStart).Milliseconds(), "page_ms", int64(0),
				"total_ms", time.Since(resumeStart).Milliseconds(), "error", err.Error())
			return HistoryPage{}, err
		}
		rebindMs = time.Since(rebindStart).Milliseconds()
	}
	a.setTabReadOnly(tab.ID, false)
	pageStart := time.Now()
	page := a.HistoryPageForTab(tab.ID, 0, limit)
	slog.Info("desktop: resume session page stages", "tab", tabID, "path", sessionPath,
		"load_ms", loadMs, "rebind_ms", rebindMs, "page_ms", time.Since(pageStart).Milliseconds(),
		"total_ms", time.Since(resumeStart).Milliseconds())
	return page, nil
}

func (a *App) retargetOpenTabsToContinuations() {
	if a == nil {
		return
	}
	type candidate struct {
		tab     *WorkspaceTab
		current string
	}
	a.mu.RLock()
	items := make([]candidate, 0, len(a.tabs)+len(a.detachedSessions))
	collect := func(tab *WorkspaceTab) {
		if tab == nil || tab.hasActiveRuntimeWork() {
			return
		}
		items = append(items, candidate{tab: tab, current: tab.currentSessionPath()})
	}
	for _, tab := range a.tabs {
		collect(tab)
	}
	for _, tab := range a.detachedSessions {
		collect(tab)
	}
	a.mu.RUnlock()
	type pending struct {
		tab  *WorkspaceTab
		next string
	}
	ready := make([]pending, 0, len(items))
	for _, item := range items {
		next := a.continuePathForOpen(item.current)
		if next == "" || sessionRuntimeKey(next) == sessionRuntimeKey(item.current) {
			continue
		}
		ready = append(ready, pending{tab: item.tab, next: next})
	}
	for _, item := range ready {
		if item.tab.hasActiveRuntimeWork() {
			continue
		}
		if item.tab.Ctrl == nil {
			a.mu.Lock()
			if !item.tab.hasActiveRuntimeWork() && (a.tabs[item.tab.ID] == item.tab || a.detachedSessions[sessionRuntimeKey(item.tab.currentSessionPath())] == item.tab) {
				item.tab.SessionPath = item.next
				a.saveTabsLocked()
			}
			a.mu.Unlock()
			continue
		}
		if err := a.rebindTabToSessionPath(item.tab, item.next); err != nil {
			continue
		}
	}
}
