package control

import (
	"fmt"
	"log/slog"
	"strings"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/provider"
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
	// tl1280: the schema-2 gate and the head_lag promote left with the
	// write-path slow lane; every session is flat now, so the diet criterion
	// below is the only live action.
	//
	// tl1506（用户终裁 20261109）：超窗判据有实账按实账判——活 executor 持有
	// 任务 1183 的实账锚时注入；无实账（冷启动首轮前/锚不可信）判据自动退回
	// 计价口径估算（agent 侧三段选择器，见 healthProjectionCriterionTokens）。
	opts := agent.HealthInspectOptions{WindowTokens: windowTokens}
	if actual, ok := c.executor.TrustedObservedContextTokens(); ok {
		opts.ActualContextTokens = actual
	}
	report := agent.InspectSessionHealth(path, opts)
	if report.Healthy {
		out.Healthy = true
		return out
	}

	// 判据 projection_overflow / compact_fail_streak：投影饮食。先备份整
	// 目录（与文件级自愈同一备份形态），再走 CAS 重写。
	if needsDiet(report.Issues) {
		s = c.executor.Session()
		if s != nil {
			backupDir, err := agent.BackupSessionFiles(path)
			if err != nil {
				// 协调线 1556 终裁：备份失败是真异常（不是「跳过备份继续修复」
				// 的合法降级）——升 ERROR 并上报 GUI notice，不许静默。事故教训：
				// 新9 静默失败拖 5 小时。
				slog.Error("selfheal: backup before diet failed; refusing to diet without a backup",
					"path", path, "err", err)
				out.Detail = strings.TrimPrefix(out.Detail+"&", "&") + "backup_failed"
				c.sink.Emit(sessionRecoveryNotice(event.NoticeCodeSessionSelfHealed,
					"repair could not back up this conversation's files first, so nothing was changed; check disk space and try again"))
			} else {
				out.BackupDir = backupDir
				// tl1352 C+：预检只判 changed；折叠改在 RewriteAndSaveCompact
				// 的保存锁窗内对落笔时刻的完整转录重算，与前台 checkpoint/
				// autosave 序列化到同一条保存路径（消除「内存已折叠、盘上旧
				// 字节」窗口里的同进程 baseline 竞态 → recovery 副本 fork）。
				// tl1506：预检判据不变（计价口径估算）；实账触发但估算门判
				// 「无需」时在 else 分支留痕，不静默 no-op。
				_, actions, changed := agent.DietSessionMessages(s.Snapshot(), windowTokens)
				if changed {
					if err := s.RewriteAndSaveCompact(path, "selfheal_diet", func(complete []provider.Message) []provider.Message {
						f, acts, ch := agent.DietSessionMessages(complete, windowTokens)
						actions = acts
						if !ch {
							return complete
						}
						return f
					}); err != nil {
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
				} else if report.TokenSource == agent.HealthTokenSourceUsageActual {
					// tl1506：实账判超窗、计价估算判未超（两口径在估算器残差
					// 带内可能分叉）。不静默 no-op——留痕说明判定来源分叉，
					// 下次打开（无实账段或实账变化）会按当次口径重判。
					out.Detail = strings.TrimPrefix(out.Detail+"&", "&") + "diet_skipped_estimate_below_line"
					slog.Info("selfheal: actual usage flagged overflow but pricing estimate stays below the diet line; nothing rewritten",
						"path", path, "actual_tokens", report.ProjectionTokens, "window_tokens", windowTokens)
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
