package capability

// Task 955: capability groups — the L0/L1 disclosure layer's middle tier.
//
// Groups are PURE METADATA on Entry (951 修点④): no group lifecycle, no group
// permissions, no per-group switch. The whole mechanism is (a) a static table
// below, (b) an Entry.Group tag consumed by the rendering layer (list group
// rows, route group suggestions, search group attribution), and (c) group
// cards inspected through inspect tool:group/<name>. Everything is gated by
// the single experimental_capability_groups switch at the catalog build —
// off means no group entries, no tags, zero behaviour.
//
// The 53-tool / 8-group split is the v3 target tree (能力架构-目标树状图
// 20261017, L1 lane). MCP servers map to natural groups by server name and
// stay out of this table.

import "strings"

// KindGroup marks a catalog entry that is a group directory row / group card
// rather than a callable capability. Group entries are never executable:
// action=call on tool:group/* is refused with inspect guidance.
const KindGroup Kind = "group"

// GroupMember is one row of a group card member table: identity, a one-line
// scenario, the key parameters, and (session group only) the mapping to the
// session_hub action that fronts it (954's aggregate tool).
type GroupMember struct {
	ID      string // capability id, e.g. "tool:rename_session"
	Summary string // one-line scenario
	Params  string // key parameters, e.g. "target=contact_id|topic_id|exact title"
	Action  string // session_hub action that fronts this member ("" if none)
}

// GroupCard is the L2 card body (引导设计 §三): scenario lines (including the
// boundary against the neighbouring group), the member table, and copy-paste
// JSON examples. Only the first-batch groups (session, im) carry a card; the
// other six are directory rows until call data justifies a card.
type GroupCard struct {
	Scenario []string      // 3-5 scenario lines, incl. neighbour-group boundary
	Members  []GroupMember // member table
	Examples []string      // 2-3 copy-paste JSON examples
}

// GroupDef is one row of the static group table. Members are builtin tool
// names; a member tool that is not registered in this session simply does not
// appear in catalog-based renderings (registration validation is structural:
// route/list/search only ever render from live catalog entries).
type GroupDef struct {
	Name     string   // stable group name (used in tool:group/<name>)
	Scenario string   // one-line scenario for list rows and route reasons
	Members  []string // member builtin tool names (the 53-tool tagging)
	Entry    string   // L0 aggregate entry tool ("session_hub"); "" = none
	Example  string   // most-used action/member for the list row
	Triggers []string // route triggers (推组); matched as substrings
	Card     *GroupCard
}

// groupCatalog is the single source of truth for the 8 groups (v3 tree L1
// lane). Order = display order.
var groupCatalog = []GroupDef{
	{
		Name:     "session",
		Scenario: "跨会话协作：发信/通讯录/判活/读尾/建会话/任务卡/会话管理（协作组聚合件承载）",
		Members: []string{
			"talk_to_session_sync", "subscribe_session",
			"create_task_card", "get_task_card", "update_task_card", "list_task_cards",
			"delete_session", "get_session_info", "search_sessions", "session_control",
			"rename_session", "rename_topic", "move_topic_to_group", "manage_sessions",
			"list_session_versions", "adopt_session_version",
		},
		Entry:    "session_hub",
		Example:  "send/contacts/tail",
		Triggers: []string{"派单", "发信", "发消息", "回信", "联系人", "判活", "读尾", "建会话", "任务卡", "跨会话", "talk_to_session", "session_hub"},
		Card: &GroupCard{
			Scenario: []string{
				"跨会话协作深件组：与另一会话发信/判活/读尾、任务卡记录、会话组织与版本管理都在这里。",
				"高频动作不需要进本组——直接用 L0 聚合件 session_hub（action=send/contacts/tail/status/create/purpose/delivery_status/card/budget）。",
				"与 IM 通道组（tool:group/im）的边界：本组只管 Reasonix 会话之间；对外发 QQ/微信/钉钉/飞书走 IM 组。",
				"与信箱投递组（tool:group/inbox）的边界：本组含发信与协作补充件；收件箱读取/消费/查询/等待在 inbox 组。",
			},
			Members: []GroupMember{
				{ID: "tool:talk_to_session_sync", Summary: "发信并同步等对方回复（超时返回 timeout，回复稍后落信箱）", Params: "to=contact_id|topic_id|精确标题, message, timeout", Action: "send（同步变体）"},
				{ID: "tool:subscribe_session", Summary: "订阅对方会话异常事件，中途推送进本会话", Params: "action=subscribe|unsubscribe|list, target, events"},
				{ID: "tool:create_task_card", Summary: "建任务卡（派单可见性记录）", Params: "标题/负责人/正文", Action: "card create"},
				{ID: "tool:get_task_card", Summary: "读一张任务卡", Params: "card_id", Action: "card get"},
				{ID: "tool:update_task_card", Summary: "更新任务卡；更新后向对方展示 ```taskcard 围栏", Params: "card_id + 变更字段", Action: "card update"},
				{ID: "tool:list_task_cards", Summary: "列任务卡", Params: "过滤条件", Action: "card list"},
				{ID: "tool:delete_session", Summary: "删除一个会话（危险：不可恢复）", Params: "target"},
				{ID: "tool:get_session_info", Summary: "读单个会话元数据（不读转录）", Params: "target"},
				{ID: "tool:search_sessions", Summary: "按关键词搜通讯录（标题/职责/ID 子串匹配）", Params: "query"},
				{ID: "tool:session_control", Summary: "停止对方运行中的回合 / 切换对方模型（需 experimental_session_control）", Params: "target, action=stop|set_model"},
				{ID: "tool:rename_session", Summary: "按 contact_id/topic_id/精确标题改会话名（纯元数据，运行中安全）", Params: "target, new_name"},
				{ID: "tool:rename_topic", Summary: "改会话显示名（ID 稳定：改名不换 contact_id/topic_id）", Params: "target, new_title"},
				{ID: "tool:move_topic_to_group", Summary: "把会话移入侧栏分组（组不存在则创建；一会话同时只属一组）", Params: "target, group"},
				{ID: "tool:manage_sessions", Summary: "会话与分组整理大全：list/rename/move/reorder/group_*/op_log/rollback", Params: "action, …（group_delete 先无 confirm 出提案再 confirm 执行）"},
				{ID: "tool:list_session_versions", Summary: "列会话版本（检查点历史）", Params: "target"},
				{ID: "tool:adopt_session_version", Summary: "回滚到指定会话版本", Params: "target, version"},
			},
			Examples: []string{
				`{"action":"call","capability_id":"tool:search_sessions","arguments":{"query":"合并线"}}`,
				`{"action":"call","capability_id":"tool:rename_session","arguments":{"target":"contact_abc","new_name":"合并线（改）"}}`,
				`{"action":"call","capability_id":"tool:get_task_card","arguments":{"card_id":"..."}}`,
			},
		},
	},
	{
		Name:     "im",
		Scenario: "IM 通道：对外发 QQ/微信/钉钉/飞书，会话内频道广播（channel 族）",
		Members: []string{
			"channel_list", "channel_read", "channel_send", "channel_cancel",
			"dingtalk", "feishu", "qq", "weixin",
		},
		Example:  "channel_send",
		Triggers: []string{"qq", "微信", "钉钉", "飞书", "dingtalk", "feishu", "weixin", "发qq", "频道"},
		Card: &GroupCard{
			Scenario: []string{
				"对外 IM 出口组：把消息发到 QQ/微信/钉钉/飞书（plugin 注册，本机未必装——工具不在即未装）。",
				"channel_* 族是 Reasonix 内虚拟频道：广播给全体成员（每人信箱一份 followup），不是对外 IM。",
				"与会话协作组（tool:group/session）的边界：给『另一个 Reasonix 会话』发信用 session 组/聚合件，不走本组。",
			},
			Members: []GroupMember{
				{ID: "tool:channel_list", Summary: "列本会话可见的频道（名称/成员/消息数）", Params: "无"},
				{ID: "tool:channel_read", Summary: "读频道历史（按成员各自标已读）", Params: "channel, limit?=50, since?"},
				{ID: "tool:channel_send", Summary: "向频道广播：落频道历史 + 逐成员信箱投递（2-5s 节流）", Params: "channel, message"},
				{ID: "tool:channel_cancel", Summary: "撤回自己发的频道消息（已投递进对方信箱的副本无法撤回）", Params: "message_id"},
				{ID: "tool:dingtalk", Summary: "发钉钉消息（plugin，未必装）", Params: "按已装 plugin 的 schema"},
				{ID: "tool:feishu", Summary: "发飞书消息（plugin，未必装）", Params: "按已装 plugin 的 schema"},
				{ID: "tool:qq", Summary: "发 QQ 消息（plugin，未必装）", Params: "按已装 plugin 的 schema"},
				{ID: "tool:weixin", Summary: "发微信消息（plugin，未必装）", Params: "按已装 plugin 的 schema"},
			},
			Examples: []string{
				`{"action":"call","capability_id":"tool:channel_send","arguments":{"channel":"dev","message":"..."}}`,
				`{"action":"call","capability_id":"tool:channel_read","arguments":{"channel":"dev","limit":20}}`,
			},
		},
	},
	{
		Name:     "inbox",
		Scenario: "信箱投递：读取/消费/查询协作信箱，等待协作方条件（mailbox 族 + event_wait）",
		Members: []string{
			"mailbox", "peek_own_inbox", "drain_inbox",
			"read_collab_status", "query_collab_mail", "event_wait",
		},
		Example:  "mailbox(action=peek)",
		Triggers: []string{"收件箱", "信箱", "未读", "投递状态", "协作邮件", "event_wait"},
	},
	{
		Name:     "worktree",
		Scenario: "worktree 建立/隔离打开/合并前准备与检查/合并回主线",
		Members: []string{
			"create_worktree", "open_isolated_worktree_project",
			"prepare_worktree_merge", "inspect_worktree_merge", "merge_worktree_back",
		},
		Example:  "create_worktree",
		Triggers: []string{"worktree", "工作树", "合并回", "隔离工程"},
	},
	{
		Name:     "system",
		Scenario: "系统杂项：注册校验/代码索引/截图/UI 操控/网页抓取/todo/删除符号/反馈",
		Members: []string{
			"claim_check", "code_index", "screenshot", "ui_interact",
			"web_fetch", "todo_read", "delete_symbol", "submit_feedback",
		},
		Example:  "web_fetch",
		Triggers: []string{"截图", "screenshot", "网页抓取", "web_fetch", "代码索引", "ui_interact"},
	},
	{
		Name:     "heartbeat",
		Scenario: "心跳任务：后台周期任务的列表/写入/开关",
		Members:  []string{"heartbeat_task_list", "heartbeat_task_upsert", "heartbeat_task_enable"},
		Example:  "heartbeat_task_upsert",
		Triggers: []string{"心跳任务", "heartbeat_task"},
	},
	{
		Name:     "files",
		Scenario: "文件操作：删除区间/移动文件/列目录/编辑 notebook",
		Members:  []string{"delete_range", "move_file", "ls", "notebook_edit"},
		Example:  "move_file",
		Triggers: []string{"移动文件", "删除区间", "delete_range", "move_file"},
	},
	{
		Name:     "memory",
		Scenario: "记忆/蒸馏：dream/distill 记忆整理与决策模式固化",
		Members:  []string{"distill", "dream", "decision_pattern_solidify"},
		Example:  "dream",
		Triggers: []string{"蒸馏", "dream", "做梦", "记忆整理", "decision_pattern"},
	},
}

// toolGroupIndex returns the tool-name → group-name tag map (the 53-tool
// tagging). Built once per call; callers cache if hot.
func toolGroupIndex() map[string]string {
	index := make(map[string]string, 64)
	for _, g := range groupCatalog {
		for _, member := range g.Members {
			index[member] = g.Name
		}
	}
	return index
}

// ToolGroup returns the group name a builtin tool belongs to ("" if none).
// Pure metadata lookup — safe to call regardless of the switch; renderers
// only consume it when the group layer is enabled.
func ToolGroup(toolName string) string {
	return toolGroupIndex()[strings.TrimSpace(toolName)]
}

// Groups returns the static group table (copy-safe: callers must not mutate
// the returned slice's GroupDef internals; cards are shared read-only).
func Groups() []GroupDef {
	out := make([]GroupDef, len(groupCatalog))
	copy(out, groupCatalog)
	return out
}

// GroupByName looks up one group definition.
func GroupByName(name string) (GroupDef, bool) {
	name = strings.TrimSpace(name)
	for _, g := range groupCatalog {
		if g.Name == name {
			return g, true
		}
	}
	return GroupDef{}, false
}

// GroupID is the capability id of a group card / directory row.
func GroupID(name string) string { return "tool:group/" + name }

// ParseGroupID extracts the group name from a tool:group/<name> id.
func ParseGroupID(id string) (string, bool) {
	name, ok := strings.CutPrefix(strings.TrimSpace(id), "tool:group/")
	name = strings.TrimSpace(name)
	return name, ok && name != ""
}

// BuildGroupEntries renders the 8 group directory rows as catalog entries.
// registeredTools is the live builtin tool-name set: an Entry pointer (e.g.
// session_hub, owned by task 954) only renders as the group's entry while it
// is actually registered — the anti-ghost rule (951 #4) applies to entry
// pointers too. Call only when experimental_capability_groups is on.
func BuildGroupEntries(registeredTools map[string]bool) []Entry {
	out := make([]Entry, 0, len(groupCatalog))
	for _, g := range groupCatalog {
		memberCount := 0
		for _, member := range g.Members {
			if registeredTools == nil || registeredTools[member] {
				memberCount++
			}
		}
		entry := g.Entry
		if entry == "" || (registeredTools != nil && !registeredTools[entry]) {
			entry = ""
		}
		out = append(out, Entry{
			ID:          GroupID(g.Name),
			Kind:        KindGroup,
			Name:        g.Name,
			Description: groupRowDescription(g, memberCount, entry),
			Status:      StatusReady,
			ReadOnly:    true,
			AutoUse:     AutoUseSuggest,
			Triggers:    append([]string(nil), g.Triggers...),
		})
	}
	return out
}

// groupRowDescription is the list v2 group-row format (引导设计 §三):
//
//	<场景一句话>（N 深件）｜入口=<L0 件或无>｜例：<最常用 action>｜组卡：inspect tool:group/<名>
func groupRowDescription(g GroupDef, memberCount int, entry string) string {
	entryPart := "无"
	if entry != "" {
		entryPart = entry
	}
	var b strings.Builder
	b.WriteString(g.Scenario)
	b.WriteString("（")
	b.WriteString(itoa(memberCount))
	b.WriteString(" 深件）｜入口=")
	b.WriteString(entryPart)
	b.WriteString("｜例=")
	b.WriteString(g.Example)
	b.WriteString("｜组卡：inspect ")
	b.WriteString(GroupID(g.Name))
	return b.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [8]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
