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
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
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
	StatusPending  CardStatus = "pending"
	StatusRunning  CardStatus = "running"
	StatusBlocked  CardStatus = "blocked"
	StatusDone     CardStatus = "done"
	StatusFailed   CardStatus = "failed"
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
	ID           string `json:"id"`
	From         string `json:"fromContactId,omitempty"`
	FromSession  string `json:"fromSession,omitempty"`
	To           string `json:"toContactId"`
	Body         string `json:"body"`
	Delivery     string `json:"delivery,omitempty"` // followup (default) | steer
	Hop          int    `json:"hop,omitempty"`
	CardID       string `json:"cardId,omitempty"`
	ReplyTo      string `json:"replyToContactId,omitempty"`
	At           int64  `json:"at"`
	Idempotency  string `json:"idempotency,omitempty"`
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

// ContactIDFromBranchID mints a stable contact id from a branch meta id.
// Contact IDs are opaque and never derived from titles or file names.
func ContactIDFromBranchID(branchID string) string {
	sum := fnv32(branchID)
	return fmt.Sprintf("sc_%08x", sum)
}

func fnv32(s string) uint32 {
	h := uint32(2166136261)
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return h
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
// Meta loader is injected so this package stays free of the agent import cycle.
func ScanDir(dir string, loadMeta func(sessionPath string) (contactID, purpose, topicID, title string, ok bool)) []Identity {
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
type CardStore struct {
	root string
	mu   sync.Mutex
}

func NewCardStore(workspaceRoot string) *CardStore {
	return &CardStore{root: filepath.Join(workspaceRoot, ".reasonix", "taskcards")}
}

func (s *CardStore) path(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		id = "unknown"
	}
	return filepath.Join(s.root, id+".json")
}

func (s *CardStore) Create(c Card) (Card, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
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
	s.mu.Lock()
	defer s.mu.Unlock()
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
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked(id)
}

func (s *CardStore) List() ([]Card, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
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

// MailStore is a durable cross-session mailbox under
// <root>/.reasonix/session-chat/<contactId>.inbox.jsonl
type MailStore struct {
	root string
	mu   sync.Mutex
}

func NewMailStore(workspaceRoot string) *MailStore {
	return &MailStore{root: filepath.Join(workspaceRoot, ".reasonix", "session-chat")}
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
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(msg.To) == "" {
		return MailMessage{}, errors.New("talk_to_session: target contact_id is required")
	}
	if strings.TrimSpace(msg.Body) == "" {
		return MailMessage{}, errors.New("talk_to_session: message body is required")
	}
	if msg.Hop > MaxHop {
		return MailMessage{}, fmt.Errorf("%w: hop=%d", ErrHopLimit, msg.Hop)
	}
	if msg.ID == "" {
		msg.ID = newID("msg_")
	}
	if msg.Delivery == "" {
		msg.Delivery = "followup"
	}
	if msg.At == 0 {
		msg.At = time.Now().UnixMilli()
	}
	if err := appendJSONL(s.inboxPath(msg.To), msg); err != nil {
		return MailMessage{}, err
	}
	return msg, nil
}

// Inbox returns pending messages for a contact (newest last).
func (s *MailStore) Inbox(contactID string) ([]MailMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
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
