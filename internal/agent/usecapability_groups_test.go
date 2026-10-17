package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"reasonix/internal/capability"
	"reasonix/internal/tool"
)

// groupTestFrontend builds a UseCapabilityTool whose catalog carries the
// group layer: the given builtin tools, tagged and joined by the real
// BuildCatalog under CapabilityGroups=true. registry holds the same tools so
// provider-visible filtering behaves like a real boot.
func groupTestFrontend(t *testing.T, toolNames []string, groupsOn bool) *UseCapabilityTool {
	t.Helper()
	reg := tool.NewRegistry()
	for _, name := range toolNames {
		reg.Add(stubTool{name: name})
	}
	// None of the stubs is a provider-visible core tool: list must render
	// them as catalog entries (real boots mark core tools explicitly).
	reg.SetProviderVisibleTools([]string{})
	catalogFn := func() capability.Catalog {
		entries := make([]tool.ContractEntry, 0, len(toolNames))
		for _, name := range toolNames {
			entries = append(entries, tool.ContractEntry{Name: name, Description: "stub " + name})
		}
		return capability.BuildCatalog(capability.CatalogOptions{
			Tools:            entries,
			CapabilityGroups: groupsOn,
		})
	}
	return NewUseCapabilityTool(context.Background(), nil, nil, reg, nil, nil, catalogFn)
}

type stubTool struct{ name string }

func (s stubTool) Name() string        { return s.name }
func (s stubTool) Description() string { return "stub " + s.name }
func (s stubTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}
func (s stubTool) Execute(_ context.Context, _ json.RawMessage) (string, error) {
	return "stub " + s.name, nil
}
func (s stubTool) ReadOnly() bool { return true }

// TestGroupInspectCardSession: inspect tool:group/session returns the L2 card
// — scenario lines, the member table with the action-mapping rows, examples —
// and marks members not registered in this session (anti-ghost).
func TestGroupInspectCardSession(t *testing.T) {
	frontend := groupTestFrontend(t, []string{"rename_session", "search_sessions"}, true)
	out, err := frontend.Execute(context.Background(), json.RawMessage(`{"action":"inspect","capability_id":"tool:group/session"}`))
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Kind     string   `json:"kind"`
		Entry    string   `json:"entry"`
		Scenario []string `json:"scenario"`
		Members  []struct {
			ID         string `json:"id"`
			Registered bool   `json:"registered"`
			Action     string `json:"action"`
		} `json:"members"`
		Examples []string `json:"examples"`
		Note     string   `json:"note"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	if payload.Kind != "group" {
		t.Fatalf("kind = %q, want group", payload.Kind)
	}
	if len(payload.Scenario) < 3 {
		t.Errorf("scenario lines = %d, want >=3", len(payload.Scenario))
	}
	if len(payload.Members) != 16 {
		t.Fatalf("member table = %d rows, want 16", len(payload.Members))
	}
	if len(payload.Examples) < 2 {
		t.Errorf("examples = %d, want >=2", len(payload.Examples))
	}
	// session_hub not registered in this stub → entry suppressed with a note.
	if payload.Entry != "" {
		t.Errorf("entry must stay empty without session_hub registered, got %q", payload.Entry)
	}
	registeredByID := map[string]bool{}
	for _, m := range payload.Members {
		registeredByID[m.ID] = m.Registered
	}
	if !registeredByID["tool:rename_session"] {
		t.Error("rename_session is registered in this session — card must mark it registered")
	}
	if registeredByID["tool:manage_sessions"] {
		t.Error("manage_sessions is NOT registered in this session — card must mark it unregistered")
	}
	// Action mapping table (task 955: session card carries it).
	foundAction := false
	for _, m := range payload.Members {
		if m.ID == "tool:get_task_card" && m.Action != "" {
			foundAction = true
		}
	}
	if !foundAction {
		t.Error("session card member table must carry the session_hub action mapping")
	}
}

// TestGroupInspectCardIM: the second first-batch card.
func TestGroupInspectCardIM(t *testing.T) {
	frontend := groupTestFrontend(t, []string{"qq", "channel_send"}, true)
	out, err := frontend.Execute(context.Background(), json.RawMessage(`{"action":"inspect","capability_id":"tool:group/im"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"tool:qq"`) || !strings.Contains(out, `"tool:channel_send"`) {
		t.Errorf("im card must list qq and channel_send members:\n%s", out)
	}
	if !strings.Contains(out, "QQ") && !strings.Contains(out, "qq") {
		t.Errorf("im card scenario must mention QQ:\n%s", out)
	}
}

// TestGroupInspectDirectoryOnlyGroups: the six non-first-batch groups return a
// compact directory payload with a note, never a fabricated card.
func TestGroupInspectDirectoryOnlyGroups(t *testing.T) {
	frontend := groupTestFrontend(t, []string{"mailbox", "dream"}, true)
	for _, id := range []string{"tool:group/inbox", "tool:group/memory"} {
		out, err := frontend.Execute(context.Background(), json.RawMessage(`{"action":"inspect","capability_id":"`+id+`"}`))
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if !strings.Contains(out, "group card not built yet") {
			t.Errorf("%s must carry the not-built note:\n%s", id, out)
		}
		if strings.Contains(out, `"members"`) {
			t.Errorf("%s must not fabricate a card member table:\n%s", id, out)
		}
		if !strings.Contains(out, `"member_count"`) {
			t.Errorf("%s must still report member_count:\n%s", id, out)
		}
	}
}

// TestGroupInspectOffStatesClearly: inspecting a group id while the group
// layer is off must say so explicitly — not render a ghost card, not a bare
// unknown-id (幽灵根治 on the inspect path).
func TestGroupInspectOffStatesClearly(t *testing.T) {
	frontend := groupTestFrontend(t, []string{"rename_session"}, false)
	_, err := frontend.Execute(context.Background(), json.RawMessage(`{"action":"inspect","capability_id":"tool:group/session"}`))
	if err == nil {
		t.Fatal("inspect of a group id with the layer off must fail")
	}
	if !strings.Contains(err.Error(), "experimental_capability_groups") {
		t.Errorf("error must name the off switch: %v", err)
	}
	// A nonexistent group with the layer ON gets the same clear family error.
	frontend = groupTestFrontend(t, []string{"rename_session"}, true)
	_, err = frontend.Execute(context.Background(), json.RawMessage(`{"action":"inspect","capability_id":"tool:group/nope"}`))
	if err == nil {
		t.Fatal("inspect of an unknown group must fail")
	}
	if !strings.Contains(err.Error(), "no group by that name exists") {
		t.Errorf("error must name the unknown-group cause: %v", err)
	}
}

// TestGroupCallRefusedWithInspectGuidance: action=call on a group id is
// refused with the inspect path — groups are documentation, not dispatch
// targets.
func TestGroupCallRefusedWithInspectGuidance(t *testing.T) {
	frontend := groupTestFrontend(t, []string{"rename_session"}, true)
	_, err := frontend.Execute(context.Background(), json.RawMessage(`{"action":"call","capability_id":"tool:group/session","arguments":{}}`))
	if err == nil {
		t.Fatal("call on a group id must be refused")
	}
	if !strings.Contains(err.Error(), `action="inspect"`) || !strings.Contains(err.Error(), "tool:group/session") {
		t.Errorf("refusal must give the inspect path: %v", err)
	}
}

// TestListGroupRowsAndMemberTags: list carries the 8 group directory rows
// (with the引导 format in the description) and the Group tag on member tools.
func TestListGroupRowsAndMemberTags(t *testing.T) {
	frontend := groupTestFrontend(t, []string{"rename_session", "qq", "mailbox"}, true)
	out, err := frontend.Execute(context.Background(), json.RawMessage(`{"action":"list"}`))
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Capabilities []struct {
			ID          string `json:"id"`
			Kind        string `json:"kind"`
			Group       string `json:"group"`
			Description string `json:"description"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	groupRows := 0
	byID := map[string]string{}
	descByID := map[string]string{}
	for _, c := range payload.Capabilities {
		byID[c.ID] = c.Group
		descByID[c.ID] = c.Description
		if c.Kind == "group" {
			groupRows++
			if !strings.Contains(c.Description, "组卡：inspect "+c.ID) {
				t.Errorf("group row %s missing inspect pointer: %s", c.ID, c.Description)
			}
		}
	}
	if groupRows != 8 {
		t.Fatalf("group rows in list = %d, want 8", groupRows)
	}
	if byID["tool:rename_session"] != "session" {
		t.Errorf("tool:rename_session group tag = %q, want session", byID["tool:rename_session"])
	}
	if byID["tool:qq"] != "im" {
		t.Errorf("tool:qq group tag = %q, want im", byID["tool:qq"])
	}
	if byID["tool:mailbox"] != "inbox" {
		t.Errorf("tool:mailbox group tag = %q, want inbox", byID["tool:mailbox"])
	}
	_ = descByID
}

// TestListGroupRowsAbsentWhenOff: the off state is byte-clean — no group
// rows, no group tags.
func TestListGroupRowsAbsentWhenOff(t *testing.T) {
	frontend := groupTestFrontend(t, []string{"rename_session", "qq"}, false)
	out, err := frontend.Execute(context.Background(), json.RawMessage(`{"action":"list"}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, `"kind": "group"`) || strings.Contains(out, `"group":`) {
		t.Errorf("off state must carry no group rows or tags:\n%s", out)
	}
}

// TestSearchCarriesGroupAttribution: search hits on group members carry
// group + group_card (scenario 6 of the七场景走查: 全迷路 search 带归属).
func TestSearchCarriesGroupAttribution(t *testing.T) {
	frontend := groupTestFrontend(t, []string{"qq", "weixin", "rename_session"}, true)
	out, err := frontend.Execute(context.Background(), json.RawMessage(`{"action":"search","query":"qq","limit":8}`))
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Results []struct {
			CapabilityID string `json:"capability_id"`
			Group        string `json:"group"`
			GroupCard    string `json:"group_card"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	found := false
	for _, r := range payload.Results {
		if r.CapabilityID == "tool:qq" {
			found = true
			if r.Group != "im" || r.GroupCard != "tool:group/im" {
				t.Errorf("tool:qq attribution = %q/%q, want im/tool:group/im", r.Group, r.GroupCard)
			}
		}
	}
	if !found {
		t.Fatalf("search for qq must hit tool:qq:\n%s", out)
	}
}

// TestSearchAttributionAbsentWhenOff.
func TestSearchAttributionAbsentWhenOff(t *testing.T) {
	frontend := groupTestFrontend(t, []string{"qq"}, false)
	out, err := frontend.Execute(context.Background(), json.RawMessage(`{"action":"search","query":"qq"}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, `"group_card"`) {
		t.Errorf("off state must carry no group attribution:\n%s", out)
	}
}

// TestRouteGroupSuggestionThroughFrontend: end-to-end route block rendering —
// a group trigger yields a capability-route block naming the group card
// (scenario 1/4 path: route 推组 → inspect 组卡).
func TestRouteGroupSuggestionThroughFrontend(t *testing.T) {
	entries := []tool.ContractEntry{{Name: "rename_session", Description: "stub"}}
	cat := capability.BuildCatalog(capability.CatalogOptions{
		Tools:            entries,
		CapabilityGroups: true,
	})
	decision := capability.Route("帮我建个任务卡记录一下这次派单", cat.Entries)
	block := capability.RenderTransientBlock(decision)
	if block == "" {
		t.Fatal("route block must render for a group-triggering input")
	}
	if !strings.Contains(block, "tool:group/session") || !strings.Contains(block, "inspect") {
		t.Errorf("route block must name the group card inspect path:\n%s", block)
	}
	// Off state: no group entries → no group suggestion.
	catOff := capability.BuildCatalog(capability.CatalogOptions{Tools: entries})
	blockOff := capability.RenderTransientBlock(capability.Route("帮我建个任务卡记录一下这次派单", catOff.Entries))
	if strings.Contains(blockOff, "tool:group/") {
		t.Errorf("off state must not suggest groups:\n%s", blockOff)
	}
}
