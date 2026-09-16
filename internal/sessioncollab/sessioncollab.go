// Package sessioncollab implements task 19 multi-session collaboration
// foundations: stable contact addressing (141), task cards (145), and a
// durable cross-session mailbox for talk_to_session (142).
//
// Addressing is additive: topicID and file paths keep working; contact_id
// never replaces them. Cards use atomic temp+rename writes. Mailbox delivery
// is durable on disk; the live wake path is optional and never required for
// correctness.
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
	UpdatedAt   int64  `json:"updatedAt,omitempty"`
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
	if msg.At == 0 {
		msg.At = time.Now().UnixMilli()
	}
	if err := appendJSONL(s.inboxPath(msg.To), msg); err != nil {
		return MailMessage{}, err
	}
	return msg, nil
}

// Claim returns messages the contact has not seen yet and advances its cursor,
// so a resumed polling loop never re-delivers the same message. It also
// enforces the hop ceiling at delivery time: a message that already exhausted
// the chain is refused here rather than being handed to the target, because a
// caller-reported hop cannot be trusted.
func (s *MailStore) Claim(contactID string) (delivered []MailMessage, refused []MailMessage, err error) {
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
		delivered = append(delivered, m)
	}
	if len(delivered) == 0 && len(refused) == 0 {
		return nil, nil, nil
	}
	for _, m := range delivered {
		seen[m.ID] = true
	}
	for _, m := range refused {
		seen[m.ID] = true
	}
	if err := s.writeCursor(contactID, seen); err != nil {
		return nil, nil, err
	}
	return delivered, refused, nil
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
