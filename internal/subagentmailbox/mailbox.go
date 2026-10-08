// Package subagentmailbox implements the runtime message channel to running
// sub-agents (task 616). Two pieces:
//
//   - Mailbox: one directory per sub-agent ref holding one JSON file per
//     message, created exclusively and consumed by rename — every message is
//     durable on disk before any delivery is attempted, so a crash between
//     send and consume never loses it.
//   - Registry: the in-process map of running sub-agent refs to their steer
//     handles, published by RunSubAgentWithSession for the exact run lifetime.
//
// The package imports nothing from internal/agent: the registry stores the
// steer closure itself, and the agent package owns the SteerItem primitive it
// forwards to. Consumers (the parent-side send_message tool and the desktop
// binding) go through Hub.Deliver, which persists first and only then tries
// the live handle.
package subagentmailbox

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Entry is one persisted mailbox message.
type Entry struct {
	ID        string `json:"id"`
	From      string `json:"from"` // "user" | "parent"
	Text      string `json:"text"`
	Summary   string `json:"summary,omitempty"`
	CreatedAt string `json:"createdAt"` // RFC3339Nano, UTC
}

// pendingSuffix marks an unconsumed message; delivered messages are renamed to
// "<id>.json" + deliveredSuffix so consumption stays a single atomic rename
// and the delivered copy remains on disk for audit until the mailbox
// directory is removed with the sub-agent record.
const (
	mailboxDirSuffix = ".mailbox"
	pendingSuffix    = ".json"
	deliveredSuffix  = ".json.delivered"
)

// ValidRef reports whether ref has the shape minted by SubagentStore.newRef
// ("sa_<timestamp>_<hex>"). It doubles as the path-traversal guard: refs are
// joined into filesystem paths, so anything outside this shape is refused
// before a path is built.
func ValidRef(ref string) bool {
	ref = strings.TrimSpace(ref)
	if !strings.HasPrefix(ref, "sa_") || len(ref) > 128 {
		return false
	}
	for _, r := range ref {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
		default:
			return false
		}
	}
	return true
}

// Mailbox is the on-disk mailbox for one sub-agent ref inside one session's
// subagents directory. Methods are nil-safe: a nil Mailbox means the channel
// is absent (switch off, no persisted transcripts) and every call is a no-op.
type Mailbox struct {
	ref string // validated store-minted ref
	dir string // <subagentsDir>/<ref>.mailbox
}

func newMailbox(subagentsDir, ref string) *Mailbox {
	return &Mailbox{ref: ref, dir: filepath.Join(subagentsDir, ref+mailboxDirSuffix)}
}

// Ref returns the owning sub-agent ref ("" on a nil Mailbox).
func (m *Mailbox) Ref() string {
	if m == nil {
		return ""
	}
	return m.ref
}

// Publish registers this run's steer handle in the process-global registry
// under the mailbox's ref and returns the unpublish function. RunSubAgentWithSession
// calls it once per run with the child's SteerItem; the deferred unpublish is
// what keeps a finished run unreachable.
func (m *Mailbox) Publish(steer SteerFunc) func() {
	if m == nil || m.ref == "" || steer == nil {
		return func() {}
	}
	return GlobalRegistry.Publish(m.ref, steer)
}

// Dir returns the mailbox directory path ("" on a nil Mailbox).
func (m *Mailbox) Dir() string {
	if m == nil {
		return ""
	}
	return m.dir
}

// Append persists one message and returns it with the assigned ID. The file
// is created exclusively and fully written before Append returns, so a
// message that any sender received an Entry for is durable on disk.
func (m *Mailbox) Append(from, summary, text string) (Entry, error) {
	if m == nil {
		return Entry{}, fmt.Errorf("subagent mailbox is unavailable")
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return Entry{}, fmt.Errorf("message text is empty")
	}
	id, err := newMessageID()
	if err != nil {
		return Entry{}, err
	}
	entry := Entry{
		ID:        id,
		From:      from,
		Text:      text,
		Summary:   strings.TrimSpace(summary),
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	data, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return Entry{}, err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(m.dir, 0o755); err != nil {
		return Entry{}, err
	}
	path := filepath.Join(m.dir, id+pendingSuffix)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return Entry{}, err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return Entry{}, err
	}
	if err := f.Close(); err != nil {
		return Entry{}, err
	}
	return entry, nil
}

// List returns the still-pending messages oldest first. Delivered messages
// (renamed copies) are not listed.
func (m *Mailbox) List() ([]Entry, error) {
	if m == nil {
		return nil, nil
	}
	ids, err := pendingIDs(m.dir)
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(ids))
	for _, id := range ids {
		entry, err := m.read(id)
		if err != nil {
			if os.IsNotExist(err) {
				continue // consumed by a concurrent reader between list and read
			}
			return nil, err
		}
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].CreatedAt < entries[j].CreatedAt })
	return entries, nil
}

// read loads one pending message body.
func (m *Mailbox) read(id string) (Entry, error) {
	data, err := os.ReadFile(filepath.Join(m.dir, id+pendingSuffix))
	if err != nil {
		return Entry{}, err
	}
	var entry Entry
	if err := json.Unmarshal(data, &entry); err != nil {
		return Entry{}, fmt.Errorf("mailbox message %s is corrupt: %w", id, err)
	}
	return entry, nil
}

// Loader returns the steer-item loader for one message: the body is read and
// only then the file is renamed to delivered, mirroring the parent-session
// inbox loader's consume-then-inject order — a later injection failure does
// not resurrect the file (the transcript keeps the unapplied record instead).
// A read failure leaves the file pending for the next attempt.
func (m *Mailbox) Loader(id string) func() (string, error) {
	return func() (string, error) {
		if m == nil {
			return "", fmt.Errorf("subagent mailbox is unavailable")
		}
		entry, err := m.read(id)
		if err != nil {
			return "", err
		}
		// Consumption is the rename; a losing rename means a concurrent
		// consumer already delivered this message, which is fine — the body
		// was read either way.
		_ = m.MarkDelivered(id)
		if strings.TrimSpace(entry.Text) == "" {
			return "", fmt.Errorf("mailbox message %s has empty body", id)
		}
		return entry.Text, nil
	}
}

// MarkDelivered renames a pending message to its delivered copy. Missing
// sources are not an error (another consumer won the rename).
func (m *Mailbox) MarkDelivered(id string) error {
	if m == nil {
		return nil
	}
	err := os.Rename(filepath.Join(m.dir, id+pendingSuffix), filepath.Join(m.dir, id+deliveredSuffix))
	if err != nil && !os.IsNotExist(err) {
		// Windows can hold the source briefly open; the caller keeps the
		// message pending and the next consume attempt retries the rename.
		return err
	}
	return nil
}

// PendingCount reports how many messages are still pending (0 on a nil
// Mailbox or when the mailbox directory does not exist).
func (m *Mailbox) PendingCount() int {
	if m == nil {
		return 0
	}
	ids, err := pendingIDs(m.dir)
	if err != nil {
		return 0
	}
	return len(ids)
}

// RemoveAll deletes the whole mailbox directory (pending and delivered). Used
// when the owning sub-agent record is deleted; orphaned mailboxes of ended
// sub-agents are harmless but this reclaims them eagerly.
func (m *Mailbox) RemoveAll() error {
	if m == nil || m.dir == "" {
		return nil
	}
	if err := os.RemoveAll(m.dir); err != nil {
		return err
	}
	return nil
}

// pendingIDs lists the IDs of pending messages by filename, oldest name
// (timestamp) first. A missing directory yields an empty list, not an error.
func pendingIDs(dir string) ([]string, error) {
	dirEntries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	ids := make([]string, 0, len(dirEntries))
	for _, de := range dirEntries {
		if de.IsDir() {
			continue
		}
		name := de.Name()
		if !strings.HasSuffix(name, pendingSuffix) {
			continue
		}
		ids = append(ids, strings.TrimSuffix(name, pendingSuffix))
	}
	sort.Strings(ids)
	return ids, nil
}

func newMessageID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "msg_" + time.Now().UTC().Format("20060102_150405_000000000") + "_" + hex.EncodeToString(b[:]), nil
}
