package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"reasonix/internal/tool"
)

// SessionHubTargets carries the nine task-19/174/570 collaboration tools the
// session_hub dispatches to (task 954). Every field is the SAME tool.Tool the
// registry already holds, so the hub is a pure routing shell — it re-uses the
// nine implementations and never re-implements their logic (架构评审修点②). A
// nil field means the underlying tool is not registered (panel gate off, or a
// host that cannot create sessions), so the hub omits that action entirely
// rather than advertising a call that would fail (铁律 3: 错误文案指的下一步必须存在).
type SessionHubTargets struct {
	Send           tool.Tool // talk_to_session
	Contacts       tool.Tool // list_addressable_sessions
	Tail           tool.Tool // read_session_tail (nil unless SessionCollabAllowReadTail)
	Status         tool.Tool // get_session_status
	Create         tool.Tool // create_collab_session (nil unless the host can create)
	Purpose        tool.Tool // set_session_purpose
	DeliveryStatus tool.Tool // get_message_status
	Card           tool.Tool // task_card
	Budget         tool.Tool // extend_research_budget (nil when unregistered)
}

// sessionHubTool is the task-954 L0 aggregation entry. It carries one JSON
// schema whose `action` enum lists only the available actions, and dispatches
// each call to the matching implementation tool with the target's own argument
// object (the hub's dispatch keys stripped). Naming (修点①): the tool is
// session_hub, NOT session — a bare `session` name would be sliced by the
// resolver's `session:` capability-alias prefix.
type sessionHubTool struct {
	actions map[string]tool.Tool
	order   []string
}

// hubActionOrder fixes the action ordering for the schema enum, the
// description's action table, and error messages — so the surface is stable
// regardless of map iteration.
var hubActionOrder = []string{
	"send", "contacts", "tail", "status", "create",
	"purpose", "delivery_status", "card", "budget",
}

// hubActionRows are the per-action mini-group-card lines (引导设计 md §一: each
// row = name + scenario + key params, so the model can copy a call without
// jumping to L2). They are joined only for the actions actually available, so
// the description and the schema enum can never disagree.
var hubActionRows = map[string]string{
	"send":            "send      发信给另一会话——to=contact_id|topic_id|精确标题，message=正文；新线程不传 thread_id，回信带 thread_id+hop。回执 queued≠已读，真实结局查 delivery_status。",
	"contacts":        "contacts  列通讯录（=原 list_addressable_sessions）——query?=关键词，limit?=200，sent?=true 读自己的发件日志。拿 contact_id 用这个。",
	"tail":            "tail      读对方会话尾部——target=contact_id|topic_id|精确标题，max_bytes?=10240。判「对方是否消费了你的派单」用这个。",
	"status":          "status    判对方运行态——targets?=[contact_id…]，缺省全体在线会话。state=unknown ≠ 不会开工，别当死线。",
	"create":          "create    建新会话并入组——title、purpose 必填；group?=\"reasonix-for-ai\"（AI 子对话默认组）；message? 首条指令随建随发。",
	"purpose":         "purpose   改会话职责——purpose 必填，target?=别人（缺省=本会话）。看对方职责用 contacts。",
	"delivery_status": "delivery_status  投递终态查询——message_id 必填（修点⑥：原名 message 与 send 的 message=正文同名异义）。injected/queued_followup=无需行动；refused=需处理。",
	"card":            "card      任务卡 create|update|get|list——card_action=子动作，title/body/assignee/id/status/result 随用；update 后展示 ```taskcard 围栏给用户。",
	"budget":          "budget    调研预算延长——reason=为什么真需要更多轮（仅只读预算 nudge 触发后可用；不需要就收敛汇报已有证据）。",
}

// NewSessionHubTool builds the aggregation shell over the nine collaboration
// tools. Only actions whose target is non-nil are exposed (schema enum +
// description rows + dispatch), so a gated-off tool silently narrows the hub
// instead of leaving a dead action behind.
func NewSessionHubTool(t SessionHubTargets) tool.Tool {
	targets := map[string]tool.Tool{
		"send": t.Send, "contacts": t.Contacts, "tail": t.Tail,
		"status": t.Status, "create": t.Create, "purpose": t.Purpose,
		"delivery_status": t.DeliveryStatus, "card": t.Card, "budget": t.Budget,
	}
	h := sessionHubTool{actions: make(map[string]tool.Tool, len(hubActionOrder))}
	for _, name := range hubActionOrder {
		if targets[name] == nil {
			continue
		}
		h.actions[name] = targets[name]
		h.order = append(h.order, name)
	}
	return h
}

func (h sessionHubTool) Name() string { return "session_hub" }

// Description is the L0 guidance surface (引导设计 md §一): trigger words, the
// action table with embedded mini group-cards, the 口径钉 that pin the mistakes
// the collab memory accumulated (queued≠已读 / unknown≠不会开工 / 新线程不传
// thread_id), and the fixed tail line indexing the deep tools. Target ≈2.5KB —
// it replaces the ≈7KB the nine scattered tools used to carry.
func (h sessionHubTool) Description() string {
	var b strings.Builder
	b.WriteString("跨会话协作唯一入口（实验室能力，工具名=session_hub——架构评审修点①：避开裸 session，与解析层 session: alias 前缀冲突）：发信/通讯录/判活/读尾/建会话/投递追踪/任务卡/预算。\n")
	b.WriteString("触发词：派单、发信、回信、回板、联系人、判活、读尾巴、建会话、任务卡。\n\n")
	b.WriteString("动作表（action=…，只列当前可用项）：\n")
	for _, name := range h.order {
		b.WriteString("  ")
		b.WriteString(hubActionRows[name])
		b.WriteString("\n")
	}
	b.WriteString("\n口径钉：回执 queued≠已读（真结局查 delivery_status）；state=unknown≠对方不会开工（查 lastActivity 再判）；新线程不传 thread_id，回信才带 thread_id+hop>0。\n")
	b.WriteString("边界：不管文件信箱/zcode 留言板协议（那走 bash 文件操作）；不管内部总线（bus）；不管已归档会话（contacts archived?=true 才列）。\n")
	b.WriteString("深件与全文档：inspect tool:group/session（rename_session/manage_sessions/版本管理等 16 深件在此）。")
	return b.String()
}

// hubParamProperties is the union of the nine tools' argument objects. Each
// property names the action(s) that read it; per-action required-ness is
// enforced by the target tool itself, so this schema stays a routing hint and
// the hub never duplicates validation (修点②).
const hubParamProperties = `"to":{"type":"string","description":"send: target contact_id|topic_id|exact title."},
"message":{"type":"string","description":"send: the message body. create: optional first instruction for each new session."},
"thread_id":{"type":"string","description":"send: when REPLYING, the inbound message id you received; omit to start a new chain."},
"hop":{"type":"integer","description":"send: 0 for a new chain; >0 REQUIRES thread_id (replies only)."},
"delivery":{"type":"string","enum":["steer","followup"],"description":"send/create: steer injects mid-turn (degrades to followup); followup queues for next turn."},
"receipt":{"type":"boolean","description":"send: request a read receipt (default off)."},
"wait":{"type":"boolean","description":"send: block (bounded) for a reply on this thread instead of returning queued."},
"timeout_ms":{"type":"integer","description":"send: wait duration (default 30000, max 120000)."},
"card_id":{"type":"string","description":"send: optional task card id to stamp."},
"require_reply":{"type":"boolean","description":"send: sender needs an answer on this thread (panel-gated)."},
"approver":{"type":"string","description":"send: contact_id that answers this task's approval prompts (default: sender)."},
"query":{"type":"string","description":"contacts: keyword filter over title/purpose/ids."},
"limit":{"type":"integer","description":"contacts: max rows (default 200, max 1000). card list: how many cards (default 20)."},
"archived":{"type":"boolean","description":"contacts: include retired archive sessions (default false)."},
"group":{"type":"string","description":"contacts: keep only one sidebar group. create: group title to file the new session into."},
"sent":{"type":"boolean","description":"contacts: return YOUR OWN outgoing log (misdirected-send check)."},
"include_stats":{"type":"boolean","description":"contacts: add eventsBytes/turns/lastActivityAt per row."},
"target":{"type":"string","description":"tail/purpose: contact_id|topic_id|exact title of one session."},
"max_bytes":{"type":"integer","description":"tail: trailing bytes to return (default 10240, max 65536)."},
"targets":{"type":"array","items":{"type":"string"},"description":"status: sessions to query (mixed ids/titles); omit for every live session."},
"include_tasks_and_subagents":{"type":"boolean","description":"status: attach background-job and running-subagent rows per session."},
"title":{"type":"string","description":"create: session title. card create: card title."},
"purpose":{"type":"string","description":"create: one-line duty for the new session. purpose action: the duty line to register."},
"group_id":{"type":"string","description":"create: existing group id to join (beats title match)."},
"project":{"type":"string","description":"create: registered project ROOT to create the session in (default: caller's project)."},
"model":{"type":"string","description":"create: provider/model ref to pin (bare model id refused)."},
"sessions":{"type":"array","description":"create: batch form [{title,purpose,group?,group_id?,model?}, …].","items":{"type":"object"}},
"identity":{"type":"object","description":"purpose: structured identity {type,domain} — replace-on-pass.","properties":{"type":{"type":"string","enum":["human","main","sub","heartbeat","system"]},"domain":{"type":"string"}}},
"duties":{"type":"array","items":{"type":"string"},"description":"purpose: structured duty list ([] clears)."},
"message_id":{"type":"string","description":"delivery_status: the messageId returned by send (msg_…)."},
"card_action":{"type":"string","enum":["create","update","get","list"],"description":"card: which card operation to run."},
"body":{"type":"string","description":"card create: the card body."},
"assignee":{"type":"string","description":"card create: optional assignee contact_id."},
"id":{"type":"string","description":"card update/get: which card."},
"status":{"type":"string","description":"card update: new status; card list: filter by status."},
"result":{"type":"string","description":"card update: the outcome text."},
"reason":{"type":"string","description":"budget: why the investigation genuinely needs more rounds."}`

func (h sessionHubTool) Schema() json.RawMessage {
	enum := `"` + strings.Join(h.order, `","`) + `"`
	props := `"action":{"type":"string","enum":[` + enum + `],"description":"Which collaboration action to run."},` + hubParamProperties
	return json.RawMessage(`{"type":"object","properties":{` + props + `},"required":["action"]}`)
}

// ReadOnly is false: the hub mixes read-only actions (contacts/tail/status/
// delivery_status) with writers (send/create/purpose/card/budget), and the
// batch classifier only parallelises a call whose Tool is ReadOnly. A single
// conservative false keeps write/read ordering intact; the read-only nine keep
// their own ReadOnly for direct use_capability dispatch.
func (h sessionHubTool) ReadOnly() bool { return false }

// Execute routes one action to its implementation tool. The only logic here is
// routing + parameter normalisation (修点②: hub 只做路由+参数归一): strip the
// hub's own dispatch keys and hand the target its own argument object. Values
// ride as json.RawMessage so each keeps its exact bytes; re-marshaling a map
// only reorders keys, which the targets' json.Unmarshal ignores.
func (h sessionHubTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var envelope struct {
		Action     string `json:"action"`
		CardAction string `json:"card_action"`
	}
	if len(args) == 0 {
		return "", fmt.Errorf("action is required (%s)", strings.Join(h.order, "|"))
	}
	if err := json.Unmarshal(args, &envelope); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	action := strings.TrimSpace(envelope.Action)
	if action == "" {
		return "", fmt.Errorf("action is required (%s)", strings.Join(h.order, "|"))
	}
	target, ok := h.actions[action]
	if !ok {
		return "", fmt.Errorf("unknown or unavailable action %q (%s)", action, strings.Join(h.order, "|"))
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(args, &raw); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	delete(raw, "action")
	if action == "card" {
		// task_card routes on ITS OWN `action` field; the hub consumed that key
		// for dispatch, so the card sub-action rides on card_action and is
		// renamed back here — pure parameter normalisation, no card logic.
		delete(raw, "card_action")
		if strings.TrimSpace(envelope.CardAction) == "" {
			return "", fmt.Errorf("card requires card_action (create|update|get|list)")
		}
		ca, err := json.Marshal(envelope.CardAction)
		if err != nil {
			return "", err
		}
		raw["action"] = ca
	}
	forwarded, err := json.Marshal(raw)
	if err != nil {
		return "", err
	}
	return target.Execute(ctx, forwarded)
}
