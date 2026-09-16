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
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"reasonix/internal/filelock"
)

// MaxHop is the collaboration chain limit (task 142). The 6th hop is refused.
const MaxHop = 5

// Identity is one addressable session (task 141).
type Identity struct {
	ContactID   string `json:"contactId"`
	Purpose     string `json:"purpose,omitempty"`
	SessionPath string `json:"sessionPath"`
	TopicID     string `json:"topicId,omitempty"`
	Title       string `json:"title,omitempty"`
	Workspace   string `json:"workspaceRoot,omitempty"`
	Scope       string `json:"scope,omitempty"`
	// Archived marks a session found in the archive: it still has an address,
	// but it is no longer an active participant, and callers must say so rather
	// than reporting it as never registered.
	Archived  bool  `json:"archived,omitempty"`
	UpdatedAt int64 `json:"updatedAt,omitempty"`
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
	ThreadID    string `json:"threadId,omitempty"`
	At          int64  `json:"at"`
	Idempotency string `json:"idempotency,omitempty"`
}

// Delivery semantics for talk_to_session (task 143). Followup is the default
// and the conservative choice: the target processes it after its current turn.
// Steer asks for mid-turn injection and degrades to followup when the target
// has no injectable turn.
type Delivery string

const (
	DeliveryFollowup Delivery = "followup"
	DeliverySteer    Delivery = "steer"
)

// ValidateDelivery normalizes an empty value to followup and rejects others.
func ValidateDelivery(value string) (Delivery, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", string(DeliveryFollowup):
		return DeliveryFollowup, nil
	case string(DeliverySteer):
		return DeliverySteer, nil
	default:
		return "", fmt.Errorf("talk_to_session: unknown delivery %q (want followup|steer)", value)
	}
}

// ErrHopLimit is returned when a chain exceeds MaxHop.
var ErrHopLimit = errors.New("talk_to_session: hop limit exceeded (max 5)")

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

// ScanDir walks one sessions directory for BranchMeta contact fields.
// workspaceRoot is the root those sessions belong to; it is published on every
// identity so delivery can route to the target's own mailbox rather than the
// sender's. Meta loader is injected so this package stays free of the agent
// import cycle.
func ScanDir(dir, workspaceRoot string, loadMeta func(sessionPath string) (contactID, purpose, topicID, title string, ok bool)) []Identity {
	if strings.TrimSpace(dir) == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []Identity
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".jsonl") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		contact, purpose, topic, title, ok := loadMeta(path)
		if !ok || contact == "" {
			continue
		}
		var updated int64
		if info, err := os.Stat(path); err == nil {
			updated = info.ModTime().UnixMilli()
		}
		out = append(out, Identity{
			ContactID:   contact,
			Purpose:     purpose,
			SessionPath: path,
			TopicID:     topic,
			Title:       title,
			Workspace:   workspaceRoot,
			UpdatedAt:   updated,
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
func (s *CardStore) lock() (func(), error) {
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return nil, err
	}
	return filelock.Acquire(context.Background(), filepath.Join(s.root, ".cards.lock"))
}

func (s *CardStore) path(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		id = "unknown"
	}
	return filepath.Join(s.root, id+".json")
}

func (s *CardStore) Create(c Card) (Card, error) {
	unlock, err := s.lock()
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

func (s *CardStore) Update(id string, mutate func(*Card) error) (Card, error) {
	unlock, err := s.lock()
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

func (s *PendingPurposeStore) lock() (func(), error) {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return nil, err
	}
	return filelock.Acquire(context.Background(), s.path+".lock")
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
	unlock, err := s.lock()
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
	unlock, err := s.lock()
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
	unlock, err := s.lock()
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
}

func NewMailStore(mailboxDir string) *MailStore {
	return &MailStore{root: mailboxDir}
}

func (s *MailStore) lock() (func(), error) {
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return nil, err
	}
	return filelock.Acquire(context.Background(), filepath.Join(s.root, ".mail.lock"))
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

// Deliver appends a message for the target contact. hop is the sender's chain
// depth; MaxHop+1 is refused.
func (s *MailStore) Deliver(msg MailMessage) (MailMessage, error) {
	unlock, err := s.lock()
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
	if msg.Hop > MaxHop {
		return MailMessage{}, fmt.Errorf("%w: hop=%d", ErrHopLimit, msg.Hop)
	}
	delivery, err := ValidateDelivery(msg.Delivery)
	if err != nil {
		return MailMessage{}, err
	}
	msg.Delivery = string(delivery)
	if msg.ID == "" {
		msg.ID = newID("msg_")
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
func (s *MailStore) Claim(contactID string) (pending []MailMessage, refused []MailMessage, err error) {
	unlock, err := s.lock()
	if err != nil {
		return nil, nil, err
	}
	defer unlock()
	all, err := s.readAll(contactID)
	if err != nil {
		return nil, nil, err
	}
	seen := s.readCursor(contactID)
	for _, m := range all {
		if seen[m.ID] {
			continue
		}
		if m.Hop > MaxHop {
			refused = append(refused, m)
			continue
		}
		pending = append(pending, m)
	}
	return pending, refused, nil
}

// Ack advances the cursor for IDs whose delivery is settled — delivered
// successfully, or refused for good. Not acking leaves them for the next pass.
func (s *MailStore) Ack(contactID string, ids ...string) error {
	if len(ids) == 0 {
		return nil
	}
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()
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
	if strings.TrimSpace(threadID) == "" {
		return MailMessage{}, false
	}
	deadline := time.Now().Add(timeout)
	for {
		all, err := s.readAll(contactID)
		if err == nil {
			for _, m := range all {
				if m.ThreadID == threadID {
					// Best-effort: a failed ack costs a duplicate, not a loss.
					_ = s.Ack(contactID, m.ID)
					return m, true
				}
			}
		}
		if time.Now().After(deadline) {
			return MailMessage{}, false
		}
		sleep := 200 * time.Millisecond
		if remaining := time.Until(deadline); remaining < sleep {
			sleep = remaining
		}
		if sleep > 0 {
			time.Sleep(sleep)
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

// MarkNotified records that a status note was sent for key, returning true only
// the first time. It lets a caller notify once without giving up the retry, so
// a target that stays unavailable is never silent but also never a flood.
func (s *MailStore) MarkNotified(contactID, key string) bool {
	if strings.TrimSpace(contactID) == "" || strings.TrimSpace(key) == "" {
		return false
	}
	unlock, err := s.lock()
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
