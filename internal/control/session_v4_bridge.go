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

// SessionV4Bridge mirrors the agent transcript into an experimental v4 store
// when session_storage=v4. Chat execution remains on the agent JSONL path so
// the experiment does not regress continue; v4 is the durable mirror users can
// inspect, and legacy continue imports the source before the first sync.
//
// This is intentionally not the upstream Controller↔Service execution binding
// (#10291). Full v4-authoritative turns are a follow-up after storage QA.
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
