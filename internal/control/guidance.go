package control

import (
	"fmt"
	"maps"
	"strings"

	"reasonix/internal/sessioninbox"
)

// 任务 229 G3（引导注入单入口）: one named entry for cross-session guidance
// injection. The 229 audit found four injection roads assembled by hand
// (readiness catch-up, the steer queue, the composer shelf display, the
// collab pump); ②③④ already share the durable inbox, so the unification is
// this thin wrapper over TryEnqueueAndSteer that fixes the guidance grammar
// (source / priority / text) in one place instead of every future consumer
// re-assembling an InboxRequest. Scope notes from the v2 evaluation:
//   - ① readiness_catch_up stays independent on purpose — it is the run
//     loop's synchronous retry prompt, not cross-session guidance (merging
//     would break its host-generated transcript semantics);
//   - priority is advisory and recorded on the durable item for audit; the
//     first-class store field (dispatch-ordering semantics) is deferred to
//     the consumer that needs it — the persistence migration is the heavy
//     half and no current consumer orders by priority.
type GuidancePriority string

const (
	GuidancePriorityLow    GuidancePriority = "low"
	GuidancePriorityNormal GuidancePriority = "normal"
	GuidancePriorityUrgent GuidancePriority = "urgent"
)

// GuidancePriorityExtraKey records the (normalized) priority on the durable
// inbox item until the store gains a first-class field.
const GuidancePriorityExtraKey = "guidance_priority"

// NormalizeGuidancePriority fails open: an unrecognised hint must never drop
// the guidance, so it degrades to normal.
func NormalizeGuidancePriority(p GuidancePriority) GuidancePriority {
	switch p {
	case GuidancePriorityLow, GuidancePriorityUrgent:
		return p
	default:
		return GuidancePriorityNormal
	}
}

// GuidanceRequest is the InjectGuidance payload. Text is trimmed; empty text
// is rejected (sessioninbox.ErrEmpty).
type GuidanceRequest struct {
	Source   string // who injected this (e.g. "collab", "bot", "trigger"); default "guidance"
	Priority GuidancePriority
	Text     string
	// Idempotency keys the durable item (same key = same item, not a new one).
	Idempotency string
	// ExpectedSessionPath fences the injection to one exact session.
	ExpectedSessionPath string
	// Extra carries caller metadata; InjectGuidance adds the priority record.
	Extra map[string]string
}

// InjectGuidance is the package-level entry: steer-first injection with the
// durable fallback TryEnqueueAndSteer already provides (rejected steer
// degrades to a queued follow-up; the receipt names the disposition).
func InjectGuidance(inbox Inbox, req GuidanceRequest) (sessioninbox.InboxReceipt, error) {
	if inbox == nil {
		return sessioninbox.InboxReceipt{}, fmt.Errorf("guidance injection requires an inbox")
	}
	text := strings.TrimSpace(req.Text)
	if text == "" {
		return sessioninbox.InboxReceipt{}, sessioninbox.ErrEmpty
	}
	priority := NormalizeGuidancePriority(req.Priority)
	source := strings.TrimSpace(req.Source)
	if source == "" {
		source = "guidance"
	}
	extra := maps.Clone(req.Extra)
	if extra == nil {
		extra = map[string]string{}
	}
	extra[GuidancePriorityExtraKey] = string(priority)
	return inbox.TryEnqueueAndSteer(InboxRequest{
		ExpectedSessionPath: req.ExpectedSessionPath,
		Intent:              sessioninbox.IntentSteer,
		Display:             text,
		Raw:                 text,
		Submit:              text,
		Source:              source,
		Idempotency:         strings.TrimSpace(req.Idempotency),
		Extra:               extra,
	})
}

// InjectGuidance is the controller-side form of the G3 single entry.
func (c *Controller) InjectGuidance(req GuidanceRequest) (sessioninbox.InboxReceipt, error) {
	return InjectGuidance(c, req)
}
