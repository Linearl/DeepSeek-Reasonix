package session

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/fileutil"
	"reasonix/internal/provider"
	"reasonix/internal/sessioncontent"
)

// Version 2 adds per-turn fork availability derived from the complete closing
// commit. Version 1 checkpoints decode that field as empty, so accepting one
// would incorrectly mark previously completed turns unverifiable until a full
// replay. Rejecting the old projection rebuilds it from the durable log.
//
// Version 3 carries the live stable message ids (task 398, upstream #10893).
// A version 2 checkpoint has no such field, so accepting it would seed the
// writer's duplicate refusal empty and re-open the durable-id blind spot the
// set exists to close; older checkpoints rebuild from the unchanged log once.
const recoveryProjectionVersion = 3

const (
	recoveryFormatVersion = 1
	// Task 1013: the bolt checkpoint/operations cache (recovery-v1.bolt under
	// .recovery-cache/) and its published recent-v1.json snapshot were removed.
	// They are never read back: cold opens replay the events log, and the
	// desktop session path runs on the v3 store with the v4 bridge off, so the
	// files were dead weight that a v4 open still created and fsynced on every
	// publish. Session generation and bounded baselines survive without them
	// (storage identity stays, RecentSnapshot builds in memory).
	//
	// The constants stay so an on-disk layout that still holds the old files
	// describes itself: perf monitors and repair sweeps locate them by name,
	// and storage.identity.json keeps carrying generation identity for the
	// content-authorization and history-index guards.
	recoveryDBName       = "recovery-v1.bolt" // legacy on-disk name; never written
	recoveryCacheDirName = ".recovery-cache"  // legacy cache root; never created
	storageIdentityName  = "storage.identity.json"
	RecentMessageLimit   = 100
	recentInlineBytes    = 32 << 10
	recentResponseBytes  = 512 << 10
	recentSnapshotName   = "recent-v1.json" // legacy on-disk name; never written
)

// RecoveryOpenStats reports work performed by the normal open path. It is
// intentionally small and stable enough for capacity tests and host telemetry.
type RecoveryOpenStats struct {
	UsedCheckpoint bool  `json:"usedCheckpoint"`
	LogBytesRead   int64 `json:"logBytesRead"`
	LogBytesTotal  int64 `json:"logBytesTotal"`
	TailCommits    int   `json:"tailCommits"`
}

// RecentSnapshot is the bounded, read-only baseline used before history or
// search projections are available.
type RecentSnapshot struct {
	Version           int                 `json:"version"`
	SessionID         string              `json:"sessionId"`
	StorageGeneration string              `json:"storageGeneration"`
	DurableSequence   uint64              `json:"durableSequence"`
	TotalTurns        int                 `json:"totalTurns"`
	Entries           []PersistentMessage `json:"entries"`
	Title             string              `json:"title,omitempty"`
	ModelRef          string              `json:"modelRef,omitempty"`
	ModelIdentity     string              `json:"modelIdentity,omitempty"`
}

type storageIdentity struct {
	Version         int       `json:"version"`
	SessionID       string    `json:"sessionId"`
	Generation      string    `json:"generation"`
	LogPrefixDigest string    `json:"logPrefixDigest,omitempty"`
	CreatedAt       time.Time `json:"createdAt"`
}

// recoveryCheckpoint was the bolt cache's serialized checkpoint record. The
// type is gone with the cache; loadRecoveryStartupState is the only remaining
// seam and it always answers "no checkpoint".
func openRecoveryStorePlaceholder() {}

// recoveryStore was the bolt checkpoint/operations cache (task 1013 removal).
// It is now a generation-scoped stub kept at the same field shape so the
// binding and session wiring keep one code path: it holds the storage identity
// and reports the cache as permanently unavailable, which makes every legacy
// read path take its no-cache fallback instead of waiting on disk.
type recoveryStore struct {
	sessionDir string
	identity   storageIdentity
}

func ensureStorageIdentity(sessionDir string, manifest Manifest) (storageIdentity, error) {
	path := filepath.Join(sessionDir, storageIdentityName)
	prefix, prefixErr := storageLogPrefix(sessionDir, manifest)
	if prefixErr != nil && !os.IsNotExist(prefixErr) {
		return storageIdentity{}, prefixErr
	}
	if data, err := os.ReadFile(path); err == nil {
		var identity storageIdentity
		if json.Unmarshal(data, &identity) == nil && identity.Version == recoveryFormatVersion && identity.SessionID == manifest.SessionID && strings.TrimSpace(identity.Generation) != "" {
			if identity.LogPrefixDigest == "" && prefix != "" {
				identity.LogPrefixDigest = prefix
				encoded, marshalErr := json.Marshal(identity)
				if marshalErr != nil {
					return storageIdentity{}, marshalErr
				}
				if writeErr := fileutil.AtomicWriteFileStrict(path, append(encoded, '\n'), 0o600); writeErr != nil {
					return storageIdentity{}, writeErr
				}
				return identity, nil
			}
			if prefix == "" || identity.LogPrefixDigest == prefix {
				return identity, nil
			}
		}
	}
	identity := storageIdentity{Version: recoveryFormatVersion, SessionID: manifest.SessionID, Generation: randomID(), LogPrefixDigest: prefix, CreatedAt: time.Now().UTC()}
	data, err := json.Marshal(identity)
	if err != nil {
		return storageIdentity{}, err
	}
	if err := fileutil.AtomicWriteFileStrict(path, append(data, '\n'), 0o600); err != nil {
		return storageIdentity{}, err
	}
	return identity, nil
}

func storageLogPrefix(sessionDir string, manifest Manifest) (string, error) {
	file, err := os.Open(logPathForManifest(sessionDir, manifest))
	if err != nil {
		return "", err
	}
	defer file.Close()
	// The framed transaction header makes the first 64 bytes immutable after
	// the first commit. Hashing a larger short-file prefix would change merely
	// because a normal append extended a log shorter than that prefix.
	buffer := make([]byte, 64)
	n, err := file.Read(buffer)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	if n == 0 {
		return "", nil
	}
	digest := sha256.Sum256(buffer[:n])
	return fmt.Sprintf("%x", digest[:]), nil
}

func readStorageIdentity(sessionDir string, manifest Manifest) (storageIdentity, error) {
	data, err := os.ReadFile(filepath.Join(sessionDir, storageIdentityName))
	if err != nil {
		return storageIdentity{}, err
	}
	var identity storageIdentity
	if err := json.Unmarshal(data, &identity); err != nil {
		return storageIdentity{}, err
	}
	if identity.Version != recoveryFormatVersion || identity.SessionID != manifest.SessionID || strings.TrimSpace(identity.Generation) == "" {
		return storageIdentity{}, ErrStaleGeneration
	}
	prefix, err := storageLogPrefix(sessionDir, manifest)
	if err != nil && !os.IsNotExist(err) {
		return storageIdentity{}, err
	}
	if identity.LogPrefixDigest != "" && prefix != identity.LogPrefixDigest {
		return storageIdentity{}, ErrStaleGeneration
	}
	return identity, nil
}

// SetOrphanSweepProbe was the experimental_recovery_orphan_sweep injection
// point (task 244 B4). The sweep lived inside the removed bolt cache; the
// setter stays as a no-op so hosts that still wire the legacy config switch
// (desktop v4 bridge) build and run unchanged.
func SetOrphanSweepProbe(probe func() bool) {}

// openRecoveryStore is the removed bolt cache's entry point, kept as the
// session-open seam (task 1013). It never touches the filesystem: no
// .recovery-cache directory, no recovery-v1.bolt, no recent-v1.json. The
// returned stub carries the storage generation and answers every legacy read
// with "not present", which is exactly what the caller's fallbacks expect.
func openRecoveryStore(sessionDir string, identity storageIdentity) (*recoveryStore, error) {
	return &recoveryStore{sessionDir: sessionDir, identity: identity}, nil
}

// openRecoveryStoreRepair was the corrupt-bolt quarantine path. With the cache
// removed there is nothing to repair or quarantine; kept so the open call site
// reads unchanged.
func openRecoveryStoreRepair(sessionDir string, identity storageIdentity) (*recoveryStore, error) {
	return openRecoveryStore(sessionDir, identity)
}

func (s *recoveryStore) close() error { return nil }

// lookupOperation reports no hit: operation idempotency now rests on the
// in-memory Session.operations table alone, and a durable re-open recomputes
// the prefix from the log.
func (s *recoveryStore) lookupOperation(operationID string) (operationRecord, bool, error) {
	return operationRecord{}, false, nil
}

// publish was the durable checkpoint write plus the recent-v1.json snapshot
// publication (task 1013 removal). It now only maintains the storage identity:
// the log-prefix digest backfill that content authorization and the history
// index depend on. The checkpoint, the operation records and the on-disk
// recent snapshot are gone; callers take their no-cache fallbacks.
func (s *recoveryStore) publish(ctx context.Context) error {
	if s == nil {
		return errors.New("session: recovery store unavailable")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.identity.LogPrefixDigest == "" {
		manifest, err := readStoredManifest(filepath.Join(s.sessionDir, "manifest.json"))
		if err != nil {
			return err
		}
		prefix, err := storageLogPrefix(s.sessionDir, manifest)
		if err != nil {
			return err
		}
		if prefix != "" {
			s.identity.LogPrefixDigest = prefix
			encodedIdentity, err := json.Marshal(s.identity)
			if err != nil {
				return err
			}
			if err := fileutil.AtomicWriteFileStrict(filepath.Join(s.sessionDir, storageIdentityName), append(encodedIdentity, '\n'), 0o600); err != nil {
				return err
			}
		}
	}
	return nil
}

// readRecentSnapshot returns the on-disk published baseline, which the removal
// no longer produces: every call reports "preparing". Cold consumers fall back
// to a live runtime session or a bounded log replay at their own call site.
func readRecentSnapshot(sessionDir string, identity storageIdentity) (RecentSnapshot, error) {
	return RecentSnapshot{}, os.ErrNotExist
}

// buildRecentEntries produces the bounded public baseline. Large canonical
// messages are stored once in ContentStore and represented by a preview plus a
// range-readable reference, so recent-v1.json cannot grow with tool output.
func buildRecentEntries(ctx context.Context, sessionDir string, messages []provider.Message, sequence uint64, totalTurns int) ([]PersistentMessage, error) {
	if len(messages) > RecentMessageLimit {
		messages = messages[len(messages)-RecentMessageLimit:]
	}
	entries := make([]PersistentMessage, 0, len(messages))
	content := contentStoreForSessionDir(sessionDir)
	inlineBytes := 0
	visibleTurn := totalTurns
	for _, message := range messages {
		if agent.IsUserAuthoredTurnMessage(message) {
			visibleTurn--
		}
	}
	visibleTurn = max(visibleTurn, 0)
	for position, message := range messages {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if agent.IsUserAuthoredTurnMessage(message) {
			visibleTurn++
		}
		body, err := json.Marshal(message)
		if err != nil {
			return nil, err
		}
		entry := PersistentMessage{
			MessageID: message.ID, Position: int64(position + 1), Version: 1,
			Role: string(message.Role), Preview: messagePreview(message),
			EventSequence: sequence, VisibleTurn: visibleTurn,
		}
		if len(body) <= recentInlineBytes && inlineBytes+len(body) <= recentResponseBytes {
			entry.Inline = body
			inlineBytes += len(body)
		} else {
			ref, err := content.Put(ctx, bytes.NewReader(body), sessioncontent.Metadata{MediaType: "application/json"})
			if err != nil {
				return nil, err
			}
			entry.ContentRef = &ref
			previewBody, err := recentDisplayMessage(message)
			if err != nil {
				return nil, err
			}
			if inlineBytes+len(previewBody) <= recentResponseBytes {
				entry.Inline = previewBody
				inlineBytes += len(previewBody)
			}
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func recentDisplayMessage(message provider.Message) (json.RawMessage, error) {
	preview := detachMessages([]provider.Message{message})[0]
	preview.Content = messagePreview(message)
	preview.RawContent = ""
	preview.ProviderContent = ""
	preview.Images = nil
	preview.ResponsesItems = nil
	preview.ThinkingBlocks = nil
	if runes := []rune(preview.ReasoningContent); len(runes) > 4096 {
		preview.ReasoningContent = string(runes[:4096])
	}
	for i := range preview.ToolCalls {
		if runes := []rune(preview.ToolCalls[i].Arguments); len(runes) > 2048 {
			preview.ToolCalls[i].Arguments = string(runes[:2048])
		}
	}
	body, err := json.Marshal(preview)
	if err != nil {
		return nil, err
	}
	if len(body) <= recentInlineBytes {
		return body, nil
	}
	// Preserve the fields required to place the row even when optional display
	// metadata alone exceeds the per-message preview budget.
	return json.Marshal(provider.Message{
		ID: preview.ID, Role: preview.Role, Content: preview.Content,
		ToolCallID: preview.ToolCallID, Name: preview.Name,
		CreatedAt: preview.CreatedAt, WorkDurationMs: preview.WorkDurationMs,
	})
}

// loadRecoveryStartupState is the removed checkpoint fast path's seam (task
// 1013). With no cache to load from it always reports "no checkpoint", so the
// open path takes the full events-log replay. The returned stats stay
// well-formed (total bytes, zero read) for host telemetry.
func loadRecoveryStartupState(ctx context.Context, dir string, file *os.File, info os.FileInfo, recovery *recoveryStore, identity storageIdentity) (*startupSessionState, int64, bool, RecoveryOpenStats, bool) {
	return nil, 0, false, RecoveryOpenStats{LogBytesTotal: info.Size()}, false
}

func applyRecentCommit(messages *[]provider.Message, commit Commit) error {
	projection, _ := Project(nil)
	projection.Messages = detachMessages(*messages)
	recent := commit
	recent.Events = nil
	for _, event := range commit.Events {
		switch event.Kind {
		case "message/complete", "message/upsert", "history/replace", "legacy/import":
			recent.Events = append(recent.Events, event)
		}
	}
	if len(recent.Events) == 0 {
		return nil
	}
	if err := applyProjectionCommit(&projection, recent); err != nil {
		return err
	}
	if len(projection.Messages) > RecentMessageLimit {
		projection.Messages = append([]provider.Message(nil), projection.Messages[len(projection.Messages)-RecentMessageLimit:]...)
	}
	*messages = detachMessages(projection.Messages)
	return nil
}
