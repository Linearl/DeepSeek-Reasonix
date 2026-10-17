package agent

// Task 955 acceptance harness: the seven guidance scenarios of
// docs/drafts/能力架构-引导设计-20261017.md §五, walked end to end against the
// group disclosure layer this task owns. The bar is not "the text exists" —
// each scenario asserts the model can walk FROM the引导 TO the right place:
// route/list/search surface the group, inspect opens the card, the card
// names the member, inspect of the member works.
//
// Scope note per scenario (951 第二批分工):
//   - S1/S3 partially depend on task 954's session_hub L0 aggregate
//     (action=send/status/tail); 955 owns the group-level path (route 推组,
//     card member table) which is what this harness asserts.
//   - S7's two-gate assertions (detail=true 预检/拒绝) belong to task 953;
//     955 asserts only that list stays bounded with the group layer on.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"reasonix/internal/capability"
)

// guidanceFrontend builds a frontend whose registry holds a plausible live
// session surface: a few session-group members, the IM tools, one inbox
// tool — enough to walk every scenario without booting the world.
func guidanceFrontend(t *testing.T, groupsOn bool) *UseCapabilityTool {
	t.Helper()
	return groupTestFrontend(t, []string{
		"rename_session", "search_sessions", "get_task_card",
		"qq", "weixin", "channel_send",
		"mailbox", "dream",
	}, groupsOn)
}

func guidanceExecute(t *testing.T, f *UseCapabilityTool, args string) string {
	t.Helper()
	out, err := f.Execute(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("execute %s: %v", args, err)
	}
	return out
}

// TestGuidanceSevenScenarios is the acceptance harness: one subtest per
// scenario of §五.
func TestGuidanceSevenScenarios(t *testing.T) {
	on := guidanceFrontend(t, true)

	t.Run("场景1_给合并线发消息_路由推组", func(t *testing.T) {
		// 应走路径：route 命中 session 组触发词 → 推组（954 落地后直达
		// session action=send；本批断言组级引导成立）。
		cat := on.currentCatalog()
		decision := capability.Route("帮我给合并线发消息同步一下进展", cat.Entries)
		block := capability.RenderTransientBlock(decision)
		if !strings.Contains(block, "tool:group/session") {
			t.Fatalf("route must suggest the session group:\n%s", block)
		}
		if !strings.Contains(block, "inspect") {
			t.Fatalf("group suggestion must point at the group card:\n%s", block)
		}
	})

	t.Run("场景2_不知道contact_id_组卡指路", func(t *testing.T) {
		// 应走路径：组卡成员表 → search_sessions（通讯录检索）行。
		out := guidanceExecute(t, on, `{"action":"inspect","capability_id":"tool:group/session"}`)
		if !strings.Contains(out, "tool:search_sessions") {
			t.Fatalf("session card must name search_sessions for directory lookups:\n%s", out)
		}
		if !strings.Contains(out, "通讯录") {
			t.Fatalf("search_sessions row must mention the contact directory:\n%s", out)
		}
	})

	t.Run("场景3_对方收到后做了没_组卡判活面", func(t *testing.T) {
		// 判活/读尾由 954 的 session_hub action=status/tail 承载；组卡 scenario
		// 必须把这条路径说出来（L0 聚合件优先，深件在成员表）。
		out := guidanceExecute(t, on, `{"action":"inspect","capability_id":"tool:group/session"}`)
		if !strings.Contains(out, "session_hub") {
			t.Fatalf("session card scenario must point at the L0 aggregate session_hub:\n%s", out)
		}
	})

	t.Run("场景4_改对方会话名_尾行到成员inspect全链", func(t *testing.T) {
		// 应走路径：组卡成员表 rename_session 行 → inspect tool:rename_session
		// 直达全文。全链逐步断言。
		card := guidanceExecute(t, on, `{"action":"inspect","capability_id":"tool:group/session"}`)
		var payload struct {
			Members []struct {
				ID         string `json:"id"`
				Registered bool   `json:"registered"`
			} `json:"members"`
		}
		if err := json.Unmarshal([]byte(card), &payload); err != nil {
			t.Fatalf("decode card: %v", err)
		}
		found := false
		for _, m := range payload.Members {
			if m.ID == "tool:rename_session" {
				found = true
				if !m.Registered {
					t.Fatal("rename_session is registered in this session — card must mark it registered so the model may inspect it")
				}
			}
		}
		if !found {
			t.Fatalf("session card member table must list rename_session:\n%s", card)
		}
		member := guidanceExecute(t, on, `{"action":"inspect","capability_id":"tool:rename_session"}`)
		if !strings.Contains(member, `"tool:rename_session"`) {
			t.Fatalf("inspect of the member must return its own entry:\n%s", member)
		}
	})

	t.Run("场景5_跨域能力_组行可见", func(t *testing.T) {
		// 应走路径：list → IM 组行 → 组卡/单件。953 落地 kind=group 过滤后
		// 本行由同一 catalog 数据驱动；本批断言组行在 list 中可见且格式带
		// inspect 指路。
		out := guidanceExecute(t, on, `{"action":"list"}`)
		if !strings.Contains(out, `"tool:group/im"`) {
			t.Fatalf("list must carry the IM group row:\n%s", out)
		}
		if !strings.Contains(out, "组卡：inspect tool:group/im") {
			t.Fatalf("IM group row must carry the inspect pointer:\n%s", out)
		}
		card := guidanceExecute(t, on, `{"action":"inspect","capability_id":"tool:group/im"}`)
		if !strings.Contains(card, "tool:qq") {
			t.Fatalf("IM card must list qq as a member:\n%s", card)
		}
	})

	t.Run("场景6_全迷路_search命中带归属", func(t *testing.T) {
		// 应走路径：search query="QQ" → 命中带属组+组入口。
		out := guidanceExecute(t, on, `{"action":"search","query":"QQ 消息"}`)
		var payload struct {
			Results []struct {
				CapabilityID string `json:"capability_id"`
				Group        string `json:"group"`
				GroupCard    string `json:"group_card"`
			} `json:"results"`
		}
		if err := json.Unmarshal([]byte(out), &payload); err != nil {
			t.Fatalf("decode search: %v\n%s", err, out)
		}
		found := false
		for _, r := range payload.Results {
			if r.CapabilityID == "tool:qq" {
				found = true
				if r.Group != "im" || r.GroupCard != "tool:group/im" {
					t.Fatalf("tool:qq attribution = %q/%q, want im/tool:group/im", r.Group, r.GroupCard)
				}
			}
		}
		if !found {
			t.Fatalf("search must hit tool:qq:\n%s", out)
		}
	})

	t.Run("场景7_傻拉全量_组层不放大list", func(t *testing.T) {
		// 两道闸断言归 953；955 断言：组层开启后 list 仍是有界 JSON，
		// 组行恰好 8 条（不多不少，无重复渲染）。
		out := guidanceExecute(t, on, `{"action":"list"}`)
		var payload struct {
			Capabilities []struct {
				Kind string `json:"kind"`
			} `json:"capabilities"`
		}
		if err := json.Unmarshal([]byte(out), &payload); err != nil {
			t.Fatalf("decode list: %v", err)
		}
		groupRows := 0
		for _, c := range payload.Capabilities {
			if c.Kind == "group" {
				groupRows++
			}
		}
		if groupRows != 8 {
			t.Fatalf("group rows = %d, want exactly 8", groupRows)
		}
	})
}

// TestGuidanceSevenScenariosOffState: the same seven walks with the switch
// off — no group surface anywhere, and every miss is an explicit error, never
// a ghost render (幽灵根治的开关关态面).
func TestGuidanceSevenScenariosOffState(t *testing.T) {
	off := guidanceFrontend(t, false)

	// S1/S3: route renders no group id.
	cat := off.currentCatalog()
	block := capability.RenderTransientBlock(capability.Route("帮我给合并线发消息同步一下进展", cat.Entries))
	if strings.Contains(block, "tool:group/") {
		t.Errorf("S1 off: route must not suggest groups:\n%s", block)
	}

	// S4/S5: inspect of a group id fails with the switch named — not a ghost
	// card, not a bare unknown-id.
	_, err := off.Execute(context.Background(), json.RawMessage(`{"action":"inspect","capability_id":"tool:group/session"}`))
	if err == nil {
		t.Fatal("S4 off: inspect of a group id must fail")
	}
	if !strings.Contains(err.Error(), "experimental_capability_groups") {
		t.Errorf("S4 off: error must name the off switch: %v", err)
	}

	// S5/S7: list carries no group rows and no member tags.
	out := guidanceExecute(t, off, `{"action":"list"}`)
	if strings.Contains(out, `"kind": "group"`) || strings.Contains(out, `"group":`) {
		t.Errorf("S5 off: list must carry no group surface:\n%s", out)
	}

	// S6: search carries no attribution.
	out = guidanceExecute(t, off, `{"action":"search","query":"QQ 消息"}`)
	if strings.Contains(out, `"group_card"`) {
		t.Errorf("S6 off: search must carry no attribution:\n%s", out)
	}
}
