// Package sessioncollab implements task 19 multi-session collaboration
// foundations: stable contact addressing (141), task cards (145), and a
// durable cross-session mailbox for talk_to_session (142).
//
// Addressing is additive: topicID and file paths keep working; contact_id
// never replaces them. Cards use atomic temp+rename writes.
//
// Delivery is split in two halves. This package owns the durable half: the
// mailbox, its per-contact cursor, and thread-based hop derivation. The live
// half — waking a target session — is host-side (the desktop pump in
// desktop/session_collab.go). A host without that pump can still send, and the
// mail waits durably, but nothing executes it until a host with a pump runs and
// has the target open. "Delivered" therefore means "in the target's inbox", not
// "the target has run it".
package sessioncollab

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"reasonix/internal/baseproc/pidalive"
	"reasonix/internal/filelock"
	"reasonix/internal/store"
)

// Collaboration chain limits. MaxHop is the default ceiling (task 142: the 6th
// hop is refused) and stays the value of every install that never touches the
// experimental hop-limit field (task 204); MinHop and MaxHopCeiling bound that
// field, so a stored ceiling is always inside [MinHop, MaxHopCeiling].
const (
	MinHop        = 3
	MaxHop        = 5
	MaxHopCeiling = 1000
)

// ClampHopLimit normalizes a configured ceiling. Unset (<= 0) keeps the default,
// and out-of-range values are clamped instead of rejected so callers that read a
// hand-edited config still get a usable chain limit.
func ClampHopLimit(limit int) int {
	if limit <= 0 {
		return MaxHop
	}
	if limit < MinHop {
		return MinHop
	}
	if limit > MaxHopCeiling {
		return MaxHopCeiling
	}
	return limit
}

// Identity is one addressable session (task 141).
type Identity struct {
	ContactID   string `json:"contactId"`
	Purpose     string `json:"purpose,omitempty"`
	SessionPath string `json:"sessionPath"`
	TopicID     string `json:"topicId,omitempty"`
	Title       string `json:"title,omitempty"`
	Workspace   string `json:"workspaceRoot,omitempty"`
	Scope       string `json:"scope,omitempty"`
	// Task 348: the structured form of the "【类型】-【编号】-【领域】" naming
	// convention. All three fields are optional — a purpose-only session
	// (every pre-348 sidecar) leaves them empty and no reader changes shape,
	// so the migration is zero by construction.
	IdentityType   string   `json:"identityType,omitempty"`
	IdentityDomain string   `json:"identityDomain,omitempty"`
	Duties         []string `json:"duties,omitempty"`
	// Turns is the sidecar-persisted turn count (task 508), copied through the
	// scan so stat consumers never re-read the meta. Zero-migration like task
	// 348: desktop's roster scans via ScanDir (four-field loader) and never
	// sets it, so its wire is unchanged; 0 omits the key.
	Turns int `json:"turns,omitempty"`
	// Archived marks a session found in the archive: it still has an address,
	// but it is no longer an active participant, and callers must say so rather
	// than reporting it as never registered.
	Archived  bool  `json:"archived,omitempty"`
	UpdatedAt int64 `json:"updatedAt,omitempty"`
}

// Task 348: the canonical identity types — the three-layer architecture roles
// (人 → 主对话 → 子对话, 2026-09-25 ruling) plus the two roles the system
// itself occupies (heartbeat tasks and the system layer). Exactly five values;
// a sixth is rejected at the write boundary instead of stored as a private
// dialect, so every later reader (3b/3c/team) sees one vocabulary.
const (
	IdentityHuman     = "human"     // 人 — the user, decision layer
	IdentityMain      = "main"      // 主对话 — a coordinating conversation
	IdentitySub       = "sub"       // 子对话 — a dispatched worker conversation
	IdentityHeartbeat = "heartbeat" // heartbeat — the scheduled-task runner
	IdentitySystem    = "system"    // 系统 — platform/system registrations
)

// identityTypeAliases accepts the naming-convention spellings alongside the
// canonical English tokens so a caller raised on 「主对话」 and a caller raised
// on "main" converge on the SAME stored value — aliases widen input, never the
// value set.
var identityTypeAliases = map[string]string{
	"人":         IdentityHuman,
	"human":     IdentityHuman,
	"主对话":       IdentityMain,
	"main":      IdentityMain,
	"子对话":       IdentitySub,
	"sub":       IdentitySub,
	"heartbeat": IdentityHeartbeat,
	"系统":        IdentitySystem,
	"system":    IdentitySystem,
}

// NormalizeIdentityType folds an input spelling to its canonical value. The
// empty string passes through (fields are optional by design — task 348: zero
// migration for every purpose-only sidecar); anything outside the five-value
// set is an error, never a silently stored variant.
func NormalizeIdentityType(value string) (string, error) {
	key := strings.ToLower(strings.TrimSpace(value))
	if key == "" {
		return "", nil
	}
	if canonical, ok := identityTypeAliases[key]; ok {
		return canonical, nil
	}
	return "", fmt.Errorf("sessioncollab: unknown identity type %q (want one of: human|main|sub|heartbeat|system)", value)
}

// IdentityTypes returns the canonical value set — for schemas and tests that
// must assert the enum has exactly five members.
func IdentityTypes() []string {
	return []string{IdentityHuman, IdentityMain, IdentitySub, IdentityHeartbeat, IdentitySystem}
}

// CardStatus is the task-card state machine (task 145).
type CardStatus string

const (
	StatusPending CardStatus = "pending"
	StatusRunning CardStatus = "running"
	StatusBlocked CardStatus = "blocked"
	StatusDone    CardStatus = "done"
	StatusFailed  CardStatus = "failed"
)

// CardNode is one hop on a collaboration chain.
type CardNode struct {
	ContactID string `json:"contactId,omitempty"`
	Session   string `json:"sessionPath,omitempty"`
	Role      string `json:"role,omitempty"` // secretariat | expert | self
	At        int64  `json:"at,omitempty"`
	Note      string `json:"note,omitempty"`
}

// Card is the durable process-visibility record (task 145).
type Card struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Status      CardStatus `json:"status"`
	Initiator   string     `json:"initiatorContactId,omitempty"`
	Assignee    string     `json:"assigneeContactId,omitempty"`
	SessionFrom string     `json:"sessionFrom,omitempty"`
	SessionTo   string     `json:"sessionTo,omitempty"`
	Workspace   string     `json:"workspaceRoot,omitempty"`
	Body        string     `json:"body,omitempty"`
	Result      string     `json:"result,omitempty"`
	Error       string     `json:"error,omitempty"`
	Hop         int        `json:"hop,omitempty"`
	Nodes       []CardNode `json:"nodes,omitempty"`
	CreatedAt   int64      `json:"createdAt"`
	UpdatedAt   int64      `json:"updatedAt"`
}

// MailMessage is one cross-session delivery (task 142).
type MailMessage struct {
	ID          string `json:"id"`
	From        string `json:"fromContactId,omitempty"`
	FromSession string `json:"fromSession,omitempty"`
	To          string `json:"toContactId"`
	Body        string `json:"body"`
	Delivery    string `json:"delivery,omitempty"` // followup (default) | steer
	Hop         int    `json:"hop,omitempty"`
	CardID      string `json:"cardId,omitempty"`
	ReplyTo     string `json:"replyToContactId,omitempty"`
	// ThreadID correlates a reply with the message it answers: it is the
	// original message's ID, so a synchronous waiter can match the answer
	// instead of guessing from the sender.
	ThreadID string `json:"threadId,omitempty"`
	// RequireReply marks a message whose sender expects an answer on the same
	// thread (task 173). It rides the record so the recipient — and any
	// reminder pass — can tell a demanded reply from an optional one.
	RequireReply bool `json:"requireReply,omitempty"`
	// Approver overrides the task-source parent as the contact whose Ask
	// channel answers this task's approval prompts (task 225, user ruling).
	// Empty means the sender is the approver. The dispatcher sets it, so the
	// approval route travels WITH the task instead of being guessed later.
	Approver string `json:"approver,omitempty"`
	ToTitle  string `json:"toTitle,omitempty"`
	At       int64  `json:"at"`
	// ReceiptRequested asks for a read receipt (task 309): the sender wants a
	// reply message once this mail enters the recipient's context (turn
	// injection / drain consumption). Delivery-level confirmation already
	// rides the talk_to_session return value, so this covers the read level
	// only. Receipt messages themselves never request receipts — that would
	// make a receipt storm self-sustaining.
	ReceiptRequested bool   `json:"receiptRequested,omitempty"`
	Idempotency      string `json:"idempotency,omitempty"`
	// Kind is the task-320 five-bucket classification stamp (approval |
	// mention | automation | system). Optional: an unstamped message is
	// classified by the index from its fields (approver / sender identity),
	// and platform-generated mail (read receipts, status notes) stamps itself
	// at creation so the system bucket never depends on a text sniff.
	Kind string `json:"kind,omitempty"`
	// Channel is the task-349 group-source stamp (349 挂账 note①): the name
	// of the chat channel this mail was fanned out from, captured at delivery
	// time. Empty = ordinary point-to-point mail. It is presentation-only
	// provenance — the inbox index surfaces it as the entry's group
	// identifier so channel copies stop looking identical to direct sends.
	Channel string `json:"channel,omitempty"`
}

// Delivery semantics for talk_to_session (task 143; default changed to steer
// by task 309). Steer is the mailbox default: it injects mid-turn when the
// target is running and degrades to a queued follow-up when it cannot, so the
// empty value now means "deliver as soon as possible". An explicit followup
// keeps the old queue-until-next-turn semantics.
type Delivery string

const (
	DeliveryFollowup Delivery = "followup"
	DeliverySteer    Delivery = "steer"
)

// ValidateDelivery normalizes an empty value to steer (task 309 mailbox
// default; the steer→followup degradation remains the safety net) and rejects
// unknown values. An explicit "followup" is unchanged.
func ValidateDelivery(value string) (Delivery, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", string(DeliverySteer):
		return DeliverySteer, nil
	case string(DeliveryFollowup):
		return DeliveryFollowup, nil
	default:
		return "", fmt.Errorf("talk_to_session: unknown delivery %q (want followup|steer)", value)
	}
}

// ErrHopLimit is returned when a chain exceeds MaxHop.
// ErrHopLimit reports a chain that reached its ceiling. The ceiling itself is
// appended by the caller, so the message always names the value in force.
var ErrHopLimit = errors.New("talk_to_session: hop limit exceeded")

// ErrNotFound is returned when a contact_id or card is missing.
var ErrNotFound = errors.New("sessioncollab: not found")

func newID(prefix string) string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return prefix + hex.EncodeToString(b[:])
}

func atomicWriteJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + "." + newID("t") + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func appendJSONL(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(b)
	return err
}

// IsMainTranscript reports whether a filename is the primary transcript of a
// session, as opposed to a sidecar. It delegates to store.IsSessionTranscriptName
// — the repo's single authority, already consumed by historycatalog, doctor,
// recovery, and sessiontool. A second predicate here would silently diverge
// (the first cut of this package did exactly that, and `.guardian.jsonl` leaked
// into the directory as a phantom session — incident 2026-09-17, audit F154-4).
// The only extra filter is the collab mailbox's own `.inbox.jsonl` delivery
// files, which live beside sessions but are not sessions.
func IsMainTranscript(name string) bool {
	base := strings.ToLower(filepath.Base(name))
	if !store.IsSessionTranscriptName(base) {
		return false
	}
	// A `.inbox.jsonl` in the collab mailbox is a delivery file, not a session.
	return !strings.HasSuffix(base, ".inbox.jsonl")
}

// MetaInfo is what a directory scan learns from one session's sidecar
// (task 348). OK=false skips the file entirely; the task-348 fields are
// optional and absent for every purpose-only (pre-348) session, so a caller
// that only reads ContactID/Purpose/TopicID/Title sees no change at all.
type MetaInfo struct {
	ContactID      string
	Purpose        string
	TopicID        string
	Title          string
	IdentityType   string
	IdentityDomain string
	Duties         []string
	// Turns is the persisted transcript turn count stamped by the writer into
	// the meta sidecar (task 508). It rides the same sidecar read as every
	// field above — no second file access — and 0 means unknown, never empty.
	Turns int
	OK    bool
}

// ScanDir walks one sessions directory for BranchMeta contact fields.
// workspaceRoot is the root those sessions belong to; it is published on every
// identity so delivery can route to the target's own mailbox rather than the
// sender's. Meta loader is injected so this package stays free of the agent
// import cycle.
//
// It is the thin (task 141) loader over ScanDirMeta: existing callers keep the
// four-field signature, callers that surface identity/duties (task 348) use
// ScanDirMeta directly instead of re-reading the sidecar.
func ScanDir(dir, workspaceRoot string, loadMeta func(sessionPath string) (contactID, purpose, topicID, title string, ok bool)) []Identity {
	return ScanDirMeta(dir, workspaceRoot, func(sessionPath string) MetaInfo {
		contact, purpose, topic, title, ok := loadMeta(sessionPath)
		return MetaInfo{ContactID: contact, Purpose: purpose, TopicID: topic, Title: title, OK: ok}
	})
}

// ScanDirMeta is ScanDir with the task-348 fields on the loader. Everything
// else (filtering, sort order, "contact_id mints on first contact") is
// identical by construction — there is one walk, not two.
func ScanDirMeta(dir, workspaceRoot string, loadMeta func(sessionPath string) MetaInfo) []Identity {
	if strings.TrimSpace(dir) == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []Identity
	for _, e := range entries {
		if e.IsDir() || !IsMainTranscript(e.Name()) {
			continue
		}
		path := filepath.Join(dir, e.Name())
		info := loadMeta(path)
		if !info.OK {
			continue
		}
		// A session with no contact_id yet still belongs in the directory: it is
		// a conversation that can be named by title, and first contact mints the
		// address. Filtering on contact here would make the directory list only
		// people who already spoke, which is the opposite of the point.
		var updated int64
		if stat, err := os.Stat(path); err == nil {
			updated = stat.ModTime().UnixMilli()
		}
		out = append(out, Identity{
			ContactID:      info.ContactID,
			Purpose:        info.Purpose,
			SessionPath:    path,
			TopicID:        info.TopicID,
			Title:          info.Title,
			Workspace:      workspaceRoot,
			IdentityType:   info.IdentityType,
			IdentityDomain: info.IdentityDomain,
			Duties:         info.Duties,
			Turns:          info.Turns,
			UpdatedAt:      updated,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	return out
}

// ResolveContact finds the newest identity matching contactID.
func ResolveContact(ids []Identity, contactID string) (Identity, bool) {
	contactID = strings.TrimSpace(contactID)
	if contactID == "" {
		return Identity{}, false
	}
	for _, id := range ids {
		if id.ContactID == contactID {
			return id, true
		}
	}
	return Identity{}, false
}

// ── task cards (145) ─────────────────────────────────────────────────────────

// StatusAllowed reports whether a status string is part of the card machine.
func StatusAllowed(s CardStatus) bool {
	switch s {
	case StatusPending, StatusRunning, StatusBlocked, StatusDone, StatusFailed:
		return true
	default:
		return false
	}
}

// StatusTransitionAllowed encodes the card state machine. Without it a card
// could go done → running and quietly hide that the earlier run finished (or
// that someone reopened abandoned work), so terminal states are terminal: a
// deliberate reopen goes through pending.
func StatusTransitionAllowed(from, to CardStatus) bool {
	if from == "" {
		from = StatusPending
	}
	if from == to {
		return true
	}
	switch from {
	case StatusPending:
		return to == StatusRunning || to == StatusBlocked || to == StatusDone || to == StatusFailed
	case StatusRunning:
		return to == StatusBlocked || to == StatusDone || to == StatusFailed
	case StatusBlocked:
		return to == StatusRunning || to == StatusDone || to == StatusFailed
	case StatusDone:
		return to == StatusPending // explicit reopen only
	case StatusFailed:
		return to == StatusPending // explicit reopen only
	default:
		return false
	}
}

// lockWaitTimeout bounds one cross-process lock wait for the collab stores
// (task 461 P1, 内层 ≤5s). 可用性 > 锁完整性：锁被别的窗口/进程占住时，操作在
// 预算内返回明确错误，而不是无限挂起。
const lockWaitTimeout = 5 * time.Second

// CardStore persists cards under <root>/.reasonix/taskcards/.
//
// Serialization is a cross-process file lock, not a struct field: every tool
// call builds its own store, so a per-instance mutex would let two concurrent
// read-modify-write cycles interleave and silently drop a node.
type CardStore struct {
	root string
}

func NewCardStore(workspaceRoot string) *CardStore {
	return &CardStore{root: filepath.Join(workspaceRoot, ".reasonix", "taskcards")}
}

// lock serializes one read-modify-write cycle across goroutines and processes.
// lock takes the card directory's cross-process lock. ctx bounds/cancels the
// wait (task 461 P1: 内层 ≤5s, 取消即时生效).
func (s *CardStore) lock(ctx context.Context) (func(), error) {
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return nil, err
	}
	release, err := filelock.AcquireWithExternalTimeout(ctx, filepath.Join(s.root, ".cards.lock"), lockWaitTimeout)
	if err != nil {
		return nil, fmt.Errorf("task card lock busy (held by another window or process?), gave up waiting: %w", err)
	}
	return release, nil
}

func (s *CardStore) path(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		id = "unknown"
	}
	return filepath.Join(s.root, id+".json")
}

func (s *CardStore) Create(ctx context.Context, c Card) (Card, error) {
	unlock, err := s.lock(ctx)
	if err != nil {
		return Card{}, err
	}
	defer unlock()
	if strings.TrimSpace(c.Title) == "" {
		return Card{}, errors.New("task card title is required")
	}
	if c.ID == "" {
		c.ID = newID("card_")
	}
	if c.Status == "" {
		c.Status = StatusPending
	}
	now := time.Now().UnixMilli()
	if c.CreatedAt == 0 {
		c.CreatedAt = now
	}
	c.UpdatedAt = now
	if err := atomicWriteJSON(s.path(c.ID), c); err != nil {
		return Card{}, err
	}
	return c, nil
}

func (s *CardStore) Update(ctx context.Context, id string, mutate func(*Card) error) (Card, error) {
	unlock, err := s.lock(ctx)
	if err != nil {
		return Card{}, err
	}
	defer unlock()
	c, err := s.loadLocked(id)
	if err != nil {
		return Card{}, err
	}
	if err := mutate(&c); err != nil {
		return Card{}, err
	}
	c.UpdatedAt = time.Now().UnixMilli()
	if err := atomicWriteJSON(s.path(id), c); err != nil {
		return Card{}, err
	}
	return c, nil
}

func (s *CardStore) Get(id string) (Card, error) {
	return s.loadLocked(id)
}

func (s *CardStore) List() ([]Card, error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Card
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		c, err := s.loadLocked(id)
		if err != nil {
			continue
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	return out, nil
}

func (s *CardStore) loadLocked(id string) (Card, error) {
	b, err := os.ReadFile(s.path(id))
	if err != nil {
		if os.IsNotExist(err) {
			return Card{}, fmt.Errorf("%w: task card %q", ErrNotFound, id)
		}
		return Card{}, err
	}
	var c Card
	if err := json.Unmarshal(b, &c); err != nil {
		return Card{}, err
	}
	return c, nil
}

// ── pending purposes (144) ───────────────────────────────────────────────────

// PendingPurposeStore records "this topic should register this purpose" for a
// session that does not exist yet. Topic creation and the session transcript
// are separate moments in the desktop, so a self-organising secretary cannot
// stamp purpose at creation time; the delivery pump applies these once the
// session path appears.
type PendingPurposeStore struct {
	path string
}

func NewPendingPurposeStore(mailboxDir string) *PendingPurposeStore {
	return &PendingPurposeStore{path: filepath.Join(mailboxDir, "pending-purpose.json")}
}

// lock takes the pending-purpose file's cross-process lock, bounded by
// lockWaitTimeout (task 461 P1). Called from the desktop purpose pump (no
// request context), so callers pass context.Background() — the budget, not
// cancellation, is what bounds these waits.
func (s *PendingPurposeStore) lock(ctx context.Context) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return nil, err
	}
	release, err := filelock.AcquireWithExternalTimeout(ctx, s.path+".lock", lockWaitTimeout)
	if err != nil {
		return nil, fmt.Errorf("pending purpose lock busy (held by another window or process?), gave up waiting: %w", err)
	}
	return release, nil
}

func (s *PendingPurposeStore) load() map[string]string {
	b, err := os.ReadFile(s.path)
	if err != nil {
		return map[string]string{}
	}
	out := map[string]string{}
	if err := json.Unmarshal(b, &out); err != nil {
		return map[string]string{}
	}
	return out
}

// Set records a purpose for a topic id.
func (s *PendingPurposeStore) Set(topicID, purpose string) error {
	topicID, purpose = strings.TrimSpace(topicID), strings.TrimSpace(purpose)
	if topicID == "" || purpose == "" {
		return errors.New("pending purpose: topic id and purpose are required")
	}
	unlock, err := s.lock(context.Background())
	if err != nil {
		return err
	}
	defer unlock()
	m := s.load()
	m[topicID] = purpose
	return atomicWriteJSON(s.path, m)
}

// List returns a copy of the pending map.
func (s *PendingPurposeStore) List() map[string]string {
	unlock, err := s.lock(context.Background())
	if err != nil {
		return nil
	}
	defer unlock()
	src := s.load()
	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

// Clear removes one topic's pending purpose after it was applied.
func (s *PendingPurposeStore) Clear(topicID string) error {
	unlock, err := s.lock(context.Background())
	if err != nil {
		return err
	}
	defer unlock()
	m := s.load()
	if _, ok := m[topicID]; !ok {
		return nil
	}
	delete(m, topicID)
	return atomicWriteJSON(s.path, m)
}

// ── mailbox (142) ────────────────────────────────────────────────────────────

// MailStore is a durable cross-session mailbox: one directory holding
// <contactId>.inbox.jsonl plus a per-contact seen-cursor. The directory is the
// single shared collab root (config.SessionCollabMailDir), not a workspace, so
// both sides of an exchange always agree on where a contact's mail lives.
// Appends are serialized by a cross-process file lock so two senders cannot
// interleave a partial line or drop one another's message.
type MailStore struct {
	root string
	// hopLimit is this store's chain ceiling (task 204). It lives on the instance
	// rather than in package state so a sender using a different configured limit
	// cannot race a reader, and 0 keeps the package default.
	hopLimit int
	// listDir is the directory-listing seam (任务511 复发③): nil means
	// os.ReadDir. A test injects a failure here to pin the "cannot see the
	// library ⇒ degraded, never a silent empty" contract.
	listDir func(name string) ([]os.DirEntry, error)
}

func NewMailStore(mailboxDir string) *MailStore {
	return &MailStore{root: mailboxDir, hopLimit: MaxHop}
}

// NewMailStoreWithHopLimit builds a store whose Deliver/Claim enforce a configured
// ceiling (task 204). The value is clamped, so an out-of-range setting never widens
// or narrows a chain beyond MinHop..MaxHopCeiling.
func NewMailStoreWithHopLimit(mailboxDir string, hopLimit int) *MailStore {
	return &MailStore{root: mailboxDir, hopLimit: ClampHopLimit(hopLimit)}
}

// HopLimit reports the ceiling this store enforces.
func (s *MailStore) HopLimit() int { return s.ceiling() }

func (s *MailStore) ceiling() int {
	if s == nil || s.hopLimit <= 0 {
		return MaxHop
	}
	return s.hopLimit
}

// lockHolderPath is a SIDECAR beside .mail.lock (任务511): the lock file's byte
// range is held via LockFileEx, so diagnostics live in a separate file — same
// shape as the collab-inbox lock's holder sidecar. Content survives release:
// informative about the LAST holder, never authoritative.
func (s *MailStore) lockHolderPath() string { return filepath.Join(s.root, ".mail.lock.holder") }

// writeLockHolderInfo stamps the sidecar right after an exclusive acquire, so a
// later waiter that gives up can name WHO held it last and since when.
func (s *MailStore) writeLockHolderInfo() {
	info := fmt.Sprintf("pid=%d held_since=%s", os.Getpid(), time.Now().Format(time.RFC3339))
	_ = os.WriteFile(s.lockHolderPath(), []byte(info), 0o600)
}

// LockHolderInfo returns the last recorded holder identity from the sidecar
// (may be empty or stale — it is a diagnostic, not a lease).
func (s *MailStore) LockHolderInfo() string {
	b, err := os.ReadFile(s.lockHolderPath())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// clearStaleHolderIfDead is the mail-lock twin of collabinbox's recycle
// (任务511 复发断根): a holder sidecar naming a DEAD pid is a stale diagnostic
// — the OS lock died with the process — so remove it and log what was cleared,
// so waiters and the degraded-read warn stop naming a zombie. 防误杀边界与
// 竞态窗口分析同 collabinbox.Store.clearStaleHolderIfDead（同一契约，两把锁
// 各自治理各自的诊断文件）：只删诊断文件、绝不触碰锁文件与 OS 锁；pid 存活
// （含复用后看似存活）或不可解析 ⇒ 一字不动。
func (s *MailStore) clearStaleHolderIfDead() string {
	info := s.LockHolderInfo()
	if info == "" {
		return ""
	}
	pid := pidalive.ParseHolderPid(info)
	if pid <= 0 {
		return "" // unparsable: cannot judge — leave it, never guess
	}
	if pidalive.Alive(pid) {
		return "" // live holder (or pid reuse): NEVER touch a live holder's record
	}
	if err := os.Remove(s.lockHolderPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Warn("session collab mail: stale lock holder sidecar removal failed",
			"path", s.lockHolderPath(), "stale_holder", info, "pid", pid, "err", err)
		return ""
	}
	slog.Info("session collab mail: stale lock holder sidecar cleared (recorded holder process is gone; the OS lock died with it)",
		"stale_holder", info, "pid", pid)
	return info
}

// lock takes the mail directory's cross-process lock. ctx is the caller's
// request context where one exists (task 461 P1): cancellation ends the wait
// immediately, and the wait itself never exceeds lockWaitTimeout even when the
// holder never lets go (可用性 > 锁完整性).
func (s *MailStore) lock(ctx context.Context) (func(), error) {
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return nil, err
	}
	release, err := filelock.AcquireWithExternalTimeout(ctx, filepath.Join(s.root, ".mail.lock"), lockWaitTimeout)
	if err != nil {
		// 任务511 复发断根：侧车指认的 pid 已死 ⇒ 陈旧诊断，清掉留痕——
		// 紧随其后的 degraded warn 不再指认一个早已死亡的进程。
		if s.clearStaleHolderIfDead() != "" {
			return nil, fmt.Errorf("session collab mail lock busy, gave up waiting (stale holder sidecar cleared — the recorded holder process is gone, a live holder may still exist): %w", err)
		}
		return nil, fmt.Errorf("session collab mail lock busy (held by another window or process?), gave up waiting: %w", err)
	}
	s.writeLockHolderInfo()
	return release, nil
}

// Dir is the mailbox root. Exposed so callers and tests can locate a contact's
// files without re-deriving the layout.
func (s *MailStore) Dir() string { return s.root }

// InboxPath is the append-only inbox file for one contact.
func (s *MailStore) InboxPath(contactID string) string { return s.inboxPath(contactID) }

func (s *MailStore) inboxPath(contactID string) string {
	contactID = strings.TrimSpace(contactID)
	if contactID == "" {
		contactID = "unknown"
	}
	return filepath.Join(s.root, contactID+".inbox.jsonl")
}

// 重发窗口（任务461 P8 ①）：普通邮件 30 分钟——窗内同内容重发按重试处理；
// 带 system/automation 戳的邮件（回执、心跳确认、平台状态通知）24 小时——
// 这类消息天然幂等，正文相同即视为同一条，无论隔多久重发。
const (
	resendDedupWindowDefault = 30 * time.Minute
	resendDedupWindowSystem  = 24 * time.Hour
)

func resendDedupWindow(kind string) time.Duration {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "system", "automation":
		return resendDedupWindowSystem
	}
	return resendDedupWindowDefault
}

// dedupeResend reports an existing inbox message that this delivery
// duplicates: same sender, same recipient, byte-identical body, inside the
// resend window. It only applies to mail that STARTS ITS OWN THREAD (empty
// thread_id): dispatch/notice/confirmation resends open a fresh thread every
// pass while carrying identical content, which is exactly the re-entry
// pollution of 任务461 P8. Conversational replies (explicit thread_id) are
// deliberate turns — identical bodies on a thread are legitimate and never
// collapse here. The scan key is (from, to, content) among fresh-thread rows,
// so a retry loop minting fresh ids every pass still collapses onto the
// original. Metadata differences beyond from/to/body (receipt flag, card id,
// hop) are treated as part of the retry and ride the ORIGINAL message's
// metadata.
func (s *MailStore) dedupeResend(msg MailMessage) (MailMessage, bool) {
	if strings.TrimSpace(msg.ThreadID) != "" {
		return MailMessage{}, false
	}
	all, err := s.readAll(msg.To)
	if err != nil {
		return MailMessage{}, false
	}
	window := resendDedupWindow(msg.Kind).Milliseconds()
	now := time.Now().UnixMilli()
	for _, m := range all {
		if m.ID == "" || m.Body != msg.Body || m.From != msg.From || m.To != msg.To {
			continue
		}
		// 只与同样「自成一线」的历史行比较：对话线程里的同内容回复是正常轮次。
		if m.ThreadID != "" && m.ThreadID != m.ID {
			continue
		}
		if m.At > 0 && now-m.At > window {
			continue
		}
		return m, true
	}
	return MailMessage{}, false
}

// ErrHopWithoutParentThread marks a send that claims chain depth (hop>0)
// without naming a parent thread. Task 548 P0-1: such mail used to be accepted
// at write time, its empty threadId silently stamped to its own id, and only
// then dropped by the consuming pump's provenance gate ("hop=N claimed but
// threadId does not name a parent") — with the refusal still settling the seen
// cursor, so the sender auditing "SEEN" measured its own dead letter. Deliver
// now refuses the combination outright: the error rides the caller's return
// value (the model can self-correct and resend) and nothing reaches disk.
var ErrHopWithoutParentThread = errors.New("hop claimed without a parent thread")

// Deliver appends a message for the target contact. hop is the sender's chain
// depth; the store's ceiling + 1 is refused. ctx is the sender's request
// context: a user stop ends a contended lock wait immediately (task 461 P1).
func (s *MailStore) Deliver(ctx context.Context, msg MailMessage) (MailMessage, error) {
	unlock, err := s.lock(ctx)
	if err != nil {
		return MailMessage{}, err
	}
	defer unlock()
	if strings.TrimSpace(msg.To) == "" {
		return MailMessage{}, errors.New("talk_to_session: target contact_id is required")
	}
	if strings.TrimSpace(msg.Body) == "" {
		return MailMessage{}, errors.New("talk_to_session: message body is required")
	}
	if limit := s.ceiling(); msg.Hop > limit {
		return MailMessage{}, fmt.Errorf("%w (max %d): hop=%d", ErrHopLimit, limit, msg.Hop)
	}
	delivery, err := ValidateDelivery(msg.Delivery)
	if err != nil {
		return MailMessage{}, err
	}
	msg.Delivery = string(delivery)
	// 任务461 P8 ①：投递层内容幂等。同一发送方→同一收件方的完全同内容消息在
	// 重发窗口内不再落新行，直接返回原消息（原 id/thread/at）——重入循环
	// （同一内容反复投递、每次新 id）不再污染收件箱和未读数；工具文档长期
	// 宣称的「重发返原 id」自此在投递层成立。窗口外的真实新消息不受影响。
	if orig, ok := s.dedupeResend(msg); ok {
		return orig, nil
	}
	if msg.ID == "" {
		msg.ID = newID("msg_")
	}
	// 任务548 P0-1（源头拒发）：「声称链深（hop>0）却无可解析父线程」在这里
	// 即拒，与消费泵 verifyHop 的判据精确对偶（threadId 为空或自指 ⇒ 盖章后
	// isReply=false，而 hop>0 的 isReply=false 必被溯源门拒收）。历史上这类
	// 消息被静默盖章落盘、随后被泵丢弃且拒收仍结算 seen——发送方看到的
	// "queued"+"SEEN" 全部失真。拒绝必须发生在写盘之前。
	if msg.Hop > 0 && (strings.TrimSpace(msg.ThreadID) == "" || msg.ThreadID == msg.ID) {
		return MailMessage{}, fmt.Errorf("%w: hop=%d claimed but threadId does not name a parent — pass the thread_id of the inbound message you are answering, or omit hop (hop=0) to start a new chain",
			ErrHopWithoutParentThread, msg.Hop)
	}
	// A message that does not continue an existing thread starts one, so a
	// synchronous waiter always has an id to match its answer against.
	if strings.TrimSpace(msg.ThreadID) == "" {
		msg.ThreadID = msg.ID
	}
	if msg.At == 0 {
		msg.At = time.Now().UnixMilli()
	}
	if err := appendJSONL(s.inboxPath(msg.To), msg); err != nil {
		return MailMessage{}, err
	}
	return msg, nil
}

// Claim returns messages the contact has not seen yet WITHOUT advancing the
// cursor. The caller must Ack what it actually delivered.
//
// This is deliberately two-phase. Advancing the cursor here would make delivery
// at-most-once: any failure after the claim (target controller not ready, disk
// error, process death) would drop the message forever, because the next claim
// no longer sees it. At-least-once is the correct guarantee for cross-session
// work, and the inbox's own idempotency key ("collab:<id>") keeps a redelivery
// from duplicating a turn.
//
// The hop ceiling is enforced here as well as on write: a caller-reported hop
// is not trustworthy, so an over-limit message is refused rather than handed to
// the target. Refused messages are reported to the caller, which Acks them so
// they do not reappear on every pass.
//
// Explicit contract (audit-2 ⑥③): Claim is NOT exclusive. The cursor is
// read-only here and advances only in Ack, so two Claim callers observe the
// same batch until one of them acks. That shape is safe ONLY because the two
// real consumers can never coexist: the pump's two-phase Claim→Ack and
// drain_inbox's same-lock Drain are mutually exclusive by the once-per-process
// boot gate (internal/boot/collab_drain_gate.go, M-a). Any new consumer of
// this cursor must join that exclusion, not assume Claim admits it.
func (s *MailStore) Claim(ctx context.Context, contactID string) (pending []MailMessage, refused []MailMessage, err error) {
	unlock, err := s.lock(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer unlock()
	return s.claimLocked(contactID)
}

// claimLocked is Claim's body under the caller's lock (B1: same-lock Claim+Ack).
func (s *MailStore) claimLocked(contactID string) (pending []MailMessage, refused []MailMessage, err error) {
	all, err := s.readAll(contactID)
	if err != nil {
		return nil, nil, err
	}
	seen := s.readCursor(contactID)
	limit := s.ceiling()
	for _, m := range all {
		if seen[m.ID] {
			continue
		}
		if m.Hop > limit {
			refused = append(refused, m)
			continue
		}
		pending = append(pending, m)
	}
	return pending, refused, nil
}

// Ack advances the cursor for IDs whose delivery is settled — delivered
// successfully, or refused for good. Not acking leaves them for the next pass.
func (s *MailStore) Ack(ctx context.Context, contactID string, ids ...string) error {
	if len(ids) == 0 {
		return nil
	}
	unlock, err := s.lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	return s.ackLocked(contactID, ids...)
}

// Settled reports whether messageID has already been consumed from the
// contact's mailbox (the seen cursor). Task 263: the session-inbox recovery
// probe uses it to drop in-flight items whose source message was consumed
// before an update restart, so consumed messages never replay onto the
// guidance shelf. Read-only; safe under the Store transaction lock.
func (s *MailStore) Settled(contactID, messageID string) bool {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return false
	}
	return s.readCursor(contactID)[messageID]
}

// ackLocked is Ack's body under the caller's lock (B1: same-lock Claim+Ack).
func (s *MailStore) ackLocked(contactID string, ids ...string) error {
	seen := s.readCursor(contactID)
	for _, id := range ids {
		if strings.TrimSpace(id) != "" {
			seen[id] = true
		}
	}
	return s.writeCursor(contactID, seen)
}

// AwaitReply waits for a message on threadID addressed to contactID and
// consumes it, so the delivery pass does not hand the same answer to the
// requester a second time: the synchronous caller already received it as its
// tool result, and a second copy would make the requester act on it twice.
//
// Only the matching message is consumed; everything else stays queued.
func (s *MailStore) AwaitReply(contactID, threadID string, timeout time.Duration) (MailMessage, bool) {
	return s.AwaitReplyContext(context.Background(), contactID, threadID, timeout)
}

// AwaitReplyContext is AwaitReply bound to the caller's context.
//
// The wait must end when the caller's turn does: a cancelled turn (user stop,
// superseded request) that keeps this goroutine asleep for the full timeout
// holds a tool call open past the work it belongs to. Cancellation reports the
// same "no reply yet" result as a timeout — the request is already delivered,
// so there is nothing to undo — and the answer still arrives in the inbox.
func (s *MailStore) AwaitReplyContext(ctx context.Context, contactID, threadID string, timeout time.Duration) (MailMessage, bool) {
	if strings.TrimSpace(threadID) == "" {
		return MailMessage{}, false
	}
	if ctx == nil {
		ctx = context.Background()
	}
	deadline := time.Now().Add(timeout)
	for {
		all, err := s.readAll(contactID)
		if err == nil {
			for _, m := range all {
				if m.ThreadID == threadID {
					// Best-effort: a failed ack costs a duplicate, not a loss.
					_ = s.Ack(ctx, contactID, m.ID)
					return m, true
				}
			}
		}
		if err := ctx.Err(); err != nil {
			return MailMessage{}, false
		}
		if time.Now().After(deadline) {
			return MailMessage{}, false
		}
		sleep := 200 * time.Millisecond
		if remaining := time.Until(deadline); remaining < sleep {
			sleep = remaining
		}
		if sleep > 0 {
			// Poll in context-sized slices so a cancel is noticed promptly
			// instead of after the whole sleep.
			select {
			case <-ctx.Done():
				return MailMessage{}, false
			case <-time.After(sleep):
			}
		}
	}
}

// ParentThread finds the message a reply answers, by id, in the mailbox that
// sent it. Chain depth is derived from this record rather than from the
// sender's claim, which is the only way a hop ceiling can be enforced.
func (s *MailStore) ParentThread(contactID, threadID string) (MailMessage, bool) {
	if strings.TrimSpace(contactID) == "" || strings.TrimSpace(threadID) == "" {
		return MailMessage{}, false
	}
	all, err := s.readAll(contactID)
	if err != nil {
		return MailMessage{}, false
	}
	for _, m := range all {
		if m.ID == threadID {
			return m, true
		}
	}
	return MailMessage{}, false
}

// InboxStatus reports, read-only, how many messages a contact has not consumed
// and when the inbox last heard a delivery (task 218). No cursor is advanced:
// a status probe must never cost the target its own pending mail, and a caller
// that cannot see the target's process still gets honest counters from here.
func (s *MailStore) InboxStatus(contactID string) (unread int, lastDeliveryAt int64) {
	unlock, err := s.lock(context.Background())
	if err != nil {
		return 0, 0
	}
	defer unlock()
	all, err := s.readAll(contactID)
	if err != nil {
		return 0, 0
	}
	seen := s.readCursor(contactID)
	var last int64
	for _, m := range all {
		if !seen[m.ID] {
			unread++
		}
		if m.At > last {
			last = m.At
		}
	}
	return unread, last
}

// CountSentFromToday counts how many messages the given contact has sent
// today, across every mailbox in the store (task 173 ⑥). The anti-storm cap
// needs the sender's own daily volume, not one target's inbox. Calendar-day
// boundaries follow the local clock — the same clock the daily-limit setting
// is reasoned about in.
func (s *MailStore) CountSentFromToday(fromContact string) int {
	fromContact = strings.TrimSpace(fromContact)
	if fromContact == "" {
		return 0
	}
	start := time.Now().Truncate(24 * time.Hour)
	matches, err := filepath.Glob(filepath.Join(s.root, "*.inbox.jsonl"))
	if err != nil {
		return 0
	}
	count := 0
	for _, path := range matches {
		contact := strings.TrimSuffix(filepath.Base(path), ".inbox.jsonl")
		all, err := s.readAll(contact)
		if err != nil {
			continue
		}
		for _, m := range all {
			if m.From == fromContact && m.At >= start.UnixMilli() {
				count++
			}
		}
	}
	return count
}

// RecordSent appends an outgoing message to the sender's own sent log (task
// 175). The inbox only shows what a session received; without a sent record a
// misdirected send was invisible on the sender's side, which is how a batch
// reply once crossed wires for hours. Best-effort: a sent-log failure must
// never fail a delivery that already landed.
func (s *MailStore) RecordSent(ctx context.Context, msg MailMessage, toTitle string) {
	if strings.TrimSpace(msg.From) == "" || strings.TrimSpace(msg.ID) == "" {
		return
	}
	entry := msg
	entry.ToTitle = strings.TrimSpace(toTitle)
	unlock, err := s.lock(ctx)
	if err != nil {
		return
	}
	defer unlock()
	_ = appendJSONL(filepath.Join(s.root, msg.From+".sent.jsonl"), entry)
}

// ListSent returns the sender's own outgoing log, newest first (task 175).
// limit <= 0 returns everything. The query that surfaces this log ships as a
// merged action under task 174's tool consolidation, not as tool #15.
func (s *MailStore) ListSent(fromContact string, limit int) []MailMessage {
	fromContact = strings.TrimSpace(fromContact)
	if fromContact == "" {
		return nil
	}
	unlock, err := s.lock(context.Background())
	if err != nil {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(s.root, fromContact+".sent.jsonl"))
	unlock()
	if err != nil {
		return nil
	}
	var out []MailMessage
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var m MailMessage
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			continue
		}
		out = append(out, m)
	}
	// Newest first: the last thing you sent is the first thing to check.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// A reply names the message it answers in thread_id. Two ways of naming the wrong
// one are refused here rather than silently dropping the message later:
//
//   - ErrReplyThreadUnknown: the id is not a message in the sender's OWN mailbox.
//     The classic mistake is passing the id of the message you just sent (which
//     lives in the peer's mailbox) — task 194.
//   - ErrReplyThreadCrossWired: the thread was opened by someone other than the peer
//     being answered, so continuing it would splice two conversations together —
//     task 156.B.
//
// The predicates live here so the tool layer and the delivery pump cannot drift:
// the tool calls it before writing anything (the sender learns from its own return
// value) and the pump calls it again as the authoritative gate.
var (
	ErrReplyThreadUnknown    = errors.New("talk_to_session: thread_id is not a message in your own mailbox")
	ErrReplyThreadCrossWired = errors.New("talk_to_session: thread_id belongs to a thread with another peer")
)

// ResolveReplyParent validates the thread a message answers and returns that parent.
// isReply is false for a message that starts a new chain (no thread_id, or a
// thread_id equal to its own id).
func (s *MailStore) ResolveReplyParent(msg MailMessage) (parent MailMessage, isReply bool, err error) {
	threadID := strings.TrimSpace(msg.ThreadID)
	if threadID == "" || threadID == msg.ID {
		return MailMessage{}, false, nil
	}
	from := strings.TrimSpace(msg.From)
	if from == "" {
		return MailMessage{}, true, fmt.Errorf("reply has no sender to resolve thread %s", threadID)
	}
	parent, ok := s.ParentThread(from, threadID)
	if !ok {
		return MailMessage{}, true, fmt.Errorf("%w: %s is not in %s's mailbox — pass the id of the inbound message you received (it sits in your own inbox), or omit thread_id to start a new chain",
			ErrReplyThreadUnknown, threadID, from)
	}
	if to := strings.TrimSpace(msg.To); to != "" && strings.TrimSpace(parent.From) != to {
		return MailMessage{}, true, fmt.Errorf("%w: thread %s was opened by %s, not %s",
			ErrReplyThreadCrossWired, threadID, parent.From, to)
	}
	return parent, true, nil
}

// MarkNotified records that a status note was sent for key, returning true only
// the first time. It lets a caller notify once without giving up the retry, so
// a target that stays unavailable is never silent but also never a flood.
func (s *MailStore) MarkNotified(contactID, key string) bool {
	if strings.TrimSpace(contactID) == "" || strings.TrimSpace(key) == "" {
		return false
	}
	unlock, err := s.lock(context.Background())
	if err != nil {
		return false
	}
	defer unlock()
	notified := s.readNotified(contactID)
	if notified[key] {
		return false
	}
	notified[key] = true
	if err := atomicWriteJSON(s.notifiedPath(contactID), notified); err != nil {
		return false
	}
	return true
}

func (s *MailStore) notifiedPath(contactID string) string {
	return filepath.Join(s.root, strings.TrimSpace(contactID)+".notified.json")
}

func (s *MailStore) readNotified(contactID string) map[string]bool {
	out := map[string]bool{}
	b, err := os.ReadFile(s.notifiedPath(contactID))
	if err != nil {
		return out
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return map[string]bool{}
	}
	return out
}

// InboxMessages returns every message ever delivered to contactID's mailbox
// file — the settled (already consumed into a turn) ones included, unlike
// Peek which reads only the un-consumed remainder. Task 530's reply-reminder
// scan needs exactly this: a require_reply mail stays answerable — and stays
// owed — after its delivery ack, so the reminder pass must still see it.
// Read-only; newest last (file order).
func (s *MailStore) InboxMessages(contactID string) ([]MailMessage, error) {
	unlock, err := s.lock(context.Background())
	if err != nil {
		return nil, err
	}
	defer unlock()
	return s.readAll(contactID)
}

// InboxWithCursor is InboxMessages plus the contact's settled cursor in one
// locked read (任务 570 c2): the peek surface needs the settled flag for every
// row, and re-reading the cursor file per row would be O(n) syscalls for the
// same small JSON. Read-only — no claim, no ack, the cursor is not written.
func (s *MailStore) InboxWithCursor(contactID string) ([]MailMessage, map[string]bool, error) {
	unlock, err := s.lock(context.Background())
	if err != nil {
		return nil, nil, err
	}
	defer unlock()
	all, err := s.readAll(contactID)
	if err != nil {
		return nil, nil, err
	}
	return all, s.readCursor(contactID), nil
}

// PendingContacts lists contacts that have at least one un-acked message.
// It is what lets a host discover *which* sessions need waking, including ones
// with no open tab — delivery must not depend on the target already being on
// screen.
func (s *MailStore) PendingContacts() []string {
	unlock, err := s.lock(context.Background())
	if err != nil {
		return nil
	}
	defer unlock()
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".inbox.jsonl") {
			continue
		}
		contact := strings.TrimSuffix(name, ".inbox.jsonl")
		all, err := s.readAll(contact)
		if err != nil {
			continue
		}
		seen := s.readCursor(contact)
		for _, m := range all {
			if !seen[m.ID] {
				out = append(out, contact)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

// Peek returns unread messages without advancing the cursor.
func (s *MailStore) Peek(contactID string) ([]MailMessage, error) {
	all, err := s.readAll(contactID)
	if err != nil {
		return nil, err
	}
	seen := s.readCursor(contactID)
	var out []MailMessage
	for _, m := range all {
		if !seen[m.ID] {
			out = append(out, m)
		}
	}
	return out, nil
}

func (s *MailStore) cursorPath(contactID string) string {
	contactID = strings.TrimSpace(contactID)
	if contactID == "" {
		contactID = "unknown"
	}
	return filepath.Join(s.root, contactID+".seen.json")
}

func (s *MailStore) readCursor(contactID string) map[string]bool {
	seen := map[string]bool{}
	b, err := os.ReadFile(s.cursorPath(contactID))
	if err != nil {
		return seen
	}
	var ids []string
	if err := json.Unmarshal(b, &ids); err != nil {
		return seen
	}
	for _, id := range ids {
		seen[id] = true
	}
	return seen
}

func (s *MailStore) writeCursor(contactID string, seen map[string]bool) error {
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return atomicWriteJSON(s.cursorPath(contactID), ids)
}

func (s *MailStore) readAll(contactID string) ([]MailMessage, error) {
	b, err := os.ReadFile(s.inboxPath(contactID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []MailMessage
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var m MailMessage
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			continue
		}
		out = append(out, m)
	}
	return out, nil
}

// Inbox returns all messages for a contact (newest last), read or not.
func (s *MailStore) Inbox(contactID string) ([]MailMessage, error) {
	b, err := os.ReadFile(s.inboxPath(contactID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []MailMessage
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var m MailMessage
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			continue
		}
		out = append(out, m)
	}
	return out, nil
}

// HistoryRow pairs one delivered message with the mailbox that holds it and
// that mailbox's read state (task 320). "Delivered" is by construction: a row
// exists only because the message sits in a recipient's inbox file — queued /
// staged sends (sent-log only) never appear, which is the notification
// contract's "真实落库后才提醒" made structural.
type HistoryRow struct {
	Mail    MailMessage
	Mailbox string // the recipient contact whose inbox file holds the row
	Read    bool   // the recipient's seen cursor covers this id
}

// listDirOrDefault resolves the directory-listing seam (任务511 复发③):
// the injected failure source in tests, os.ReadDir in production.
func (s *MailStore) listDirOrDefault(name string) ([]os.DirEntry, error) {
	if s.listDir != nil {
		return s.listDir(name)
	}
	return os.ReadDir(name)
}

// History returns every delivered message across every inbox in the store,
// newest first. One lock, one cursor read per mailbox — the task-320 index
// consumer, so it must not re-read a cursor per message.
// History reads every mailbox into the unified table rows (task 320 read
// side). ctx bounds/cancels the lock wait.
//
// 任务511：拿不到 .mail.lock 时不再静默返回 nil——那会让收件箱面板在锁繁忙时
// 渲染成「暂无信件」且零痕迹（排查报告 §4 缺口 1）。现在留一条 slog.Warn
// （锁路径 + last_holder + wait_ms，对齐桌面端 degraded unlocked read 的日志
// 形态），并把 degraded=true 返回给索引层，快照据此带 Degraded，前端显示
// 「收件箱暂时不可用」而非空态。可用性 > 锁完整性。
func (s *MailStore) History(ctx context.Context) ([]HistoryRow, bool) {
	start := time.Now()
	unlock, err := s.lock(ctx)
	if err != nil {
		slog.Warn("session collab mail: degraded history read (lock busy)",
			"lock", filepath.Join(s.root, ".mail.lock"),
			"last_holder", s.LockHolderInfo(),
			"wait_ms", time.Since(start).Milliseconds())
		return nil, true
	}
	defer unlock()
	entries, err := s.listDirOrDefault(s.root)
	if err != nil {
		// 任务511 复发③：读不到信箱库 ≠ 没有信件。旧行为 (nil, false) 是整条
		// 读链最后一个静默空分支——「库不可读」会被渲染成「暂无信件」且零痕迹，
		// 与 320 复盘修掉的空面板同形。现在 Warn 留痕 + degraded=true，快照
		// 显示「暂时不可用」而不是诚实的空态。
		slog.Warn("session collab mail: cannot list the mail directory — degraded history read",
			"root", s.root, "err", err)
		return nil, true
	}
	var out []HistoryRow
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".inbox.jsonl") {
			continue
		}
		contact := strings.TrimSuffix(name, ".inbox.jsonl")
		all, err := s.readAll(contact)
		if err != nil {
			continue
		}
		seen := s.readCursor(contact)
		for _, m := range all {
			out = append(out, HistoryRow{Mail: m, Mailbox: contact, Read: seen[m.ID]})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Mail.At > out[j].Mail.At })
	return out, false
}

// PruneInbox physically removes every message older than beforeUnixMilli from
// every inbox file in the store (task 320 retention). It runs under the same
// cross-process lock that serializes Deliver, so a prune can never interleave
// with an append. Unparsable lines are kept verbatim — retention deletes old
// mail, never corrupt data. Returns how many messages were removed. The
// sender-side sent logs are deliberately NOT pruned: they are the sender's own
// audit trail, and the index never reads them (queued ≠ delivered).
func (s *MailStore) PruneInbox(ctx context.Context, beforeUnixMilli int64) (int, error) {
	return s.PruneBy(ctx, func(_ string, m MailMessage) bool {
		return m.At != 0 && m.At < beforeUnixMilli
	})
}

// PruneBy is the predicate-shaped generalization of PruneInbox (任务 464): it
// physically removes every inbox message for which remove returns true. The
// first argument of the predicate is the owning mailbox contact (the recipient
// whose inbox file holds the row — identical to m.To by construction). Same
// guarantees as PruneInbox: cross-process lock, unparsable lines kept verbatim,
// sent logs untouched. Returns how many messages were removed.
func (s *MailStore) PruneBy(ctx context.Context, remove func(mailbox string, m MailMessage) bool) (int, error) {
	if remove == nil {
		return 0, nil
	}
	unlock, err := s.lock(ctx)
	if err != nil {
		return 0, err
	}
	defer unlock()
	entries, err := os.ReadDir(s.root)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	removed := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".inbox.jsonl") {
			continue
		}
		path := filepath.Join(s.root, name)
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		mailbox := strings.TrimSuffix(name, ".inbox.jsonl")
		var keep []string
		fileRemoved := 0
		for _, line := range strings.Split(string(b), "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				continue
			}
			var m MailMessage
			if err := json.Unmarshal([]byte(trimmed), &m); err != nil {
				keep = append(keep, trimmed) // never destroy an unparsable line
				continue
			}
			if remove(mailbox, m) {
				fileRemoved++
				continue
			}
			keep = append(keep, trimmed)
		}
		if fileRemoved == 0 {
			continue
		}
		out := strings.Join(keep, "\n")
		if out != "" {
			out += "\n"
		}
		tmp := path + "." + newID("t") + ".tmp"
		if err := os.WriteFile(tmp, []byte(out), 0o600); err != nil {
			continue
		}
		if err := os.Rename(tmp, path); err != nil {
			_ = os.Remove(tmp)
			continue
		}
		removed += fileRemoved
	}
	return removed, nil
}
