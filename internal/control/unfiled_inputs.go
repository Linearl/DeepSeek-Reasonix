package control

import (
	"reasonix/internal/provider"
	"reasonix/internal/unfiled"
)

// UnfiledInput is one recovered prompt plus its transcript anchor (task 1023
// phase 0). The checkpoint sidecar recorded the prompt, but no transcript user
// message carries its text — the input never entered the conversation.
type UnfiledInput struct {
	unfiled.Entry
	// AnchorID is the ID of the message currently sitting at the anchor
	// position, so a frontend can insert the recovered card immediately before
	// it. Empty when the anchor falls past the end of the live transcript.
	AnchorID string
}

// UnfiledInputs reconciles the session's checkpoint prompts against the live
// transcript's user messages. Read-only: it reads the checkpoint list and a
// session snapshot, and writes nothing anywhere. Session load/switch and
// startup surface it so a recovered input is visible without any action from
// the user.
func (c *Controller) UnfiledInputs() []UnfiledInput {
	if c == nil || c.executor == nil {
		return nil
	}
	metas := c.Checkpoints()
	if len(metas) == 0 {
		return nil
	}
	history := c.executor.Session().Snapshot()
	texts := make([]string, 0, len(history))
	for _, m := range history {
		if m.Role == provider.RoleUser {
			texts = append(texts, m.Content)
		}
	}
	inputs := make([]unfiled.Input, 0, len(metas))
	for _, m := range metas {
		inputs = append(inputs, unfiled.Input{Turn: m.Turn, Time: m.Time, Prompt: m.Prompt, MsgIndex: m.MsgIndex})
	}
	entries := unfiled.Reconcile(inputs, texts)
	out := make([]UnfiledInput, 0, len(entries))
	for _, e := range entries {
		anchor := ""
		if e.AnchorIndex >= 0 && e.AnchorIndex < len(history) {
			anchor = history[e.AnchorIndex].ID
		}
		out = append(out, UnfiledInput{Entry: e, AnchorID: anchor})
	}
	return out
}
