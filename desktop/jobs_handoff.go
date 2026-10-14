package main

import (
	"reasonix/internal/control"
	"reasonix/internal/jobs"
	"reasonix/internal/workspacelease"
)

// jobsHandoffFrom extracts the background-job manager and the Delivery write
// lease from the outgoing controller of a hot model/settings rebuild (task
// 890). The pair travels together: the manager's job-start observer retains
// the lease it was created with, so one without the other would rebind half a
// pair. Nil/nil whenever the handoff cannot be established — the old
// controller is not a concrete *control.Controller (test stubs), or it runs
// without background jobs — and boot then builds fresh, preserving the
// pre-890 behavior exactly.
func jobsHandoffFrom(oldCtrl control.SessionAPI) (*jobs.Manager, *workspacelease.Owner) {
	old, ok := oldCtrl.(*control.Controller)
	if !ok || old == nil {
		return nil, nil
	}
	jm := old.JobsManager()
	lease := old.WorkspaceLeaseOwner()
	if jm == nil || lease == nil {
		return nil, nil
	}
	return jm, lease
}

// closeReplacedControllerAfterHandoff tears down the outgoing controller
// after a successful swap (task 890). When the replacement was built on the
// outgoing controller's job manager — the shared pointer proves the handoff
// actually happened — ownership first moves to the replacement (AdoptJobs),
// then the outgoing close keeps the manager and its running background jobs
// alive. Any other pairing (stubs, handoff-less builds) keeps the plain close.
func closeReplacedControllerAfterHandoff(newCtrl, oldCtrl control.SessionAPI) {
	if oldCtrl == nil {
		return
	}
	old, oldOK := oldCtrl.(*control.Controller)
	next, nextOK := newCtrl.(*control.Controller)
	if oldOK && nextOK && old != nil && next != nil {
		if jm := old.JobsManager(); jm != nil && jm == next.JobsManager() {
			next.AdoptJobs()
			old.CloseHandingOffJobs()
			return
		}
	}
	oldCtrl.Close()
}
