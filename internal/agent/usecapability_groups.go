package agent

// Task 953: L0/L1 能力组披露面的组元数据（架构评审修点④：组是纯元数据 +
// 渲染层消费，禁止成为运行时对象——本文件只有静态表与纯函数，无生命周期、
// 无权限、无开关状态）。八组划分与成员清单来自设计表 sheet「3-53tool分组」
// （能力架构设计表-20261017.xlsx，用户拍板版）。955 的组 catalog 接口直接
// 消费 capabilityGroups 表与 groupForTool 反查，勿在别处重复硬编码成员。

import (
	"fmt"
	"strings"

	"reasonix/internal/capability"
)

// capabilityGroup is one L1 tool group's disclosure metadata. Entry names the
// optional L0 aggregated entry tool (修点①: session group's entry is
// session_hub, task 954); it is only rendered when that tool actually exists
// in the current catalog — guidance must never point at an absent tool
// (开发纪律 3).
type capabilityGroup struct {
	Key      string   // stable id fragment: tool:group/<Key>
	Title    string   // 组名（中文展示名）
	Scenario string   // 场景一句话
	Members  []string // member tool names (bare, no "tool:" prefix)
	Entry    string   // optional L0 entry tool name; "" = 无入口件
	Example  string   // 最常用调用示例（只列已存在的深件）
}

// capabilityGroups is the frozen eight-group table (design sheet 3; 51 tools).
// Member names that do not resolve in the current catalog are simply not
// counted in the rendered "N 深件" and are skipped in member cards — the table
// is the disclosure map, not a registration requirement.
var capabilityGroups = []capabilityGroup{
	{
		Key: "session", Title: "会话协作组",
		Scenario: "跨会话协作与会话管理（发信判活、会话改名、任务卡、版本管理）",
		Members: []string{
			"talk_to_session_sync", "subscribe_session",
			"create_task_card", "get_task_card", "update_task_card", "list_task_cards",
			"delete_session", "get_session_info", "search_sessions", "session_control",
			"rename_session", "rename_topic", "move_topic_to_group", "manage_sessions",
			"list_session_versions", "adopt_session_version",
		},
		// 954 的 session_hub 聚合件落地后此入口自动点亮（渲染时查 catalog）。
		Entry:   "session_hub",
		Example: "search_sessions/rename_session",
	},
	{
		Key: "im", Title: "IM 通道组",
		Scenario: "经 IM 通道（钉钉/飞书/QQ/微信）或旧总线通道收发消息",
		Members: []string{
			"channel_cancel", "channel_list", "channel_read", "channel_send",
			"dingtalk", "feishu", "qq", "weixin",
		},
		Example: "channel_send/qq",
	},
	{
		Key: "mailbox", Title: "信箱投递组",
		Scenario: "会话信箱收发、收件箱拉取与投递状态查询",
		Members: []string{
			"mailbox", "peek_own_inbox", "drain_inbox",
			"read_collab_status", "query_collab_mail", "event_wait",
		},
		Example: "mailbox/drain_inbox",
	},
	{
		Key: "worktree", Title: "worktree/合并组",
		Scenario: "git worktree 隔离开发与合并回主线",
		Members: []string{
			"create_worktree", "inspect_worktree_merge", "merge_worktree_back",
			"open_isolated_worktree_project", "prepare_worktree_merge",
		},
		Example: "create_worktree/merge_worktree_back",
	},
	{
		Key: "misc", Title: "系统杂项组",
		Scenario: "系统杂项（认领检查、代码索引、截图、UI 操作、网页抓取、待办读取）",
		Members: []string{
			"claim_check", "code_index", "screenshot", "ui_interact",
			"web_fetch", "todo_read", "delete_symbol",
		},
		Example: "web_fetch/screenshot",
	},
	{
		Key: "heartbeat", Title: "心跳任务组",
		Scenario: "心跳定时任务的查看与增改",
		Members: []string{
			"heartbeat_task_enable", "heartbeat_task_list", "heartbeat_task_upsert",
		},
		Example: "heartbeat_task_list",
	},
	{
		Key: "workflow", Title: "工作流编排组",
		Scenario: "声明式 DAG 工作流的定义、查询、触发与运行状态",
		Members: []string{
			"workflow_define", "workflow_list", "workflow_get", "workflow_run", "workflow_status",
		},
		Example: "workflow_run",
	},
	{
		Key: "files", Title: "文件操作组",
		Scenario: "文件移动、区间删除与列目录",
		Members:  []string{"delete_range", "move_file", "ls"},
		Example:  "move_file/ls",
	},
	{
		Key: "memory", Title: "记忆/蒸馏组",
		Scenario: "记忆蒸馏与决策模式固化",
		Members:  []string{"distill", "dream", "decision_pattern_solidify"},
		Example:  "distill/dream",
	},
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
