package agent

// Task 953: L0/L1 能力组披露面的组元数据（架构评审修点④：组是纯元数据 +
// 渲染层消费，禁止成为运行时对象——本文件只有派生表与纯函数，无生命周期、
// 无权限、无开关状态）。
//
// tl1630 收敛：组集合/成员/入口/场景/示例/组名的唯一权威源 =
// internal/capability 的 groupCatalog（经 capability.Groups() 导出，955 建立
// 时即自称 single source of truth）。本文件原先维护的第二份手写冻结表已删——
// 它与权威源出生即漂移（缺 submit_feedback/notebook_edit，组 key 分叉
// mailbox/misc vs inbox/system），且 953 头注释宣称「955 的组 catalog 接口
// 直接消费本表」与事实相反（955 自建了权威表）。改组请只改
// internal/capability/groups.go；本文件的投影若有偏差，
// TestCapabilityGroupsIsFaithfulProjectionOfAuthority 即红。

import (
	"fmt"
	"strings"

	"reasonix/internal/capability"
)

// capabilityGroup is one L1 tool group's disclosure metadata (the agent-face
// projection of capability.GroupDef). Entry names the optional L0 aggregated
// entry tool (修点①: session group's entry is session_hub, task 954); it is
// only rendered when that tool actually exists in the current catalog —
// guidance must never point at an absent tool (开发纪律 3).
type capabilityGroup struct {
	Key      string   // stable id fragment: tool:group/<Key>
	Title    string   // 组名（中文展示名）
	Scenario string   // 场景一句话
	Members  []string // member tool names (bare, no "tool:" prefix)
	Entry    string   // optional L0 entry tool name; "" = 无入口件
	Example  string   // 最常用调用示例（只列已存在的深件）
}

// capabilityGroups is the agent disclosure view of the authoritative group
// table (capability.Groups — tl1630 收敛后唯一真相源在 internal/capability).
// Member names that do not resolve in the current catalog are simply not
// counted in the rendered "N 深件" and are skipped in member cards — the table
// is the disclosure map, not a registration requirement. Derived, never
// hand-edited: add/remove members and groups in internal/capability/groups.go.
var capabilityGroups = deriveCapabilityGroups()

// deriveCapabilityGroups projects the authoritative capability.GroupDef table
// onto the agent disclosure shape. One group, one definition — the projection
// must stay 1:1 (guards: usecapability_groups_ssot_test.go).
func deriveCapabilityGroups() []capabilityGroup {
	defs := capability.Groups()
	out := make([]capabilityGroup, 0, len(defs))
	for _, def := range defs {
		out = append(out, capabilityGroup{
			Key:      def.Name,
			Title:    def.Title,
			Scenario: def.Scenario,
			Members:  def.Members,
			Entry:    def.Entry,
			Example:  def.Example,
		})
	}
	return out
}

// groupForTool returns the group a bare tool name belongs to (first match
// wins; the frozen table assigns each tool to at most one group).
func groupForTool(name string) (capabilityGroup, bool) {
	name = strings.TrimSpace(name)
	for _, group := range capabilityGroups {
		for _, member := range group.Members {
			if member == name {
				return group, true
			}
		}
	}
	return capabilityGroup{}, false
}

// groupByID looks up a group by its tool:group/<key> id or bare key.
func groupByID(id string) (capabilityGroup, bool) {
	key := strings.TrimSpace(id)
	key = strings.TrimPrefix(key, "tool:group/")
	key = strings.TrimPrefix(key, "group:")
	for _, group := range capabilityGroups {
		if group.Key == key {
			return group, true
		}
	}
	return capabilityGroup{}, false
}

// groupPresentMembers returns the group's members that resolve as catalog
// tool entries right now, in table order. The count drives the "N 深件"
// disclosure so a group never claims members the session cannot reach.
func (t *UseCapabilityTool) groupPresentMembers(group capabilityGroup) []capability.Entry {
	cat := t.currentCatalog()
	byName := make(map[string]capability.Entry, len(cat.Entries))
	for _, e := range cat.Entries {
		if e.Kind == capability.KindTool && e.ToolName != "" {
			byName[e.ToolName] = e
		}
	}
	out := make([]capability.Entry, 0, len(group.Members))
	for _, member := range group.Members {
		if e, ok := byName[member]; ok {
			out = append(out, e)
		}
	}
	return out
}

// groupEntryTool returns the L0 entry tool name to advertise on the group row,
// or "" when the entry tool is not registered in this session. Guidance never
// names a tool that cannot be called (开发纪律 3).
func (t *UseCapabilityTool) groupEntryTool(group capabilityGroup) string {
	if group.Entry == "" || t.registry == nil {
		return ""
	}
	if _, ok := t.registry.Get(group.Entry); ok {
		return group.Entry
	}
	return ""
}

// groupRow is one kind=group disclosure row: structured fields plus the
// fixed-format human row 「group/<名> | 场景（N 深件）| 入口= | 例：」 from
// the guidance design (引导设计 §三 list v2 组行).
type groupRow struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Members     int    `json:"members"`
	Entry       string `json:"entry,omitempty"`
	Example     string `json:"example,omitempty"`
	Row         string `json:"row"`
}

func (t *UseCapabilityTool) renderGroupRows() []groupRow {
	rows := make([]groupRow, 0, len(capabilityGroups))
	for _, group := range capabilityGroups {
		count := len(t.groupPresentMembers(group))
		entry := t.groupEntryTool(group)
		entryDisplay := entry
		if entryDisplay == "" {
			entryDisplay = "无"
		}
		desc := fmt.Sprintf("%s（%d 深件）", group.Scenario, count)
		rows = append(rows, groupRow{
			ID:          "tool:group/" + group.Key,
			Kind:        "group",
			Name:        group.Key,
			Title:       group.Title,
			Description: desc,
			Members:     count,
			Entry:       entry,
			Example:     group.Example,
			Row: fmt.Sprintf("group/%s | %s %s | 入口=%s | 例：%s",
				group.Key, group.Title, desc, entryDisplay, group.Example),
		})
	}
	return rows
}

// inspectGroupCard renders the L2 group card for action=inspect
// capability_id=tool:group/<key>: three sections — 场景、成员表（id + 摘要）、
// 照抄调用示例. Member rows reuse the 240B search truncation so one card
// cannot blow the 16KB inspect budget.
func (t *UseCapabilityTool) inspectGroupCard(group capabilityGroup) (string, error) {
	members := t.groupPresentMembers(group)
	memberRows := make([]map[string]any, 0, len(members))
	for _, e := range members {
		row := map[string]any{
			"id":          e.ID,
			"name":        e.Name,
			"description": truncateSearchDescription(e.Description),
			"status":      string(e.Status),
			"read_only":   e.ReadOnly,
		}
		memberRows = append(memberRows, row)
	}
	entry := t.groupEntryTool(group)
	payload := map[string]any{
		"id":           "tool:group/" + group.Key,
		"kind":         "group",
		"name":         group.Key,
		"title":        group.Title,
		"scenario":     group.Scenario,
		"members":      memberRows,
		"member_count": len(members),
		"call_example": map[string]any{
			"action":        "call",
			"capability_id": "tool:<member from the table above>",
			"arguments":     map[string]any{"…": "the target tool's own parameters"},
		},
	}
	if entry != "" {
		payload["entry"] = entry
	}
	if group.Example != "" {
		payload["example"] = group.Example
	}
	if len(members) == 0 {
		payload["note"] = "No member of this group is registered in the current session; use action=search to find the capability you need."
	}
	return marshalBoundedInspect(payload), nil
}
