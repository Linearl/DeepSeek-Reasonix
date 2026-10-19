// Package reminder is the durable in-session delayed self-reminder queue
// (task 1001): a conversation can schedule "remind me of X in N minutes" and
// the entry fires back into the same session's inbox when due.
//
// Design (方案 B of the tl 1001 comparison report):
//   - One <stem>.reminders/reminders.json file per session holds every pending
//     entry. Set is durable before the tool returns, so a crash or app restart
//     never loses an alarm — the hard cross-restart promise.
//   - The process-side timer lives in Manager (reminder.go's sibling): the
//     controller scans the store when a session binds and re-arms pending
//     timers; entries found already overdue are delivered immediately with an
//     explicit catch-up annotation.
//   - Due entries are delivered through the controller's follow-up enqueue,
//     which rides the 任务579 idle-turn bridge — the reminder path never goes
//     through talk_to_session, so the cross-session receipt face (and its
//     to==from self-send detection, tl 1000) is structurally untouched.
//
// The queue is deliberately NOT the sessioninbox: the inbox is a
// deliver-on-arrival queue, reminders are a delayed scheduler. Different
// lifecycles, separate files, separate schemas.
package reminder

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"reasonix/internal/filelock"
	"reasonix/internal/store"
)

const (
	// fileName is the single durable queue file inside <stem>.reminders/.
	fileName = "reminders.json"
	// diskLockName serializes read-modify-write cycles across processes,
	// mirroring the sessioninbox transaction lock discipline.
	diskLockName = "transaction.lock"
	diskLockWait = 5 * time.Second
	// MaxMessageBytes bounds one reminder body. Alarms are short by nature;
	// the cap keeps the queue file small and the injected prompt modest.
	MaxMessageBytes = 8 << 10
	// MaxPending bounds the queue so a runaway loop cannot pile up entries.
	MaxPending = 64
	// MaxDelay is the longest schedulable delay (7 days). Longer horizons are
	// heartbeat-tasks territory (守护心跳体系), not in-session alarms.
	MaxDelay = 7 * 24 * time.Hour
)

// Status of a persisted entry. The store keeps only pending entries: fired
// entries are removed on delivery, cancelled ones on cancel.
const StatusPending = "pending"

// Reminder is one durable delayed self-reminder.
type Reminder struct {
	ID      string `json:"id"`
	DueAt   int64  `json:"dueAt"` // unix milliseconds, absolute wall clock
	Message string `json:"message"`
	SetAt   int64  `json:"setAt"` // unix milliseconds
	Status  string `json:"status"`
	// FiredLate is a delivery-time annotation, never persisted: the manager
	// sets it when an entry was found already overdue (restart catch-up).
	FiredLate bool `json:"-"`
}

// newID returns a random hex id with a compact prefix.
func newID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("r%d", time.Now().UnixNano())
	}
	return "rem_" + hex.EncodeToString(b[:])
}

// ValidateReminder checks the schedulable window for a due time. The floor is
// "strictly in the future" (the tool layer enforces whole minutes); sub-minute
// delays stay schedulable for programmatic callers and tests.
func ValidateReminder(due time.Time, message string, now time.Time) error {
	if strings.TrimSpace(message) == "" {
		return fmt.Errorf("reminder message is required")
	}
	if len(message) > MaxMessageBytes {
		return fmt.Errorf("reminder message is %d bytes, over the %d byte limit", len(message), MaxMessageBytes)
	}
	if !due.After(now) {
		return fmt.Errorf("reminder due time must be in the future")
	}
	delay := due.Sub(now)
	if delay > MaxDelay {
		return fmt.Errorf("reminder delay must be at most %s", MaxDelay)
	}
	return nil
}

// queue is the on-disk shape of reminders.json.
type queue struct {
	Entries []Reminder `json:"entries"`
}

// Set durably appends one pending reminder. It returns the stored entry with
// its id assigned; the write is committed before Set returns (the hard
// cross-restart promise).
func Set(sessionPath, message string, due, now time.Time) (Reminder, error) {
	if strings.TrimSpace(sessionPath) == "" {
		return Reminder{}, fmt.Errorf("reminder: empty session path")
	}
	r := Reminder{
		ID:      newID(),
		DueAt:   due.UnixMilli(),
		Message: strings.TrimSpace(message),
		SetAt:   now.UnixMilli(),
		Status:  StatusPending,
	}
	err := mutate(sessionPath, func(q *queue) error {
		if len(q.Entries) >= MaxPending {
			return fmt.Errorf("reminder: queue full (%d pending); cancel or wait for one to fire", MaxPending)
		}
		q.Entries = append(q.Entries, r)
		return nil
	})
	if err != nil {
		return Reminder{}, err
	}
	return r, nil
}

// Cancel removes a pending reminder by id. It reports whether an entry was
// found (false already covers both "fired earlier" and "unknown id").
func Cancel(sessionPath, id string) (bool, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return false, fmt.Errorf("reminder: empty id")
	}
	found := false
	err := mutate(sessionPath, func(q *queue) error {
		kept := q.Entries[:0]
		for _, e := range q.Entries {
			if e.ID == id {
				found = true
				continue
			}
			kept = append(kept, e)
		}
		q.Entries = kept
		return nil
	})
	if err != nil {
		return false, err
	}
	return found, nil
}

// List returns every pending reminder for the session, earliest due first.
func List(sessionPath string) ([]Reminder, error) {
	q, err := load(sessionPath)
	if err != nil {
		return nil, err
	}
	out := append([]Reminder(nil), q.Entries...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].DueAt < out[j-1].DueAt; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out, nil
}

// Remove deletes a consumed (fired) entry; missing ids are not an error —
// delivery is best-effort cleanup and a concurrent cancel may have won.
func Remove(sessionPath, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("reminder: empty id")
	}
	return mutate(sessionPath, func(q *queue) error {
		kept := q.Entries[:0]
		for _, e := range q.Entries {
			if e.ID != id {
				kept = append(kept, e)
			}
		}
		q.Entries = kept
		return nil
	})
}

// load reads the queue file. A missing file (or directory — a session that
// never set a reminder) is an empty queue; a corrupt file surfaces as an
// error — reminders must fail loudly, never silently vanish.
func load(sessionPath string) (queue, error) {
	var q queue
	dir := store.SessionRemindersDir(sessionPath)
	if dir == "" {
		return q, fmt.Errorf("reminder: empty session path")
	}
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return q, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), diskLockWait)
	defer cancel()
	release, err := filelock.Acquire(ctx, filepath.Join(dir, diskLockName))
	if err != nil {
		return q, fmt.Errorf("reminder: acquire lock: %w", err)
	}
	defer release()
	return readLocked(dir)
}

func readLocked(dir string) (queue, error) {
	var q queue
	data, err := os.ReadFile(filepath.Join(dir, fileName))
	if os.IsNotExist(err) {
		return q, nil
	}
	if err != nil {
		return q, fmt.Errorf("reminder: read queue: %w", err)
	}
	if err := json.Unmarshal(data, &q); err != nil {
		return q, fmt.Errorf("reminder: decode queue %s: %w", dir, err)
	}
	return q, nil
}

// mutate runs fn against the queue under the cross-process lock and commits
// the result atomically (tmp file + rename).
func mutate(sessionPath string, fn func(*queue) error) error {
	dir := store.SessionRemindersDir(sessionPath)
	if dir == "" {
		return fmt.Errorf("reminder: empty session path")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("reminder: create dir: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), diskLockWait)
	defer cancel()
	release, err := filelock.Acquire(ctx, filepath.Join(dir, diskLockName))
	if err != nil {
		return fmt.Errorf("reminder: acquire lock: %w", err)
	}
	defer release()
	q, err := readLocked(dir)
	if err != nil {
		return err
	}
	if err := fn(&q); err != nil {
		return err
	}
	data, err := json.MarshalIndent(q, "", "  ")
	if err != nil {
		return fmt.Errorf("reminder: encode queue: %w", err)
	}
	tmp := filepath.Join(dir, fileName+".tmp")
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("reminder: write queue: %w", err)
	}
	// Windows: transient external holders (indexer/AV) can deny the rename
	// for a few milliseconds; retry briefly so a consumed entry is really
	// removed — a half-removed entry would re-deliver on the next scan.
	var renameErr error
	for attempt := 0; attempt < 5; attempt++ {
		if renameErr = os.Rename(tmp, filepath.Join(dir, fileName)); renameErr == nil {
			return nil
		}
		time.Sleep(25 * time.Millisecond)
	}
	_ = os.Remove(tmp)
	return fmt.Errorf("reminder: commit queue: %w", renameErr)
}
