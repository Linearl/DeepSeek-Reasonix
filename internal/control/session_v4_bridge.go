package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"

	"reasonix/internal/provider"
	"reasonix/internal/session"
)

// SessionV4Bridge maintains an experimental v4 store when session_storage=v4.
// Writes still originate from the agent transcript (execution remains on JSONL
// until full Controller↔Service binding). Idle history reads prefer v4 via
// Query so UI paging can exercise the v4 index; Resume imports the legacy
// source before the first sync.
//
// Full v4-authoritative turns (#10291 execution binding) remain a follow-up.
type SessionV4Bridge struct {
	mu       sync.Mutex
	service  *session.Service
	root     string
	byAgent  map[string]string
	bindings map[string]*session.ClientBinding
	digests  map[string]string
}

// NewSessionV4Bridge opens the v4 service rooted at root.
func NewSessionV4Bridge(root string) (*SessionV4Bridge, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, fmt.Errorf("session v4 bridge: empty root")
	}
	svc, err := session.NewService("desktop-v4-bridge", session.NewFilesystemPersistence(root))
	if err != nil {
		return nil, err
	}
	return &SessionV4Bridge{
		service:  svc,
		root:     root,
		byAgent:  map[string]string{},
		bindings: map[string]*session.ClientBinding{},
		digests:  map[string]string{},
	}, nil
}

// CloseAll releases every v4 writer held by the bridge.
func (b *SessionV4Bridge) CloseAll(ctx context.Context) error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	svc := b.service
	bindings := b.bindings
	b.service = nil
	b.bindings = map[string]*session.ClientBinding{}
	b.mu.Unlock()
	if svc == nil {
		return nil
	}
	errs := make([]error, 0, len(bindings)+1)
	for _, binding := range bindings {
		if binding != nil {
			errs = append(errs, binding.Release(ctx))
		}
	}
	if err := svc.CloseAll(ctx); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// BindFresh creates a new v4 session, optionally seeds initial messages, and
// maps agentPath (may be empty until the caller knows the transcript path).
// It returns the published SessionRef.
func (b *SessionV4Bridge) BindFresh(ctx context.Context, sessionID string, seed []provider.Message, agentPath string) (session.SessionRef, error) {
	if b == nil || b.service == nil {
		return session.SessionRef{}, fmt.Errorf("session v4 bridge is closed")
	}
	runtime, err := b.service.Create(ctx, session.CreateOptions{SessionID: sessionID})
	if err != nil {
		return session.SessionRef{}, err
	}
	if len(seed) > 0 {
		payload, err := json.Marshal(map[string]any{"messages": seed})
		if err != nil {
			_ = b.service.Close(ctx, runtime.Ref())
			return session.SessionRef{}, err
		}
		if _, err := runtime.Session().Append(ctx, session.Batch{
			OperationID: "bind-fresh-seed:" + runtime.Ref().SessionID,
			Events:      []session.Event{{Kind: "legacy/import", Payload: payload}},
		}); err != nil {
			_ = b.service.Close(ctx, runtime.Ref())
			return session.SessionRef{}, err
		}
		if _, err := runtime.Session().Flush(ctx); err != nil {
			_ = b.service.Close(ctx, runtime.Ref())
			return session.SessionRef{}, err
		}
	}
	// Map the agent path; later Sync/openOrCreate attaches a client binding.
	if strings.TrimSpace(agentPath) != "" {
		key := filepath.Clean(agentPath)
		b.mu.Lock()
		b.byAgent[key] = runtime.Ref().SessionID
		b.digests[key] = ""
		b.mu.Unlock()
	}
	// Release the create owner so Windows TempDir cleanup is not blocked;
	// Sync will reopen through Open when needed.
	if err := b.service.Close(ctx, runtime.Ref()); err != nil {
		logSessionV4Bridge(err, "bind-fresh-close-owner", agentPath)
	}
	return runtime.Ref(), nil
}

// ContinueLegacy freezes a legacy transcript into v4 via ContinueImported
// (migration + open) and records the agent-path mapping.
func (b *SessionV4Bridge) ContinueLegacy(ctx context.Context, sourcePath, headID string) (session.SessionRef, error) {
	if b == nil || b.service == nil {
		return session.SessionRef{}, fmt.Errorf("session v4 bridge is closed")
	}
	sourcePath = filepath.Clean(sourcePath)
	runtime, result, err := b.service.ContinueImported(ctx, sourcePath, headID)
	if err != nil {
		return session.SessionRef{}, err
	}
	b.mu.Lock()
	b.byAgent[sourcePath] = result.TargetID
	b.digests[sourcePath] = ""
	b.mu.Unlock()
	// Drop the import owner immediately; Sync reopens on demand.
	if err := b.service.Close(ctx, runtime.Ref()); err != nil {
		logSessionV4Bridge(err, "continue-legacy-close-owner", sourcePath)
	}
	return runtime.Ref(), nil
}

// OpenExisting attaches to an existing v4 session id and optionally maps it to
// agentPath. It never creates a missing session.
func (b *SessionV4Bridge) OpenExisting(ctx context.Context, sessionID, agentPath string) (session.SessionRef, error) {
	if b == nil || b.service == nil {
		return session.SessionRef{}, fmt.Errorf("session v4 bridge is closed")
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return session.SessionRef{}, fmt.Errorf("session v4 bridge: empty session id")
	}
	ref := session.SessionRef{HostID: "desktop-v4-bridge", SessionID: sessionID}
	binding, err := b.service.Open(ctx, ref)
	if err != nil {
		return session.SessionRef{}, err
	}
	if strings.TrimSpace(agentPath) != "" {
		key := filepath.Clean(agentPath)
		b.mu.Lock()
		b.byAgent[key] = sessionID
		b.bindings[key] = binding
		b.mu.Unlock()
	} else if releaseErr := binding.Release(ctx); releaseErr != nil {
		logSessionV4Bridge(releaseErr, "open-existing-release", sessionID)
	}
	return ref, nil
}

// Service exposes the underlying service for advanced callers/tests.
func (b *SessionV4Bridge) Service() *session.Service {
	if b == nil {
		return nil
	}
	return b.service
}

// ImportLegacy migrates sourcePath into v4 when needed and records the mapping.
func (b *SessionV4Bridge) ImportLegacy(ctx context.Context, sourcePath string) (session.MigrationResult, error) {
	if b == nil || b.service == nil {
		return session.MigrationResult{}, fmt.Errorf("session v4 bridge is closed")
	}
	sourcePath = filepath.Clean(sourcePath)
	result, err := session.MigrateLegacy(ctx, sourcePath, b.root)
	if err != nil {
		return session.MigrationResult{}, err
	}
	b.mu.Lock()
	b.byAgent[sourcePath] = result.TargetID
	b.mu.Unlock()
	return result, nil
}

// SyncAgentTranscript projects the full agent transcript into the mapped v4
// session using history/replace. Empty transcripts are skipped.
func (b *SessionV4Bridge) SyncAgentTranscript(ctx context.Context, agentPath string, messages []provider.Message) error {
	if b == nil || b.service == nil {
		return nil
	}
	if len(messages) == 0 {
		return nil
	}
	agentPath = filepath.Clean(agentPath)
	digest := transcriptDigest(messages)

	b.mu.Lock()
	if b.digests[agentPath] == digest {
		b.mu.Unlock()
		return nil
	}
	sessionID := b.byAgent[agentPath]
	b.mu.Unlock()

	binding, err := b.openOrCreate(ctx, agentPath, sessionID)
	if err != nil {
		return err
	}
	runtime := binding.Runtime()
	if runtime == nil {
		return fmt.Errorf("session v4 bridge: nil runtime")
	}
	payload, err := json.Marshal(map[string]any{"messages": messages})
	if err != nil {
		return err
	}
	op := "bridge-sync:" + runtime.Ref().SessionID
	if _, err := runtime.Session().Append(ctx, session.Batch{
		OperationID: op,
		Events:      []session.Event{{Kind: "history/replace", Payload: payload}},
	}); err != nil {
		return err
	}
	if _, err := runtime.Session().Flush(ctx); err != nil {
		return err
	}

	b.mu.Lock()
	b.byAgent[agentPath] = runtime.Ref().SessionID
	b.bindings[agentPath] = binding
	b.digests[agentPath] = digest
	b.mu.Unlock()
	return nil
}

func (b *SessionV4Bridge) openOrCreate(ctx context.Context, agentPath, sessionID string) (*session.ClientBinding, error) {
	if sessionID == "" {
		sessionID = deterministicSessionID(agentPath)
	}
	b.mu.Lock()
	svc := b.service
	existing := b.bindings[agentPath]
	b.mu.Unlock()
	if svc == nil {
		return nil, fmt.Errorf("session v4 bridge is closed")
	}
	if existing != nil && existing.Runtime() != nil {
		return existing, nil
	}

	ref := session.SessionRef{HostID: "desktop-v4-bridge", SessionID: sessionID}
	binding, err := svc.Open(ctx, ref)
	if err == nil {
		return binding, nil
	}
	runtime, createErr := svc.Create(ctx, session.CreateOptions{SessionID: sessionID})
	if createErr != nil {
		// Create failed because it already exists; Open again.
		return svc.Open(ctx, ref)
	}
	binding, err = svc.Open(ctx, runtime.Ref())
	if err != nil {
		return nil, fmt.Errorf("open created v4 session %s: %w", runtime.Ref().SessionID, err)
	}
	return binding, nil
}

// RefForAgentPath returns the mapped v4 session ref for an agent transcript path.
func (b *SessionV4Bridge) RefForAgentPath(agentPath string) (session.SessionRef, bool) {
	if b == nil {
		return session.SessionRef{}, false
	}
	agentPath = filepath.Clean(agentPath)
	b.mu.Lock()
	id := b.byAgent[agentPath]
	b.mu.Unlock()
	if id == "" {
		return session.SessionRef{}, false
	}
	return session.SessionRef{HostID: "desktop-v4-bridge", SessionID: id}, true
}

// HistoryMessages reads the durable v4 transcript via Query. It returns
// ok=false when the path is not mapped or the store has no readable history so
// callers can fall back to the agent JSONL path.
func (b *SessionV4Bridge) HistoryMessages(ctx context.Context, agentPath string) ([]provider.Message, bool) {
	if b == nil || b.service == nil {
		return nil, false
	}
	ref, ok := b.RefForAgentPath(agentPath)
	if !ok {
		return nil, false
	}
	q := b.service.Query()
	if q == nil {
		return nil, false
	}
	msgs, err := q.History(ctx, ref)
	if err != nil || len(msgs) == 0 {
		return nil, false
	}
	return msgs, true
}

func deterministicSessionID(agentPath string) string {
	sum := sha256.Sum256([]byte("v4bridge\x00" + agentPath))
	return "bridge-" + hex.EncodeToString(sum[:12])
}

func transcriptDigest(messages []provider.Message) string {
	h := sha256.New()
	for _, m := range messages {
		_, _ = h.Write([]byte(m.ID))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(m.Role))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(m.Content))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func logSessionV4Bridge(err error, op, path string) {
	if err != nil {
		slog.Warn("session v4 bridge", "op", op, "path", path, "err", err)
	}
}
