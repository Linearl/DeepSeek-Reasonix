package servepool

// Task 539 phase-1 guided-message routing: while a desktop-held session is
// YIELDING to a remote takeover, the desktop turn is the only live writer —
// a remote guided message must reach THAT turn, not the serve foreground
// (which would either 409 on the fence or steer the wrong session). The
// gateway (running inside the desktop process) intercepts POST
// /p/<id>/inbox/items requests that carry a session selector, and the
// app-owned gate injects the item into the holding tab's controller.
// Requests without a session selector forward untouched (the serve inbox is
// foreground-scoped by design).

// InboxGateRequest is one intercepted remote inbox enqueue.
type InboxGateRequest struct {
	ProjectID   string
	ProjectRoot string
	SessionName string
	// Intent is the parsed body intent ("steer" or "" for followup).
	Intent string
	// Body is the raw request body; the gate re-exposes it when it does not
	// handle the request.
	Body []byte
}

// InboxGateFunc returns handled=true when the gate answered the client itself
// (status + contentType + payload); handled=false forwards the request.
type InboxGateFunc func(req InboxGateRequest) (handled bool, status int, contentType string, payload []byte)

// SetInboxGate installs the inbox gate. Call before serving traffic.
func (g *Gateway) SetInboxGate(fn InboxGateFunc) {
	g.inboxGate = fn
}
