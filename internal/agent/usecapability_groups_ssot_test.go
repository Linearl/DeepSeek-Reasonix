package agent

// tl1630: 能力清单单一真相源守卫。
//
// 收敛前（审计 F-B2-4）：agent 手写 capabilityGroups 冻结表与
// capability.groupCatalog 权威表双真相源、无同步机制，且出生即漂移——
// B 表缺 submit_feedback/notebook_edit、组 key 分叉（mailbox/misc vs
// inbox/system），两个披露面给同组发了两个公开 id。
// 收敛后 agent 表 = capability.Groups() 的纯投影。本文件守三条红线：
//  1. 投影忠实（防有人把手写表加回来/过滤/改名——tl1037 同款静默失效守卫）；
//  2. 两消费面组身份一致（catalog 行 id 与披露行 id 必须同源同序）；
//  3. agent 反查与权威打标一致（防 groupForTool 被改指向第二份表）。

import (
	"testing"

	"reasonix/internal/capability"
)

// TestCapabilityGroupsIsFaithfulProjectionOfAuthority: the agent disclosure
// table must be exactly the derivation of capability.Groups() — no hand
// copy, no filtering, no key renames, no per-field overrides. Any group or
// member change belongs in internal/capability/groups.go only.
func TestCapabilityGroupsIsFaithfulProjectionOfAuthority(t *testing.T) {
	defs := capability.Groups()
	if len(defs) == 0 {
		t.Fatal("authority table is empty — capability.Groups() must never be empty")
	}
	if len(capabilityGroups) != len(defs) {
		t.Fatalf("agent view has %d groups, authority has %d — the disclosure table must be a 1:1 projection of capability.Groups(), not a hand-maintained copy", len(capabilityGroups), len(defs))
	}
	for i, def := range defs {
		view := capabilityGroups[i]
		if view.Key != def.Name {
			t.Errorf("group[%d].Key = %q, authority Name = %q — agent view must not rename groups (pre-convergence this forked mailbox/misc vs inbox/system)", i, view.Key, def.Name)
		}
		if view.Title == "" {
			t.Errorf("group %q has empty Title — the display name lives in the authority (GroupDef.Title); fill it there, not in an agent-side overlay", def.Name)
		}
		if view.Scenario != def.Scenario {
			t.Errorf("group %q scenario diverges from authority: agent %q vs authority %q", def.Name, view.Scenario, def.Scenario)
		}
		if view.Entry != def.Entry {
			t.Errorf("group %q entry diverges from authority: agent %q vs authority %q", def.Name, view.Entry, def.Entry)
		}
		if view.Example != def.Example {
			t.Errorf("group %q example diverges from authority: agent %q vs authority %q", def.Name, view.Example, def.Example)
		}
		if len(view.Members) != len(def.Members) {
			t.Fatalf("group %q member count = %d, authority = %d — member inventory must come from the authority only (pre-convergence the agent copy was missing members at birth)", def.Name, len(view.Members), len(def.Members))
		}
		for j := range def.Members {
			if view.Members[j] != def.Members[j] {
				t.Errorf("group %q member[%d] = %q, authority = %q — member lists must match element-wise", def.Name, j, view.Members[j], def.Members[j])
			}
		}
	}
}

// TestGroupIdentityConsistentAcrossFaces: the group ids the catalog face
// renders (BuildGroupEntries rows) and the ids the agent disclosure face
// renders (capabilityGroups keys) must be the same set in the same order —
// before tl1630 the same group shipped two public identities
// (tool:group/mailbox vs tool:group/inbox, tool:group/misc vs tool:group/system).
func TestGroupIdentityConsistentAcrossFaces(t *testing.T) {
	catalogRows := capability.BuildGroupEntries(nil)
	if len(catalogRows) != len(capabilityGroups) {
		t.Fatalf("catalog face renders %d group rows, agent face %d — one group table must drive both faces", len(catalogRows), len(capabilityGroups))
	}
	for i, row := range catalogRows {
		view := capabilityGroups[i]
		if row.ID != "tool:group/"+view.Key {
			t.Errorf("group identity split at position %d: catalog row %q vs agent row tool:group/%q", i, row.ID, view.Key)
		}
		if _, ok := groupByID(row.ID); !ok {
			t.Errorf("agent groupByID(%q) must resolve — the disclosure map and the catalog rows share one identity", row.ID)
		}
	}
}

// TestAgentGroupLookupMatchesAuthorityTagging: every member lookup through
// the agent view must agree with the authority's own tool→group tagging.
// Guards groupForTool against ever being re-pointed at a second table.
func TestAgentGroupLookupMatchesAuthorityTagging(t *testing.T) {
	for _, group := range capabilityGroups {
		for _, member := range group.Members {
			if got := capability.ToolGroup(member); got != group.Key {
				t.Errorf("tool %q: agent view says group %q, authority tagging says %q", member, group.Key, got)
			}
			if view, ok := groupForTool(member); !ok || view.Key != group.Key {
				t.Errorf("groupForTool(%q) = %q,%v must agree with the authority-derived table", member, view.Key, ok)
			}
		}
	}
}
