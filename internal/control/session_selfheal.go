package control

import (
	"fmt"
	"log/slog"
	"strings"

	"reasonix/internal/agent"
	"reasonix/internal/event"
)

// 任务 1011：打开会话的内建自动检测 + 自动自愈。
//
// 边界（与 1010 的纯防护、1011 文件级自愈互补）：
//   - 检测层（1010 通知 + agent.InspectSessionHealth）永远只读；
//   - 活会话的自愈动作只有两个：head 重选（SwitchBranch，可从版本面板撤销）
//     和投影饮食（剥图 + 折叠，Rewrite+SaveRewriteCompact 的 CAS 通路）；
//   - 活会话绝不做补水物化（外部重写 .jsonl 会和活 Session 的保存基线互相
//     打架）——水位差对 UI 无影响（加载优先 events），补水留给闭合会话的
//     SelfHealSession 或手动修复入口；
//   - 开关关闭（gate 注入 nil 或返回 false）= 现状：只提示，不改任何数据。

// sessionSelfHealGate 由宿主（desktop）注入：读取实验开关。nil 或返回 false
// 时自愈关闭，全部路径退化为 1010 的只读提示。
var sessionSelfHealGate func() bool

// SetSessionSelfHealGate wires the self-heal switch. Pass nil to disable.
func SetSessionSelfHealGate(gate func() bool) {
	sessionSelfHealGate = gate
}

func sessionSelfHealActive() bool {
	return sessionSelfHealGate != nil && sessionSelfHealGate()
}

// selfHealAfterResume is the open-conversation heal's hook on the Resume
// path (every open and tab switch lands here). The head-level switch already
// happened inside recoverInterruptedTurn's emitHeadEvents; this runs the
// remaining criteria (projection diet) in the background so the repair never
// delays the first paint, and emits its own notice when it acts.
func (c *Controller) selfHealAfterResume(path string) {
	if !sessionSelfHealActive() || path == "" {
		return
	}
	go func() {
		out := c.SelfHealOpenSession(0)
		if len(out.Actions) > 0 {
			slog.Info("selfheal: open-session repair done", "path", path, "actions", strings.Join(out.Actions, "+"), "backup", out.BackupDir)
		}
	}()
}

// SelfHealOpenOutcome reports what the open-session heal did, for the host to
// surface (desktop notice / repair log panel).
type SelfHealOpenOutcome struct {
	SwitchedHeadFrom string   `json:"switched_head_from,omitempty"`
	SwitchedHeadTo   string   `json:"switched_head_to,omitempty"`
	Dieted           bool     `json:"dieted,omitempty"`
	BackupDir        string   `json:"backup_dir,omitempty"`
	Actions          []string `json:"actions,omitempty"`
	Healthy          bool     `json:"healthy"`
	Detail           string   `json:"detail,omitempty"`
}

// SelfHealOpenSession inspects the open conversation and repairs what the
// five criteria flag, within the live-session action set: promote a strictly
// dominated selected head to the longer, newer chain, and diet an
// over-window projection (strip images, fold old messages). A healthy session
// returns without touching anything.
//
// windowTokens is the destination model's context window; <=0 uses the agent
// default. Call it after the controller is ready (executor bound), off the
// request path.
func (c *Controller) SelfHealOpenSession(windowTokens int) SelfHealOpenOutcome {
	out := SelfHealOpenOutcome{}
	if !sessionSelfHealActive() {
		out.Detail = "selfheal_disabled"
		return out
	}
	if c.executor == nil {
		out.Detail = "no_executor"
		return out
	}
	s := c.executor.Session()
	if s == nil {
		out.Detail = "no_session"
		return out
	}
	path := c.SessionPath()
	if path == "" {
		out.Detail = "no_path"
		return out
	}
	if _, dag := s.Head(); !dag {
		out.Detail = "not_schema2"
		return out
	}

	report := agent.InspectSessionHealth(path, agent.HealthInspectOptions{WindowTokens: windowTokens})
	if report.Healthy {
		out.Healthy = true
		return out
	}

	// 判据 head_lag：切到「更长且更新」链。SwitchBranch 是版本面板同一条
	// 通路：旧链保留为版本，切换天然可撤销。
	if report.SuggestedHeadID != "" && report.SuggestedHeadID != report.SelectedHeadID {
		if _, err := c.SwitchBranch(report.SuggestedHeadID); err != nil {
			slog.Warn("selfheal: head switch failed", "path", path, "to", report.SuggestedHeadID, "err", err)
			out.Detail = "switch_failed:" + err.Error()
		} else {
			out.SwitchedHeadFrom = report.SelectedHeadID
			out.SwitchedHeadTo = report.SuggestedHeadID
			out.Actions = append(out.Actions, agent.HealActionPromoteHead)
		}
	}

	// 判据 projection_overflow / compact_fail_streak：投影饮食。先备份整
	// 目录（与文件级自愈同一备份形态），再走 CAS 重写。
	if needsDiet(report.Issues) {
		s = c.executor.Session() // SwitchBranch may have replaced the transcript
		if s != nil {
			backupDir, err := agent.BackupSessionFiles(path)
			if err != nil {
				slog.Warn("selfheal: backup before diet failed", "path", path, "err", err)
				out.Detail = strings.TrimPrefix(out.Detail+"&", "&") + "backup_failed"
			} else {
				out.BackupDir = backupDir
				folded, actions, changed := agent.DietSessionMessages(s.Snapshot(), windowTokens)
				if changed {
					s.Rewrite(folded, "selfheal_diet")
					if err := s.SaveRewriteCompact(path); err != nil {
						slog.Warn("selfheal: diet rewrite failed", "path", path, "err", err)
						out.Detail = strings.TrimPrefix(out.Detail+"&", "&") + "diet_failed"
					} else {
						out.Dieted = true
						for _, a := range actions {
							if a.Status == "done" {
								out.Actions = append(out.Actions, a.Action)
							}
						}
					}
				}
			}
		}
	}

	if len(out.Actions) > 0 {
		out.Healthy = true
		detail := fmt.Sprintf("actions=%s", strings.Join(out.Actions, "+"))
		if out.BackupDir != "" {
			detail += " backup=" + out.BackupDir
		}
		notice := sessionRecoveryNotice(event.NoticeCodeSessionSelfHealed,
			"this conversation was repaired automatically and is usable again; the previous version is kept and reversible from View versions")
		notice.Detail = detail
		c.sink.Emit(notice)
		agent.WriteSelfHealRepairRecord(path, out.BackupDir, out.Actions, issueCodesOf(report))
	}
	return out
}

func needsDiet(issues []agent.SessionHealthIssue) bool {
	for _, issue := range issues {
		if issue.Code == agent.HealthProjectionOverflow || issue.Code == agent.HealthCompactFailStreak {
			return true
		}
	}
	return false
}

func issueCodesOf(report agent.SessionHealthReport) string {
	codes := make([]string, 0, len(report.Issues))
	for _, issue := range report.Issues {
		codes = append(codes, issue.Code)
	}
	return strings.Join(codes, ",")
}
