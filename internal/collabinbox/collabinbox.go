// Package collabinbox is the task-320 read-side of the cross-session mailbox:
// a read-only aggregate index over the mail directory the sessioncollab
// MailStore already owns. The transport layer is NOT reworked — each session's
// inbox.jsonl + seen.json stay the single source of truth; this package only
// reads them (History), keeps its own small state file for view-shaped data
// (dismissals, decisions, retention), and applies the retention prune through
// the MailStore lock.
//
// Design contracts carried here (task 320 a-g):
//
//   - Buckets are VIEWS, not storage: every consumer (panel, query tool,
//     chains) reads the same unified entry table classified by Kind.
//   - Notification honesty (contract ②): an entry exists only once the message
//     sits in a recipient's inbox — a queued/sent-only message is invisible,
//     and ids dedupe, so replay never duplicates an entry.
//   - Revision snapshots (contract ①): list/dismiss return a revision that
//     changes whenever state or content changes, so two windows converge.
//   - Restart retention (f): dismissals/decisions/retention live on disk in
//     the mail directory, next to the messages they describe.
package collabinbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"reasonix/internal/filelock"
	"reasonix/internal/sessioncollab"
)

// Buckets — the five views of the unified mail table (task 320 d).
const (
	BucketAll        = "all"
	BucketApproval   = "approval"
	BucketMention    = "mention"
	BucketAutomation = "automation"
	BucketSystem     = "system"
)

// Buckets lists every bucket name in panel order (all first).
func Buckets() []string {
	return []string{BucketAll, BucketApproval, BucketMention, BucketAutomation, BucketSystem}
}

// ValidBucket reports whether name is a known bucket view.
func ValidBucket(name string) bool {
	switch name {
	case BucketAll, BucketApproval, BucketMention, BucketAutomation, BucketSystem, "":
		return true
	}
	return false
}

// Retention presets (task 320 c): 7 / 30 / 90 days or forever. Stored as a
// string so "unset" can never be confused with "forever" (both would be 0 as
// an int). Default for a fresh state file is 7d.
const (
	Retention7d      = "7d"
	Retention30d     = "30d"
	Retention90d     = "90d"
	RetentionForever = "forever"
)

// ValidRetention reports whether v is one of the four presets.
func ValidRetention(v string) bool {
	switch v {
	case Retention7d, Retention30d, Retention90d, RetentionForever:
		return true
	}
	return false
}

// Cleanup rules (任务 464): when a SESSION is deleted, what happens to the
// mail it exchanged? Four choices, orthogonal to the retention window — the
// rule keys on session existence, retention keys on message age. Default is
// CleanupNever: mail always survives (current behavior, now pinned by test).
// 「已删除」= the contact no longer appears in the addressable directory scan
// (live sessions + archived ones) — i.e. the session was moved to trash.
// Archived sessions stay addressable (restorable, still receive mail), so
// archive is NOT deletion for this purpose.
const (
	// CleanupNever keeps everything (default) — rule ④.
	CleanupNever = "never"
	// CleanupSender removes a message when the SENDER's session is deleted — rule ①.
	CleanupSender = "sender"
	// CleanupReceiver removes a message when the RECIPIENT's session is deleted — rule ②.
	CleanupReceiver = "receiver"
	// CleanupBoth removes a message only when BOTH ends' sessions are deleted — rule ③.
	CleanupBoth = "both"
)

// ValidCleanupRule reports whether v is one of the four cleanup rules.
func ValidCleanupRule(v string) bool {
	switch v {
	case CleanupNever, CleanupSender, CleanupReceiver, CleanupBoth:
		return true
	}
	return false
}

// Sub-state filters over the approval bucket (task 320 d: 待我审 / 我发起的 / 已裁决).
const (
	StateAll       = "all"
	StatePendingMe = "pendingMe"
	StateMine      = "mine"
	StateDecided   = "decided"
)

// Entry is one row of the unified table (task 320 a/d/f).
type Entry struct {
	ID           string `json:"id"`
	From         string `json:"from"`
	FromSession  string `json:"fromSession,omitempty"`
	To           string `json:"to"`
	ToTitle      string `json:"toTitle,omitempty"`
	At           int64  `json:"at"`
	ThreadID     string `json:"threadId"`
	Hop          int    `json:"hop,omitempty"`
	CardID       string `json:"cardId,omitempty"`
	Approver     string `json:"approver,omitempty"`
	RequireReply bool   `json:"requireReply,omitempty"`
	Delivery     string `json:"delivery,omitempty"`
	// Bucket is the five-way view classification (approval|mention|automation|system).
	Bucket string `json:"bucket"`
	// Channel is the task-349 group identifier (349 挂账 note①): the chat
	// channel this mail was fanned out from (its name at delivery time).
	// Empty = point-to-point mail. Presentation-only provenance — the bucket
	// classification (task 320 d) is deliberately untouched by it.
	Channel string `json:"channel,omitempty"`
	// Preview is the first line of the body, rune-bounded — enough to triage,
	// never a transcript dump.
	Preview string `json:"preview"`
	// Delivered is true by construction: the index only sees messages sitting
	// in a recipient inbox (queued sends are not entries at all).
	Delivered bool `json:"delivered"`
	// Read is the recipient's seen cursor covering this id (task 320 b).
	Read bool `json:"read"`
	// Dismissed marks an entry the viewer eliminated; it stays in the table
	// (until retention removes it) so dismissal survives restart (f).
	Dismissed bool `json:"dismissed,omitempty"`
	// DuplicateCount is the 任务461 P8 ③ fold of fully identical mail (same
	// sender+recipient+body within duplicateFoldWindow): the cluster shows as
	// ONE entry carrying the number of copies. 0/1 = no duplicates; MarkRead
	// settles the whole cluster from this entry's id alone.
	DuplicateCount int `json:"duplicateCount,omitempty"`
	DecidedBy string `json:"decidedBy,omitempty"`
	DecidedAt int64  `json:"decidedAt,omitempty"`
	PendingMe bool   `json:"pendingMe,omitempty"`
	Mine      bool   `json:"mine,omitempty"`
}

// Settings is the persisted panel configuration (task 320 c/f + 任务 464).
type Settings struct {
	Retention string `json:"retention"` // 7d | 30d | 90d | forever
	// CleanupRule is the session-deletion semantics (任务 464): never (default)
	// | sender | receiver | both. Orthogonal to Retention — one keys on session
	// existence, the other on message age.
	CleanupRule string `json:"cleanupRule,omitempty"`
}

// Decision is a recorded approval verdict (task 320 d: 裁决者必须记录).
type Decision struct {
	By string `json:"by"` // "human" or the deciding conversation's contact_id
	At int64  `json:"at"`
}

// stateFile is the on-disk view state: dismissals, decisions, retention,
// cleanup rule (任务 464), last successful sweep stamp (任务 511 节流闸).
type stateFile struct {
	Revision    int64               `json:"revision"`
	Dismissed   map[string]int64    `json:"dismissed,omitempty"`
	Decided     map[string]Decision `json:"decided,omitempty"`
	Retention   string              `json:"retention,omitempty"`
	CleanupRule string              `json:"cleanupRule,omitempty"`
	// LastSweepAt is when the write-side maintenance last completed
	// successfully (ms epoch). It is what makes the panel read's sweep a
	// low-frequency maintenance instead of a full-library rewrite on every
	// open — 任务 511 节流闸. 0/absent = never swept = due.
	LastSweepAt int64 `json:"lastSweepAt,omitempty"`
}

// Query filters and pages one list/chain call (task 320 a/b).
type Query struct {
	Bucket   string
	From     string
	To       string
	ThreadID string
	Since    int64 // inclusive lower bound, ms
	Until    int64 // exclusive upper bound, ms
	Unread   bool
	State    string // all | pendingMe | mine | decided
	Viewer   string // the asking session's contact_id (drives pendingMe/mine)
	Limit    int
	Offset   int
	Order    string // desc (default) | asc, by At
	// IncludeDismissed returns eliminated entries too (the panel's "已消除"
	// view); default hides them.
	IncludeDismissed bool
}

// Snapshot is the revision-stamped answer of one list/dismiss/decide call
// (task 320 e: 快照式 list/dismiss，多端状态一致).
type Snapshot struct {
	Revision  string   `json:"revision"`
	Settings  Settings `json:"settings"`
	Total     int      `json:"total"`
	Returned  int      `json:"returned"`
	Truncated bool     `json:"truncated"`
	Entries   []Entry  `json:"entries"`
	// Degraded marks a snapshot read WITHOUT the inbox lock (任务461 P11):
	// the lock was wedged, so the read proceeded unlocked rather than
	// failing/emptying the panel. 可用性 > 锁完整性.
	Degraded bool `json:"degraded,omitempty"`
}

// Chain is one thread aggregation (task 320 g: 对话链视图 — 每条 = 一个 thread).
type Chain struct {
	ThreadID     string   `json:"threadId"`
	Participants []string `json:"participants"`
	Count        int      `json:"count"`
	FirstAt      int64    `json:"firstAt"`
	LastAt       int64    `json:"lastAt"`
	Preview      string   `json:"preview"` // first entry's preview
	Entries      []Entry  `json:"entries"` // chronological within the chain
}

// ChainSnapshot is the revision-stamped chain view.
type ChainSnapshot struct {
	Revision string   `json:"revision"`
	Settings Settings `json:"settings"`
	Total    int      `json:"total"`
	Chains   []Chain  `json:"chains"`
}

const (
	defaultLimit = 50
	maxLimit     = 500 // task 320 b: hard ceiling, no unbounded reads
	stateName    = "collab-inbox-state.json"
	lockName     = ".collab-inbox.lock"
	// lockWaitTimeout bounds one cross-process lock wait for every inbox
	// operation (task 461 P1, 内层 ≤5s). 可用性 > 锁完整性：锁被别的窗口/进程
	// 占住时，操作在预算内返回明确错误，而不是把调用工具无限挂起——这是
	// 「query_collab_mail 卡死 39 分钟」事故的根因修复。
	lockWaitTimeout = 5 * time.Second
	// duplicateFoldWindow bounds the consumer-side fold of byte-identical mail
	// (任务461 P8 ③): copies older than this from the cluster primary stay
	// separate entries — genuine repeated content across days never merges.
	duplicateFoldWindow = 24 * time.Hour
	// readLockWaitTimeout is the SHORT shared-lock budget for read-only paths
	// (任务461 P11): a reader never queues behind a wedged holder for the full
	// write budget — 可用性 > 锁完整性, the user's standing ruling.
	readLockWaitTimeout = 1500 * time.Millisecond
	// sweepThrottleWindow is the 任务511 throttle: a panel read skips the
	// retention sweep when the last SUCCESSFUL sweep is younger than this.
	// 排查实证（任务 511 报告 §4 缺口 2）：面板每开一次就对全库 inbox 做一遍
	// 读-改-写（独占锁横跨两把锁），连点桶即自碰撞降级。5 分钟把「读一次=全库
	// 重写一遍」降为低频维护，同时保留期变更仍即时生效（SetRetention 走
	// ApplyRetention，不受此闸约束）。非配置常量：改窗口 = 改这一行。
	sweepThrottleWindow = 5 * time.Minute
)

// Store reads the mail directory through the sessioncollab MailStore and
// keeps the view state beside it. resolver (optional) maps a sender contact
// to its registered identity type (task 348) so heartbeat/system senders
// classify into the right bucket without a body sniff. liveContacts (optional,
// 任务 464) reports which contact ids still belong to an existing session —
// the cleanup rule needs it to tell "deleted" from "elsewhere"; nil disables
// cleanup entirely (the honest no-op).
type Store struct {
	mailDir      string
	mail         *sessioncollab.MailStore
	resolver     func(contact string) string
	liveContacts func() map[string]bool
	now          func() int64 // injectable clock for tests (ms)
	// sweep is the write-side maintenance ran by panel reads (retention +
	// session-deletion cleanup). A seam so tests can inject a failure and pin
	// the "a failed sweep must not empty the panel" contract (320 运行时复盘).
	sweep func(ctx context.Context) (int, error)
}

// New builds a store over mailDir. resolver may be nil.
func New(mailDir string, resolver func(contact string) string) *Store {
	s := &Store{
		mailDir:  mailDir,
		mail:     sessioncollab.NewMailStore(mailDir),
		resolver: resolver,
		now:      func() int64 { return time.Now().UnixMilli() },
	}
	s.sweep = s.ApplyRetention
	return s
}

// SetLiveContacts installs the liveness oracle used by the cleanup rule
// (任务 464): the function returns every contact id whose session still
// exists (live AND archived — archive is not deletion). nil (the default)
// turns cleanup into a no-op even when a rule is configured.
func (s *Store) SetLiveContacts(fn func() map[string]bool) { s.liveContacts = fn }

// Mail exposes the underlying transport store (retention / test seams).
func (s *Store) Mail() *sessioncollab.MailStore { return s.mail }

func (s *Store) statePath() string { return filepath.Join(s.mailDir, stateName) }

func (s *Store) lockFilePath() string { return filepath.Join(s.mailDir, lockName) }

// lockHolderPath is a SIDECAR, not the lock file itself: the lock file's byte
// range is held via LockFileEx, and a second-handle write to that range fails
// on Windows. The sidecar keeps the same diagnostics without touching it.
func (s *Store) lockHolderPath() string { return filepath.Join(s.mailDir, lockName+".holder") }

// writeLockHolderInfo stamps the sidecar with this process's identity right
// after an exclusive acquire (任务461 P11 ①): a later waiter that times out can
// read WHO held it last and since when, instead of a bare "busy". The content
// survives release, so right after a release it reads as the previous holder —
// informative, never authoritative.
func (s *Store) writeLockHolderInfo() {
	info := fmt.Sprintf("pid=%d held_since=%s", os.Getpid(), time.Now().Format(time.RFC3339))
	_ = os.WriteFile(s.lockHolderPath(), []byte(info), 0o600)
}

// LockHolderInfo returns the last recorded holder identity from the sidecar
// (may be empty or stale — it is a diagnostic, not a lease).
func (s *Store) LockHolderInfo() string {
	b, err := os.ReadFile(s.lockHolderPath())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// lock takes the inbox's cross-process lock. ctx is the caller's request
// context: cancellation (用户点停止) ends the wait immediately, and the wait
// itself never exceeds lockWaitTimeout even when the holder never lets go.
func (s *Store) lock(ctx context.Context) (func(), error) {
	if err := os.MkdirAll(s.mailDir, 0o755); err != nil {
		return nil, err
	}
	release, err := filelock.AcquireWithExternalTimeout(ctx, s.lockFilePath(), lockWaitTimeout)
	if err != nil {
		holder := s.LockHolderInfo()
		if holder != "" {
			// 任务511 复发断根：侧车指认的 pid 已死 ⇒ 那是一份陈旧诊断（OS 锁
			// 随进程死亡已被内核释放，此刻的 busy 只能来自其他存活持有者，常见
			// 为共享读）。清掉留痕，错误信息不再把死进程当持锁者指认。
			if s.clearStaleHolderIfDead() != "" {
				return nil, fmt.Errorf("collab inbox lock busy, gave up waiting (stale holder sidecar cleared — the recorded holder process is gone, a live holder may still exist): %w", err)
			}
			return nil, fmt.Errorf("collab inbox lock busy, gave up waiting (last holder: %s): %w", holder, err)
		}
		return nil, fmt.Errorf("collab inbox lock busy (held by another window or process?), gave up waiting: %w", err)
	}
	s.writeLockHolderInfo()
	return release, nil
}

// lockRead takes the inbox lock in SHARED mode with a short budget (任务461
// P11 ②): list/count are read-only, so a wedged exclusive holder must not
// empty the panel — the reader degrades to an UNLOCKED read instead. The
// transport reads stay bounded by their own budgets; a degraded read never
// mutates (retention is skipped). Returns release==nil when degraded.
func (s *Store) lockRead(ctx context.Context) (release func(), degraded bool, err error) {
	if err := os.MkdirAll(s.mailDir, 0o755); err != nil {
		return nil, false, err
	}
	// 任务511 复发断根：读入口先做一次 stale 侧车回收——指认死进程的 holder
	// 诊断在下一次读取时自动清掉并留痕，「pid 26048 持锁 1.5 天」这类误导性
	// 诊断不再跨日残留（锁本体是 OS 句柄锁，死进程的锁由内核回收，这里只
	// 治理诊断文件；防误杀边界见 clearStaleHolderIfDead）。
	s.clearStaleHolderIfDead()
	readCtx, cancel := context.WithTimeout(ctx, readLockWaitTimeout)
	defer cancel()
	release, err = filelock.AcquireMode(readCtx, s.lockFilePath(), filelock.ModeShared)
	if err != nil {
		return nil, true, nil // degraded: unlocked read wins over a failed read
	}
	return release, false, nil
}

func (s *Store) loadState() stateFile {
	st := stateFile{Dismissed: map[string]int64{}, Decided: map[string]Decision{}, Retention: Retention7d, CleanupRule: CleanupNever}
	b, err := os.ReadFile(s.statePath())
	if err != nil {
		return st
	}
	var loaded stateFile
	if err := json.Unmarshal(b, &loaded); err != nil {
		return st
	}
	if loaded.Dismissed == nil {
		loaded.Dismissed = map[string]int64{}
	}
	if loaded.Decided == nil {
		loaded.Decided = map[string]Decision{}
	}
	if !ValidRetention(loaded.Retention) {
		loaded.Retention = Retention7d
	}
	// 任务 464: states written before the cleanup rule existed lack the key —
	// empty normalizes to the default (never = keep everything), so existing
	// state files silently carry today's semantics forward.
	if !ValidCleanupRule(loaded.CleanupRule) {
		loaded.CleanupRule = CleanupNever
	}
	return loaded
}

func (s *Store) saveState(st stateFile) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.statePath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.statePath())
}

// classify assigns one message to a bucket (task 320 d). Priority: explicit
// stamp > approver-carried approval > sender identity (heartbeat→automation,
// system→system) > mention (the default: point-to-point 派活).
func (s *Store) classify(m sessioncollab.MailMessage) string {
	switch strings.ToLower(strings.TrimSpace(m.Kind)) {
	case BucketApproval, BucketMention, BucketAutomation, BucketSystem:
		return strings.ToLower(strings.TrimSpace(m.Kind))
	}
	if strings.TrimSpace(m.Approver) != "" {
		return BucketApproval
	}
	if s.resolver != nil {
		switch s.resolver(m.From) {
		case "heartbeat":
			return BucketAutomation
		case "system":
			return BucketSystem
		}
	}
	return BucketMention
}

// approverOf is who answers this approval: the stamped approver, else the
// sender (task 225 default).
func approverOf(m sessioncollab.MailMessage) string {
	if a := strings.TrimSpace(m.Approver); a != "" {
		return a
	}
	return m.From
}

// foldIndex is build's full output: the folded entry table plus what MarkRead
// needs to settle a whole duplicate cluster (任务461 P8 ③).
type foldIndex struct {
	entries []Entry
	// degraded carries the transport read's lock-busy flag (任务511): when
	// MailStore.History could not take .mail.lock in budget, the row set is
	// empty BECAUSE of the lock, not because the mailboxes are — the panel
	// snapshot must say so instead of rendering an honest-looking empty state.
	degraded bool
	// foldGroups maps a folded entry's id to every member id of its cluster
	// (primary first). Unfolded entries have no entry in this map.
	foldGroups map[string][]string
	// foldPrimary maps any member id back to its cluster's primary id.
	foldPrimary map[string]string
	// mailboxes maps any mail id to its owning mailbox contact.
	mailboxes map[string]string
}

// build reads the transport layer once and derives every entry.
func (s *Store) build(ctx context.Context) ([]Entry, error) {
	idx, err := s.buildIndex(ctx)
	if err != nil {
		return nil, err
	}
	return idx.entries, nil
}

func (s *Store) buildIndex(ctx context.Context) (*foldIndex, error) {
	rows, historyDegraded := s.mail.History(ctx)
	// Replies per thread drive the conversation-side decision derivation:
	// a reply ON the approval's own thread FROM the expected approver is the
	// verdict ("哪个对话批的"). Receipts never qualify — they start fresh
	// threads by contract (task 309). Map: thread -> set of senders who
	// replied on it (threadId == own id starts a chain, so it is excluded).
	threadSenders := map[string]map[string]bool{}
	for _, row := range rows {
		m := row.Mail
		if m.ThreadID == "" || m.ThreadID == m.ID {
			continue
		}
		if threadSenders[m.ThreadID] == nil {
			threadSenders[m.ThreadID] = map[string]bool{}
		}
		threadSenders[m.ThreadID][m.From] = true
	}

	st := s.loadState()
	seen := map[string]bool{}
	mailboxes := map[string]string{}
	readByID := map[string]bool{}
	var uniq []sessioncollab.HistoryRow
	for _, row := range rows {
		m := row.Mail
		if m.ID == "" || seen[m.ID] {
			continue // contract ②: replayed ids collapse to one entry
		}
		seen[m.ID] = true
		mailboxes[m.ID] = row.Mailbox
		readByID[m.ID] = row.Read
		uniq = append(uniq, row)
	}
	primaries, foldGroups, foldPrimary := foldRows(uniq)

	out := make([]Entry, 0, len(primaries))
	for _, row := range primaries {
		m := row.Mail
		e := Entry{
			ID:           m.ID,
			From:         m.From,
			FromSession:  m.FromSession,
			To:           m.To,
			ToTitle:      m.ToTitle,
			At:           m.At,
			ThreadID:     firstNonEmpty(m.ThreadID, m.ID),
			Hop:          m.Hop,
			CardID:       m.CardID,
			Approver:     m.Approver,
			RequireReply: m.RequireReply,
			Delivery:     m.Delivery,
			Bucket:       s.classify(m),
			Channel:      m.Channel,
			Preview:      previewOf(m.Body),
			Delivered:    true,
			Read:         row.Read,
		}
		// 任务461 P8 ③：折叠簇的整体已读口径——簇内任一副本未消费就仍算未读
		//（MarkRead 会一次性结算整簇）。
		if members := foldGroups[e.ID]; len(members) > 0 {
			e.DuplicateCount = len(members)
			for _, mid := range members {
				if !readByID[mid] {
					e.Read = false
					break
				}
				e.Read = true
			}
		}
		// 任务461 P8 ②：系统类邮件（回执、平台状态通知、投递失败说明——
		// Kind 戳或 system 桶归类）自动视为已读，不计入「用户需处理的未读」。
		// 面板仍可在系统桶查看它们；badge 与 Unread 过滤都不再被它们顶住。
		if e.Bucket == BucketSystem {
			e.Read = true
		}
		if at, ok := st.Dismissed[m.ID]; ok && at > 0 {
			e.Dismissed = true
		}
		if d, ok := st.Decided[m.ID]; ok {
			e.DecidedBy, e.DecidedAt = d.By, d.At
		} else if e.Bucket == BucketApproval {
			// Conversation-side verdict: the expected approver answered on
			// this message's own thread.
			if senders := threadSenders[e.ThreadID]; senders[approverOf(m)] {
				e.DecidedBy = approverOf(m)
				e.DecidedAt = m.At
			}
		}
		out = append(out, e)
	}
	return &foldIndex{
		entries:     out,
		degraded:    historyDegraded,
		foldGroups:  foldGroups,
		foldPrimary: foldPrimary,
		mailboxes:   mailboxes,
	}, nil
}

// foldRows clusters byte-identical mail (same sender, recipient, body) that
// arrived within duplicateFoldWindow of the newest copy (任务461 P8 ③): the
// delivery-layer resend windows block NEW duplicates, folding is the
// consumer-side backstop for copies already on disk (the 9-duplicates
// incident). The newest copy is the cluster primary; rows outside the window
// start their own cluster. Input must be newest-first (History order).
// Thread-continuation rows (explicit thread_id) never fold — identical bodies
// on a conversation thread are legitimate turns (same rule as the delivery
// layer's resend dedup).
func foldRows(uniq []sessioncollab.HistoryRow) (primaries []sessioncollab.HistoryRow, groups map[string][]string, primaryOf map[string]string) {
	type foldKey struct{ from, to, body string }
	clusters := map[foldKey][]sessioncollab.HistoryRow{}
	var keyOrder []foldKey
	for _, row := range uniq {
		if row.Mail.ThreadID != "" && row.Mail.ThreadID != row.Mail.ID {
			primaries = append(primaries, row) // a conversational turn: always its own entry
			continue
		}
		k := foldKey{row.Mail.From, row.Mail.To, row.Mail.Body}
		if _, ok := clusters[k]; !ok {
			keyOrder = append(keyOrder, k)
		}
		clusters[k] = append(clusters[k], row)
	}
	groups = map[string][]string{}
	primaryOf = map[string]string{}
	window := duplicateFoldWindow.Milliseconds()
	for _, k := range keyOrder {
		rowsInKey := clusters[k]
		i := 0
		for i < len(rowsInKey) {
			primary := rowsInKey[i]
			primaries = append(primaries, primary)
			members := []string{primary.Mail.ID}
			primaryOf[primary.Mail.ID] = primary.Mail.ID
			j := i + 1
			for ; j < len(rowsInKey); j++ {
				if primary.Mail.At-rowsInKey[j].Mail.At > window {
					break
				}
				members = append(members, rowsInKey[j].Mail.ID)
				primaryOf[rowsInKey[j].Mail.ID] = primary.Mail.ID
			}
			if len(members) > 1 {
				groups[primary.Mail.ID] = members
			}
			i = j
		}
	}
	sort.SliceStable(primaries, func(a, b int) bool { return primaries[a].Mail.At > primaries[b].Mail.At })
	return primaries, groups, primaryOf
}

// MarkRead advances the recipient seen cursor for the given entry ids (任务
// 461 P8 ③ 批量已读). A folded entry's id expands to every copy in its
// cluster, so one call settles the whole duplicate group. Returns a fresh
// snapshot of the default view.
func (s *Store) MarkRead(ctx context.Context, ids []string) (Snapshot, error) {
	unlock, err := s.lock(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	idx, err := s.buildIndex(ctx)
	if err != nil {
		unlock()
		return Snapshot{}, err
	}
	perMailbox := map[string][]string{}
	for _, id := range ids {
		primary := id
		if p := idx.foldPrimary[id]; p != "" {
			primary = p
		}
		members := append([]string{primary}, idx.foldGroups[primary]...)
		for _, mid := range members {
			mb, ok := idx.mailboxes[mid]
			if !ok {
				continue
			}
			perMailbox[mb] = append(perMailbox[mb], mid)
		}
	}
	for mailbox, mids := range perMailbox {
		if err := s.mail.Ack(ctx, mailbox, mids...); err != nil {
			unlock()
			return Snapshot{}, err
		}
	}
	unlock()
	return s.List(ctx, Query{}, false)
}

// decorate fills the viewer-dependent sub-states (task 320 d).
func decorate(entries []Entry, viewer string) {
	for i := range entries {
		e := &entries[i]
		e.Mine = viewer != "" && e.From == viewer
		e.PendingMe = e.Bucket == BucketApproval && e.DecidedBy == "" &&
			viewer != "" && approverOf(sessioncollab.MailMessage{From: e.From, Approver: e.Approver}) == viewer
	}
}

// ApplyRetention enforces the write-side maintenance against the transport
// layer under one lock hold (task 320 c + 任务 464): the configured retention
// window prunes by AGE, the session-deletion cleanup rule prunes by session
// existence — two independent dimensions, both applied here. Returns how many
// messages were physically removed (both halves combined). forever + never →
// no-op. ctx bounds/cancels the lock wait.
func (s *Store) ApplyRetention(ctx context.Context) (int, error) {
	unlock, err := s.lock(ctx)
	if err != nil {
		return 0, err
	}
	defer unlock()
	st := s.loadState()
	removed := 0
	if cutoff, ok := retentionCutoff(st.Retention, s.now()); ok {
		pruned, perr := s.mail.PruneInbox(ctx, cutoff)
		if perr != nil {
			return 0, perr
		}
		removed += pruned
	}
	cleaned, cerr := s.applyCleanupLocked(ctx, st)
	if cerr != nil {
		return removed, cerr
	}
	removed += cleaned
	if removed > 0 {
		// Dismissals/decisions die WITH their entries (task 320 设计要点 2):
		// garbage-collect state keys whose message no longer exists, then bump
		// the revision so every window resnapshots.
		entries, berr := s.build(ctx)
		if berr == nil {
			alive := make(map[string]bool, len(entries))
			for _, e := range entries {
				alive[e.ID] = true
			}
			for id := range st.Dismissed {
				if !alive[id] {
					delete(st.Dismissed, id)
				}
			}
			for id := range st.Decided {
				if !alive[id] {
					delete(st.Decided, id)
				}
			}
		}
		st.Revision++
	}
	// 任务511 节流闸：成功的 sweep 盖时间戳（含「无事可做」的 sweep——那正是
	// 常态），节流窗口内的下一次面板读据此跳过这次全库重写。失败不盖时间戳，
	// 下一次读会重试。
	st.LastSweepAt = s.now()
	if err := s.saveState(st); err != nil {
		return removed, err
	}
	return removed, nil
}

// sweepDue reports whether the panel read's retention sweep should run now
// (任务511 节流闸): the state file stamps the last successful sweep, and a
// younger one means the next open skips the full-library rewrite. Best-effort
// by design — two windows racing inside the same window may both sweep once,
// which is still a 5-minute frequency, not a per-click one.
func (s *Store) sweepDue() bool {
	last := s.loadState().LastSweepAt
	return last <= 0 || s.now()-last >= sweepThrottleWindow.Milliseconds()
}

// applyCleanupLocked enforces the session-deletion cleanup rule (任务 464).
// Caller holds the store lock. Rule semantics per message:
//
//	sender   — remove when the SENDING session is deleted
//	receiver — remove when the RECEIVING session (the mailbox owner) is deleted
//	both     — remove only when BOTH ends' sessions are deleted
//	never    — keep everything (default; the pre-464 behavior, now pinned)
//
// A contact counts as 「已删除」 only when it is a real sc_ contact absent from
// the liveContacts oracle: empty senders (system mail — 603 such rows observed
// at runtime) and pseudo-identities are NEVER treated as deleted, so platform
// mail cannot be mass-deleted by a sender-side rule. liveContacts==nil (no
// oracle installed) keeps everything — cleanup needs an authoritative roster,
// and guessing would be worse than skipping.
func (s *Store) applyCleanupLocked(ctx context.Context, st stateFile) (int, error) {
	if st.CleanupRule == "" || st.CleanupRule == CleanupNever {
		return 0, nil
	}
	if s.liveContacts == nil {
		return 0, nil
	}
	live := s.liveContacts()
	if live == nil {
		return 0, nil
	}
	deleted := func(contact string) bool {
		contact = strings.TrimSpace(contact)
		if !strings.HasPrefix(contact, "sc_") {
			return false // empty / pseudo identity: never "deleted"
		}
		return !live[contact]
	}
	remove := func(mailbox string, m sessioncollab.MailMessage) bool {
		switch st.CleanupRule {
		case CleanupSender:
			return deleted(m.From)
		case CleanupReceiver:
			return deleted(mailbox)
		case CleanupBoth:
			return deleted(m.From) && deleted(mailbox)
		default:
			return false
		}
	}
	return s.mail.PruneBy(ctx, remove)
}

// List answers one revision-stamped snapshot (task 320 a/e). applyRetention
// should be true for panel calls and false for the read-only query tool — a
// read must never mutate the transport layer. ctx bounds/cancels the lock wait.
//
// 任务461 P11：读路径走共享锁 + 短预算（lockRead）；锁被楔住时降级为无锁读
// （Degraded=true，保留期跳过——降级读绝不改动任何状态），面板有数据而非空。
//
// 320 运行时复盘（2026-10-06）：保留期/清理的写锁在 00:59 与 01:13 被楔住的
// 持有者卡死 5s 超时，旧实现把该错误原样上抛 → 整个 List 失败 → 面板空
// （磁盘上明明有 58 个非空信箱）。现在写侧维护（sweep）失败只跳过本次维护、
// 照常完成读，并把快照标记 Degraded 让桌面端留痕指认最后持锁者——
// 可用性 > 锁完整性。
func (s *Store) List(ctx context.Context, q Query, applyRetention bool) (Snapshot, error) {
	started := time.Now()
	unlock, degraded, err := s.lockRead(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	if unlock != nil {
		defer unlock()
	}
	if applyRetention && !degraded {
		// 保留期/清理是写操作：只在锁健康时执行（先释放共享锁再取写锁）。
		unlock()
		sweepFailed := false
		// 任务511 节流闸：距上次成功 sweep 不足一个窗口时直接跳过——面板每开
		// 一次全库重写一遍是排查实证的自碰撞源。跳过是健康快路径，不算降级。
		if s.sweepDue() {
			if _, rerr := s.sweep(ctx); rerr != nil {
				// 写侧维护失败 ≠ 读失败：跳过本次维护继续读（上抛会把面板打成空）。
				sweepFailed = true
			}
		}
		unlock2, d2, lerr := s.lockRead(ctx)
		if lerr != nil {
			return Snapshot{}, lerr
		}
		if unlock2 != nil {
			defer unlock2()
		}
		degraded = d2 || sweepFailed
	}
	st := s.loadState()
	idx, err := s.buildIndex(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	// 任务511：mail 锁繁忙时 History 返回空行集，若不向上传递，面板会把这个
	// 「锁导致的空」渲染成「暂无信件」——与 320 复盘修掉的空面板同形。锁层
	// 降级必须一路带到快照上（排查报告 §4 缺口 1）。
	if idx.degraded {
		degraded = true
	}
	entries := idx.entries
	decorate(entries, q.Viewer)
	filtered := filterEntries(entries, q)

	sort.SliceStable(filtered, func(i, j int) bool {
		if q.Order == "asc" {
			return filtered[i].At < filtered[j].At
		}
		return filtered[i].At > filtered[j].At
	})

	total := len(filtered)
	limit := q.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	offset := q.Offset
	if offset < 0 {
		offset = 0
	}
	if offset > total {
		offset = total
	}
	page := filtered[offset:]
	if len(page) > limit {
		page = page[:limit]
	}
	// 任务511 复发断根（静默通道堵截）：面板每次打开必须留下一条后端 INFO——
	// 「请求到没到后端、看到了几封信、是否降级、耗时多少」从此一处可查。复发
	// 事故的「点开零后端日志」正是静默通道的特征：这条 INFO 在，即证明请求
	// 落到了数据层；不在，问题在前端绑定/网关（前端侧 reportFrontendLog 已留痕）。
	// 仅 applyRetention=true 的面板路径打点；查询工具、徽标、mutate 回读（false）
	// 不打，避免高频噪音。
	if applyRetention {
		slog.Info("collab inbox: panel read",
			"bucket", q.Bucket, "state", q.State, "order", q.Order, "limit", limit,
			"total", total, "returned", len(page), "degraded", degraded,
			"ms", time.Since(started).Milliseconds())
	}
	return Snapshot{
		Revision:  revisionOf(st.Revision, filtered),
		Settings:  Settings{Retention: st.Retention, CleanupRule: st.CleanupRule},
		Total:     total,
		Returned:  len(page),
		Truncated: offset+len(page) < total,
		Entries:   page,
		Degraded:  degraded,
	}, nil
}

// Chains groups the filtered table by threadId (task 320 g): one row per
// conversation chain, newest chain first, entries chronological inside.
func (s *Store) Chains(ctx context.Context, q Query) (ChainSnapshot, error) {
	unlock, _, err := s.lockRead(ctx)
	if err != nil {
		return ChainSnapshot{}, err
	}
	if unlock != nil {
		defer unlock()
	}
	st := s.loadState()
	entries, err := s.build(ctx)
	if err != nil {
		return ChainSnapshot{}, err
	}
	decorate(entries, q.Viewer)
	filtered := filterEntries(entries, q)

	groups := map[string][]Entry{}
	for _, e := range filtered {
		groups[e.ThreadID] = append(groups[e.ThreadID], e)
	}
	chains := make([]Chain, 0, len(groups))
	for threadID, group := range groups {
		sort.SliceStable(group, func(i, j int) bool { return group[i].At < group[j].At })
		participants := map[string]bool{}
		for _, e := range group {
			participants[e.From] = true
			participants[e.To] = true
		}
		names := make([]string, 0, len(participants))
		for p := range participants {
			names = append(names, p)
		}
		sort.Strings(names)
		chains = append(chains, Chain{
			ThreadID:     threadID,
			Participants: names,
			Count:        len(group),
			FirstAt:      group[0].At,
			LastAt:       group[len(group)-1].At,
			Preview:      group[0].Preview,
			Entries:      group,
		})
	}
	sort.SliceStable(chains, func(i, j int) bool { return chains[i].LastAt > chains[j].LastAt })
	total := len(chains)
	limit := q.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	if len(chains) > limit {
		chains = chains[:limit]
	}
	return ChainSnapshot{
		Revision: revisionOf(st.Revision, filtered),
		Settings: Settings{Retention: st.Retention, CleanupRule: st.CleanupRule},
		Total:    total,
		Chains:   chains,
	}, nil
}

// Dismiss eliminates entries from the default view (task 320 e: 批量 dismiss
// 后返回新快照). Returns the fresh list snapshot so the second window can
// converge on the same revision.
func (s *Store) Dismiss(ctx context.Context, ids []string) (Snapshot, error) {
	return s.mutate(ctx, func(st *stateFile) {
		now := s.now()
		for _, id := range ids {
			if id = strings.TrimSpace(id); id != "" {
				st.Dismissed[id] = now
			}
		}
	})
}

// Undismiss restores previously eliminated entries.
func (s *Store) Undismiss(ctx context.Context, ids []string) (Snapshot, error) {
	return s.mutate(ctx, func(st *stateFile) {
		for _, id := range ids {
			delete(st.Dismissed, strings.TrimSpace(id))
		}
	})
}

// Decide records an approval verdict WITH its decider (task 320 d:
// decidedBy = "human" for a panel click, or the deciding conversation's
// contact_id).
func (s *Store) Decide(ctx context.Context, messageID, by string) (Snapshot, error) {
	messageID = strings.TrimSpace(messageID)
	by = strings.TrimSpace(by)
	if messageID == "" {
		return Snapshot{}, errors.New("collabinbox: message id is required")
	}
	if by == "" {
		return Snapshot{}, errors.New("collabinbox: decider is required (\"human\" or a contact id)")
	}
	return s.mutate(ctx, func(st *stateFile) {
		st.Decided[messageID] = Decision{By: by, At: s.now()}
	})
}

// SetRetention switches the retention window (7d|30d|90d|forever) and applies
// it immediately, so the acceptance "切换 7→30 后超期条目被清理" is one call.
func (s *Store) SetRetention(ctx context.Context, retention string) (Snapshot, error) {
	if !ValidRetention(retention) {
		return Snapshot{}, fmt.Errorf("collabinbox: unknown retention %q (want 7d|30d|90d|forever)", retention)
	}
	snap, err := s.mutate(ctx, func(st *stateFile) {
		st.Retention = retention
	})
	if err != nil {
		return Snapshot{}, err
	}
	if _, err := s.ApplyRetention(ctx); err != nil {
		return snap, err
	}
	return s.List(ctx, Query{Limit: 1}, false)
}

// SetCleanupRule switches the session-deletion cleanup rule (任务 464:
// never|sender|receiver|both) and applies it immediately, so switching to a
// stricter rule cleans in the same call. never (the default) keeps everything.
func (s *Store) SetCleanupRule(ctx context.Context, rule string) (Snapshot, error) {
	if !ValidCleanupRule(rule) {
		return Snapshot{}, fmt.Errorf("collabinbox: unknown cleanup rule %q (want never|sender|receiver|both)", rule)
	}
	snap, err := s.mutate(ctx, func(st *stateFile) {
		st.CleanupRule = rule
	})
	if err != nil {
		return Snapshot{}, err
	}
	if _, err := s.ApplyRetention(ctx); err != nil {
		return snap, err
	}
	return s.List(ctx, Query{Limit: 1}, false)
}

// mutate runs one state write under the lock: load → apply → bump revision →
// save, then returns a fresh snapshot of the default view.
func (s *Store) mutate(ctx context.Context, apply func(*stateFile)) (Snapshot, error) {
	unlock, err := s.lock(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	st := s.loadState()
	apply(&st)
	st.Revision++
	if err := s.saveState(st); err != nil {
		unlock()
		return Snapshot{}, err
	}
	unlock()
	return s.List(ctx, Query{Limit: 1}, false)
}

// Settings reads the persisted panel configuration without touching anything.
func (s *Store) Settings(ctx context.Context) Settings {
	unlock, err := s.lock(ctx)
	if err != nil {
		return Settings{Retention: Retention7d, CleanupRule: CleanupNever}
	}
	defer unlock()
	st := s.loadState()
	return Settings{Retention: st.Retention, CleanupRule: st.CleanupRule}
}

// filterEntries applies query filters to a decorated table.
func filterEntries(entries []Entry, q Query) []Entry {
	out := make([]Entry, 0, len(entries))
	for _, e := range entries {
		if q.Bucket != "" && q.Bucket != BucketAll && e.Bucket != q.Bucket {
			continue
		}
		if q.From != "" && e.From != q.From {
			continue
		}
		if q.To != "" && e.To != q.To {
			continue
		}
		if q.ThreadID != "" && e.ThreadID != q.ThreadID {
			continue
		}
		if q.Since != 0 && e.At < q.Since {
			continue
		}
		if q.Until != 0 && e.At >= q.Until {
			continue
		}
		if q.Unread && e.Read {
			continue
		}
		if !q.IncludeDismissed && e.Dismissed {
			continue
		}
		switch q.State {
		case StatePendingMe:
			if !e.PendingMe {
				continue
			}
		case StateMine:
			if !e.Mine {
				continue
			}
		case StateDecided:
			if e.DecidedBy == "" {
				continue
			}
		}
		out = append(out, e)
	}
	return out
}

// revisionOf composes the snapshot revision from the mutation counter and the
// content itself: state changes bump it, new/removed mail bumps it, dismissals
// bump it twice over. Two windows that print the same revision are looking at
// the same snapshot.
func revisionOf(stateRev int64, entries []Entry) string {
	latest := int64(0)
	for _, e := range entries {
		if e.At > latest {
			latest = e.At
		}
	}
	return fmt.Sprintf("%d.%d.%d", stateRev, len(entries), latest)
}

// retentionCutoff converts a preset to a absolute ms cutoff.
func retentionCutoff(retention string, nowMs int64) (int64, bool) {
	var days int
	switch retention {
	case Retention7d:
		days = 7
	case Retention30d:
		days = 30
	case Retention90d:
		days = 90
	case RetentionForever:
		return 0, false
	default:
		return 0, false
	}
	return nowMs - int64(days)*24*60*60*1000, true
}

// previewOf is the first body line, rune-bounded.
func previewOf(body string) string {
	line := body
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	line = strings.TrimSpace(line)
	const maxRunes = 160
	runes := []rune(line)
	if len(runes) > maxRunes {
		return string(runes[:maxRunes]) + "…"
	}
	return line
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
