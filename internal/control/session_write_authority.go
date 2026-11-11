package control

import (
	"context"
	"errors"

	"reasonix/internal/agent"
	"reasonix/internal/event"
)

// BindSessionWriteAuthority issues a generation-bound write authority from
// the lease's SessionWriter onto the executor session. A nil lease clears
// the binding so later saves fail closed instead of forking recovery.
func (c *Controller) BindSessionWriteAuthority(lease *agent.SessionLease) error {
	if c == nil {
		return nil
	}
	gen := agent.NextSessionWriteGeneration()
	if c.executor == nil {
		return nil
	}
	sess := c.executor.Session()
	if sess == nil {
		return nil
	}
	sess.RequireWriteAuthority()
	if lease == nil {
		sess.ClearWriteAuthority()
		return nil
	}
	// Mint through the lease writer so saves serialize and update its baseline.
	// Recovery rebinds the lease before sessionPath updates; saves still
	// enforce auth.Covers(targetPath).
	if err := lease.Writer().Bind(sess, gen); err != nil {
		sess.ClearWriteAuthority()
		return err
	}
	return nil
}

// WriteAuthorityGeneration reports the generation currently bound on this
// controller. Tests use it to prove old generations become stale after rebind.
func (c *Controller) WriteAuthorityGeneration() uint64 {
	if c == nil || c.executor == nil || c.executor.Session() == nil {
		return 0
	}
	return c.executor.Session().WriteAuthority().Generation()
}

// noticeSessionNotWritable is the single admission-refusal line for the
// stale/missing write authority family (tl1311). The remedy names the exact
// user action — closing and reopening the session tab is the resource remount
// that re-acquires the lease and rebinds a fresh write authority
// (desktop/tabs.go buildTabController → commitStartupWriteAuthorityLocked →
// bindTabWriteAuthority); the abstract "reopen" alone left users without the
// next step. tl1296: the trailing clause names the crash-recovery state the
// 2026-10-31 crash loop kept producing — the refusal usually means the
// session is mid-recovery after an app crash, not a dead session — and the
// desktop frontend replaces this line wholesale through the notice code below
// (localized copy, three languages).
const noticeSessionNotWritable = "input was not accepted: this session is no longer writable — close and reopen this session's tab and try again (right after an app crash this is the recovery still settling; reopening completes it)"

// noticeCodeWriteAuthorityStale is the stable notice code for the
// stale/missing write authority refusal family (tl1296). The desktop frontend
// maps it to a localized copy that carries the "recovering after an app
// crash" state explanation (notice.writeAuthorityStale, en/zh/zh-TW); every
// other frontend shows noticeSessionNotWritable verbatim.
const noticeCodeWriteAuthorityStale = "session_write_authority_stale"

func (c *Controller) submitCommandOrTurn(trimmed, input, display string, scopedRefsOnly bool, editedOriginal, format string) {
	if err := c.ensureWriteAuthorityReady(); err != nil {
		c.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelWarn, Code: noticeCodeWriteAuthorityStale, Text: noticeSessionNotWritable})
		return
	}
	c.submitCommandOrTurnReady(trimmed, input, display, scopedRefsOnly, editedOriginal, format)
}

// Run verifies the live write generation before synchronous headless turns.
func (c *Controller) Run(ctx context.Context, input string) error {
	if err := c.ensureWriteAuthorityReady(); err != nil {
		return err
	}
	return c.runReady(ctx, input)
}

// RebindSessionWriteAuthority is a convenience for keepers that already hold a
// lease: it issues a fresh generation so any previous controller authority for
// the same lease object is immediately stale.
func (c *Controller) RebindSessionWriteAuthority(lease *agent.SessionLease) error {
	return c.BindSessionWriteAuthority(lease)
}

// ensureWriteAuthorityReady refuses turn admission when the session path is
// set but the bound authority is missing or stale. Empty session paths (no
// persistence yet) are allowed.
func (c *Controller) ensureWriteAuthorityReady() error {
	if c == nil || c.executor == nil {
		return nil
	}
	path := c.SessionPath()
	if path == "" {
		return nil
	}
	sess := c.executor.Session()
	if sess == nil {
		return nil
	}
	auth := sess.WriteAuthority()
	if auth == nil {
		if sess.WriteAuthorityRequired() {
			return agent.ErrSessionWriteAuthorityMissing
		}
		return nil
	}
	if auth.Covers(path) {
		return nil
	}
	return agent.ErrSessionWriteAuthorityStale
}

// WriteAuthorityReady exposes the exact verdict the turn admission gates apply
// (ensureWriteAuthorityReady) to hosts that need to classify a submission's
// fate after the fact (tl1342): the desktop submit surface calls the controller
// asynchronously and learns nothing from the void Submit* contracts, so right
// after a submit returns it probes this method — a stale/missing verdict means
// the input was refused at the gate (the gates run before admission and drop
// with a notice only) and the host may heal and retry.
func (c *Controller) WriteAuthorityReady() error {
	return c.ensureWriteAuthorityReady()
}

// IssueAndBindWriteAuthority is used by SessionLeaseKeeper and desktop tabs
// after a successful lease acquire/rebind.
func IssueAndBindWriteAuthority(c *Controller, lease *agent.SessionLease) error {
	if c == nil {
		return nil
	}
	return c.BindSessionWriteAuthority(lease)
}

// authoritySaveError classifies authority failures so recovery does not fire.
func authoritySaveError(err error) bool {
	return errors.Is(err, agent.ErrSessionWriteAuthorityMissing) ||
		errors.Is(err, agent.ErrSessionWriteAuthorityStale)
}
