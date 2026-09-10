// Autopilot's answer to an approval prompt nobody is going to answer (task 49
// A5). An unattended run must not stall on a dialog, and it must not be handed
// blanket permission either. The two layers here refuse what looks dangerous and
// ask the reviewer about everything else.
//
// Refusing is the safe direction: the model is told why and can look for another
// way, so a request only proceeds when something affirmatively said yes. Reaching
// no verdict falls back to waiting for the human, never to an approval.
package control

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"reasonix/internal/event"
)

// DefaultAutopilotApprovalGrace is how long an unattended run waits for a human
// to answer an approval prompt before handing the decision to the reviewer. Long
// enough to answer a prompt you are standing in front of; short enough that a
// long unattended run is not stalled for minutes per prompt.
const DefaultAutopilotApprovalGrace = 15 * time.Second

// autopilotApprovalGrace resolves the grace period for this run. Zero disables
// the reviewer fallback, restoring the plain interactive wait.
func autopilotApprovalGrace(opts Options) time.Duration {
	if !opts.Autopilot {
		return 0
	}
	if opts.AutopilotApprovalGrace > 0 {
		return opts.AutopilotApprovalGrace
	}
	return DefaultAutopilotApprovalGrace
}

// reviewUnattendedApproval decides a pending approval on behalf of an absent
// user, and reports whether it reached a decision at all.
//
// Two layers guard the run:
//
//  1. A keyword screen refuses anything dangerous on its face - deleting,
//     pushing, publishing, credentials - without asking a model. It is cheap,
//     and no argument can talk it out of a refusal.
//  2. Everything else goes to the reviewer, which judges the action alone, with
//     no transcript the requesting model could shape.
//
// A nil reviewer returns false so the caller keeps waiting for the human.
func (c *Controller) reviewUnattendedApproval(ctx context.Context, tool, subject, reason string, args json.RawMessage) (approvalReply, bool) {
	if askRiskOfQuestion(askQuestionText{Text: strings.Join([]string{tool, subject, reason, string(args)}, "\n")}) == askRiskNeedsHuman {
		c.emitAutopilotApprovalNotice(tool, subject, "refused: destructive, outward-facing, or credential-touching")
		return approvalReply{allow: false}, true
	}
	reviewer := c.guardianSess
	if reviewer == nil {
		return approvalReply{}, false
	}
	allow, why, err := reviewer.ReviewAction(ctx, tool, args, reason)
	if err != nil {
		// The reviewer could not be reached. An unattended run must not act on a
		// guess, so wait for the human instead of deciding either way.
		c.emitAutopilotApprovalNotice(tool, subject, "reviewer unavailable: "+err.Error())
		return approvalReply{}, false
	}
	if allow {
		c.emitAutopilotApprovalNotice(tool, subject, "approved by the reviewer")
		return approvalReply{allow: true}, true
	}
	if strings.TrimSpace(why) == "" {
		why = "judged unsafe to take unattended"
	}
	c.emitAutopilotApprovalNotice(tool, subject, "refused by the reviewer: "+why)
	return approvalReply{allow: false}, true
}

// emitAutopilotApprovalNotice records an unattended decision in the transcript.
// Autopilot touches files and spends money with nobody watching, so every
// decision taken on the user's behalf has to be visible afterwards.
func (c *Controller) emitAutopilotApprovalNotice(tool, subject, verdict string) {
	c.sink.Emit(event.Event{
		Kind:  event.Notice,
		Level: event.LevelInfo,
		Text:  "autopilot · " + tool + " " + subject + " — " + verdict,
	})
}
