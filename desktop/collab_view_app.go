package main

// Task 409 — 群聊式协作视图（多 Agent 协作可视化一期）的 Wails 面。
//
// 一期定位是「只读观察窗」：把已经存在的数据聚合呈现——通讯录（身份目录扫描）、
// 共享忙闲判定（agent.CollabStatusRecords，get_session_status 回答的同一函数）、
// 任务卡（sessioncollab.CardStore，create/update_task_card 落盘的同一批文件）、
// 已投递往来（sessioncollab.MailStore.History）、频道实体自身的读口
// （collabchannel.Store: ListChannels/Messages/FanoutStates，任务 349）。
// 不加新协议、不建第二份存储、没有写入路径（409 正文原则 3 + 4：与 Cue 的
// 差异 = 本任务是用户观察窗，不制造 AI 互聊内容）。
//
// 绑定模式沿 collab_inbox_app.go（任务 320）：每次调用自包含，用完即关，
// 不持有跨调用的文件句柄。

import (
	"context"
	"strings"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/collabchannel"
	"reasonix/internal/config"
	"reasonix/internal/sessioncollab"
)

// collabViewSessionCap bounds one overview page: the directory can be large,
// and the panel is a group roster, not an audit export. The status records'
// own order is the scan's newest-first page.
const collabViewSessionCap = 50

// CollabViewCard is the session's current task card (409 内容 2 进度维度：
// 派单 → 执行 → 回执复用任务卡状态机，不新造状态).
type CollabViewCard struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Status    string `json:"status"`
	UpdatedAt int64  `json:"updatedAt"`
}

// CollabViewExchange is the session's most recent delivered mail touching it
// (最近一条往来). Delivered mail only — a queued send is nobody's exchange
// (与 320 契约 ② 同一口径).
type CollabViewExchange struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Preview  string `json:"preview"`
	At       int64  `json:"at"`
	ThreadID string `json:"threadId,omitempty"`
}

// CollabViewSession is one roster row: who, what state, on which task.
// State reuses the get_session_status vocabulary verbatim (running | queued |
// idle | unknown) — the panel and the tool can never disagree about a peer.
type CollabViewSession struct {
	ContactID    string              `json:"contactId"`
	TopicID      string              `json:"topicId,omitempty"`
	Title        string              `json:"title"`
	Purpose      string              `json:"purpose,omitempty"`
	IdentityType string              `json:"identityType,omitempty"`
	State        string              `json:"state"`
	LastActivity int64               `json:"lastActivity"`
	UnreadInbox  int                 `json:"unreadInbox"`
	Card         *CollabViewCard     `json:"card,omitempty"`
	LastExchange *CollabViewExchange `json:"lastExchange,omitempty"`
}

// CollabViewOverview is one overview snapshot. sessions may be empty — an
// empty collaboration directory renders the panel's empty state, never an
// error (409 验收: 空态不劣化现状).
type CollabViewOverview struct {
	Sessions    []CollabViewSession `json:"sessions"`
	GeneratedAt int64               `json:"generatedAt"`
}

// CollabChannelMessageView is one channel line plus its per-recipient
// delivery rows (349: delivered/read are per member and never merged — the
// panel annotates each member's 送达/已读 from these rows).
type CollabChannelMessageView struct {
	ID     string                    `json:"id"`
	Sender string                    `json:"sender"`
	Body   string                    `json:"body"`
	At     int64                     `json:"at"`
	Fanout []collabchannel.FanoutRow `json:"fanout,omitempty"`
}

// CollabChannelReadView is one channel read: the channel header plus its
// recent lines oldest-first.
type CollabChannelReadView struct {
	Channel  collabchannel.Channel      `json:"channel"`
	Messages []CollabChannelMessageView `json:"messages"`
}

// collabViewStatusOverride is the test seam for the in-process probe — nil in
// production, where the overview always answers from a.collabSessionStatus
// (the same walk OnSessionStatus wires into boot). A bare test App owns no
// controllers, so without the seam every row would honestly read unknown and
// the running/queued/idle mapping could only be pinned agent-side.
var collabViewStatusOverride func(contactID string) (running bool, lastTurnAtMS int64, pending int, known bool)

// GetCollabViewOverview aggregates the roster (409 内容 1): live
// directory sessions × shared status judgement × current task card × last
// exchange. All read-only, all existing data sources.
func (a *App) GetCollabViewOverview() (CollabViewOverview, error) {
	out := CollabViewOverview{
		Sessions:    []CollabViewSession{},
		GeneratedAt: time.Now().UnixMilli(),
	}

	// One directory walk for the descriptive fields (purpose / identity type);
	// the status records below re-walk it for the shared judgement — two cheap
	// metadata scans, and the state NEVER comes from this file's own logic.
	// 任务 600：直接复用收件箱面的 30s TTL 扫描缓存，群聊视图的轮询不再
	// 额外打穿全目录（单次扫描实测可达秒级，两个面板各扫各的会把轮询成本翻倍）。
	identities := scanIdentityDirectoryCached()
	byContact := make(map[string]sessioncollab.Identity, len(identities))
	for _, id := range identities {
		if id.Archived {
			continue
		}
		if id.ContactID != "" {
			byContact[id.ContactID] = id
		}
	}

	status := a.collabSessionStatus
	if collabViewStatusOverride != nil {
		status = collabViewStatusOverride
	}
	cfg := agent.SessionCollabConfig{
		SessionDir:    config.SessionDir(),
		MailDir:       config.SessionCollabMailDir(),
		SessionStatus: status,
	}
	records, _, _ := agent.CollabStatusRecords(cfg, nil)

	cards := a.collabViewCards()
	exchanges := collabViewLastExchanges()

	for _, record := range records {
		if len(out.Sessions) >= collabViewSessionCap {
			break
		}
		contactID, _ := record["contactId"].(string)
		row := CollabViewSession{
			ContactID:   contactID,
			State:       "unknown",
			UnreadInbox: 0,
		}
		row.Title, _ = record["title"].(string)
		row.TopicID, _ = record["topicId"].(string)
		row.State, _ = record["state"].(string)
		row.LastActivity, _ = record["lastActivity"].(int64)
		row.UnreadInbox, _ = record["unreadInbox"].(int)
		if id, ok := byContact[contactID]; ok {
			row.Purpose = id.Purpose
			row.IdentityType = id.IdentityType
			row.Card = collabViewCardFor(cards, contactID, id.SessionPath)
		}
		if ex, ok := exchanges[contactID]; ok {
			exCopy := ex
			row.LastExchange = &exCopy
		}
		out.Sessions = append(out.Sessions, row)
	}
	return out, nil
}

// collabViewCards lists the task cards under every workspace root this App
// can see through its live controllers (honest scope: a runtime this process
// cannot see may have cards under roots not listed here — those sessions
// report state=unknown anyway). Newest-updated first (CardStore's order).
func (a *App) collabViewCards() []sessioncollab.Card {
	seen := map[string]bool{}
	var out []sessioncollab.Card
	for _, target := range a.sessionCollabLiveTargets(nil) {
		if target.ctrl == nil {
			continue
		}
		root, ok := safeControllerWorkspaceRoot(target.ctrl)
		if !ok || strings.TrimSpace(root) == "" || seen[root] {
			continue
		}
		seen[root] = true
		cards, err := sessioncollab.NewCardStore(root).List()
		if err != nil {
			continue
		}
		out = append(out, cards...)
	}
	return out
}

// collabViewCardFor picks the session's CURRENT card: contact match first
// (assignee, then initiator), session-path fallback for pre-contact cards;
// among matches an open card (running/pending/blocked) beats a terminal one,
// then the most recently updated wins.
func collabViewCardFor(cards []sessioncollab.Card, contactID, sessionPath string) *CollabViewCard {
	rank := func(c sessioncollab.Card) int {
		switch c.Status {
		case sessioncollab.StatusRunning:
			return 0
		case sessioncollab.StatusPending:
			return 1
		case sessioncollab.StatusBlocked:
			return 2
		default:
			return 3
		}
	}
	var best *sessioncollab.Card
	for i := range cards {
		c := cards[i]
		match := (c.Assignee != "" && c.Assignee == contactID) ||
			(c.Initiator != "" && c.Initiator == contactID) ||
			(sessionPath != "" && c.SessionTo == sessionPath)
		if !match {
			continue
		}
		if best == nil || rank(c) < rank(*best) || (rank(c) == rank(*best) && c.UpdatedAt > best.UpdatedAt) {
			best = &cards[i]
		}
	}
	if best == nil {
		return nil
	}
	return &CollabViewCard{ID: best.ID, Title: best.Title, Status: string(best.Status), UpdatedAt: best.UpdatedAt}
}

// collabViewLastExchanges maps every contact to its most recent delivered
// mail row (History is newest-first, so the first hit per contact wins).
// degraded=true（锁繁忙，任务 511）时返回空表：群里少一列「最近往来」可以，
// 用别的会话的信冒充不行。
func collabViewLastExchanges() map[string]CollabViewExchange {
	out := map[string]CollabViewExchange{}
	mail := sessioncollab.NewMailStore(config.SessionCollabMailDir())
	rows, degraded := mail.History(context.Background())
	if degraded {
		return out
	}
	for _, hrow := range rows {
		preview := hrow.Mail.Body
		if i := strings.IndexByte(preview, '\n'); i >= 0 {
			preview = preview[:i]
		}
		for _, contact := range []string{hrow.Mail.From, hrow.Mail.To} {
			if contact == "" {
				continue
			}
			if _, ok := out[contact]; ok {
				continue
			}
			out[contact] = CollabViewExchange{
				From:     hrow.Mail.From,
				To:       hrow.Mail.To,
				Preview:  preview,
				At:       hrow.Mail.At,
				ThreadID: hrow.Mail.ThreadID,
			}
		}
	}
	return out
}

// ListCollabChannels answers the panel's channel roster from the 349 entity's
// own read verb — zero view-side reimplementation.
func (a *App) ListCollabChannels() ([]collabchannel.Channel, error) {
	store, err := collabchannel.Open(config.SessionCollabMailDir())
	if err != nil {
		return nil, err
	}
	defer store.Close()
	return store.ListChannels()
}

// ReadCollabChannel returns one channel's header plus its recent lines with
// per-recipient fan-out rows attached (每成员送达/已读标注). limit <= 0 uses
// 50; hard max 200 (the panel is a chat tail, not an export).
func (a *App) ReadCollabChannel(ref string, limit int) (CollabChannelReadView, error) {
	out := CollabChannelReadView{Messages: []CollabChannelMessageView{}}
	store, err := collabchannel.Open(config.SessionCollabMailDir())
	if err != nil {
		return out, err
	}
	defer store.Close()

	channel, err := store.Channel(ref)
	if err != nil {
		return out, err
	}
	out.Channel = channel

	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	messages, err := store.Messages(ref, 0, limit)
	if err != nil {
		return out, err
	}
	fanout, err := store.FanoutStates(ref)
	if err != nil {
		return out, err
	}
	byMessage := make(map[string][]collabchannel.FanoutRow, len(messages))
	for _, row := range fanout {
		byMessage[row.MessageID] = append(byMessage[row.MessageID], row)
	}
	out.Messages = make([]CollabChannelMessageView, 0, len(messages))
	for _, m := range messages {
		view := CollabChannelMessageView{ID: m.ID, Sender: m.Sender, Body: m.Body, At: m.At}
		// FanoutStates orders by (message at, member) — keep that order so the
		// member annotation reads in the roster's own order.
		view.Fanout = byMessage[m.ID]
		out.Messages = append(out.Messages, view)
	}
	return out, nil
}
