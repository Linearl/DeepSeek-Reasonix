package busmcp

import (
	"strings"

	"reasonix/internal/sessioncollab"
)

// Mail→inject wiring (bus dev item #3). The mailbox is the durable channel:
// every bus mail for a zcode role contact is already appended to
// <contactID>.inbox.jsonl before the injector is consulted, and the role's
// own collab_inbox_read polling remains the guaranteed delivery path (the
// "自觉轮询" fallback). The injector adds only the realtime half: a nudge
// into a running zcode session through the zcodebridge, so a busy session
// learns about the mail now instead of at its next poll.
//
// Consequence that shapes the contract below: an injection failure must never
// fail or undo the delivery — the mail simply stays in the inbox (not lost,
// not duplicated) and the next poll picks it up.

// ZcodeContactPrefix marks the synthetic bus contacts derived from configured
// roles ("zcode-<role>", see New). Only deliveries to these contacts are
// injectable; Reasonix session contacts have their own steer channel.
const ZcodeContactPrefix = "zcode-"

// MailInjector is serve's bridge-side half of the mail→inject wiring. busmcp
// calls it synchronously right after a successful Deliver whose recipient is
// a zcode- contact, on the HTTP/tool-handler goroutine, so the implementation
// must be cheap, non-blocking, and panic-free — serve implements it by
// spawning its own bounded goroutine and never propagates errors (there are
// none to propagate: injection is best-effort by design).
type MailInjector interface {
	InjectMail(contactID string, msg sessioncollab.MailMessage)
}

// notifyInjector fires the realtime nudge after a successful delivery. The
// zcode- prefix gate keeps this a no-op for Reasonix session contacts, and a
// nil injector (feature unwired, e.g. bus mounted without serve wiring in
// tests) keeps the whole path out — zero overhead, same posture as the env
// gate on the bridge itself.
func (s *Server) notifyInjector(to string, msg sessioncollab.MailMessage) {
	if s.injector == nil || !strings.HasPrefix(to, ZcodeContactPrefix) {
		return
	}
	s.injector.InjectMail(to, msg)
}

// Injector returns the mail→inject hook this server was built with, or nil
// when unwired. Read-only surface for the serve-side wiring test; the hook
// itself stays behind notifyInjector.
func (s *Server) Injector() MailInjector {
	return s.injector
}
