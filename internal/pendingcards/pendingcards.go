// Package pendingcards owns the durable queue of pending decision cards
// (任务 408 异步决策点回访). A card records one approval/ask prompt that a run
// reached while the user was away, so the decision point survives turn scroll
// and process restarts instead of living only in the in-flight prompt maps.
//
// The store is deliberately simple: one JSON file per session, whole-file
// rewrite on every mutation (atomic temp+rename), guarded by a mutex. Card
// volume is bounded by DefaultMaxCards; the queue never grows unbounded.
package pendingcards

import (
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

// Card states — the three-way closure of 任务 408 plus the open state.
const (
	// StatePending: the decision point is (or was) waiting for the user.
	StatePending = "pending"
	// StateResolved: the user decided (approved/denied/answered) — 批完.
	StateResolved = "resolved"
	// StateTimeout: the wait expired or the card outlived its TTL — 超时.
	StateTimeout = "timeout"
	// StateWithdrawn: the user explicitly withdrew the card without deciding — 撤回.
	StateWithdrawn = "withdrawn"
)

// Card kinds.
const (
	KindApproval = "approval"
	KindAsk      = "ask"
)

// DefaultMaxCards bounds the on-disk queue. When the cap is hit, terminal
// (settled) cards are dropped oldest-first; only if every entry is pending does
// Enqueue refuse.
const DefaultMaxCards = 64

// Sentinel errors.
var (
	ErrNotFound     = errors.New("pending card not found")
	ErrSettled      = errors.New("pending card already settled")
	ErrCapacity     = errors.New("pending card queue is full of pending cards")
	ErrNoSession    = errors.New("pending card queue requires a session path")
	ErrInvalidState = errors.New("invalid pending card state")
)

// Card is one durable pending-decision row: {会话, 时间, 动作摘要, 上下文指针}.
type Card struct {
	ID        string    `json:"id"`               // the underlying prompt id
	Session   string    `json:"session"`          // session transcript path
	CreatedAt time.Time `json:"createdAt"`        // when the decision point fired
	Kind      string    `json:"kind"`             // approval | ask
	Summary   string    `json:"summary"`          // 动作摘要 (tool+subject / question header+prompt)
	TurnID    string    `json:"turnId,omitempty"` // 上下文指针: the turn that blocked
	State     string    `json:"state"`            // pending | resolved | timeout | withdrawn
	SettledAt time.Time `json:"settledAt,omitempty"`
	Outcome   string    `json:"outcome,omitempty"` // allow/deny/answered/dismissed/wait_expired/...
}

// Terminal reports whether the card has left the pending state.
func (c Card) Terminal() bool {
	switch c.State {
	case StateResolved, StateTimeout, StateWithdrawn:
		return true
	default:
		return false
	}
}

func validState(s string) bool {
	switch s {
	case StatePending, StateResolved, StateTimeout, StateWithdrawn:
		return true
	default:
		return false
	}
}

// file is the on-disk shape. A schema version lets a future format change fail
// loud instead of misreading old data.
type file struct {
	SchemaVersion int    `json:"schemaVersion"`
	Session       string `json:"session,omitempty"`
	Cards         []Card `json:"cards"`
}

const schemaVersion = 1

// Queue is the file-backed card queue for one session.
type Queue struct {
	mu      sync.Mutex
	path    string
	session string
	cards   []Card
}

// FileNameFor derives the queue file path next to the session transcript, the
// same sibling-file convention as store.SessionInboxDir.
func FileNameFor(sessionPath string) string {
	sessionPath = strings.TrimSpace(sessionPath)
	if sessionPath == "" {
		return ""
	}
	stem := strings.TrimSuffix(sessionPath, filepath.Ext(sessionPath))
	if stem == "" {
		return ""
	}
	return stem + ".pending-cards.json"
}

// Open loads (or initializes) the queue bound to a session path.
func Open(sessionPath string) (*Queue, error) {
	sessionPath = strings.TrimSpace(sessionPath)
	if sessionPath == "" {
		return nil, ErrNoSession
	}
	path := FileNameFor(sessionPath)
	if path == "" {
		return nil, ErrNoSession
	}
	q := &Queue{path: path, session: sessionPath}
	q.load()
	return q, nil
}

// load reads the file; a missing or corrupt file starts empty (corruption must
// never wedge a session that only wanted a status read).
func (q *Queue) load() {
	data, err := os.ReadFile(q.path)
	if err != nil || len(data) == 0 {
		return
	}
	var f file
	if err := json.Unmarshal(data, &f); err != nil || f.SchemaVersion > schemaVersion {
		// Unknown newer schema: start read-only-empty rather than misparse.
		return
	}
	q.cards = f.Cards
}

// persistLocked rewrites the file atomically. Caller holds q.mu.
func (q *Queue) persistLocked() error {
	if err := os.MkdirAll(filepath.Dir(q.path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(file{SchemaVersion: schemaVersion, Session: q.session, Cards: q.cards})
	if err != nil {
		return err
	}
	tmp := q.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, q.path)
}

// Enqueue appends a pending card (dedup by ID: re-noting an existing prompt is
// a no-op) and persists.
func (q *Queue) Enqueue(card Card) error {
	if card.ID == "" {
		return fmt.Errorf("pending card id is required")
	}
	if card.Kind != KindApproval && card.Kind != KindAsk {
		return fmt.Errorf("pending card kind %q: must be approval or ask", card.Kind)
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	for i := range q.cards {
		if q.cards[i].ID == card.ID {
			return nil // already noted
		}
	}
	// Capacity: drop oldest terminal cards first; refuse only when everything
	// still pending.
	terminal := []int{}
	for i := range q.cards {
		if q.cards[i].Terminal() {
			terminal = append(terminal, i)
		}
	}
	if len(q.cards)-len(terminal) >= DefaultMaxCards {
		return ErrCapacity
	}
	if len(q.cards) >= DefaultMaxCards {
		sort.Slice(terminal, func(a, b int) bool { return q.cards[terminal[a]].SettledAt.Before(q.cards[terminal[b]].SettledAt) })
		drop := map[int]bool{}
		for _, i := range terminal[:len(q.cards)+1-DefaultMaxCards] {
			drop[i] = true
		}
		kept := q.cards[:0]
		for i, c := range q.cards {
			if !drop[i] {
				kept = append(kept, c)
			}
		}
		q.cards = kept
	}
	if card.State == "" {
		card.State = StatePending
	}
	if card.CreatedAt.IsZero() {
		card.CreatedAt = time.Now()
	}
	q.cards = append(q.cards, card)
	return q.persistLocked()
}

// Settle moves a pending card to a terminal state and persists. Settling an
// unknown id is ErrNotFound; re-settling is a no-op (first settlement wins —
// the answer that resolved the prompt is the truth, late bookkeeping must not
// overwrite it).
func (q *Queue) Settle(id, state, outcome string) error {
	if !validState(state) || state == StatePending {
		return fmt.Errorf("%w: %q", ErrInvalidState, state)
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	for i := range q.cards {
		if q.cards[i].ID != id {
			continue
		}
		if q.cards[i].Terminal() {
			return nil
		}
		q.cards[i].State = state
		q.cards[i].Outcome = outcome
		q.cards[i].SettledAt = time.Now()
		return q.persistLocked()
	}
	return ErrNotFound
}

// SweepExpired moves pending cards older than ttl to StateTimeout (the 超时
// closure for cards whose prompt died with a process restart or an orphaned
// wait) and persists. Returns how many cards expired.
func (q *Queue) SweepExpired(now time.Time, ttl time.Duration) (int, error) {
	if ttl <= 0 {
		return 0, nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	n := 0
	changed := false
	for i := range q.cards {
		if q.cards[i].State != StatePending {
			continue
		}
		if now.Sub(q.cards[i].CreatedAt) >= ttl {
			q.cards[i].State = StateTimeout
			q.cards[i].Outcome = "ttl_expired"
			q.cards[i].SettledAt = now
			n++
			changed = true
		}
	}
	if changed {
		return n, q.persistLocked()
	}
	return n, nil
}

// Pending returns the pending cards, oldest first. now+ttl drive the expiry
// sweep so callers never see a card the TTL already killed.
func (q *Queue) Pending(now time.Time, ttl time.Duration) []Card {
	_, _ = q.SweepExpired(now, ttl)
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]Card, 0, len(q.cards))
	for _, c := range q.cards {
		if c.State == StatePending {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].CreatedAt.Before(out[b].CreatedAt) })
	return out
}

// CountPending is the cheap in-memory count feeding the runtime-state badge.
func (q *Queue) CountPending() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	n := 0
	for i := range q.cards {
		if q.cards[i].State == StatePending {
			n++
		}
	}
	return n
}

// All returns a copy of every card (diagnostics/tests).
func (q *Queue) All() []Card {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]Card, len(q.cards))
	copy(out, q.cards)
	return out
}
