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
	Dismissed bool   `json:"dismissed,omitempty"`
	DecidedBy string `json:"decidedBy,omitempty"`
	DecidedAt int64  `json:"decidedAt,omitempty"`
	PendingMe bool   `json:"pendingMe,omitempty"`
	Mine      bool   `json:"mine,omitempty"`
}

// Settings is the persisted panel configuration (task 320 c/f).
type Settings struct {
	Retention string `json:"retention"` // 7d | 30d | 90d | forever
}

// Decision is a recorded approval verdict (task 320 d: 裁决者必须记录).
type Decision struct {
	By string `json:"by"` // "human" or the deciding conversation's contact_id
	At int64  `json:"at"`
}

// stateFile is the on-disk view state: dismissals, decisions, retention.
type stateFile struct {
	Revision  int64               `json:"revision"`
	Dismissed map[string]int64    `json:"dismissed,omitempty"`
	Decided   map[string]Decision `json:"decided,omitempty"`
	Retention string              `json:"retention,omitempty"`
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
)

// Store reads the mail directory through the sessioncollab MailStore and
// keeps the view state beside it. resolver (optional) maps a sender contact
// to its registered identity type (task 348) so heartbeat/system senders
// classify into the right bucket without a body sniff.
type Store struct {
	mailDir  string
	mail     *sessioncollab.MailStore
	resolver func(contact string) string
	now      func() int64 // injectable clock for tests (ms)
}

// New builds a store over mailDir. resolver may be nil.
func New(mailDir string, resolver func(contact string) string) *Store {
	return &Store{
		mailDir:  mailDir,
		mail:     sessioncollab.NewMailStore(mailDir),
		resolver: resolver,
		now:      func() int64 { return time.Now().UnixMilli() },
	}
}

// Mail exposes the underlying transport store (retention / test seams).
func (s *Store) Mail() *sessioncollab.MailStore { return s.mail }

func (s *Store) statePath() string { return filepath.Join(s.mailDir, stateName) }

// lock takes the inbox's cross-process lock. ctx is the caller's request
// context: cancellation (用户点停止) ends the wait immediately, and the wait
// itself never exceeds lockWaitTimeout even when the holder never lets go.
func (s *Store) lock(ctx context.Context) (func(), error) {
	if err := os.MkdirAll(s.mailDir, 0o755); err != nil {
		return nil, err
	}
	release, err := filelock.AcquireWithExternalTimeout(ctx, filepath.Join(s.mailDir, lockName), lockWaitTimeout)
	if err != nil {
		return nil, fmt.Errorf("collab inbox lock busy (held by another window or process?), gave up waiting: %w", err)
	}
	return release, nil
}

func (s *Store) loadState() stateFile {
	st := stateFile{Dismissed: map[string]int64{}, Decided: map[string]Decision{}, Retention: Retention7d}
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

// build reads the transport layer once and derives every entry.
func (s *Store) build(ctx context.Context) ([]Entry, error) {
	rows := s.mail.History(ctx)
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
	out := make([]Entry, 0, len(rows))
	for _, row := range rows {
		m := row.Mail
		if m.ID == "" || seen[m.ID] {
			continue // contract ②: replayed ids collapse to one entry
		}
		seen[m.ID] = true
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
	return out, nil
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

// ApplyRetention enforces the configured retention window against the
// transport layer (task 320 c). Returns how many messages were physically
// removed. forever → no-op. ctx bounds/cancels the lock wait.
func (s *Store) ApplyRetention(ctx context.Context) (int, error) {
	unlock, err := s.lock(ctx)
	if err != nil {
		return 0, err
	}
	defer unlock()
	st := s.loadState()
	cutoff, ok := retentionCutoff(st.Retention, s.now())
	if !ok {
		return 0, nil
	}
	removed, err := s.mail.PruneInbox(ctx, cutoff)
	if err != nil {
		return 0, err
	}
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
		if err := s.saveState(st); err != nil {
			return removed, err
		}
	}
	return removed, nil
}

// List answers one revision-stamped snapshot (task 320 a/e). applyRetention
// should be true for panel calls and false for the read-only query tool — a
// read must never mutate the transport layer. ctx bounds/cancels the lock wait.
func (s *Store) List(ctx context.Context, q Query, applyRetention bool) (Snapshot, error) {
	if applyRetention {
		if _, err := s.ApplyRetention(ctx); err != nil {
			return Snapshot{}, err
		}
	}
	unlock, err := s.lock(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	defer unlock()
	st := s.loadState()
	entries, err := s.build(ctx)
	if err != nil {
		return Snapshot{}, err
	}
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
	return Snapshot{
		Revision:  revisionOf(st.Revision, filtered),
		Settings:  Settings{Retention: st.Retention},
		Total:     total,
		Returned:  len(page),
		Truncated: offset+len(page) < total,
		Entries:   page,
	}, nil
}

// Chains groups the filtered table by threadId (task 320 g): one row per
// conversation chain, newest chain first, entries chronological inside.
func (s *Store) Chains(ctx context.Context, q Query) (ChainSnapshot, error) {
	unlock, err := s.lock(ctx)
	if err != nil {
		return ChainSnapshot{}, err
	}
	defer unlock()
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
		Settings: Settings{Retention: st.Retention},
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
		return Settings{Retention: Retention7d}
	}
	defer unlock()
	return Settings{Retention: s.loadState().Retention}
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
