package control

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"reasonix/internal/config"
)

// Task 1022 (994 方案二): the ask routing decision made explicit.
//
// Before this task the routing was implicit in Ask()'s branch order — risk,
// then grant, then an unconditional cascade hop, then the local panel. The
// unconditional hop was the defect tl 994 documented: a heartbeat-scheduled
// task source (nobody watching, 20-minute polling) still received the
// forwarded ask and the turn burned the whole 10-minute cascade wait before
// falling back to the local panel. routeAsk names every input and every
// outcome in one place so the decision is unit-testable and auditable from
// the log alone.
//
// Four inputs (任务书「四维输入」):
//	risk tier      — reversible vs needs-human, from the keyword classifier
//	grant posture  — the user's explicit ask grant (task 806b) in this session
//	source posture — who dispatched this session and whether that source can
//	                 answer an ask AT ALL (capability declaration + topology)
//	           including the heartbeat/守护线 exclusion (本件止血段)
//	capability     — the source's declared ask-responder capability
//
// Four channels (任务书 channel ∈ {localPanel, cascade, autoDecide,
// terminalError}); this function decides among the first three — the
// terminalError lane stays with the existing autopilot high-risk timer.

// AskChannel names where an ask goes.
type AskChannel int

const (
	// AskChannelLocalPanel: the prompt is shown on THIS session's panel —
	// the pre-cascade behaviour, and the fail-closed default for every
	// unproven source.
	AskChannelLocalPanel AskChannel = iota
	// AskChannelCascade: forward to the task source's Ask channel and wait
	// bounded (task 225), falling back to the local panel on a miss.
	AskChannelCascade
	// AskChannelAutoDecide: the run answers the reversible question itself
	// (task 806 interaction-timed auto-decide); the panel still shows.
	AskChannelAutoDecide
)

func (ch AskChannel) String() string {
	switch ch {
	case AskChannelLocalPanel:
		return "localPanel"
	case AskChannelCascade:
		return "cascade"
	case AskChannelAutoDecide:
		return "autoDecide"
	}
	return "unknown"
}

// AskRoute is routeAsk's verdict: where the ask goes, how long the cascade
// hop may wait, and why. Deadline is the cascade wait's own semantic
// (cascadeWait, configurable per run) — deliberately NOT the autopilot
// high-risk wait, which keeps governing the local panel timer. onCancel
// names the user-visible escape hatch the frontend offers during the wait.
type AskRoute struct {
	Channel      AskChannel
	Deadline     time.Duration
	Reason       string
	Source       string
	CancelNotice string
}

// CascadeSource declares one candidate cascade target: the task-source
// contact id and what the host knows about it. Hosts fill it from their own
// topology (the desktop knows which sessions are heartbeat-scheduled and
// which registered an ask responder); control only reads it.
type CascadeSource struct {
	// Contact is the task source's collaboration address (empty = none).
	Contact string
	// Heartbeat marks a heartbeat/守护线-scheduled session: nobody watches
	// its panel, so it is NEVER a legal cascade target (tl 994 止血).
	Heartbeat bool
	// AskResponder is the source's declared capability to answer forwarded
	// asks (仿 MCP initialize 声明式能力协议). No declaration = no
	// forwarding: the route falls back to the local panel fail-closed.
	AskResponder bool
	// LatencyBudget is the source's declared rough answer latency, used to
	// pick the cascade wait when it is sane; zero means "no opinion".
	LatencyBudget time.Duration
	// Liveness is the source's last-known aliveness verdict — true means
	// the host saw it alive recently (doctor 六态的活性读数). A dead
	// source keeps the prompt local instead of burning the wait.
	Liveness bool
}

// askRouteInputs is the full four-dimensional input set of routeAsk.
type askRouteInputs struct {
	risk          askRiskClass
	explicitGrant bool // task 806b: the user's explicit ask grant is active
	autopilot     bool
	heartbeatTurn bool // task 1062: this turn was submitted via SubmitHeartbeatTurn
	cascadeLive   bool // task 225 switch on
	hopExhausted  bool // task 225 round-2 chain bound
	source        CascadeSource
	cascadeWait   time.Duration // resolved configured cascade wait
}

// DefaultCascadeAskWait is the fallback cascade wait: as long as the
// pre-1022 hard-coded hop bound (it WAS DefaultAutopilotAskWait) but resolved
// through its own name so cascade semantics stop borrowing the autopilot
// high-risk timer's constant. Configurable per run via
// Options.CascadeAskWait / REASONIX_CASCADE_ASK_WAIT_SECONDS (tests set it
// short; production keeps the 10-minute default).
const DefaultCascadeAskWait = DefaultAutopilotAskWait

// CascadeAskWaitEnv is the escape hatch for operators and tests.
const CascadeAskWaitEnv = "REASONIX_CASCADE_ASK_WAIT_SECONDS"

// cascadeAskWaitFor resolves the effective cascade wait: explicit option,
// else the env override, else the 10-minute default.
func cascadeAskWaitFor(configured time.Duration) time.Duration {
	if configured > 0 {
		return configured
	}
	if raw := strings.TrimSpace(os.Getenv(CascadeAskWaitEnv)); raw != "" {
		if secs, err := strconv.Atoi(raw); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second
		}
	}
	return DefaultCascadeAskWait
}

// routeAsk is the single decision function (tl 994 方案二拍板). The chain is
// explicit and ordered — risk → grant → cascade → local:
//
//  1. heartbeat turn (task 1062 posture) — the caller's existing immediate
//     refusal owns it; routeAsk just reports it, never a cascade hop;
//  2. reversible under autopilot without an explicit grant → autoDecide;
//  3. cascade candidates are screened BEFORE any hop: the chain bound, the
//     heartbeat/守护线 source exclusion, the undeclared-capability
//     fail-closed, and source liveness all keep the prompt local;
//  4. everything else is the local panel — the safe default.
//
// routeAsk never resolves the delegate itself (topology stays with the host
// via onCascadeDelegate); it only decides whether a hop is LEGAL and on
// which wait. The verdict is always logged — the 影子日志 that lets a
// routing-comparison report read the decision without behavior archaeology.
func routeAsk(in askRouteInputs) AskRoute {
	route := routeAskDecision(in)
	log.Printf("[ask-route] routeAsk → %s (reason=%s risk=%s autopilot=%v grant=%v heartbeatTurn=%v cascadeLive=%v hopExhausted=%v source=%q heartbeat=%v responder=%v liveness=%v cascadeWait=%s task=1022)",
		route.Channel, route.Reason, askRouteRiskName(in.risk), in.autopilot, in.explicitGrant,
		in.heartbeatTurn, in.cascadeLive, in.hopExhausted,
		in.source.Contact, in.source.Heartbeat, in.source.AskResponder, in.source.Liveness,
		in.cascadeWait)
	return route
}

func routeAskDecision(in askRouteInputs) AskRoute {
	// 1. A heartbeat-driven turn is never a cascade source or target: task
	// 1062's immediate refusal owns this posture (the caller answers before
	// any panel exists). Reporting it keeps the ordering explicit.
	if in.heartbeatTurn {
		return AskRoute{Channel: AskChannelLocalPanel, Deadline: in.cascadeWait,
			Reason: "heartbeatTurn: scheduled-run ask, refused by the task-1062 backstop"}
	}

	// 2. Risk → grant: a reversible ask under autopilot without the user's
	// explicit grant (task 806b) is the autoDecide lane. The grant revokes
	// the unattended premise, and the high-risk tier is never self-decidable.
	if in.risk == askRiskReversible && in.autopilot && !in.explicitGrant {
		return AskRoute{Channel: AskChannelAutoDecide, Deadline: in.cascadeWait,
			Reason: "reversible ask under autopilot answers itself (task 806)"}
	}

	// 3. Cascade screening — every gate must pass or the hop is illegal.
	if !in.cascadeLive {
		return AskRoute{Channel: AskChannelLocalPanel, Deadline: in.cascadeWait,
			Reason: "cascade switch off (task 225)"}
	}
	if in.hopExhausted {
		return AskRoute{Channel: AskChannelLocalPanel, Deadline: in.cascadeWait,
			Reason: "cascade hop chain bound reached (task 225 round-2)"}
	}
	if strings.TrimSpace(in.source.Contact) == "" {
		return AskRoute{Channel: AskChannelLocalPanel, Deadline: in.cascadeWait,
			Reason: "no task source dispatched this session"}
	}
	// 3a. 止血段 (tl 994): a heartbeat/守护线 source watches no panel —
	// forwarding to it GUARANTEES the full cascade wait followed by a local
	// fall-through, i.e. the defect itself. Never a legal target.
	if in.source.Heartbeat {
		return AskRoute{Channel: AskChannelLocalPanel, Deadline: in.cascadeWait,
			Reason: "heartbeat source is not a legal cascade target (tl 994 止血; keep the prompt local)"}
	}
	// 3b. 能力声明 fail-closed (仿 MCP initialize): a source that never
	// declared the ask-responder capability cannot be assumed able to
	// answer — forwarding would burn the wait on a maybe. Unproven = local.
	if !in.source.AskResponder {
		return AskRoute{Channel: AskChannelLocalPanel, Deadline: in.cascadeWait,
			Reason: "source did not declare ask-responder capability; fail-closed local panel (tl 1022)"}
	}
	// 3c. A dead source has no panel to answer on either; liveness is the
	// doctor 六态活性读数.
	if !in.source.Liveness {
		return AskRoute{Channel: AskChannelLocalPanel, Deadline: in.cascadeWait,
			Reason: "source liveness check failed; keep the prompt local"}
	}

	// Cascade legal: wait per the cascade's own configurable semantics. A
	// source that declared a sane latency budget caps the wait at that —
	// no point waiting ten minutes on a source that answers in seconds.
	deadline := in.cascadeWait
	if in.source.LatencyBudget > 0 && in.source.LatencyBudget < deadline {
		deadline = in.source.LatencyBudget
	}
	return AskRoute{Channel: AskChannelCascade, Deadline: deadline,
		Reason:       "legal cascade target: responder declared, alive, not a heartbeat source",
		Source:       in.source.Contact,
		CancelNotice: cascadeCancelNotice(in.source.Contact, deadline)}
}

func askRouteRiskName(r askRiskClass) string {
	if r == askRiskNeedsHuman {
		return "needsHuman"
	}
	return "reversible"
}

// cascadeCancelNotice is the user-visible escape hatch (验收 3) rendered for
// the cascade wait: the frontend shows it alongside the forwarded ask, and
// the local cancel path (Cancel / steer) aborts the bounded wait. It is
// rendered here so the deadline it names is the one the wait actually uses.
func cascadeCancelNotice(source string, deadline time.Duration) string {
	return "cascade · question forwarded to task source " + source + " — you can cancel this wait within " +
		deadline.Round(time.Second).String() + " (Esc / Cancel / steer); on timeout the question falls back to this session's panel"
}

// sourceCapabilityFromMeta reads a host capability declaration blob — the
// same on-disk shape the desktop writes for a session ({"capabilities":
// {"askResponder": true, ...}}) — into a CascadeSource. Malformed or absent
// declarations are NOT capabilities: the zero value keeps every bool false,
// which routes fail-closed local (the tl 1022 default).
func sourceCapabilityFromMeta(raw json.RawMessage) CascadeSource {
	var doc struct {
		Capabilities struct {
			AskResponder bool          `json:"askResponder"`
			LatencyMs    time.Duration `json:"latencyBudgetMs"`
		} `json:"capabilities"`
	}
	if len(raw) == 0 {
		return CascadeSource{}
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return CascadeSource{}
	}
	return CascadeSource{
		AskResponder:  doc.Capabilities.AskResponder,
		LatencyBudget: doc.Capabilities.LatencyMs * time.Millisecond,
		Liveness:      true, // the declaration was read from a live host record
	}
}

// cascadeSourceFromAsker adapts the desktop-side source declaration for the
// controller's screen: the host passes whatever it knows about the delegate's
// source session. Kept tiny so the desktop wiring stays a one-liner.
func cascadeSourceFromAsker(contact string, meta json.RawMessage) CascadeSource {
	src := sourceCapabilityFromMeta(meta)
	src.Contact = strings.TrimSpace(contact)
	return src
}

// cascadeAskWaitFromContext is the deadline the Ask() cascade hop uses —
// exposed for the wait sites so every hop reads the same configured value.
func (c *Controller) cascadeAskWaitFromContext() time.Duration {
	return cascadeAskWaitFor(c.cascadeAskWait)
}

// cascadeSourceFor asks the host for what it knows about THIS session's task
// source, via the same onCascadeDelegate resolver the hop uses — the
// declaration lives with the topology. A nil resolver or a miss yields the
// zero source (no contact), which routes local. Liveness is probed with a
// short bounded call so a wedged source cannot stall the route decision
// itself (the hop below has its own bounded wait either way).
func (c *Controller) cascadeSourceFor(ctx context.Context) CascadeSource {
	if c == nil || c.onCascadeDelegate == nil || !config.CascadeApprovalLive() {
		return CascadeSource{}
	}
	delegate, source, ok := c.onCascadeDelegate(c.SessionPath())
	if !ok || delegate == nil || strings.TrimSpace(source) == "" {
		return CascadeSource{}
	}
	src := CascadeSource{Contact: source}
	if c.onCascadeSourceCapability != nil {
		if declared := c.onCascadeSourceCapability(source); declared != nil {
			src = *declared
			src.Contact = source
		}
	}
	// Liveness probe: a bounded Ask with an empty batch never reaches the
	// source's user (Ask answers a zero-question batch trivially); a source
	// that cannot even accept the call is not alive for cascade purposes.
	if !src.Liveness && src.AskResponder {
		probeCtx, cancel := context.WithTimeout(ctx, cascadeProbeTimeout)
		defer cancel()
		_, err := delegate.Ask(probeCtx, nil)
		src.Liveness = err == nil
	}
	return src
}

// cascadeProbeTimeout bounds the liveness probe above; short enough that a
// dead source cannot stall the ask path, long enough to cross an in-process
// call.
const cascadeProbeTimeout = 2 * time.Second
