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
	"runtime"
	"strings"
	"sync"
	"sync/atomic"

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
	rootKey  string
	byAgent  map[string]string
	bindings map[string]*session.ClientBinding
	digests  map[string]string
	// readsV4 gates the read side of the four-mode switch (task 155): modes 2
	// keep mirroring into v4 while history still comes from the legacy
	// transcript. It is live-switchable because it only changes which copy a
	// read prefers.
	readsV4 atomic.Bool
	// v3Frozen marks mode 4: the v4 store is the maintained copy and the legacy
	// transcript is a read-only fallback, so new legacy imports are refused
	// instead of minting v4 sessions from frozen history.
	v3Frozen atomic.Bool
}

// v4BridgePeers tracks the bridges this process has open for one v4 root. The
// desktop runs one bridge per tab controller, so a rebuilt controller replaces
// its predecessor while the file lock that predecessor held lives in the
// process-wide local registry: a replaced bridge must be able to hand its
// writer back, otherwise every later sync of that session keeps failing with
// ErrWriterOwned for the rest of the run (desktop.log health debt).
var (
	v4BridgePeersMu sync.Mutex
	v4BridgePeers   = map[string]map[*SessionV4Bridge]struct{}{}
)

func v4BridgeRootKey(root string) string {
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = filepath.Clean(root)
	}
	abs = filepath.Clean(abs)
	if runtime.GOOS == "windows" {
		return strings.ToLower(filepath.ToSlash(abs))
	}
	return filepath.ToSlash(abs)
}

func registerV4BridgePeer(b *SessionV4Bridge) {
	if b == nil || b.rootKey == "" {
		return
	}
	v4BridgePeersMu.Lock()
	peers := v4BridgePeers[b.rootKey]
	if peers == nil {
		peers = map[*SessionV4Bridge]struct{}{}
		v4BridgePeers[b.rootKey] = peers
	}
	peers[b] = struct{}{}
	v4BridgePeersMu.Unlock()
}

func unregisterV4BridgePeer(b *SessionV4Bridge) {
	if b == nil || b.rootKey == "" {
		return
	}
	v4BridgePeersMu.Lock()
	peers := v4BridgePeers[b.rootKey]
	delete(peers, b)
	if len(peers) == 0 {
		delete(v4BridgePeers, b.rootKey)
	}
	v4BridgePeersMu.Unlock()
}

func v4BridgePeersFor(rootKey string, self *SessionV4Bridge) []*SessionV4Bridge {
	v4BridgePeersMu.Lock()
	defer v4BridgePeersMu.Unlock()
	peers := v4BridgePeers[rootKey]
	if len(peers) == 0 {
		return nil
	}
	out := make([]*SessionV4Bridge, 0, len(peers))
	for peer := range peers {
		if peer != nil && peer != self {
			out = append(out, peer)
		}
	}
	return out
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
	bridge := &SessionV4Bridge{
		service:  svc,
		root:     root,
		rootKey:  v4BridgeRootKey(root),
		byAgent:  map[string]string{},
		bindings: map[string]*session.ClientBinding{},
		digests:  map[string]string{},
	}
	bridge.readsV4.Store(true)
	registerV4BridgePeer(bridge)
	return bridge, nil
}

// ErrLegacyReadOnly reports that mode 4 refuses to mint new v4 sessions from
// the frozen legacy transcript (v3_only -> staged reads -> v4_only).
var ErrLegacyReadOnly = errors.New("session v4 bridge: legacy transcript is read-only in v4_only mode")

// CloseAll releases every v4 writer held by the bridge.
func (b *SessionV4Bridge) CloseAll(ctx context.Context) error {
	if b == nil {
		return nil
	}
	unregisterV4BridgePeer(b)
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

// SetReadsV4 switches which copy history reads prefer (modes 3/4 read v4,
// mode 2 reads the legacy transcript). Reads consult it per call, so it can
// move under a live runtime.
func (b *SessionV4Bridge) SetReadsV4(enabled bool) {
	if b == nil {
		return
	}
	b.readsV4.Store(enabled)
}

// ReadsV4 reports whether history reads prefer the v4 store.
func (b *SessionV4Bridge) ReadsV4() bool {
	if b == nil {
		return false
	}
	return b.readsV4.Load()
}

// SetV3Frozen freezes the legacy transcript as a read-only fallback (mode 4).
func (b *SessionV4Bridge) SetV3Frozen(frozen bool) {
	if b == nil {
		return
	}
	b.v3Frozen.Store(frozen)
}

// V3Frozen reports whether new legacy imports are refused (mode 4).
func (b *SessionV4Bridge) V3Frozen() bool {
	if b == nil {
		return false
	}
	return b.v3Frozen.Load()
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
	if b.V3Frozen() {
		return session.SessionRef{}, fmt.Errorf("%w: %s", ErrLegacyReadOnly, filepath.Base(sourcePath))
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
	if b.V3Frozen() {
		return session.MigrationResult{}, fmt.Errorf("%w: %s", ErrLegacyReadOnly, filepath.Base(sourcePath))
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
		b.releaseBindings(ctx, agentPath, "")
		return fmt.Errorf("session v4 bridge: nil runtime")
	}
	// Register the client slot before the commit: every later failure path then
	// leaves the binding reachable from CloseAll, so a rejected sync can never
	// strand the v4 writer lease for the rest of the process (health debt:
	// "session writer is owned by another runtime").
	b.trackBinding(agentPath, binding)
	payload, err := json.Marshal(map[string]any{"messages": messages})
	if err != nil {
		return err
	}
	// The operation id carries the transcript digest. A retry of identical
	// content stays idempotent (same id, same hash) instead of colliding with
	// the previous batch, and a grown transcript becomes a distinct operation.
	op := "bridge-sync:" + runtime.Ref().SessionID + ":" + digest[:16]
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

// trackBinding records a client binding under agentPath so CloseAll can always
// release it, replacing (and releasing) a stale slot for the same path.
func (b *SessionV4Bridge) trackBinding(agentPath string, binding *session.ClientBinding) {
	if b == nil || binding == nil {
		return
	}
	b.mu.Lock()
	previous := b.bindings[agentPath]
	if previous == binding {
		b.mu.Unlock()
		return
	}
	b.bindings[agentPath] = binding
	b.mu.Unlock()
	if previous != nil {
		logSessionV4Bridge(previous.Release(context.Background()), "replace-binding", agentPath)
	}
}

// releaseBindings drops the client bindings this bridge holds for agentPath, or
// for every path mapped to sessionID when agentPath is empty. Releasing them is
// what lets the v4 runtime retire and hand its writer lease back.
func (b *SessionV4Bridge) releaseBindings(ctx context.Context, agentPath, sessionID string) int {
	if b == nil {
		return 0
	}
	if ctx == nil {
		ctx = context.Background()
	}
	b.mu.Lock()
	released := make([]*session.ClientBinding, 0, 1)
	for key, binding := range b.bindings {
		if binding == nil {
			continue
		}
		if key != agentPath {
			runtime := binding.Runtime()
			if sessionID == "" || runtime == nil || runtime.Ref().SessionID != sessionID {
				continue
			}
		}
		delete(b.bindings, key)
		released = append(released, binding)
	}
	b.mu.Unlock()
	for _, binding := range released {
		logSessionV4Bridge(binding.Release(ctx), "release-binding", agentPath)
	}
	return len(released)
}

// reclaimSession hands a session's writer back inside this process: it releases
// the client bindings held for sessionID by this bridge and by every peer
// bridge sharing the same v4 root, then retires those runtimes. A runtime only
// gives up its writer lease once no client binds it (Service.closeOwned refuses
// to retire a bound runtime), so both steps are required. It returns how many
// bindings were released.
func (b *SessionV4Bridge) reclaimSession(ctx context.Context, sessionID string) int {
	if b == nil {
		return 0
	}
	if ctx == nil {
		ctx = context.Background()
	}
	released := b.releaseBindings(ctx, "", sessionID)
	peers := v4BridgePeersFor(b.rootKey, b)
	for _, peer := range peers {
		released += peer.releaseBindings(ctx, "", sessionID)
	}
	b.closeSessionRuntime(ctx, sessionID)
	for _, peer := range peers {
		peer.closeSessionRuntime(ctx, sessionID)
	}
	return released
}

// closeSessionRuntime retires a session runtime that no client binds any more,
// which is what actually hands the v4 writer lease back.
func (b *SessionV4Bridge) closeSessionRuntime(ctx context.Context, sessionID string) {
	if b == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	b.mu.Lock()
	svc := b.service
	b.mu.Unlock()
	if svc == nil {
		return
	}
	ref := session.SessionRef{HostID: "desktop-v4-bridge", SessionID: sessionID}
	if err := svc.Close(ctx, ref); err != nil && !errors.Is(err, session.ErrSessionNotRunning) {
		logSessionV4Bridge(err, "reclaim-close", sessionID)
	}
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
	if existing != nil {
		if runtimeUsable(existing.Runtime()) {
			return existing, nil
		}
		// The slot points at a retired runtime; release it instead of reusing a
		// dead writer handle.
		b.releaseBindings(ctx, agentPath, "")
	}

	ref := session.SessionRef{HostID: "desktop-v4-bridge", SessionID: sessionID}
	binding, err := svc.Open(ctx, ref)
	if err == nil {
		return binding, nil
	}
	if errors.Is(err, session.ErrWriterOwned) {
		// A lease held by this bridge — or by a bridge this process already
		// replaced — would lock the session out for the rest of the run. Hand
		// the writer back (in this process) and retry exactly once.
		if reclaimed := b.reclaimSession(ctx, sessionID); reclaimed > 0 {
			logSessionV4Bridge(err, "writer-owned-reclaim", agentPath)
			binding, err = svc.Open(ctx, ref)
			if err == nil {
				return binding, nil
			}
		}
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

// runtimeUsable reports whether a bound runtime can still accept commits.
func runtimeUsable(runtime *session.Runtime) bool {
	if runtime == nil {
		return false
	}
	return runtime.Snapshot().Phase != session.RuntimeClosed
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
	if b == nil || b.service == nil || !b.ReadsV4() {
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
	// A frozen legacy transcript is a mode, not a failure: mode 4 refuses new
	// imports by design and the caller keeps working off the v4 copy, so the
	// refusal must not show up as a warning on every resume.
	if err == nil || errors.Is(err, ErrLegacyReadOnly) {
		return
	}
	slog.Warn("session v4 bridge", "op", op, "path", path, "err", err)
}
