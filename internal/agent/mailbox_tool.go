package agent

// 任务 689：信箱三合一 mailbox(action=peek|drain|query)。
//
// 背景（等待/触发/查询家族盘点结论）：peek_own_inbox / drain_inbox /
// query_collab_mail 本质是同一信箱的三个视角——自己信箱只读快照（570）、
// 消费未读（235）、历史只读查询（320）——唯一真冗余。本文件把它们合并为
// 单入口，照 task 174 taskCardTool 的多 action 范式：旧工具 struct 仍是
// 唯一实现代码路径（mailbox 按 action 委托），合并后的 schema 永远不会
// 漂移出测试钉住的行为。
//
// 兼容迁移（铁律：不得静默破坏既有技能/会话中的调用）：三个旧名过渡期
// 保留为转发器（peekOwnInboxAliasTool 等）——转发到 mailbox 对应 action，
// 在返回 JSON 内注入 deprecated/useInstead 淡化提示（JSON 内而不是追加
// 文本，保证结果仍是一段可解析 JSON）；boot 注册转发器而非旧 struct，
// 旧构造器（NewPeekOwnInboxTool 等）保留给测试与直调（174 先例）。
//
// 消费权（M-a 单消费者，不变式保持）：mailbox 的 action=drain 与旧
// drain_inbox 同受 experimental_collab_background_delivery 的 boot 门约束
// ——泵拥有投递时（开关 OFF）action=drain 调用时拒绝、转发器不注册；
// action=peek / query 是只读视角，与旧两工具一样无条件可用。

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"reasonix/internal/tool"
)

// NewMailboxTool builds the merged mailbox entry (task 689). drainRegistered
// is the boot-time single-consumer decision (collabDrainInboxEnabled at boot):
// it decides whether action=drain may execute, exactly as the old
// drain_inbox registration gate did.
func NewMailboxTool(cfg SessionCollabConfig, drainRegistered bool) tool.Tool {
	return mailboxTool{cfg: cfg, drainRegistered: drainRegistered}
}

type mailboxTool struct {
	cfg             SessionCollabConfig
	drainRegistered bool
}

func (mailboxTool) Name() string { return "mailbox" }

func (mailboxTool) ReadOnly() bool { return false }

func (mailboxTool) Description() string {
	return "Single entry for the cross-session mailbox (task 689), routed by `action`; the three former tools are merged here. action=peek — read YOUR OWN durable MailStore inbox (the former peek_own_inbox, task 570): platform status notes a sender could never read before (steer_degraded, refused_*, delivery_failed), each row with settled (delivery-pump cursor) and outcome (delivery receipt). Strictly read-only: no claim, no ack, no cursor write, so peeking never consumes anything. action=drain — pull unread mail into the result and settle it (the former drain_inbox, task 235): settle=true (default) claims+acks the batch, settle=false peeks without advancing the cursor; source/limit/layer filters preserved; this is the consuming half and follows the same boot gate as before — with the host delivery pump owning the mailbox (experimental_collab_background_delivery off) action=drain refuses, use action=peek to read without consuming. action=query — read-only SQL-shaped history over the unified mail table the inbox panel shows (the former query_collab_mail, task 320): filters from/to/bucket/thread_id/since/until/unread_only/state, newest first, hard-capped. Waiting-family selection: waiting on YOUR OWN background jobs → wait (blocking, full result) or bash_output (non-blocking, incremental output); waiting on a COLLAB PEER's condition → event_wait; sending a message and waiting for ITS one reply → talk_to_session(wait=true); mailbox reads/writes → this tool. The old names peek_own_inbox / drain_inbox / query_collab_mail still work as deprecated aliases that forward here; new calls should use mailbox."
}

func (mailboxTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"action":{"type":"string","enum":["peek","drain","query"],"description":"peek: read-only self-inbox snapshot with delivery receipts. drain: pull and optionally settle unread mail (consuming). query: read-only mail-history search."},"limit":{"type":"integer","description":"peek/drain: max rows returned (default 20; peek max 100, drain clamped 1..100). query: max rows (default 50, hard max 500). Counters always cover the whole set."},"only_system":{"type":"boolean","description":"peek only: only platform status notes (kind=system). Default false."},"hide_settled":{"type":"boolean","description":"peek only: hide rows the delivery pump already settled (default false)."},"settle":{"type":"boolean","description":"drain only: true (default) claim and ack — messages are consumed. false: peek only, cursor does not advance."},"source":{"type":"string","description":"drain only: only messages whose fromContactId matches this value."},"layer":{"type":"string","enum":["mailbox","inbox","all"],"description":"drain only: which layer to pull (mailbox = MailStore, inbox = session queue, all = merged; default all)."},"from":{"type":"string","description":"query only: only messages sent BY this contact_id."},"to":{"type":"string","description":"query only: only messages sent TO this contact_id."},"bucket":{"type":"string","enum":["all","approval","mention","automation","system"],"description":"query only: five-bucket inbox view (default all)."},"thread_id":{"type":"string","description":"query only: only messages on this conversation thread."},"since":{"type":"integer","description":"query only: inclusive lower bound on at (ms epoch)."},"until":{"type":"integer","description":"query only: exclusive upper bound on at (ms epoch)."},"unread_only":{"type":"boolean","description":"query only: only messages the recipient has not consumed."},"state":{"type":"string","enum":["all","pendingMe","mine","decided"],"description":"query only: approval sub-state filter (pendingMe = waiting on MY verdict)."},"offset":{"type":"integer","description":"query only: pagination offset into the filtered set."},"order":{"type":"string","enum":["desc","asc"],"description":"query only: date order (default desc = newest first)."}},"required":["action"]}`)
}

func (t mailboxTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	raw := map[string]json.RawMessage{}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &raw); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
	}
	action := strings.ToLower(strings.TrimSpace(unquoteJSONString(raw["action"])))
	switch action {
	case "peek":
		return t.delegate(ctx, peekOwnInboxTool{cfg: t.cfg}, raw)
	case "drain":
		if !t.drainRegistered {
			return "", fmt.Errorf("mailbox(action=drain): the host delivery pump owns this mailbox (experimental_collab_background_delivery is off) — only the pump may consume, a tool drain would double-consume the same batch (single-consumer rule); use action=peek to read without consuming")
		}
		return t.delegate(ctx, drainInboxTool{cfg: t.cfg}, raw)
	case "query":
		return t.delegate(ctx, queryCollabMailTool{cfg: t.cfg}, raw)
	case "":
		return "", fmt.Errorf("action is required (peek|drain|query)")
	default:
		return "", fmt.Errorf("unknown action %q (peek|drain|query)", action)
	}
}

// delegate strips the routing action from the raw args and hands the rest to
// the single implementation code path (the old tool struct), so the merged
// schema can never drift from the behavior the family tests pinned (task 174
// pattern).
func (t mailboxTool) delegate(ctx context.Context, target interface {
	Execute(context.Context, json.RawMessage) (string, error)
}, raw map[string]json.RawMessage) (string, error) {
	delete(raw, "action")
	rest, err := json.Marshal(raw)
	if err != nil {
		return "", err
	}
	return target.Execute(ctx, rest)
}

// unquoteJSONString decodes a json.RawMessage that should be a JSON string;
// missing or non-string values come back empty.
func unquoteJSONString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return s
}

// mergeMailboxArgs rebuilds a tool's original args under a fixed mailbox
// action, so a deprecated alias forwards without re-parsing the payload it
// does not understand.
func mergeMailboxArgs(action string, args json.RawMessage) (json.RawMessage, error) {
	raw := map[string]json.RawMessage{}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &raw); err != nil {
			return nil, fmt.Errorf("invalid args: %w", err)
		}
	}
	raw["action"], _ = json.Marshal(action)
	return json.Marshal(raw)
}

// stampDeprecated parses a mailbox JSON result and injects the deprecation
// hint INSIDE the object (never appended text — the result must stay one
// parseable JSON payload). A payload that fails to parse (cannot happen for
// the three delegates, but stay honest) is returned unchanged.
func stampDeprecated(out string, action string) string {
	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		return out
	}
	payload["deprecated"] = true
	payload["useInstead"] = "mailbox(action=" + action + ")"
	payload["deprecationNote"] = "this tool name is a deprecated alias (task 689) — call mailbox(action=" + action + ") instead; the old name will be removed after the transition window"
	b, err := json.Marshal(payload)
	if err != nil {
		return out
	}
	return string(b)
}

// —— 过渡期转发器（铁律：旧名不得静默破坏）——
//
// boot 注册的是这三个转发器；旧 struct 构造器（NewPeekOwnInboxTool /
// NewDrainInboxTool / NewQueryCollabMailTool）保留给测试与直调（174 先例），
// 它们的 Execute 仍是唯一实现代码路径。转发器经 mailbox 转发，行为等价 +
// JSON 内淡化提示。

// NewPeekOwnInboxAliasTool builds the deprecated peek_own_inbox forwarder.
func NewPeekOwnInboxAliasTool(cfg SessionCollabConfig) tool.Tool {
	return peekOwnInboxAliasTool{cfg: cfg}
}

type peekOwnInboxAliasTool struct{ cfg SessionCollabConfig }

func (peekOwnInboxAliasTool) Name() string { return "peek_own_inbox" }

func (peekOwnInboxAliasTool) Description() string {
	return "Deprecated alias (task 689): forwards to mailbox(action=peek) — the same read-only peek at YOUR OWN cross-session mailbox (platform delivery notes: steer_degraded / refused_* / delivery_failed, with settled cursor and receipt outcome). The result carries a deprecated marker naming the replacement. Call mailbox(action=peek) instead; this name will be removed after the transition window."
}

// Schema stays the old tool's schema: existing callers' arg shapes keep
// validating unchanged during the transition window.
func (peekOwnInboxAliasTool) Schema() json.RawMessage { return peekOwnInboxTool{}.Schema() }

func (peekOwnInboxAliasTool) ReadOnly() bool { return true }

func (t peekOwnInboxAliasTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	merged, err := mergeMailboxArgs("peek", args)
	if err != nil {
		return "", err
	}
	out, err := mailboxTool{cfg: t.cfg, drainRegistered: true}.Execute(ctx, merged)
	if err != nil {
		return "", err
	}
	return stampDeprecated(out, "peek"), nil
}

// NewDrainInboxAliasTool builds the deprecated drain_inbox forwarder.
// drainRegistered carries the same boot single-consumer gate the old
// drain_inbox registration used; boot only registers this alias when the gate
// is on, and the mailbox drain action re-checks it at call time.
func NewDrainInboxAliasTool(cfg SessionCollabConfig, drainRegistered bool) tool.Tool {
	return drainInboxAliasTool{cfg: cfg, drainRegistered: drainRegistered}
}

type drainInboxAliasTool struct {
	cfg             SessionCollabConfig
	drainRegistered bool
}

func (drainInboxAliasTool) Name() string { return "drain_inbox" }

func (drainInboxAliasTool) Description() string {
	return "Deprecated alias (task 689): forwards to mailbox(action=drain) — the same consuming pull of unread cross-session mail (settle/limit/source/layer preserved; settle=false peeks without advancing the cursor). The result carries a deprecated marker naming the replacement. Call mailbox(action=drain) instead; this name will be removed after the transition window."
}

// Schema stays the old tool's schema: existing callers' arg shapes keep
// validating unchanged during the transition window.
func (drainInboxAliasTool) Schema() json.RawMessage { return drainInboxTool{}.Schema() }

func (drainInboxAliasTool) ReadOnly() bool { return false }

func (t drainInboxAliasTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	merged, err := mergeMailboxArgs("drain", args)
	if err != nil {
		return "", err
	}
	out, err := mailboxTool{cfg: t.cfg, drainRegistered: t.drainRegistered}.Execute(ctx, merged)
	if err != nil {
		return "", err
	}
	return stampDeprecated(out, "drain"), nil
}

// NewQueryCollabMailAliasTool builds the deprecated query_collab_mail forwarder.
func NewQueryCollabMailAliasTool(cfg SessionCollabConfig) tool.Tool {
	return queryCollabMailAliasTool{cfg: cfg}
}

type queryCollabMailAliasTool struct{ cfg SessionCollabConfig }

func (queryCollabMailAliasTool) Name() string { return "query_collab_mail" }

func (queryCollabMailAliasTool) Description() string {
	return "Deprecated alias (task 689): forwards to mailbox(action=query) — the same read-only, hard-capped search of the cross-session mail history (from/to/bucket/thread_id/since/until/unread_only/state/limit/offset/order preserved, no cursor side effects). The result carries a deprecated marker naming the replacement. Call mailbox(action=query) instead; this name will be removed after the transition window."
}

// Schema stays the old tool's schema: existing callers' arg shapes keep
// validating unchanged during the transition window.
func (queryCollabMailAliasTool) Schema() json.RawMessage { return queryCollabMailTool{}.Schema() }

func (queryCollabMailAliasTool) ReadOnly() bool { return true }

func (t queryCollabMailAliasTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	merged, err := mergeMailboxArgs("query", args)
	if err != nil {
		return "", err
	}
	out, err := mailboxTool{cfg: t.cfg, drainRegistered: true}.Execute(ctx, merged)
	if err != nil {
		return "", err
	}
	return stampDeprecated(out, "query"), nil
}
