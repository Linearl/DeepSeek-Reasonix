package main

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/collabinbox"
	"reasonix/internal/config"
	"reasonix/internal/sessioncollab"
)

// collabInboxCtx is the context every panel method hands down. Wails bindings
// carry no per-call request context, so this is Background on purpose — the
// panel path is not a cancellable chain, and the inner lock budget
// (collabinbox lockWaitTimeout, 5s) is what keeps these calls bounded
// (task 461 P1: 内层 ≤5s 有界).
func collabInboxCtx() context.Context { return context.Background() }

// Task 320 — the cross-session inbox panel's Wails surface.
//
// Every method answers a revision-stamped Snapshot (contract ①: two windows
// printing the same revision look at the same state), the panel's dismiss /
// decide / retention actions return the NEW snapshot directly so the caller
// never re-fetches to converge, and the state lives beside the mail directory
// (contract f: restart keeps entries, dismissals, decisions and settings).

// 任务 600（收件箱性能优化）：identity 目录扫描的 30s TTL 进程内缓存。
//
// 排查实证（调研报告 收件箱性能优化调研-20261008 §2）：旧实现把全目录扫描放进
// resolver 闭包，classify 对每条无身份戳的消息调一次 → 一次面板读触发 ~2600 次
// 全目录扫描（单次实测 3.4~18s）→ 生产日志单次面板读 18.6~39.5 分钟。查表化后
// 每次构店只扫一次，TTL 缓存再把「切桶 / 徽标刷新 / 连续点开」合并为至多 30s
// 一次真实扫描。
//
// 新鲜度边界（有意为之）：30s 内新建/改身份的会话晚 ≤30s 生效——只影响桶归类
// 与会话删除清理（后者本有 5 分钟 sweep 节流，任务 511），无实质风险。
const identityScanTTL = 30 * time.Second

var (
	identityScanMu sync.Mutex
	// identityScanCache holds the last scan verbatim — an EMPTY roster is a
	// valid result and is cached too (latch keyed on identityScanAt, not on
	// nil-ness: a nil-capable slice would otherwise rescan per store build).
	identityScanCache []sessioncollab.Identity
	identityScanAt    time.Time // zero = never scanned
	// identityScanNow is the injectable clock for the TTL (task 600 tests).
	identityScanNow = time.Now
	// identityScanFn is the injectable scan source (task 600 tests); production
	// keeps the real directory scan.
	identityScanFn = func() []sessioncollab.Identity {
		return agent.ScanCollabIdentityDirectory(config.SessionDir(), "")
	}
)

// scanIdentityDirectoryCached returns the addressable identity roster, reusing
// one directory scan across the whole desktop inbox surface for at most
// identityScanTTL. The mutex coalesces concurrent cold calls: the second window
// waits for the first scan instead of double-scanning (the wait happens OUTSIDE
// the inbox locks, so it cannot wedge the panel's lock chain).
func scanIdentityDirectoryCached() []sessioncollab.Identity {
	identityScanMu.Lock()
	defer identityScanMu.Unlock()
	if !identityScanAt.IsZero() && identityScanNow().Sub(identityScanAt) < identityScanTTL {
		return identityScanCache
	}
	ids := identityScanFn()
	identityScanCache = ids
	identityScanAt = identityScanNow()
	return ids
}

// collabInboxStore opens the aggregate index over the shared mail dir. The
// sender-identity resolver (task 348) reads the same BranchMeta the directory
// scan uses, so heartbeat/system senders land in the right bucket; a session
// outside the scanned dirs simply falls back to the mention bucket.
//
// 任务 600：扫描结果在构店时查表成 map——resolver 必须 O(1)（与查询工具
// query_collab_mail_tool 的既有注释契约对齐），绝不再对每条消息扫一遍目录。
func collabInboxStore() *collabinbox.Store {
	return newCollabInboxStore(config.SessionCollabMailDir(), scanIdentityDirectoryCached)
}

// newCollabInboxStore is the injectable constructor behind collabInboxStore
// (task 600 tests): one scan feeds BOTH the O(1) identity-type resolver and the
// cleanup rule's liveness oracle. The liveness roster is as-of-construction —
// consistent with the sweep's own 5-minute throttle (任务 511); with the default
// cleanup rule (never) the oracle is not consulted at all.
func newCollabInboxStore(mailDir string, scan func() []sessioncollab.Identity) *collabinbox.Store {
	identityByContact := map[string]string{}
	live := map[string]bool{}
	for _, id := range scan() {
		if id.ContactID == "" {
			continue
		}
		if id.IdentityType != "" {
			identityByContact[id.ContactID] = id.IdentityType
		}
		live[id.ContactID] = true
	}
	store := collabinbox.New(mailDir, func(contact string) string {
		return identityByContact[contact]
	})
	store.SetLiveContacts(func() map[string]bool { return live })
	return store
}

// collabInboxViewer is the calling window's own contact id — it drives the
// approval sub-states (待我审 / 我发起的). No active session → "" and those
// sub-states simply stay false (honest absence, never a guess).
func (a *App) collabInboxViewer() string {
	if a == nil {
		return ""
	}
	if tab := a.activeTab(); tab != nil && tab.SessionPath != "" {
		return agent.SessionContactID(tab.SessionPath)
	}
	return ""
}

// ListCollabMail returns one revision-stamped page of the unified mail table.
// bucket: all|approval|mention|automation|system (empty = all); state:
// all|pendingMe|mine|decided; limit <= 0 uses the default (50, hard max 500);
// order: desc (newest first, the default) | asc (oldest first) — task 320 a's
// date sort, surfaced as a panel toggle (the index layer has always been
// dual-order; the agent query tool exposes the same field).
func (a *App) ListCollabMail(bucket, from, to, state string, limit int, includeDismissed bool, order string) (collabinbox.Snapshot, error) {
	// 任务511 复发断根（②入口留痕）：每次点开必有一行后端 INFO——复发事故的
	// 「15:27 点开零后端日志」是静默通道特征，这条 INFO 与数据层的结果 INFO
	// （collab inbox: panel read）一起构成两级哨兵：本行在 = 绑定到达 Go；
	// 两行都在 = 请求走完了读取链。都不在 ⇒ 问题在前端绑定/网关（前端
	// reportFrontendLog 通道已留痕），不再是无从诊断的空面板。
	slog.Info("collab inbox: ListCollabMail", "bucket", bucket, "from", from,
		"to", to, "state", state, "limit", limit, "dismissed", includeDismissed,
		"order", order, "viewer", a.collabInboxViewer())
	store := collabInboxStore()
	snap, err := store.List(collabInboxCtx(), collabinbox.Query{
		Bucket:           bucket,
		From:             from,
		To:               to,
		State:            state,
		Viewer:           a.collabInboxViewer(),
		Order:            order,
		Limit:            limit,
		IncludeDismissed: includeDismissed,
	}, true) // panel calls may apply retention (the agent tool path never does)
	if err == nil && snap.Degraded {
		// 任务461 P11：读路径降级（锁被楔住、无锁直读）——面板有数据，但必须
		// 留痕并指认最后持锁者，否则「面板空/数据旧」再次无从诊断。
		slog.Warn("collab inbox: degraded unlocked read (lock busy)",
			"last_holder", store.LockHolderInfo(),
			"bucket", bucket, "state", state, "limit", limit, "order", order)
	}
	if err != nil {
		// The panel's frontend catch is intentionally silent (a closed gateway
		// must not crash it), which turns backend failures into an empty list
		// with no trace anywhere. Log it so a "N unread but empty panel"
		// report has a reachable cause.
		slog.Warn("collab inbox: ListCollabMail failed", "err", err,
			"bucket", bucket, "state", state, "limit", limit, "order", order)
	}
	return snap, err
}

// ListCollabMailChains returns the thread-grouped view (task 320 g): one row
// per conversation chain with its rounds expanded.
func (a *App) ListCollabMailChains(bucket string, limit int) (collabinbox.ChainSnapshot, error) {
	return collabInboxStore().Chains(collabInboxCtx(), collabinbox.Query{
		Bucket: bucket,
		Viewer: a.collabInboxViewer(),
		Limit:  limit,
	})
}

// CountUnreadCollabMail answers the icon-row badge (task 320 遗留 #1): how
// many unified-table entries are unread (the recipient's seen cursor does not
// cover them) and not dismissed — exactly what the panel's default view would
// list as awaiting attention. READ-ONLY by contract: applyRetention stays
// false, so a sidebar refresh never prunes the transport layer (that side
// effect belongs to the panel path alone), and no entries travel the wire —
// just the count.
func (a *App) CountUnreadCollabMail() (int64, error) {
	snap, err := collabInboxStore().List(collabInboxCtx(), collabinbox.Query{Unread: true, Limit: 1}, false)
	if err != nil {
		return 0, err
	}
	return int64(snap.Total), nil
}

// DismissCollabMail eliminates entries from the default view and returns the
// fresh snapshot (batch dismiss → new revision, e-①).
func (a *App) DismissCollabMail(ids []string) (collinboxSnapshot, error) {
	return collabInboxStore().Dismiss(collabInboxCtx(), ids)
}

// UndismissCollabMail restores previously eliminated entries.
func (a *App) UndismissCollabMail(ids []string) (collinboxSnapshot, error) {
	return collabInboxStore().Undismiss(collabInboxCtx(), ids)
}

// MarkCollabMailRead advances the seen cursor for the given entries (任务
// 461 P8 ③ 批量已读). A folded duplicate entry settles its whole cluster;
// the returned snapshot refreshes the panel in one round trip.
func (a *App) MarkCollabMailRead(ids []string) (collinboxSnapshot, error) {
	return collabInboxStore().MarkRead(collabInboxCtx(), ids)
}

// MarkCollabMailDecided records an approval verdict with its decider ("human"
// for a panel click; agent-driven decisions pass the contact id) — task 320 d
// (已裁决条目必须记录裁决者).
func (a *App) MarkCollabMailDecided(messageID, by string) (collinboxSnapshot, error) {
	if by == "" {
		by = "human"
	}
	return collabInboxStore().Decide(collabInboxCtx(), messageID, by)
}

// SetCollabMailRetention switches the retention window (7d|30d|90d|forever)
// and applies it immediately — task 320 c.
func (a *App) SetCollabMailRetention(retention string) (collinboxSnapshot, error) {
	return collabInboxStore().SetRetention(collabInboxCtx(), retention)
}

// SetCollabMailCleanupRule switches the session-deletion cleanup rule (任务
// 464: never|sender|receiver|both) and applies it immediately — never (the
// default) keeps everything regardless of who deleted which conversation.
// Orthogonal to the retention window: one keys on session existence, the other
// on message age, and both are enforced in one sweep.
func (a *App) SetCollabMailCleanupRule(rule string) (collinboxSnapshot, error) {
	return collabInboxStore().SetCleanupRule(collabInboxCtx(), rule)
}

// CleanCollabMailNow runs one cleanup pass immediately (任务 620 立即清理):
// the same write-side maintenance a panel open applies — the retention window
// prunes by age and the session-deletion cleanup rule prunes by session
// existence, both under one lock hold — minus the 任务511 five-minute sweep
// throttle, because the button is the explicit "clean now" trigger. Returns
// how many messages were physically removed plus a fresh default-view
// snapshot (the same page the panel's own read renders) so the list converges
// in one round trip.
func (a *App) CleanCollabMailNow() (CollabInboxCleanResult, error) {
	store := collabInboxStore()
	removed, err := store.ApplyRetention(collabInboxCtx())
	if err != nil {
		return CollabInboxCleanResult{Removed: removed}, err
	}
	snap, lerr := store.List(collabInboxCtx(), collabinbox.Query{
		Bucket: collabinbox.BucketAll,
		State:  collabinbox.StateAll,
		Order:  "desc",
		Limit:  100, // the panel's own default page size
		Viewer: a.collabInboxViewer(),
	}, false)
	return CollabInboxCleanResult{Removed: removed, Snapshot: snap}, lerr
}

// CollabInboxCleanResult carries the 「立即清理」 outcome (任务 620): how many
// messages the pass physically removed (both maintenance halves combined)
// plus the fresh default-view snapshot.
type CollabInboxCleanResult struct {
	Removed  int                  `json:"removed"`
	Snapshot collabinbox.Snapshot `json:"snapshot"`
}

// collinboxSnapshot pins the wire type name for the Wails bindings.
type collinboxSnapshot = collabinbox.Snapshot
