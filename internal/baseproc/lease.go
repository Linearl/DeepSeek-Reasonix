package baseproc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"reasonix/internal/baseproc/pidalive"
)

// Slice S1c — session attach/detach lease accounting (design §6, matrix C3/C4).
//
// A lease is pure in-memory bookkeeping inside the serve process: it says
// "session S is attached from workspace root R, owned by client pid P". It is
// deliberately NOT a file lock — the *.jsonl.lease.json / writer_id system
// keeps its own semantics untouched (design §6, R8: two layers, two jobs).
//
// Ownership key is the client pid (design §6 C4): a client that crashes
// without detaching leaves a lease behind, and the next base.hello from a
// surviving client sweeps those whose owner is gone.

// lease is one base.attach claim.
type lease struct {
	ID             string
	SessionID      string
	Root           string
	WorkspaceScope string
	OwnerPID       int
	CreatedAt      time.Time
}

// leaseTable is the process-scoped lease store. Every method is safe for
// concurrent use: attaches from two sessions race by design (matrix C1's
// siblings), and the health loop may reclaim while a request is in flight.
type leaseTable struct {
	mu     sync.Mutex
	byID   map[string]lease
	nextID uint64
}

func newLeaseTable() *leaseTable {
	return &leaseTable{byID: make(map[string]lease)}
}

// create records one lease and returns it (ID is pid + sequence, so a log
// line names its owner without decoding).
func (t *leaseTable) create(p AttachParams, ownerPID int) lease {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.nextID++
	l := lease{
		ID:             fmt.Sprintf("l%d.%d", ownerPID, t.nextID),
		SessionID:      p.SessionID,
		Root:           p.Root,
		WorkspaceScope: p.WorkspaceScope,
		OwnerPID:       ownerPID,
		CreatedAt:      time.Now(),
	}
	t.byID[l.ID] = l
	return l
}

// release drops one lease and reports whether it existed. An unknown id is
// not an error: a lease already swept by the orphan reclaim (C4) must not
// turn a well-behaved detach into a failure.
func (t *leaseTable) release(id string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.byID[id]; !ok {
		return false
	}
	delete(t.byID, id)
	return true
}

// reclaimDead sweeps leases owned by a pid other than current that is no
// longer alive — matrix C4's "漏 detach" path. It returns how many went.
// Conservative by construction: a reused pid that happens to look alive keeps
// its lease (never wrongly drops a live client's claim).
func (t *leaseTable) reclaimDead(currentPID int, alive func(int) bool) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	removed := 0
	for id, l := range t.byID {
		if l.OwnerPID == currentPID {
			continue
		}
		if alive != nil && alive(l.OwnerPID) {
			continue
		}
		delete(t.byID, id)
		removed++
	}
	return removed
}

// count is the test/observability read of the table size.
func (t *leaseTable) count() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.byID)
}

// leaseAlive reports whether a client pid still names a live process.
// Overridden in tests; kept as a variable so the C4 sweep can be driven
// deterministically instead of depending on real process lifetimes. The
// oracle itself moved to the zero-dependency leaf internal/baseproc/pidalive
// (任务511 复发断根) so sessioncollab/collabinbox can share it without an
// import cycle through config.
var leaseAlive = pidalive.Alive

// AttachSessionAccounting registers the session face on this server:
// base.attach / base.detach plus the CapSessions capability base.hello
// advertises, and the base.hello hook that sweeps orphan leases (C4).
//
// It is opt-in, mirroring AttachToolSurface: a bare core server keeps the S1a
// contract (no session capability, methods answer -32601, connection usable),
// and `reasonix base serve --stdio` turns it on — the real subprocess always
// has it.
func (s *Server) AttachSessionAccounting() {
	table := newLeaseTable()
	s.mu.Lock()
	s.leases = table
	s.mu.Unlock()
	s.Register(MethodAttach, guardHandler("base.attach", s.handleAttach))
	s.Register(MethodDetach, guardHandler("base.detach", s.handleDetach))
	s.mu.Lock()
	s.onHello = s.reclaimOrphansOnHello
	s.mu.Unlock()
	s.AddCapabilities(CapSessions)
}

// sessionLeases loads the attached table (nil when accounting is off).
func (s *Server) sessionLeases() *leaseTable {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.leases
}

// helloClientPID is the owner key the most recent base.hello declared.
func (s *Server) helloClientPID() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.clientPID
}

// reclaimOrphansOnHello records the declaring client and sweeps leases whose
// owner is gone (design §6 C4).
func (s *Server) reclaimOrphansOnHello(clientPID int) {
	s.mu.Lock()
	s.clientPID = clientPID
	s.mu.Unlock()
	table := s.sessionLeases()
	if table == nil {
		return
	}
	if removed := table.reclaimDead(clientPID, leaseAlive); removed > 0 {
		slog.Info("base: orphan leases reclaimed",
			"count", removed, "client_pid", clientPID)
	}
}

// handleAttach answers base.attach: validate, record, hand back the lease id.
// Root is mandatory (design §5) — it is the whole reason the base learns a
// workspace in the first place.
func (s *Server) handleAttach(_ context.Context, params json.RawMessage) (any, error) {
	var p AttachParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &RPCError{Code: CodeInvalidParams, Message: fmt.Sprintf("attach params: %v", err)}
	}
	if strings.TrimSpace(p.SessionID) == "" {
		return nil, &RPCError{Code: CodeInvalidParams, Message: "session_id is required"}
	}
	if strings.TrimSpace(p.Root) == "" {
		return nil, &RPCError{Code: CodeInvalidParams, Message: "root is required (attach carries the workspace root)"}
	}
	table := s.sessionLeases()
	if table == nil {
		// The handler only exists when accounting was attached; reaching here
		// is inconsistent wiring, not a client error.
		return nil, errors.New("baseproc: session accounting not attached")
	}
	created := table.create(p, s.helloClientPID())
	return AttachResult{LeaseID: created.ID}, nil
}

// handleDetach answers base.detach. Missing lease_id is a client mistake
// (-32602); an id nobody holds is a no-op that answers ok=false (idempotent —
// see leaseTable.release).
func (s *Server) handleDetach(_ context.Context, params json.RawMessage) (any, error) {
	var p DetachParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &RPCError{Code: CodeInvalidParams, Message: fmt.Sprintf("detach params: %v", err)}
	}
	if strings.TrimSpace(p.LeaseID) == "" {
		return nil, &RPCError{Code: CodeInvalidParams, Message: "lease_id is required"}
	}
	table := s.sessionLeases()
	if table == nil {
		return nil, errors.New("baseproc: session accounting not attached")
	}
	return DetachResult{OK: table.release(p.LeaseID)}, nil
}
