package serve

import (
	"encoding/json"
	"net/http"
	"strings"

	"reasonix/internal/agent"
	"reasonix/internal/control"
)

// spawnChildSession is the task-540 remote entry (experimental_gc_child_session):
// the GrandCouncil client derives a child session from the conversation it is
// driving. The child starts as a full copy of the current conversation in its
// own session file (BranchToFile), the foreground moves to it, and the session
// lease follows the foreground through the same single rebind path /new and
// /fork use — one ownership mechanism, no second writer model. The source
// session keeps its transcript and stays resumable from the session list, so
// the remote client can switch back with /resume at any time.
//
// The route is only mounted while the lab switch is on (fail-closed otherwise),
// and GET /capabilities advertises "child-session" alongside it.
func (s *Server) spawnChildSession(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if r.Body != nil && r.Body != http.NoBody {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, foregroundMutationMaxBody)).Decode(&body); err != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}
	}
	// Session-path-changing critical sequence: same binding lock and fence as
	// /new and /fork, so the controller and the lease keeper move together.
	s.bindMu.Lock()
	defer s.bindMu.Unlock()
	if !s.validateSwitchExpectedLocked(w, r) {
		return
	}
	// Branching snapshots the source session first; a mirrored foreground has
	// no write authority (a local runtime owns the live transcript), so refuse
	// with the takeover wording — the same posture as /fork.
	if s.rejectMirroredForegroundLocked(w) {
		return
	}
	sourcePath := agent.CanonicalSessionPath(s.ctl().SessionPath())
	childPath, err := s.ctl().BranchToFile(strings.TrimSpace(body.Name))
	if err != nil {
		if control.IsSessionRotationBusy(err) {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if ctrl, ok := s.ctl().(*control.Controller); ok {
		s.setControllerPath(ctrl, ctrl.SessionPath())
	}
	s.bc.ResetSessionPath(s.ctl().SessionPath())
	if err := s.rebindSessionLease(s.ctl().SessionPath()); err != nil {
		http.Error(w, sessionInUseError(err), http.StatusConflict)
		return
	}
	// The controller switched to the child; tell SSE clients (the remote side
	// refreshes its conversation from the new foreground) like /new does.
	s.announceSessionChanged(s.ctl().SessionPath(), true)
	// childPath is the transcript the controller is on now; the source session
	// stays on disk, resumable by path.
	writeJSON(w, map[string]string{"path": agent.CanonicalSessionPath(childPath), "source": sourcePath})
}
