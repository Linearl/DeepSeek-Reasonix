package main

// 任务 1206 — 开轮仲裁抽象层（579 族 SSOT，用户 2026-10-27 拍板：「四个触发源
// 没问题，中间有一个抽象层统一负责不就行了么」）。
//
// 病根：579 家族六件反复（579 重试 / 887 target 不含无 tab 会话 / 1007 已读
// 没开轮记账分叉 / 1185 skeleton 三处判定两说有一说否）共用同一个根——「没在
// 跑的会话收到消息后谁负责拉起来」没有唯一裁判。四个触发源（①信箱补位 drain
// ②579 空闲开轮桥 ③tab 恢复补链 ④恢复面板 resume）各自判断 tab/runtime/lease
// 状态，判定条件互不一致，每条路口各漏过一种形态。
//
// 本文件是唯一裁判（SSOT）：
//   - 三态判定 canWakeSession：tab（含 skeleton 语义）/runtime（含 detached 与
//     构建在飞 buildCancel）/lease（StartupErrLeaseHeld，租约在他处）一个函数
//     回答唯一问题——「此会话是否可开轮、谁负责、排除原因」（任务书原话
//     canWake(session) → {可开轮?, 责任方, 排除原因}）；
//   - 幂等 claimWake：同一会话同一时刻至多一个拉起认领在飞，并发触发（桥+drain
//     同窗）时后到者全路短路——防 887 双开、防双份回答；
//   - 记账单点 recordWakeOutcome / WakeLedgerFor：开轮成功/失败/跳过原因回写
//     单一状态位，四路共用一个可查询的账本——1007 的「已读没开轮、原因无处查」
//     分叉随之消（570 回执仍是发送方侧的持久通道，账本是进程内的归因单点）。
//
// 触发源只管「时机」（pump tick / 恢复点 / 面板点击），仲裁层管「条件与唯一性」。
// 四个触发源的源文件不允许再出现直接的 tab/runtime/lease 状态判定——由
// TestWakeArbiterSourcesAskArbiter 源码守卫钉住（含下限自证）。
//
// 范围护栏（任务书）：本件不动四触发源的时机逻辑——pump 周期、空闲窗口、579
// 三振预算、恢复点次序、面板交互全部原样；改的只是「谁来做状态判定」。

import (
	"strings"
	"sync"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/control"
)

// wakeOwner 回答「谁负责」：本进程内该会话的开轮责任方。
type wakeOwner string

const (
	// wakeOwnerNone 本进程没有任何机制负责——触发源可以按需拉起。
	wakeOwnerNone wakeOwner = "none"
	// wakeOwnerLiveTab 可见 tab 的活 runtime 负责（Ctrl 已发布）。
	wakeOwnerLiveTab wakeOwner = "live_tab"
	// wakeOwnerDetached detached runtime 负责（tab 关了工作还在）。
	wakeOwnerDetached wakeOwner = "detached"
	// wakeOwnerBuilding 构建在飞（buildCancel != nil）——机制已运行，重复拉起
	// 会竞争出第二次构建（1185 反向判据：表现为偶发双份回答）。
	wakeOwnerBuilding wakeOwner = "building"
)

// wakeVerdict 是 canWakeSession 的裁定：tab/runtime/lease 三态快照 + 责任方。
// 字段全部是只读快照，调用方不得据以修改任何状态。
type wakeVerdict struct {
	// Owner 是本进程当前的开轮责任方（none = 无人负责，可拉起）。
	Owner wakeOwner
	// Unaddressable：会话路径为空/无法建立运行时键——fail-closed，永不可拉起。
	Unaddressable bool
	// HasSurface：tab 态——本进程存在绑定该会话的表面（含 skeleton，含 detached）。
	HasSurface bool
	// RuntimeLive：runtime 态——Ctrl 已发布或构建在飞（任务 1185 判据，本文件
	// 是该判据的唯一出处；sessionHasLiveRuntimeForPath 是它的薄委托）。
	RuntimeLive bool
	// LeaseHeldElsewhere：lease 态——某绑定表面记着 StartupErrLeaseHeld（上次
	// 构建因会话租约被他处持有而失败）。此时拉起只会撞锁空转，仲裁层否。
	LeaseHeldElsewhere bool
	// ReadOnly / Spectator：任一活 runtime 表面的只读/旁观标记（记账与归因用；
	// 投递面本就排除它们，见 wakeSurfaceLive）。
	ReadOnly  bool
	Spectator bool
}

// OwnedLive 报告本进程已有活机制负责该会话——触发源应当让位（covered），
// 不得再拉起。这正是 1185 收窄后的判据。
func (v wakeVerdict) OwnedLive() bool { return v.RuntimeLive }

// CanStandUp 报告仲裁层是否允许按需拉起：无任何活机制负责，租约不在他处，
// 且会话路径可寻址。
func (v wakeVerdict) CanStandUp() bool {
	return !v.Unaddressable && !v.RuntimeLive && !v.LeaseHeldElsewhere
}

// BlockReason 给出排除原因（记账用）；可拉起时返回空串。
func (v wakeVerdict) BlockReason() string {
	switch {
	case v.Unaddressable:
		return "no_session_path"
	case v.RuntimeLive:
		return "owned:" + string(v.Owner)
	case v.LeaseHeldElsewhere:
		return "lease_held_elsewhere"
	default:
		return ""
	}
}

// skipOutcome 把裁定映射成账本 outcome：责任方已在 = owned（让位而非失败），
// 其余排除 = skipped。
func (v wakeVerdict) skipOutcome() wakeOutcome {
	if v.RuntimeLive {
		return wakeOwned
	}
	return wakeSkipped
}

// wakeSurfaceLive 是「这个表面能否承接投递/开轮」的唯一判据：活 runtime
// （Ctrl 已发布）+ 可写 + 非旁观 + 未移除。1185 的 skeleton 语义（Ctrl=nil
// 不算活）由此单点定义——四个触发源的投递面扫描全部经它判定。
func wakeSurfaceLive(tab *WorkspaceTab) bool {
	return tab != nil && !tab.removed && tab.Ctrl != nil && !tab.ReadOnly && !tab.Takeover.Spectator
}

// canWakeSession 回答唯一问题：「此会话是否可开轮、谁负责、排除原因」。
// tab/runtime/lease 三态判定全部收口在这里；空路径 fail-closed（无人负责也
// 不可拉起——无路径的会话无法建立运行时键）。
func (a *App) canWakeSession(sessionPath string) wakeVerdict {
	want := sessionRuntimeKey(sessionPath)
	if want == "" || a == nil {
		return wakeVerdict{Owner: wakeOwnerNone, Unaddressable: true}
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	v := wakeVerdict{Owner: wakeOwnerNone}
	promote := func(owner wakeOwner) {
		// 责任方优先级：可见 tab > detached > 构建在飞（人在哪个面前，谁负责
		// 的可读性最高）；构建在飞只在没有已发布 runtime 时显露。
		switch v.Owner {
		case wakeOwnerNone, wakeOwnerBuilding:
			v.Owner = owner
		}
	}
	scan := func(tab *WorkspaceTab, liveOwner wakeOwner) {
		if tab == nil || !tabMatchesSessionKey(tab, want) {
			return
		}
		if liveOwner != wakeOwnerDetached && tab.removed {
			return
		}
		v.HasSurface = true
		switch {
		case tab.Ctrl != nil:
			v.RuntimeLive = true
			promote(liveOwner)
			if tab.ReadOnly {
				v.ReadOnly = true
			}
			if tab.Takeover.Spectator {
				v.Spectator = true
			}
		case tab.buildCancel != nil:
			v.RuntimeLive = true
			promote(wakeOwnerBuilding)
		}
		if tab.StartupErrLeaseHeld {
			v.LeaseHeldElsewhere = true
		}
	}
	for _, tab := range a.tabs {
		scan(tab, wakeOwnerLiveTab)
	}
	for _, tab := range a.detachedSessions {
		scan(tab, wakeOwnerDetached)
	}
	return v
}

// sessionHasLiveRuntimeForPath 是任务 1185 的种子判据，现为仲裁层 runtime 态
// 的薄委托（判定本体在 canWakeSession）。保留原名：dormant 反查的 seam 注入
// 与 build-time 复查两处接线由 TestDormantWiringUsesLiveRuntimeGuard 钉住。
func (a *App) sessionHasLiveRuntimeForPath(sessionPath string) bool {
	return a.canWakeSession(sessionPath).RuntimeLive
}

// wakeTabRuntimeLive 是仲裁层的 per-tab 臂：这张表面（非 removed）是否持有活
// runtime（Ctrl 已发布或构建在飞）。恢复链③作用在具体 tab 上（tab 可以还没有
// SessionPath），问的是「这张表面的 runtime 活不活」，与路径臂同判据（1185
// 语义），同在本文件单点定义。
func wakeTabRuntimeLive(tab *WorkspaceTab) bool {
	return tab != nil && !tab.removed && (tab.Ctrl != nil || tab.buildCancel != nil)
}

// wakeTabLiveCtrl 是 per-tab 臂的取用器：触发源从仲裁层拿已发布控制器，
// 不直接读 tab.Ctrl（源码守卫钉住）。
func wakeTabLiveCtrl(tab *WorkspaceTab) control.SessionAPI {
	if !wakeTabRuntimeLive(tab) {
		return nil
	}
	return tab.Ctrl
}

// dormantWakeCtrlForSessionPath returns the published controller bound to
// sessionPath across visible tabs AND detached runtimes (controllerForSessionPath
// only scans a.tabs — a parked stand-up would otherwise read as "never
// published" and burn the whole wake timeout), plus the first build error of a
// bound tab so a failed build can fail the wake fast.
// 任务 1206：自 session_collab_dormant_wake.go 搬入仲裁层——这是 attach 探测
// 的状态扫描本体，四触发源共用。
func (a *App) dormantWakeCtrlForSessionPath(sessionPath string) (control.SessionAPI, string) {
	want := sessionRuntimeKey(sessionPath)
	if want == "" || a == nil {
		return nil, ""
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	var startupErr string
	for _, tab := range a.tabs {
		if tab == nil || tab.removed {
			continue
		}
		if !tabMatchesSessionKey(tab, want) {
			continue
		}
		if tab.Ctrl != nil {
			return tab.Ctrl, ""
		}
		if startupErr == "" {
			startupErr = tab.StartupErr
		}
	}
	for _, tab := range a.detachedSessions {
		if tab == nil || tab.Ctrl == nil {
			continue
		}
		if tabMatchesSessionKey(tab, want) {
			return tab.Ctrl, ""
		}
	}
	return nil, startupErr
}

// wakeClaimant 是认领方（四个触发源各一个名字），记账归因用。
type wakeClaimant string

const (
	wakeClaimantDrain   wakeClaimant = "drain_standup"  // ① 信箱补位
	wakeClaimantBridge  wakeClaimant = "bridge_dormant" // ② 579 桥 dormant 拉起
	wakeClaimantRestore wakeClaimant = "restore_chain"  // ③ tab 恢复补链
	wakeClaimantPanel   wakeClaimant = "panel_resume"   // ④ 恢复面板
)

// wakeClaim 是一次在飞的拉起认领。
type wakeClaim struct {
	claimant wakeClaimant
	since    time.Time
}

// wakeArbiter 持有幂等认领表与记账单点。零值可用（map 惰性建）。
type wakeArbiter struct {
	mu     sync.Mutex
	claims map[string]wakeClaim // sessionRuntimeKey → 在飞认领

	ledger map[string]wakeLedgerEntry // sessionRuntimeKey → 最近一次开轮结果
}

// claimWake 认领一次会话拉起：同一会话同一时刻至多一个认领在飞。并发触发
// （桥+drain 同窗）时后到者拿到 ok=false，全路短路——这就是 887「桥+drain
// 双开、双份回答」的幂等闸。认领方必须在动作结束后调用 release（defer 安全：
// 重复调用是空操作）。空路径 fail-closed：永远认领失败。
func (a *App) claimWake(sessionPath string, claimant wakeClaimant) (release func(), ok bool) {
	noop := func() {}
	if a == nil {
		return noop, false
	}
	key := sessionRuntimeKey(sessionPath)
	if key == "" {
		return noop, false
	}
	a.wake.mu.Lock()
	defer a.wake.mu.Unlock()
	if a.wake.claims == nil {
		a.wake.claims = map[string]wakeClaim{}
	}
	if _, held := a.wake.claims[key]; held {
		return noop, false
	}
	a.wake.claims[key] = wakeClaim{claimant: claimant, since: time.Now()}
	released := false
	return func() {
		a.wake.mu.Lock()
		defer a.wake.mu.Unlock()
		if released {
			return
		}
		released = true
		// 只撤自己的认领：release 与后续他人认领竞态时不误删。
		if claim, held := a.wake.claims[key]; held && claim.claimant == claimant {
			delete(a.wake.claims, key)
		}
	}, true
}

// wakeOutcome 是账本里的一次开轮结果分类。
type wakeOutcome string

const (
	wakeOpened  wakeOutcome = "opened"  // 拉起/开轮动作已发生
	wakeSkipped wakeOutcome = "skipped" // 仲裁层排除（原因见 Reason）
	wakeFailed  wakeOutcome = "failed"  // 动作尝试了但失败
	wakeOwned   wakeOutcome = "owned"   // 责任方已在，触发源让位
	wakeResumed wakeOutcome = "resumed" // 恢复动作（面板恢复/恢复链清门）已发生
)

// wakeStandUpInFlightReason 是认领竞争失败的账本原因：三态状态干净、但另一
// 触发源的拉起已在飞（887 桥+drain 同窗形态的幂等短路）。
const wakeStandUpInFlightReason = "wake_in_flight"

// wakeLedgerEntry 是记账单点的一行：谁（Claimant）对哪个会话做了什么开轮
// 决定、结果与原因。1007 的病灶——「已读没开轮、原因无处查」——由本行回答。
type wakeLedgerEntry struct {
	SessionPath string      `json:"sessionPath"`
	ContactID   string      `json:"contactId,omitempty"`
	Claimant    string      `json:"claimant"`
	Outcome     wakeOutcome `json:"outcome"`
	Reason      string      `json:"reason,omitempty"`
	ItemID      string      `json:"itemId,omitempty"`
	At          time.Time   `json:"at"`
}

// recordWakeOutcome 回写一行开轮结果（同一会话保留最近一行：账本回答的是
// 「现在谁在负责、为什么」，历史归因由日志与 570 回执承载）。
func (a *App) recordWakeOutcome(entry wakeLedgerEntry) {
	if a == nil {
		return
	}
	key := sessionRuntimeKey(entry.SessionPath)
	if key == "" {
		return
	}
	if entry.At.IsZero() {
		entry.At = time.Now()
	}
	a.wake.mu.Lock()
	defer a.wake.mu.Unlock()
	if a.wake.ledger == nil {
		a.wake.ledger = map[string]wakeLedgerEntry{}
	}
	a.wake.ledger[key] = entry
}

// WakeLedgerFor 查询某会话最近一次开轮结果（验收④：成功/跳过原因可查）。
func (a *App) WakeLedgerFor(sessionPath string) (wakeLedgerEntry, bool) {
	empty := wakeLedgerEntry{}
	if a == nil {
		return empty, false
	}
	key := sessionRuntimeKey(sessionPath)
	if key == "" {
		return empty, false
	}
	a.wake.mu.Lock()
	defer a.wake.mu.Unlock()
	entry, ok := a.wake.ledger[key]
	return entry, ok
}

// wakeGateResult 是 wakeStandUpGate 的问询结果。
type wakeGateResult struct {
	// Granted：仲裁层放行且认领成功——调用方可以拉起，用完后必须调 release。
	Granted bool
	// Verdict 是三态快照（无论放行与否都带全）。
	Verdict wakeVerdict
	// Reason 在拒绝时非空：三态排除原因（BlockReason）或 wake_in_flight。
	Reason string
	// Outcome 是拒绝时的账本分类（owned = 责任方已在让位，skipped = 排除）。
	Outcome wakeOutcome
}

// wakeStandUpGate 是触发源①②「按需拉起」前的统一问询：先问 canWakeSession
// （三态判定），可拉起再原子认领（幂等）。拒绝时调用方记账（Reason/Outcome
// 已备好）后让位即可。
func (a *App) wakeStandUpGate(sessionPath string, claimant wakeClaimant) (release func(), res wakeGateResult) {
	noop := func() {}
	verdict := a.canWakeSession(sessionPath)
	if !verdict.CanStandUp() {
		return noop, wakeGateResult{Verdict: verdict, Reason: verdict.BlockReason(), Outcome: verdict.skipOutcome()}
	}
	release, ok := a.claimWake(sessionPath, claimant)
	if !ok {
		return noop, wakeGateResult{Verdict: verdict, Reason: wakeStandUpInFlightReason, Outcome: wakeSkipped}
	}
	return release, wakeGateResult{Granted: true, Verdict: verdict}
}

// sessionCollabLiveTargets is every place a message can land without first
// standing anything up: visible tabs bound to a session, plus detached runtimes
// whose tab was closed but whose work is still alive. Only the contacts in
// pendingContacts are considered, so an idle pass costs nothing.
// 任务 1206：自 session_collab.go 搬入仲裁层——投递面的表面判定收口到
// wakeSurfaceLive 单点；可见臂自本件起回填 ctrl（扫描时已验过 Ctrl != nil，
// 消费方无需再经 tabByID 二次解析）。
func (a *App) sessionCollabLiveTargets(pendingContacts []string) []sessionCollabTarget {
	want := map[string]bool{}
	for _, c := range pendingContacts {
		want[c] = true
	}
	out := append([]sessionCollabTarget(nil), a.sessionCollabTargets()...)
	seen := map[string]bool{}
	for _, t := range out {
		seen[t.contactID] = true
	}

	a.mu.Lock()
	detached := make([]*WorkspaceTab, 0, len(a.detachedSessions))
	for _, tab := range a.detachedSessions {
		if tab != nil {
			detached = append(detached, tab)
		}
	}
	a.mu.Unlock()

	for _, tab := range detached {
		if !wakeSurfaceLive(tab) {
			continue
		}
		path := strings.TrimSpace(tab.Ctrl.SessionPath())
		if path == "" {
			continue
		}
		contact := agent.SessionContactID(path)
		if contact == "" || seen[contact] {
			continue
		}
		if len(want) > 0 && !want[contact] {
			continue
		}
		seen[contact] = true
		out = append(out, sessionCollabTarget{
			tabID:     tab.ID,
			contactID: contact,
			detached:  true,
			topicID:   strings.TrimSpace(tab.TopicID),
			ctrl:      tab.Ctrl,
		})
	}
	return out
}

// sessionCollabTargets snapshots open tabs that own a registered contact_id.
// Sessions that never registered one are skipped: nothing can address them yet.
// 任务 1206：表面资格判定经仲裁层 wakeSurfaceLive（Ctrl/ReadOnly/Spectator/
// removed 单点语义），可见臂回填 ctrl。
func (a *App) sessionCollabTargets() []sessionCollabTarget {
	a.mu.Lock()
	tabs := make([]*WorkspaceTab, 0, len(a.tabs))
	for _, tab := range a.tabs {
		if tab != nil && !tab.removed {
			tabs = append(tabs, tab)
		}
	}
	// Snapshot under the same lock as the tabs: a concurrent close could flip
	// the activeTabID between the read and the loop, and an unlocked read would be a
	// data race.
	activeTabID := a.activeTabID
	a.mu.Unlock()

	var out []sessionCollabTarget
	seen := map[string]bool{}
	for _, tab := range tabs {
		if !wakeSurfaceLive(tab) {
			continue
		}
		path := strings.TrimSpace(tab.Ctrl.SessionPath())
		if path == "" {
			continue
		}
		key := strings.ToLower(path)
		if seen[key] {
			continue
		}
		contact := agent.SessionContactID(path)
		if contact == "" {
			continue
		}
		seen[key] = true
		out = append(out, sessionCollabTarget{
			tabID:     tab.ID,
			contactID: contact,
			activeTab: tab.ID == activeTabID,
			topicID:   strings.TrimSpace(tab.TopicID),
			ctrl:      tab.Ctrl,
		})
	}
	return out
}
