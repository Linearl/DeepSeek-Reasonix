package subagentmailbox

import "sync"

// SteerFunc admits one mid-turn guidance item into a running agent, mirroring
// agent.(*Agent).SteerItem: it reports whether an active run accepted the
// item, and load is invoked only when the entry is consumed. Keeping the
// closure here (instead of the *agent.Agent) is what lets this package avoid
// importing the agent package.
type SteerFunc func(itemID string, load func() (string, error)) bool

// Registry maps running sub-agent refs to their steer handles. It is
// process-global because refs are globally unique ("sa_<timestamp>_<random>",
// minted by SubagentStore.newRef), so the desktop binding can resolve a
// handle from the ref alone without plumbing a per-session object through the
// controller stack. Publish happens inside RunSubAgentWithSession for the
// exact run lifetime (defer-unpublish covers panic unwinding).
type Registry struct {
	mu      sync.Mutex
	handles map[string]*registration
}

// registration is the indirection that makes stale closers safe: funcs are not
// comparable in Go, but registration pointers are, so a late unpublish from a
// superseded run can never delete a newer same-ref handle.
type registration struct{ steer SteerFunc }

// GlobalRegistry is the process-wide handle registry every publisher and
// sender shares.
var GlobalRegistry = NewRegistry()

// NewRegistry returns an empty registry (tests use isolated registries;
// production sends and publishes share GlobalRegistry).
func NewRegistry() *Registry {
	return &Registry{handles: map[string]*registration{}}
}

// Publish registers the steer handle for ref and returns the unpublish
// function. Publishing an empty ref (ephemeral headless runs have none) is a
// no-op: such runs are not reachable by ref and behave exactly as before.
func (r *Registry) Publish(ref string, steer SteerFunc) func() {
	if r == nil || ref == "" || steer == nil {
		return func() {}
	}
	reg := &registration{steer: steer}
	r.mu.Lock()
	r.handles[ref] = reg
	r.mu.Unlock()
	return func() {
		r.mu.Lock()
		// Only remove our own registration: a same-ref republish (a continued
		// run reusing the ref while the old handle unpublishes late) must not
		// be torn down by a stale closer.
		if r.handles[ref] == reg {
			delete(r.handles, ref)
		}
		r.mu.Unlock()
	}
}

// Lookup returns the steer handle for ref, if a run is registered.
func (r *Registry) Lookup(ref string) (SteerFunc, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	reg, ok := r.handles[ref]
	if !ok {
		return nil, false
	}
	return reg.steer, true
}
