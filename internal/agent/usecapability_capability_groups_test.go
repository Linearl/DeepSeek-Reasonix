package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"reasonix/internal/capability"
	"reasonix/internal/tool"
)

// Task 953 acceptance harness: the seven guidance walkthroughs from the
// design doc (能力架构-引导设计-20261017.md §五) plus the switch dual-state
// contract. Scenarios 1-3 exercise the session L0 aggregate tool landed by
// task 954; what 953 owns for them is the L1 disclosure layer (the session
// group row, its member table, and the group-card chain), so each of those
// scenarios asserts the 953-owned precondition with a comment naming the
// 954-owned hop instead of silently skipping it.

// groupsTestCatalog builds a catalog from bare tool name → description.
func groupsTestCatalog(tools map[string]string) func() capability.Catalog {
	return func() capability.Catalog {
		entries := make([]capability.Entry, 0, len(tools))
		for name, desc := range tools {
			entries = append(entries, capability.Entry{
				ID:          "tool:" + name,
				Kind:        capability.KindTool,
				Name:        name,
				ToolName:    name,
				Description: desc,
				Status:      capability.StatusReady,
				ReadOnly:    true,
			})
		}
		return capability.Catalog{Entries: entries}
	}
}

func newCapabilityGroupsProxy(t *testing.T, on bool, tools map[string]string) *UseCapabilityTool {
	t.Helper()
	reg := tool.NewRegistry()
	// An empty-but-set provider-visible set makes every fixture tool a
	// non-core catalog entry (a nil set would mark everything visible and
	// list would skip the whole fixture).
	reg.SetProviderVisibleTools([]string{})
	proxy := NewUseCapabilityTool(context.Background(), nil, nil, reg, nil, nil, groupsTestCatalog(tools))
	proxy.capabilityGroups = on
	return proxy
}

func listKindGroup(t *testing.T, proxy *UseCapabilityTool) string {
	t.Helper()
	out, err := proxy.Execute(context.Background(), json.RawMessage(`{"action":"list","kind":"group"}`))
	if err != nil {
		t.Fatalf("list kind=group: %v", err)
	}
	return out
}

// Scenario 4 (deep tool via group card): the session group card must list
// rename_session so the model can walk group → member → inspect single tool.
// (The first hop — the session L0 description tail pointing at
// tool:group/session — is task 954's deliverable; 953 guarantees the target
// of that pointer resolves.)
func TestScenario4InspectGroupCardListsDeepMembers(t *testing.T) {
	proxy := newCapabilityGroupsProxy(t, true, map[string]string{
		"rename_session":  "Rename a session by exact id. (session group) 组文档：tool:group/session",
		"search_sessions": "Search sessions by keyword.",
	})
	out := listKindGroup(t, proxy)
	if !strings.Contains(out, `"tool:group/session"`) {
		t.Fatalf("kind=group output missing session group row:\n%s", out)
	}
	card, err := proxy.Execute(context.Background(), json.RawMessage(`{"action":"inspect","capability_id":"tool:group/session"}`))
	if err != nil {
		t.Fatalf("inspect group card: %v", err)
	}
	for _, want := range []string{"rename_session", "tool:rename_session", "member_count", "call_example"} {
		if !strings.Contains(card, want) {
			t.Fatalf("group card missing %q:\n%s", want, card)
		}
	}
	// The single tool stays reachable after the card hop (953 owns the chain
	// from the card down; the entry is in the catalog fixture above).
	single, err := proxy.Execute(context.Background(), json.RawMessage(`{"action":"inspect","capability_id":"tool:rename_session"}`))
	if err != nil {
		t.Fatalf("inspect single member after card: %v", err)
	}
	if !strings.Contains(single, "tool:rename_session") {
		t.Fatalf("single-tool inspect lost its id:\n%s", single)
	}
}

// Scenario 5 (cross-domain discovery): list kind=group must surface the IM
// channel group row in the fixed 组名|场景（N 深件）|入口|例 format.
func TestScenario5ListGroupRowsIMChannel(t *testing.T) {
	proxy := newCapabilityGroupsProxy(t, true, map[string]string{
		"qq":     "Send a QQ message.",
		"feishu": "Send a Feishu message.",
	})
	out := listKindGroup(t, proxy)
	if !strings.Contains(out, "IM 通道组") || !strings.Contains(out, "group/im") {
		t.Fatalf("IM channel group row missing:\n%s", out)
	}
	if !strings.Contains(out, "深件") || !strings.Contains(out, "例：") {
		t.Fatalf("group row lacks the fixed disclosure format (场景 N 深件 / 例):\n%s", out)
	}
	// Scenarios 1-3 的 954 前置：session 组行存在且成员表含协作深件。
	if !strings.Contains(out, "tool:group/session") {
		t.Fatalf("session group row missing (954 L0 aggregate's disclosure target):\n%s", out)
	}
}

// Scenario 6 (lost model): search hits inside a group carry group membership,
// the group card id, and a copy-paste call hint.
func TestScenario6SearchHitCarriesGroupAndCallHint(t *testing.T) {
	proxy := newCapabilityGroupsProxy(t, true, map[string]string{
		"qq": "Send a message through QQ.",
	})
	out, err := proxy.Execute(context.Background(), json.RawMessage(`{"action":"search","query":"qq","limit":5}`))
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	var payload struct {
		Results []capabilitySearchResult `json:"results"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("decode search: %v\n%s", err, out)
	}
	var hit *capabilitySearchResult
	for i := range payload.Results {
		if payload.Results[i].CapabilityID == "tool:qq" {
			hit = &payload.Results[i]
		}
	}
	if hit == nil {
		t.Fatalf("search did not return tool:qq:\n%s", out)
	}
	if hit.Group == "" || hit.GroupID != "tool:group/im" {
		t.Fatalf("search hit lost group membership: %+v", hit)
	}
	if !strings.Contains(hit.CallHint, `"capability_id":"tool:qq"`) {
		t.Fatalf("call hint is not copy-pasteable: %q", hit.CallHint)
	}
}

// Scenario 7 (greedy full dump): default list is the summary tier; a
// detail=true dump over the injection budget is refused with pagination
// guidance; a small detail=true dump reports total and bytes.
func TestScenario7ListSummaryTierAndVolumeGates(t *testing.T) {
	big := make(map[string]string, 45)
	for i := range 45 {
		big[fmt.Sprintf("tool_%03d", i)] = strings.Repeat("x", 1100)
	}
	proxy := newCapabilityGroupsProxy(t, true, big)

	// Default = summary tier: ≤30 rows, every description ≤240B, total and
	// next_offset present.
	out, err := proxy.Execute(context.Background(), json.RawMessage(`{"action":"list"}`))
	if err != nil {
		t.Fatalf("default list: %v", err)
	}
	var payload struct {
		Capabilities []capInfoForList `json:"capabilities"`
		Total        int              `json:"total"`
		NextOffset   int              `json:"next_offset"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("decode list: %v\n%s", err, out)
	}
	if len(payload.Capabilities) != listDefaultLimit {
		t.Fatalf("summary tier returned %d rows, want %d", len(payload.Capabilities), listDefaultLimit)
	}
	for _, c := range payload.Capabilities {
		if len(c.Description) > listSummaryDescBytes {
			t.Fatalf("summary description %q exceeds %dB", c.Description, listSummaryDescBytes)
		}
	}
	if payload.Total != len(big) || payload.NextOffset != listDefaultLimit {
		t.Fatalf("paging metadata wrong: total=%d next_offset=%d", payload.Total, payload.NextOffset)
	}
	// offset pages forward.
	page2, err := proxy.Execute(context.Background(), json.RawMessage(`{"action":"list","offset":30}`))
	if err != nil {
		t.Fatalf("offset list: %v", err)
	}
	var p2 struct {
		Capabilities []capInfoForList `json:"capabilities"`
		NextOffset   int              `json:"next_offset"`
	}
	if err := json.Unmarshal([]byte(page2), &p2); err != nil {
		t.Fatalf("decode page2: %v\n%s", err, page2)
	}
	if len(p2.Capabilities) != len(big)-listDefaultLimit || p2.NextOffset != 0 {
		t.Fatalf("page2 wrong: %d rows next_offset=%d", len(p2.Capabilities), p2.NextOffset)
	}

	// Gate: detail=true over the 32KB budget is refused with a pagination
	// example (guardrail, not failure).
	refused, err := proxy.Execute(context.Background(), json.RawMessage(`{"action":"list","detail":true}`))
	if err != nil {
		t.Fatalf("detail list should refuse as a payload, not an error: %v", err)
	}
	// The copyable example lives inside a JSON string value, so match its
	// escaped serialization rather than raw quotes; MarshalIndent puts a
	// space after the colon.
	if !strings.Contains(refused, `"refused": true`) || !strings.Contains(refused, "guardrail") ||
		!strings.Contains(refused, `\"action\":\"list\"`) || !strings.Contains(refused, "offset") {
		t.Fatalf("detail refusal lacks guardrail payload or paging example:\n%s", refused)
	}

	// Under budget: detail=true reports total and byte volume.
	small := newCapabilityGroupsProxy(t, true, map[string]string{"qq": "Send a QQ message."})
	ok, err := small.Execute(context.Background(), json.RawMessage(`{"action":"list","detail":true}`))
	if err != nil {
		t.Fatalf("small detail list: %v", err)
	}
	var detail struct {
		Total int `json:"total"`
		Bytes int `json:"bytes"`
	}
	if err := json.Unmarshal([]byte(ok), &detail); err != nil {
		t.Fatalf("decode detail: %v\n%s", err, ok)
	}
	if detail.Total != 1 || detail.Bytes <= 0 {
		t.Fatalf("detail precheck missing total/bytes: %+v", detail)
	}
}

// Dual-state: off must equal the pre-953 behavior on every surface the group
// tier changes (495 口径: behavior equivalence, not byte identity of this test
// fixture — the assertions name the exact behaviors).
func TestCapabilityGroupsOffEqualsPre953(t *testing.T) {
	tools := map[string]string{
		"qq": strings.Repeat("y", 500),
	}
	off := newCapabilityGroupsProxy(t, false, tools)

	// Off: default list is the full untruncated enumeration, no paging keys,
	// no group rows.
	out, err := off.Execute(context.Background(), json.RawMessage(`{"action":"list"}`))
	if err != nil {
		t.Fatalf("off list: %v", err)
	}
	if strings.Contains(out, "next_offset") || strings.Contains(out, `"groups"`) || strings.Contains(out, "refused") {
		t.Fatalf("off list leaked group-tier keys:\n%s", out)
	}
	if !strings.Contains(out, strings.Repeat("y", 500)) {
		t.Fatalf("off list truncated descriptions (must stay full):\n%s", out)
	}

	// Off: limit>8 keeps the pre-953 bound error for every action.
	if _, err := off.Execute(context.Background(), json.RawMessage(`{"action":"list","limit":9}`)); err == nil ||
		!strings.Contains(err.Error(), "limit must be between 1 and 8") {
		t.Fatalf("off list limit=9 should keep the 1-8 bound, got: %v", err)
	}

	// Off: group cards are refused. Merged-surface caliber (task 955): the
	// group mechanism owns the off-state answer and replies with its
	// explicit "not available ... experimental_capability_groups is off"
	// guidance instead of the pre-953 generic "unknown capability_id" —
	// both refuse, neither leaks member content.
	if _, err := off.Execute(context.Background(), json.RawMessage(`{"action":"inspect","capability_id":"tool:group/im"}`)); err == nil ||
		!strings.Contains(err.Error(), "experimental_capability_groups is off") {
		t.Fatalf("off inspect of a group id should be refused by the group mechanism, got: %v", err)
	}

	// Off: search hits carry no group fields.
	sout, err := off.Execute(context.Background(), json.RawMessage(`{"action":"search","query":"qq","limit":5}`))
	if err != nil {
		t.Fatalf("off search: %v", err)
	}
	if strings.Contains(sout, "group_id") || strings.Contains(sout, "call_hint") {
		t.Fatalf("off search leaked group attachments:\n%s", sout)
	}

	// Off: schema and description stay on the pre-953 surface.
	if strings.Contains(string(off.Schema()), `"kind"`) || strings.Contains(string(off.Schema()), `"detail"`) {
		t.Fatal("off schema advertises group-tier parameters")
	}
	if strings.Contains(off.Description(), "GROUP DIRECTORY") {
		t.Fatal("off description carries the group-tier tail line")
	}
}

// Dual-state (on): the schema and description advertise the new surface.
func TestCapabilityGroupsOnSchemaAndDescription(t *testing.T) {
	on := newCapabilityGroupsProxy(t, true, nil)
	schema := string(on.Schema())
	for _, want := range []string{`"kind"`, `"offset"`, `"detail"`, `action=search`, "50"} {
		if !strings.Contains(schema, want) {
			t.Fatalf("on schema missing %q:\n%s", want, schema)
		}
	}
	desc := on.Description()
	for _, want := range []string{"ACTION PARAMETERS", "TWO VOLUME GUARDS", "GROUP DIRECTORY"} {
		if !strings.Contains(desc, want) {
			t.Fatalf("on description missing %q", want)
		}
	}
}

// The query-required error carries a copyable example in BOTH switch states —
// it is the fix for the 10-17 four-strike loop, not a group feature.
func TestSearchMissingQueryErrorCarriesCopyableExample(t *testing.T) {
	for _, on := range []bool{false, true} {
		proxy := newCapabilityGroupsProxy(t, on, nil)
		_, err := proxy.Execute(context.Background(), json.RawMessage(`{"action":"search"}`))
		if err == nil || !strings.Contains(err.Error(), `{"action":"search","query":"<topic keywords>","limit":5}`) {
			t.Fatalf("groups=%v: missing-query error lacks copyable example: %v", on, err)
		}
	}
}

// kind filter narrows the summary tier; unknown kinds are rejected with the
// valid set (guidance, not silence).
func TestListGroupKindFilter(t *testing.T) {
	proxy := newCapabilityGroupsProxy(t, true, map[string]string{
		"qq":             "IM tool",
		"rename_session": "session tool",
	})
	// Both fixture entries are tools; kind=tool keeps both.
	out, err := proxy.Execute(context.Background(), json.RawMessage(`{"action":"list","kind":"tool"}`))
	if err != nil {
		t.Fatalf("list kind=tool: %v", err)
	}
	if !strings.Contains(out, "tool:rename_session") || !strings.Contains(out, "tool:qq") {
		t.Fatalf("kind=tool dropped tool entries:\n%s", out)
	}
	// kind=skill narrows to zero of the tool fixture (skill rows absent).
	skillOut, err := proxy.Execute(context.Background(), json.RawMessage(`{"action":"list","kind":"skill"}`))
	if err != nil {
		t.Fatalf("list kind=skill: %v", err)
	}
	if strings.Contains(skillOut, "tool:qq") {
		t.Fatalf("kind=skill leaked tool entries:\n%s", skillOut)
	}
	if _, err := proxy.Execute(context.Background(), json.RawMessage(`{"action":"list","kind":"bogus"}`)); err == nil ||
		!strings.Contains(err.Error(), "valid kinds") {
		t.Fatalf("unknown kind should list the valid set, got: %v", err)
	}
}

// On-state list widens the limit bound to 50 (search stays 1-8).
func TestListGroupLimitWidensOnState(t *testing.T) {
	proxy := newCapabilityGroupsProxy(t, true, map[string]string{"qq": "x"})
	if _, err := proxy.Execute(context.Background(), json.RawMessage(`{"action":"list","limit":30}`)); err != nil {
		t.Fatalf("on list limit=30 should pass: %v", err)
	}
	if _, err := proxy.Execute(context.Background(), json.RawMessage(`{"action":"list","limit":51}`)); err == nil ||
		!strings.Contains(err.Error(), "limit must be between 1 and 50") {
		t.Fatalf("on list limit=51 should hit the 1-50 bound, got: %v", err)
	}
	if _, err := proxy.Execute(context.Background(), json.RawMessage(`{"action":"search","query":"qq","limit":9}`)); err == nil ||
		!strings.Contains(err.Error(), "limit must be between 1 and 8") {
		t.Fatalf("on search keeps the 1-8 bound, got: %v", err)
	}
}
