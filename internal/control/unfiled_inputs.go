package control

import (
	"os"
	"path/filepath"

	"reasonix/internal/agent"
	"reasonix/internal/provider"
	"reasonix/internal/unfiled"
)

// unfiledRecoveryBucketEnabled gates the tl1372 A2 experiment (判则①补充，默认关):
// REASONIX_EXPERIMENT_TL1372_RECOVERY_BUCKET=1 turns on the dual-bucket
// classification that separates CAS-refused turns (text recoverable from a
// recovery fork) from never-filed gaps. Default off keeps the single-bucket
// status quo — no new classification surface until opted in.
var unfiledRecoveryBucketEnabled = os.Getenv("REASONIX_EXPERIMENT_TL1372_RECOVERY_BUCKET") == "1"

// UnfiledInput is one recovered prompt plus its transcript anchor (task 1023
// phase 0). The checkpoint sidecar recorded the prompt, but no transcript user
// message carries its text — the input never entered the conversation.
type UnfiledInput struct {
	unfiled.Entry
	// Class is the tl1372 gap classification (GapUnfiled / GapPendingRecovery;
	// GapFoldedArchive never reaches this list — those are exempt). With the
	// A2 experiment off every entry is GapUnfiled.
	Class unfiled.GapClass
	// AnchorID is the ID of the message currently sitting at the anchor
	// position, so a frontend can insert the recovered card immediately before
	// it. Empty when the anchor falls past the end of the live transcript.
	AnchorID string
}

// recoveryForkUserTexts reads the user-message texts of every recovery fork
// sharing the session's topic root (agent.RecoveryTopicRootID), strictly
// read-only. tl1372 A2 evidence: recovery forks hold exactly the stale
// runtime's unsaved changes — text found there was consumed by a runtime
// whose transcript saves were CAS-refused, i.e. "pending recovery", not
// "never entered". Fork count is bounded by SnapshotForkQuotaPerTopic; load
// failures degrade to no evidence (conservative: the turn stays a plain gap).
func recoveryForkUserTexts(sessionPath string) []string {
	if sessionPath == "" {
		return nil
	}
	root := agent.RecoveryTopicRootID(sessionPath)
	if root == "" {
		return nil
	}
	dir := filepath.Dir(sessionPath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		sibling := filepath.Join(dir, e.Name())
		if sibling == sessionPath || !agent.LooksLikeRecoveryFilename(sibling) {
			continue
		}
		if agent.RecoveryTopicRootID(sibling) != root {
			continue
		}
		users, err := agent.LoadSessionUserMessages(sibling)
		if err != nil {
			continue
		}
		for _, u := range users {
			out = append(out, u.Message.Content)
		}
	}
	return out
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
//
// tl1372 判则①补充（A2，实验开关默认关）：开关开启时扫描同根 recovery 副本，
// 原文在其中出现的 turn 归「待恢复」桶（CAS 拒档形态）而非「未进入对话」。
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
	if unfiledRecoveryBucketEnabled {
		// tl1372 A2 实验开关（默认关）：双桶分类——CAS 拒档（原文在 recovery
		// 副本中）的 turn 归「待恢复」，不再与「未进入对话」冤案混桶。
		opts.AllowPendingRecovery = true
		opts.RecoveredTexts = recoveryForkUserTexts(c.SessionPath())
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
		out = append(out, UnfiledInput{Entry: g.Entry, Class: g.Class, AnchorID: anchor})
	}
	return out
}
