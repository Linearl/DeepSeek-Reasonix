package serve

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"reasonix/internal/safego"
	"reasonix/internal/sessioncollab"
	"reasonix/internal/zcodebridge"
)

// Mail→inject consumer (bus dev item #3): when a bus mail lands in a zcode
// role's inbox and the zcodebridge has a live app-server child, nudge the
// running session with a sendText so it learns about the mail now instead of
// at its next collab_inbox_read poll.
//
// Failure semantics are the point: the inbox is the durable channel, so every
// failure here (bridge not connected, session list failed, no live session,
// sendText refused) only downgrades to the polling fallback — the mail stays
// in the inbox, never lost and never duplicated, and the nudge logs at debug.
// The env gate rides the bridge itself: with REASONIX_ZCODE_BRIDGE off there
// is no child, ZcodeBridge() is nil, and this whole path is two atomic reads
// away from a return (zero overhead).

const (
	// zcodeBridgeInjectTimeout bounds one nudge (list + sendText). The nudge
	// runs on its own goroutine, so this never blocks the mail handler; it
	// only keeps a wedged child from leaking goroutines.
	zcodeBridgeInjectTimeout = 15 * time.Second
	// zcodeBridgeInjectBodyClip caps the mail excerpt carried in the nudge.
	// The full body always lives in the inbox; the nudge must stay small
	// enough that a busy session can triage it at a glance.
	zcodeBridgeInjectBodyClip = 200
)

// mailStartNowMark is the explicit mail-level mark that would upgrade the
// injection from queue to startNow (phone semantics). The sessioncollab mail
// vocabulary today is steer|followup only and Deliver rejects unknown values,
// so nothing can carry this mark yet — until the bus grows one, every nudge
// is queue and the choice is pinned here where the next person will look.
const mailStartNowMark = "start_now"

// zcodeInjectionBridge is the bridge subset the nudge consumes. An interface
// only so tests can fake the child; production always passes *zcodebridge.Bridge.
type zcodeInjectionBridge interface {
	List(ctx context.Context) ([]zcodebridge.Session, error)
	SendText(ctx context.Context, sessionID, text string, d zcodebridge.Delivery) (zcodebridge.Ack, error)
}

// zcodeMailInjector implements busmcp.MailInjector on top of the server's
// live bridge. The synchronous half is two atomic reads (cheap enough for
// the busmcp tool-handler goroutines); the actual list+sendText runs on a
// safego-supervised goroutine bounded by zcodeBridgeInjectTimeout.
type zcodeMailInjector struct {
	s *Server
}

// InjectMail implements busmcp.MailInjector. Never fails, never blocks on IO.
func (z zcodeMailInjector) InjectMail(contactID string, msg sessioncollab.MailMessage) {
	bridge := z.s.ZcodeBridge()
	if bridge == nil {
		// Gate off or child down (also covers the window between child death
		// and rebuild): mail stays in the inbox for the role's own polling.
		slog.Debug("serve: bus mail nudge skipped, zcode bridge not connected (mail stays in inbox)",
			"contact", contactID, "msg", msg.ID)
		return
	}
	ws := ""
	if p := z.s.zcodeBridgeWorkspace.Load(); p != nil {
		ws = *p
	}
	safego.Go("serve.zcodebridge.inject", func() {
		injectBusMailVia(bridge, ws, contactID, msg)
	})
}

// injectBusMailVia is the synchronous nudge: resolve the target session from
// the bridge's live enumeration, then queue the pointer text into it.
func injectBusMailVia(bridge zcodeInjectionBridge, ws, contactID string, msg sessioncollab.MailMessage) {
	ctx, cancel := context.WithTimeout(context.Background(), zcodeBridgeInjectTimeout)
	defer cancel()
	sessions, err := bridge.List(ctx)
	if err != nil {
		slog.Debug("serve: bus mail nudge skipped, session list failed (mail stays in inbox)",
			"contact", contactID, "msg", msg.ID, "err", err)
		return
	}
	target, ok := pickInjectSession(sessions, ws)
	if !ok {
		slog.Debug("serve: bus mail nudge skipped, no live zcode session (mail stays in inbox)",
			"contact", contactID, "msg", msg.ID)
		return
	}
	text := injectTextFor(contactID, msg)
	ack, err := bridge.SendText(ctx, target.SessionID, text, injectDeliveryFor(msg))
	if err != nil {
		var ackErr *zcodebridge.AckError
		if errors.As(err, &ackErr) {
			// The frozen face worked; the session's CommandInbox refused this
			// command (busy race, session inactive, ...). Same downgrade.
			slog.Debug("serve: bus mail nudge refused by session inbox (mail stays in inbox)",
				"contact", contactID, "msg", msg.ID, "session", target.SessionID, "ack", ackErr.Ack.Status, "reason", ackErr.Ack.ReasonCode)
			return
		}
		slog.Debug("serve: bus mail nudge failed (mail stays in inbox)",
			"contact", contactID, "msg", msg.ID, "session", target.SessionID, "err", err)
		return
	}
	slog.Info("serve: bus mail nudged into zcode session",
		"contact", contactID, "msg", msg.ID, "session", target.SessionID, "command", ack.CommandID, "ack", ack.Status)
}

// pickInjectSession chooses which live session receives the nudge. The bus
// topology this slice targets is one workspace per serve (see the bus
// activation report): the bridge child is spawned in that workspace, so the
// rule is "prefer the most recently updated session whose workspace matches
// the bridge child's, else the most recently updated session overall". Roles
// share their inbox by convention (single drain consumer), so delivering the
// nudge to the live session of the workspace reaches whoever would poll it.
// A false result means "nothing live" — the mail waits in the inbox.
func pickInjectSession(sessions []zcodebridge.Session, ws string) (zcodebridge.Session, bool) {
	wsClean := cleanWorkspacePath(ws)
	var best, inWS zcodebridge.Session
	haveBest, haveInWS := false, false
	for _, sess := range sessions {
		if strings.TrimSpace(sess.SessionID) == "" {
			continue
		}
		if !haveBest || sess.UpdatedAtMs > best.UpdatedAtMs {
			best, haveBest = sess, true
		}
		if wsClean == "" {
			continue
		}
		if sClean := cleanWorkspacePath(sess.WorkspacePath); sClean != "" && sameWorkspacePath(sClean, wsClean) {
			if !haveInWS || sess.UpdatedAtMs > inWS.UpdatedAtMs {
				inWS, haveInWS = sess, true
			}
		}
	}
	if haveInWS {
		return inWS, true
	}
	return best, haveBest
}

func cleanWorkspacePath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	return filepath.Clean(p)
}

// sameWorkspacePath compares cleaned workspace paths. Fold case: Windows
// filesystems are case-insensitive and the CLI reports paths it was given.
func sameWorkspacePath(a, b string) bool {
	return strings.EqualFold(a, b)
}

// injectDeliveryFor maps the mail vocabulary onto the sendText delivery. Mail
// is mail: queue semantics, never a turn preemption — unless a future bus
// mark explicitly asks for startNow (see mailStartNowMark).
func injectDeliveryFor(msg sessioncollab.MailMessage) zcodebridge.Delivery {
	if strings.TrimSpace(msg.Delivery) == mailStartNowMark {
		return zcodebridge.DeliveryStartNow
	}
	return zcodebridge.DeliveryQueue
}

// injectTextFor renders the nudge. It deliberately points at the inbox
// instead of carrying the full body: the injected text becomes a session
// turn, and two full copies of the mail (nudge + inbox) would invite the
// session to act on the content twice.
func injectTextFor(contactID string, msg sessioncollab.MailMessage) string {
	body := msg.Body
	if n := []rune(body); len(n) > zcodeBridgeInjectBodyClip {
		body = string(n[:zcodeBridgeInjectBodyClip]) + "…"
	}
	body = strings.ReplaceAll(body, "\n", " ")
	return fmt.Sprintf("[reasonix bus] contact %s has new mail: id=%s from=%s delivery=%s\n"+
		"This is a realtime nudge only — the mail itself is in your bus inbox; read it with collab_inbox_read (id %s). "+
		"Do not treat this nudge as the mail body.\n"+
		"Excerpt: %s",
		contactID, msg.ID, msg.From, msg.Delivery, msg.ID, body)
}
