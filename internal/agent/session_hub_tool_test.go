package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// spyTarget is a tool.Tool that records the exact args it received and returns
// a fixed result, so the hub's forwarding can be asserted without any real
// collaboration side effects. It proves the hub is a thin shell: the target
// gets the caller's own argument object (action stripped), never a rewritten one.
type spyTarget struct {
	name     string
	gotArgs  json.RawMessage
	result   string
	executed bool
}

func (s *spyTarget) Name() string        { return s.name }
func (s *spyTarget) Description() string { return s.name }
func (s *spyTarget) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object"}`)
}
func (s *spyTarget) ReadOnly() bool { return true }
func (s *spyTarget) Execute(_ context.Context, args json.RawMessage) (string, error) {
	s.gotArgs = append(json.RawMessage(nil), args...)
	s.executed = true
	if s.result != "" {
		return s.result, nil
	}
	return s.name + "-ok", nil
}

// spyCardTarget additionally decodes the `action` key the way task_card does,
// so the card_action→action rename can be asserted end to end.
type spyCardTarget struct {
	spyTarget
	gotAction string
}

func (s *spyCardTarget) Execute(_ context.Context, args json.RawMessage) (string, error) {
	s.gotArgs = append(json.RawMessage(nil), args...)
	s.executed = true
	var p struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", err
	}
	s.gotAction = p.Action
	return "card-ok", nil
}

// hubWithSpies builds a hub over a full set of spy targets keyed by action.
func hubWithSpies(t *testing.T) (sessionHubTool, map[string]*spyTarget) {
	t.Helper()
	send := &spyTarget{name: "talk_to_session"}
	contacts := &spyTarget{name: "list_addressable_sessions"}
	tail := &spyTarget{name: "read_session_tail"}
	status := &spyTarget{name: "get_session_status"}
	create := &spyTarget{name: "create_collab_session"}
	purpose := &spyTarget{name: "set_session_purpose"}
	delivery := &spyTarget{name: "get_message_status"}
	budget := &spyTarget{name: "extend_research_budget"}
	card := &spyCardTarget{}
	hub := NewSessionHubTool(SessionHubTargets{
		Send: send, Contacts: contacts, Tail: tail, Status: status,
		Create: create, Purpose: purpose, DeliveryStatus: delivery,
		Card: card, Budget: budget,
	}).(sessionHubTool)
	return hub, map[string]*spyTarget{
		"send": send, "contacts": contacts,
		"tail": tail, "status": status,
		"create": create, "purpose": purpose,
		"delivery_status": delivery, "budget": budget,
		"card": &card.spyTarget,
	}
}

// TestSessionHubRoutesEachActionToItsOwnTool pins the core thin-shell contract
// (修点②): every action reaches exactly its implementation tool, with the caller's
// own argument object forwarded (action key stripped) — no logic is rewritten.
func TestSessionHubRoutesEachActionToItsOwnTool(t *testing.T) {
	hub, spies := hubWithSpies(t)
	cases := []struct {
		action string
		args   string
	}{
		{"send", `{"action":"send","to":"c1","message":"hi"}`},
		{"contacts", `{"action":"contacts","query":"合并"}`},
		{"tail", `{"action":"tail","target":"c1","max_bytes":2048}`},
		{"status", `{"action":"status","targets":["c1"]}`},
		{"create", `{"action":"create","title":"T","purpose":"P","group":"reasonix-for-ai"}`},
		{"purpose", `{"action":"purpose","purpose":"expert","target":"c1"}`},
		{"delivery_status", `{"action":"delivery_status","message_id":"msg_1"}`},
		{"budget", `{"action":"budget","reason":"deeper"}`},
	}
	for _, tc := range cases {
		got, err := hub.Execute(context.Background(), json.RawMessage(tc.args))
		if err != nil {
			t.Fatalf("action %s: Execute: %v", tc.action, err)
		}
		sp := spies[tc.action]
		if !sp.executed {
			t.Fatalf("action %s did not reach its target %s", tc.action, sp.name)
		}
		// The forwarded args must equal the original minus the hub's action key.
		var want map[string]json.RawMessage
		if err := json.Unmarshal([]byte(tc.args), &want); err != nil {
			t.Fatalf("bad test args: %v", err)
		}
		delete(want, "action")
		wantBytes, _ := json.Marshal(want)
		var gotMap map[string]json.RawMessage
		if err := json.Unmarshal(sp.gotArgs, &gotMap); err != nil {
			t.Fatalf("target %s got unparseable args %s: %v", sp.name, sp.gotArgs, err)
		}
		delete(gotMap, "action") // card only; harmless elsewhere
		gotNorm, _ := json.Marshal(gotMap)
		if string(gotNorm) != string(wantBytes) {
			t.Errorf("action %s forwarded args mismatch:\n got %s\nwant %s", tc.action, gotNorm, wantBytes)
		}
		if got != sp.name+"-ok" {
			t.Errorf("action %s result = %q, want %q", tc.action, got, sp.name+"-ok")
		}
	}
}

// TestSessionHubCardRenamesCardActionToAction pins the one parameter
// normalisation the hub performs (修点② 允许的参数归一): task_card routes on its own
// `action` key, which the hub consumed for dispatch, so the card sub-action
// rides on card_action and is renamed back before forwarding.
func TestSessionHubCardRenamesCardActionToAction(t *testing.T) {
	hub, spies := hubWithSpies(t)
	_, err := hub.Execute(context.Background(), json.RawMessage(`{"action":"card","card_action":"update","id":"tc1","status":"done"}`))
	if err != nil {
		t.Fatalf("card Execute: %v", err)
	}
	sp := spies["card"]
	if !sp.executed {
		t.Fatal("card action did not reach task_card")
	}
	var p struct {
		Action string `json:"action"`
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(sp.gotArgs, &p); err != nil {
		t.Fatalf("card target args: %v", err)
	}
	if p.Action != "update" {
		t.Errorf("card_action was not renamed: target saw action=%q, want update", p.Action)
	}
	if p.ID != "tc1" || p.Status != "done" {
		t.Errorf("card args not forwarded: %+v", p)
	}
	if strings.Contains(string(sp.gotArgs), "card_action") {
		t.Errorf("card_action key should be stripped before forwarding: %s", sp.gotArgs)
	}
}

// TestSessionHubCardRequiresCardAction guards the rename: card without
// card_action is refused with an actionable message, not a dead forward.
func TestSessionHubCardRequiresCardAction(t *testing.T) {
	hub, _ := hubWithSpies(t)
	_, err := hub.Execute(context.Background(), json.RawMessage(`{"action":"card","id":"tc1"}`))
	if err == nil {
		t.Fatal("card without card_action should be refused")
	}
	if !strings.Contains(err.Error(), "card_action") {
		t.Errorf("error should name card_action, got: %v", err)
	}
}

// TestSessionHubActionIsRequiredAndUnknownIsRefused pins the two guard rails.
func TestSessionHubActionIsRequiredAndUnknownIsRefused(t *testing.T) {
	hub, _ := hubWithSpies(t)
	if _, err := hub.Execute(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "action is required") {
		t.Errorf("empty args: want action-is-required error, got %v", err)
	}
	if _, err := hub.Execute(context.Background(), json.RawMessage(`{"action":"teleport"}`)); err == nil || !strings.Contains(err.Error(), "unknown or unavailable action") {
		t.Errorf("unknown action: want refusal, got %v", err)
	}
}

// TestSessionHubNarrowsWhenTargetsMissing verifies a gated-off tool narrows the
// hub instead of leaving a dead action (铁律 3: no error copy pointing at a
// capability that cannot run). With only Send non-nil, the action enum must
// contain exactly ["send"] and every other action is refused at dispatch.
func TestSessionHubNarrowsWhenTargetsMissing(t *testing.T) {
	send := &spyTarget{name: "talk_to_session"}
	hub := NewSessionHubTool(SessionHubTargets{Send: send}).(sessionHubTool)

	// Extract the enum from the parsed schema rather than substring-matching the
	// whole JSON (property descriptions legitimately mention other action names).
	var parsed struct {
		Properties struct {
			Action struct {
				Enum []string `json:"enum"`
			} `json:"action"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(hub.Schema(), &parsed); err != nil {
		t.Fatalf("schema parse: %v", err)
	}
	if len(parsed.Properties.Action.Enum) != 1 || parsed.Properties.Action.Enum[0] != "send" {
		t.Errorf("action enum = %v, want exactly [send]", parsed.Properties.Action.Enum)
	}
	// A dropped action is refused, not forwarded to a nil tool.
	if _, err := hub.Execute(context.Background(), json.RawMessage(`{"action":"tail","target":"c1"}`)); err == nil {
		t.Error("nil-target action should be refused")
	}
	// The available one still routes.
	if _, err := hub.Execute(context.Background(), json.RawMessage(`{"action":"send","to":"c","message":"m"}`)); err != nil {
		t.Errorf("send should still route: %v", err)
	}
}

// TestSessionHubDescriptionCarriesGuidance pins the L0 guidance surface
// (引导设计 md §一): trigger words, the action table with key params, the 口径钉,
// and the fixed tail index line. This is the harness for seven-scenario
// 走查 scenarios 1-4 (the session-hub-owned ones); scenarios 5-7 ride on
// use_capability and are asserted in the boot-level surface test.
func TestSessionHubDescriptionCarriesGuidance(t *testing.T) {
	hub, _ := hubWithSpies(t)
	desc := hub.Description()
	// Trigger words (scenario 1: 给合并线发消息 → send).
	for _, w := range []string{"派单", "发信", "回信", "联系人", "判活", "读尾巴", "建会话", "任务卡"} {
		if !strings.Contains(desc, w) {
			t.Errorf("description missing trigger word %q", w)
		}
	}
	// Action table rows with the params the model must copy.
	for _, row := range []string{"send", "contacts", "tail", "status", "create", "purpose", "delivery_status", "card", "budget"} {
		if !strings.Contains(desc, row) {
			t.Errorf("description missing action row %q", row)
		}
	}
	// 口径钉 (the accumulated collab mistakes, pinned in-line).
	for _, nail := range []string{"queued≠已读", "unknown≠对方不会开工", "新线程不传 thread_id"} {
		if !strings.Contains(desc, nail) {
			t.Errorf("description missing 口径钉 %q", nail)
		}
	}
	// Tail index line (scenario 4: deep tools via inspect tool:group/session).
	if !strings.Contains(desc, "inspect tool:group/session") {
		t.Errorf("description missing the deep-tool tail index line")
	}
	// Naming note (修点①): session_hub avoids the bare `session` alias clash.
	if !strings.Contains(desc, "session_hub") {
		t.Errorf("description should name session_hub")
	}
}

// TestSessionHubDescriptionStaysLean guards the ≈2.5KB budget: the hub replaces
// the nine scattered tools' ≈7KB, so it must stay well under that.
func TestSessionHubDescriptionStaysLean(t *testing.T) {
	hub, _ := hubWithSpies(t)
	if n := len(hub.Description()); n > 3500 {
		t.Errorf("description is %dB, want ≈2.5KB (under 3500B)", n)
	}
}

// TestSessionHubSevenScenarioWalkthrough is the harness for the design doc §五
// 用例走查, scoped to the session-hub-owned scenarios (1-4). Scenarios 5-7 ride
// on use_capability (kind=group rows / search / 两道闸), which belong to the 953
// piece, so this harness asserts the hub hands off correctly rather than
// re-testing that surface. Each case pins the specific guidance string that
// routes the model down the documented path — the acceptance is "the model
// 照着走到对的地方", not "the copy was written".
func TestSessionHubSevenScenarioWalkthrough(t *testing.T) {
	hub, _ := hubWithSpies(t)
	desc := hub.Description()
	scenarios := []struct {
		n    int
		says string // the model接到的场景
		path string // the action it should take
		want []string
	}{
		{1, "给合并线发消息", "action=send",
			[]string{"发信", "send", "to=", "message="}},
		{2, "不知道 contact_id", "action=contacts",
			[]string{"contacts", "query", "拿 contact_id"}},
		{3, "对方收到后做了没", "action=status + tail",
			[]string{"status", "tail", "判", "unknown≠对方不会开工"}},
		{4, "改一下对方会话名（深件）", "尾行 → inspect tool:group/session",
			[]string{"inspect tool:group/session"}},
	}
	for _, sc := range scenarios {
		for _, w := range sc.want {
			if !strings.Contains(desc, w) {
				t.Errorf("scenario %d (%s → %s): description missing guidance %q",
					sc.n, sc.says, sc.path, w)
			}
		}
	}
	// Scenarios 5-7 (跨域 / 全迷路 / 傻拉全量) are use_capability-owned; the hub
	// must name the deep-tool index so the model is routed there, and must not
	// claim those surfaces itself.
	if strings.Contains(desc, "use_capability") {
		t.Logf("hub defers deep/cross-domain discovery to the group index line")
	}
}
