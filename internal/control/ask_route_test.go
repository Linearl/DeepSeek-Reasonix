package control

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
)

// Task 1022 (994 方案二): routeAsk is the single explicit ask-routing decision.
// These tests cover the four-dimensional input set — risk tier × grant
// posture × session topology (heartbeat turn / heartbeat source / no source)
// × declared capability — including the mandated
// heartbeat source × autopilot × high-risk case, and the pre-1022 defect
// shape (heartbeat task source) that must now stay local.

func routeBase() askRouteInputs {
	return askRouteInputs{
		risk:        askRiskNeedsHuman,
		autopilot:   true,
		cascadeLive: true,
		cascadeWait: DefaultCascadeAskWait,
		source: CascadeSource{
			Contact:       "src-human",
			AskResponder:  true,
			Liveness:      true,
			LatencyBudget: 30 * time.Second,
		},
	}
}

// The heartbeat/守护线 defect, tl 994: a scheduled task source watches no
// panel, so the hop must be illegal and the prompt must stay local — even
// under autopilot with a high-risk question (the exact reported incident).
func TestRouteAskHeartbeatSourceStaysLocal(t *testing.T) {
	in := routeBase()
	in.source.Heartbeat = true
	route := routeAsk(in)
	if route.Channel != AskChannelLocalPanel {
		t.Fatalf("heartbeat source route = %s, want localPanel (tl 994 止血)", route.Channel)
	}
	if !strings.Contains(route.Reason, "heartbeat source") {
		t.Fatalf("reason = %q, want it to name the heartbeat-source screen", route.Reason)
	}
}

// A source that never declared the ask-responder capability is unproven;
// forwarding would burn the whole cascade wait on a maybe (tl 1022
// fail-closed).
func TestRouteAskUndeclaredCapabilityFailsClosedLocal(t *testing.T) {
	in := routeBase()
	in.source.AskResponder = false
	route := routeAsk(in)
	if route.Channel != AskChannelLocalPanel {
		t.Fatalf("undeclared source route = %s, want localPanel (fail-closed)", route.Channel)
	}
	if !strings.Contains(route.Reason, "ask-responder") {
		t.Fatalf("reason = %q, want the capability screen named", route.Reason)
	}
}

// A declared-but-dead source also keeps the prompt local: liveness is the
// doctor 六态活性读数, and a dead panel cannot answer.
func TestRouteAskDeadSourceStaysLocal(t *testing.T) {
	in := routeBase()
	in.source.Liveness = false
	if route := routeAsk(in); route.Channel != AskChannelLocalPanel {
		t.Fatalf("dead source route = %s, want localPanel", route.Channel)
	}
}

// The happy path: a declared, alive, human source takes the hop, and the
// wait follows the source's declared latency budget when it is smaller than
// the configured cascade wait — cascadeWait semantics, not highRiskWait.
func TestRouteAskHealthySourceCascadesWithSourceBudget(t *testing.T) {
	in := routeBase()
	route := routeAsk(in)
	if route.Channel != AskChannelCascade {
		t.Fatalf("healthy source route = %s, want cascade", route.Channel)
	}
	if route.Source != "src-human" {
		t.Fatalf("route source = %q, want the source contact", route.Source)
	}
	if route.Deadline != 30*time.Second {
		t.Fatalf("deadline = %v, want the declared latency budget 30s", route.Deadline)
	}
	if route.CancelNotice == "" || !strings.Contains(route.CancelNotice, "cancel") {
		t.Fatalf("cascade route must carry a user-visible cancel notice, got %q", route.CancelNotice)
	}
}

// A sane declared budget only CAPS the wait; a huge budget never extends it
// past the configured cascade wait.
func TestRouteAskCascadeWaitCapsBudget(t *testing.T) {
	in := routeBase()
	in.source.LatencyBudget = 3 * time.Hour
	if route := routeAsk(in); route.Deadline != DefaultCascadeAskWait {
		t.Fatalf("deadline = %v, want the configured cascade wait %v", route.Deadline, DefaultCascadeAskWait)
	}
}

// Reversible under autopilot without the explicit grant is the autoDecide
// lane — the pre-existing task-806 posture, now explicit.
func TestRouteAskReversibleAutopilotAutoDecides(t *testing.T) {
	in := routeBase()
	in.risk = askRiskReversible
	if route := routeAsk(in); route.Channel != AskChannelAutoDecide {
		t.Fatalf("reversible autopilot route = %s, want autoDecide", route.Channel)
	}
}

// The explicit grant (806b) revokes the unattended premise: the reversible
// ask falls through to the cascade screen like any attended question.
func TestRouteAskExplicitGrantReachesCascade(t *testing.T) {
	in := routeBase()
	in.risk = askRiskReversible
	in.explicitGrant = true
	if route := routeAsk(in); route.Channel != AskChannelCascade {
		t.Fatalf("granted reversible route = %s, want cascade", route.Channel)
	}
	// …unless the source is a heartbeat session — the screen is orthogonal
	// to the grant.
	in.source.Heartbeat = true
	if route := routeAsk(in); route.Channel != AskChannelLocalPanel {
		t.Fatalf("granted reversible + heartbeat source route = %s, want localPanel", route.Channel)
	}
}

// A heartbeat-driven turn is the task-1062 posture: never a cascade hop.
func TestRouteAskHeartbeatTurnNeverCascades(t *testing.T) {
	in := routeBase()
	in.heartbeatTurn = true
	if route := routeAsk(in); route.Channel != AskChannelLocalPanel {
		t.Fatalf("heartbeat turn route = %s, want localPanel (task 1062 owns it)", route.Channel)
	}
}

// Topology edges: no source at all, cascade switch off, hop chain exhausted —
// every one keeps the prompt local (pre-1022 behaviour, now explicit).
func TestRouteAskTopologyEdgesStayLocal(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*askRouteInputs)
	}{
		{"no source", func(in *askRouteInputs) { in.source = CascadeSource{} }},
		{"cascade switch off", func(in *askRouteInputs) { in.cascadeLive = false }},
		{"hop chain exhausted", func(in *askRouteInputs) { in.hopExhausted = true }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := routeBase()
			tc.mutate(&in)
			if route := routeAsk(in); route.Channel != AskChannelLocalPanel {
				t.Fatalf("route = %s (%s), want localPanel", route.Channel, route.Reason)
			}
		})
	}
}

// The 能力声明 parser is fail-closed on absent and malformed blobs — the
// zero CascadeSource is never a legal hop.
func TestSourceCapabilityFromMetaFailsClosed(t *testing.T) {
	for _, raw := range []json.RawMessage{nil, json.RawMessage(`{}`), json.RawMessage(`{"capabilities":{"askRespo`), json.RawMessage(`{"capabilities":{}}`)} {
		src := sourceCapabilityFromMeta(raw)
		if src.AskResponder {
			t.Fatalf("raw %q declared a capability, want fail-closed", raw)
		}
	}
	declared := sourceCapabilityFromMeta(json.RawMessage(`{"capabilities":{"askResponder":true,"latencyBudgetMs":1500}}`))
	if !declared.AskResponder || declared.LatencyBudget != 1500*time.Millisecond || !declared.Liveness {
		t.Fatalf("declared = %+v, want responder + 1.5s budget + alive", declared)
	}
}

// The cascade wait resolves its own semantic: explicit option, else env,
// else the default — never borrowed from the autopilot high-risk timer.
func TestCascadeAskWaitResolution(t *testing.T) {
	if got := cascadeAskWaitFor(42 * time.Second); got != 42*time.Second {
		t.Fatalf("explicit cascade wait = %v, want 42s", got)
	}
	t.Setenv(CascadeAskWaitEnv, "7")
	if got := cascadeAskWaitFor(0); got != 7*time.Second {
		t.Fatalf("env cascade wait = %v, want 7s", got)
	}
	t.Setenv(CascadeAskWaitEnv, "")
	if got := cascadeAskWaitFor(0); got != DefaultCascadeAskWait {
		t.Fatalf("default cascade wait = %v, want %v", got, DefaultCascadeAskWait)
	}
	// The default is the pre-1022 hop bound's value — behaviour-compatible.
	if DefaultCascadeAskWait != DefaultAutopilotAskWait {
		t.Fatalf("DefaultCascadeAskWait = %v, want the pre-1022 value %v", DefaultCascadeAskWait, DefaultAutopilotAskWait)
	}
}

// The hop chain bound stays a route gate: a child that already forwarded
// MaxCascadeHops times must not forward again (task 225 round-2 regression
// guard at the route level).
func TestRouteAskHopBoundGateMatchesAgent(t *testing.T) {
	in := routeBase()
	ctx := context.Background()
	for i := 0; i < agent.MaxCascadeHops(); i++ {
		ctx = agent.WithCascadeHop(ctx)
	}
	in.hopExhausted = agent.CascadeHopExhausted(ctx)
	if !in.hopExhausted {
		t.Fatal("fixture: hop depth must read as exhausted at the bound")
	}
	if route := routeAsk(in); route.Channel != AskChannelLocalPanel {
		t.Fatalf("hop-exhausted route = %s, want localPanel", route.Channel)
	}
}
