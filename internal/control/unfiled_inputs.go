package control

import (
	"reasonix/internal/agent"
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
//
// tl1372 判则①主修：对账时查 repairlog 取最近一次完成的投影折叠时间；checkpoint
// 早于该时间的 turn 归「已折叠存档」桶——原文在档后被折叠占位替换，属误报，
// 不进入返回列表（横幅零报入）。无 repairlog 或无完成折叠记录时行为与原口径
// 完全一致。
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
	opts := unfiled.ClassifyOptions{}
	if latestFold, ok := agent.LatestFoldRepairTime(c.SessionPath()); ok {
		opts.LatestFold = latestFold
	}
	gaps := unfiled.ReconcileGaps(inputs, texts, opts)
	out := make([]UnfiledInput, 0, len(gaps))
	for _, g := range gaps {
		// 已折叠存档桶免报：原文进过对话、被折叠归档，不是缺口。
		if g.Class == unfiled.GapFoldedArchive {
			continue
		}
		anchor := ""
		if g.AnchorIndex >= 0 && g.AnchorIndex < len(history) {
			anchor = history[g.AnchorIndex].ID
		}
		out = append(out, UnfiledInput{Entry: g.Entry, AnchorID: anchor})
	}
	return out
}
