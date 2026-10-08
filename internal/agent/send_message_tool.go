package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"reasonix/internal/subagentmailbox"
	"reasonix/internal/tool"
)

// SendMessageTool is the parent-side half of the running-subagent message
// channel (task 616): it persists one message in the target sub-agent's
// mailbox (subagents/<ref>.mailbox/) and, when the sub-agent is running,
// hands the item to its steer queue for injection at the next tool-round gap.
// Delivery is three-state and never drops the message:
//
//	steered — the running sub-agent admitted the item mid-turn;
//	queued  — the handle was live but admission lost the terminal race, the
//	          message stays pending and a later continue_from delivers it;
//	parked  — the sub-agent is not running; the message stays in its mailbox
//	          for a later continue_from (and the user sees it in the panel).
//
// The tool is registered only when experimental_subagent_messaging is on, and
// sub-agents never inherit it (subagentAlwaysHiddenTools).
type SendMessageTool struct {
	mailboxes *subagentmailbox.Hub
}

// NewSendMessageTool wires the parent-side sender to the session's mailbox
// hub. A nil hub keeps the tool constructible but refuses sends (the boot
// path only registers the tool when the switch is on).
func NewSendMessageTool(mailboxes *subagentmailbox.Hub) *SendMessageTool {
	return &SendMessageTool{mailboxes: mailboxes}
}

func (*SendMessageTool) Name() string { return tool.HostSendMessage }

func (*SendMessageTool) Description() string {
	return "Send a message to a sub-agent you spawned with task, parallel_tasks, or fleet (the sa_... reference from its result). The message is persisted first, then delivered: steered means the running sub-agent consumes it at its next tool-round gap as guidance; queued means its run is finishing and the message waits in its mailbox (delivered if you continue_from that ref); parked means the sub-agent is not running and the message waits for a later continue_from. Messages are one-way guidance; the sub-agent's answer still returns through its normal result."
}

func (*SendMessageTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"to":{"type":"string","description":"The sa_... Subagent reference of the target sub-agent."},"summary":{"type":"string","description":"Short label (3-7 words) shown to the user while the message waits."},"message":{"type":"string","description":"Full message text. The sub-agent sees it as mid-turn guidance for its current task, not a new task."}},"required":["to","message"]}`)
}

// ReadOnly is false: the conservative classification (matching task) keeps
// the messaging send out of parallel read-only dispatches, even though it
// only writes inside the session's own subagents directory.
func (*SendMessageTool) ReadOnly() bool { return false }

func (t *SendMessageTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		To      string `json:"to"`
		Summary string `json:"summary"`
		Message string `json:"message"`
	}
	dec := json.NewDecoder(bytes.NewReader(args))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	p.To = strings.TrimSpace(p.To)
	p.Message = strings.TrimSpace(p.Message)
	if p.To == "" {
		return "", fmt.Errorf("to is required (the sa_... subagent reference)")
	}
	if p.Message == "" {
		return "", fmt.Errorf("message is required")
	}
	if t == nil || t.mailboxes == nil {
		return "", fmt.Errorf("send_message is not available in this session (experimental_subagent_messaging is off)")
	}
	receipt, err := t.mailboxes.Deliver(p.To, subagentmailbox.FromParent, p.Summary, p.Message)
	if err != nil {
		return "", err
	}
	return SendMessageReceiptText(p.To, receipt), nil
}

// SendMessageReceiptText renders the three-state delivery receipt the parent
// model sees. The wording names the next action for queued and parked
// outcomes (continue_from), which exist today.
func SendMessageReceiptText(ref string, receipt subagentmailbox.Receipt) string {
	switch receipt.Disposition {
	case subagentmailbox.DispositionSteered:
		return fmt.Sprintf("Message delivered to sub-agent %s (steered): the sub-agent consumes it at its next tool-round gap as guidance for its current task.", ref)
	case subagentmailbox.DispositionQueued:
		return fmt.Sprintf("Sub-agent %s is finishing its run, so the message is persisted in its mailbox (queued, id %s). It is delivered automatically if this transcript is continued with continue_from=%s; otherwise the user can read it in the subagent panel.", ref, receipt.Entry.ID, ref)
	case subagentmailbox.DispositionParked:
		return fmt.Sprintf("Sub-agent %s is not running (ended or unknown), so the message is persisted in its mailbox (parked, id %s). To deliver it, continue that transcript with continue_from=%s; the user can also read it in the subagent panel.", ref, receipt.Entry.ID, ref)
	default:
		return fmt.Sprintf("Message for sub-agent %s was persisted (disposition %s).", ref, receipt.Disposition)
	}
}
