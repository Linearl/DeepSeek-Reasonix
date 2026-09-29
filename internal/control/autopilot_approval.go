// Autopilot's answer to an approval prompt nobody is going to answer (task 49
// A5). An unattended run must not stall on a dialog, and it must not be handed
// blanket permission either. The two layers here refuse what looks dangerous and
// ask the reviewer about everything else.
//
// Refusing is the safe direction: the model is told why and can look for another
// way, so a request only proceeds when something affirmatively said yes. Once the
// grace elapses there is no human left to wait for — an unavailable reviewer
// refuses rather than parking the run forever (task 109 B6).
//
// Task 52 adds a configurable decision-maker tier for the reversible half:
// guardian (default, A5), parent (the requesting session self-approves low-risk
// with an audit notice), or human (never auto-decide). High-risk approvals
// refuse on every tier.
package control

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"

	"reasonix/internal/event"
)

// Approval tiers for reversible unattended approvals (task 52).
const (
	// ApprovalTierGuardian is the default: the independent reviewer decides
	// reversible approvals; high-risk ones refuse without asking anyone.
	ApprovalTierGuardian = "guardian"
	// ApprovalTierParent lets the requesting session approve reversible
	// actions itself. The decision is audited in the transcript. High-risk
	// actions still refuse — self-approval never covers destructive or
	// outward-facing work.
	ApprovalTierParent = "parent"
	// ApprovalTierHuman disables every auto-decision. Reversible approvals
	// also refuse after the grace period instead of handing off to a reviewer.
	ApprovalTierHuman = "human"
)

// NormalizeApprovalTier maps a config value onto a supported tier. Empty or
// unknown values keep the conservative guardian default so old configs and
// typos never silently become "parent".
func NormalizeApprovalTier(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case ApprovalTierParent:
		return ApprovalTierParent
	case ApprovalTierHuman:
		return ApprovalTierHuman
	default:
		return ApprovalTierGuardian
	}
}

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
// Layers guard the run:
//
//  1. A keyword screen refuses anything dangerous on its face - deleting,
//     pushing, publishing, credentials - without asking a model. It is cheap,
//     and no argument can talk it out of a refusal. This layer is absolute:
//     no approval tier can override it.
//  2. Everything else is reversible. The configured ApprovalTier picks the
//     decision-maker: parent self-approves with an audit notice, guardian
//     judges the action alone with no transcript, human refuses after grace.
//
// A nil reviewer or a failed review REFUSES rather than waiting forever: the
// model is told why and can try another way. Waiting for a human is not an
// option once the grace period has already elapsed (task 109 B6).
func (c *Controller) reviewUnattendedApproval(ctx context.Context, tool, subject, reason string, args json.RawMessage) (approvalReply, bool) {
	if askRiskOfApproval(tool, subject, reason, args) == askRiskNeedsHuman {
		c.emitAutopilotApprovalNotice(tool, subject, "refused: destructive, outward-facing, or credential-touching")
		return approvalReply{allow: false}, true
	}
	// Task 365 C6: this timeout fires for child sessions too — their
	// inbox-started turns run the same unattended review, and before this
	// fix a child with no usable cascade grant just refused here while its
	// real decision-maker (the task source) was reachable. Before the tier
	// machinery refuses, re-evaluate the cascade once: the create-path grant
	// (task 365 C5) or a mail grant may have landed while the request was
	// waiting. Freshness, source liveness and the hop ceiling are all
	// enforced inside cascadeDelegateFor / CascadeHopExhausted. High-risk
	// asks never reach this point (askRiskNeedsHuman returned above), so
	// they stay human-only. A miss falls through to the original tier logic
	// unchanged — the fail-closed direction is preserved.
	if c.onCascadeDelegate != nil && config.CascadeApprovalLive() && !agent.CascadeHopExhausted(ctx) {
		if delegate, source, ok := c.onCascadeDelegate(c.SessionPath()); ok && delegate != nil {
			question := event.AskQuestion{
				ID:     "approval-" + tool,
				Header: tool,
				Prompt: approvalCascadePrompt(subject, reason),
				Options: []event.AskOption{
					{Label: "Allow", Description: "grant this one request"},
					{Label: "Deny", Description: "refuse and let the model find another way"},
				},
			}
			answers, askErr := delegate.Ask(agent.WithCascadeHop(ctx), []event.AskQuestion{question})
			if askErr == nil && len(answers) == 1 {
				selection := strings.Join(answers[0].Selected, " ")
				allow := strings.Contains(selection, "Allow") || strings.Contains(selection, "decide for yourself")
				c.emitAutopilotApprovalNotice(tool, subject, fmt.Sprintf("cascaded to task source %s: allow=%v (task 365 C6)", source, allow))
				return approvalReply{allow: allow}, true
			}
			missText := "no answer"
			if askErr != nil {
				missText = askErr.Error()
			}
			c.emitAutopilotApprovalNotice(tool, subject, "cascade re-evaluation missed ("+missText+"); falling back to the unattended tiers (task 365 C6)")
		}
	}
	tier := c.approvalTier
	if tier == "" {
		tier = ApprovalTierGuardian
	}
	switch tier {
	case ApprovalTierParent:
		c.emitAutopilotApprovalNotice(tool, subject, "approved by the parent session (low-risk approval tier)")
		return approvalReply{allow: true}, true
	case ApprovalTierHuman:
		c.emitAutopilotApprovalNotice(tool, subject, "refused: approval tier requires a human and none is available")
		return approvalReply{allow: false}, true
	}
	reviewer := c.guardianSess
	if reviewer == nil {
		c.emitAutopilotApprovalNotice(tool, subject, "refused: no reviewer available to judge an unattended approval")
		return approvalReply{allow: false}, true
	}
	// Task 388: the proxy reviewer gets the batch context — scope dial
	// (level 1 related-only / level 2 full) and, when configured, the
	// natural-language allow/deny manifest — prepended to the request
	// reason so the verdict is context-aware instead of in-a-vacuum.
	// Absent manifest = model self-judgment (no hardcoded fallback).
	reviewReason := reason
	if proxyCtx := c.autopilotProxyContext(); proxyCtx != "" {
		reviewReason = proxyCtx + "\n" + reason
	}
	allow, why, err := reviewer.ReviewAction(ctx, tool, args, reviewReason)
	if err != nil {
		// Fail closed: never guess. Refuse so the model can take another path
		// instead of parking the run on a prompt nobody will answer.
		c.emitAutopilotApprovalNotice(tool, subject, "refused: reviewer unavailable: "+err.Error())
		return approvalReply{allow: false}, true
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

// Task 388: the proxy-review context block — scope dial plus, when
// configured, the natural-language manifest file. Empty return = no proxy
// context (model self-judgment with no manifest section). The manifest is
// size-capped and truncated OLDEST FIRST (tail kept), per the acceptance.
func (c *Controller) autopilotProxyContext() string {
	cfg, err := config.Load()
	if err != nil || cfg == nil {
		return ""
	}
	var b strings.Builder
	switch cfg.AutopilotProxyScopeLevel() {
	case "all":
		b.WriteString("PROXY SCOPE: level 2 (full proxy) — you may proxy-approve requests even when they only loosely relate to this session's own task.\n")
	default:
		b.WriteString("PROXY SCOPE: level 1 (related-only) — approve ONLY requests that clearly serve this session's own task; requests unrelated to this session's own work must NOT be proxy-approved (treat them as refused so a human handles them).\n")
	}
	if path := strings.TrimSpace(cfg.Desktop.AutopilotProxyManifest); path != "" {
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			b.WriteString("PROXY MANIFEST: configured at " + path + " but unreadable (" + readErr.Error() + ") — judge without it.\n")
			return b.String()
		}
		content := strings.TrimSpace(string(raw))
		const manifestCap = 4096
		if len(content) > manifestCap {
			// Truncate OLDEST first: keep the tail (latest entries), cut at a
			// line boundary so no entry is half-presented.
			lines := strings.Split(content, "\n")
			kept := 0
			total := 0
			start := len(lines)
			for start > 0 && total <= manifestCap {
				start--
				total += len(lines[start]) + 1
				kept++
			}
			content = "… (older entries truncated)\n" + strings.Join(lines[start+1:], "\n")
		}
		b.WriteString("PROXY MANIFEST (natural-language allow/deny list; follow it when judging this request):\n" + content + "\n")
	}
	return b.String()
}
