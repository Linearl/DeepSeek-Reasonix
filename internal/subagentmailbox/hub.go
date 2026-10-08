package subagentmailbox

import (
	"fmt"
	"strings"
	"time"
)

// Dispositions are the three delivery outcomes of Hub.Deliver plus the two
// refusal states. They match the parent-side send_message receipt wording and
// the desktop binding's disposition strings.
const (
	DispositionSteered  = "steered"  // running sub-agent accepted the item mid-turn
	DispositionQueued   = "queued"   // handle was live but admission was refused (terminal race); file stays pending
	DispositionParked   = "parked"   // no running handle; file stays pending for a later continue_from
	DispositionDisabled = "disabled" // the messaging switch is off (caller-side gate)
	DispositionNotFound = "not_found"
)

// FromUser / FromParent tag the message author in the persisted entry.
const (
	FromUser   = "user"
	FromParent = "parent"
)

// Hub scopes the channel to one session's subagents directory. Both senders
// (the parent-side send_message tool and the desktop binding) build one from
// the session's subagents dir and share the process-global handle registry.
// A nil Hub means the channel is off: every method is a safe no-op.
type Hub struct {
	// Dir is the session's subagents directory (the same dir the
	// SubagentStore persists <ref>.jsonl / <ref>.meta.json into). Mailbox
	// directories are siblings of those files, so ListSubagentsByParent's
	// *.meta.json scan and every existing sidecar sweep stay untouched.
	Dir string
}

// MailboxFor returns the mailbox for one ref, or nil when the hub is nil or
// the ref is not a valid store-minted ref.
func (h *Hub) MailboxFor(ref string) *Mailbox {
	if h == nil || !ValidRef(ref) {
		return nil
	}
	return newMailbox(h.Dir, ref)
}

// Receipt is the outcome of one delivery attempt.
type Receipt struct {
	Entry       Entry
	Disposition string // steered | queued | parked | disabled | not_found
}

// Disabled is the receipt for sends while the channel is off.
var Disabled = Receipt{Disposition: DispositionDisabled}

// Deliver persists the message first, then tries the live handle: steered
// when the running sub-agent admits it mid-turn, queued when the handle was
// live but admission lost the terminal race (the file stays pending and a
// later continue_from delivers it), parked when no handle is registered.
// Nothing is ever dropped: every disposition except a hard Append error has
// the message on disk.
func (h *Hub) Deliver(ref, from, summary, text string) (Receipt, error) {
	mb := h.MailboxFor(ref)
	if mb == nil {
		return Receipt{Disposition: DispositionNotFound}, fmt.Errorf("subagent reference %q is not a valid mailbox target", ref)
	}
	entry, err := mb.Append(from, summary, text)
	if err != nil {
		return Receipt{}, err
	}
	steer, ok := GlobalRegistry.Lookup(ref)
	if !ok {
		return Receipt{Entry: entry, Disposition: DispositionParked}, nil
	}
	if steer(entry.ID, mb.Loader(entry.ID)) {
		return Receipt{Entry: entry, Disposition: DispositionSteered}, nil
	}
	return Receipt{Entry: entry, Disposition: DispositionQueued}, nil
}

// DrainForContinue renders every pending message of a continued-from ref as a
// host-framing block for the resumed run's prompt, marking each delivered.
// An empty ref (fresh dispatch) or an empty mailbox returns "". This is the
// delivery path for messages that arrived after the sub-agent's run ended.
func (h *Hub) DrainForContinue(ref string) string {
	mb := h.MailboxFor(ref)
	if mb == nil {
		return ""
	}
	entries, err := mb.List()
	if err != nil || len(entries) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<pending-mail event=\"SubagentMailbox\">\n")
	b.WriteString("The following messages arrived for this subagent while it was not running and were persisted in its mailbox. They are delivered now with this resumed run; treat them as user guidance for the task below.\n")
	for _, entry := range entries {
		b.WriteString("\n--- message " + entry.ID + " ---\n")
		if entry.Summary != "" {
			b.WriteString("Summary: " + entry.Summary + "\n")
		}
		b.WriteString("From: " + entry.From + "\n")
		if ts, err := time.Parse(time.RFC3339Nano, entry.CreatedAt); err == nil {
			b.WriteString("Received: " + ts.Format(time.RFC3339) + "\n")
		}
		b.WriteString("\n" + entry.Text + "\n")
		if err := mb.MarkDelivered(entry.ID); err != nil {
			// Leave it pending: the next drain re-renders it rather than
			// losing a message that could not be renamed to delivered.
			b.WriteString("(note: this message could not be marked delivered and may be delivered again)\n")
		}
	}
	b.WriteString("</pending-mail>")
	return b.String()
}

// PendingCount reports how many messages are pending for one ref (0 for an
// invalid ref or absent mailbox). The desktop panel uses it for the
// "undelivered" badge.
func (h *Hub) PendingCount(ref string) int {
	return h.MailboxFor(ref).PendingCount()
}

// RemoveAllMailbox deletes the whole mailbox directory of one ref.
func (h *Hub) RemoveAllMailbox(ref string) error {
	return h.MailboxFor(ref).RemoveAll()
}

// MailboxDirName returns the directory name used for one ref's mailbox inside
// the subagents directory (shared with the agent-side record cleanup).
func MailboxDirName(ref string) string {
	return ref + mailboxDirSuffix
}
