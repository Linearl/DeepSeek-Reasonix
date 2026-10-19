package main

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
)

// 任务 1011：桌面宿主的自愈接线与健康面板后端。
//
//   - 启动时注入 control.SetSessionSelfHealGate（开关每次现读，设置页翻转
//     即时生效，无需重启）；
//   - 启动后台扫描最近会话（检测路 2）：只读健康检查，异常会话进缓存并发
//     runtime 事件给前端徽标；闭合会话绝不自动改数据——修复发生在用户打开
//     该会话（控制层）或设置页面板手动触发；
//   - 需求 E：设置页「工作阶段版本与副本」面板的 v3 heads 分区——列出多
//     head 会话的 head 读数（kind/messages/turns/last_activity/preview、
//     当前选中与「更长且更新」建议），选择/切换复用 chooseSessionHead 的
//     既有通路，操作前预览复用链预览。

// sessionHealthScanStartupDelay 让启动扫描避开进程最忙的首屏窗口。
const sessionHealthScanStartupDelay = 20 * time.Second

// sessionHealthScanFreshWindow 只扫最近活动的会话：更旧的会话等用户自己
// 打开时再查（打开路径全覆盖），启动扫描不为冰柜里的历史付重放成本。
const sessionHealthScanFreshWindow = 48 * time.Hour

// sessionHealthScanMaxSessions 是一轮扫描的重放上限——最坏情况下的磁盘
// 成本封顶，超出部分留给下一轮/打开路径。
const sessionHealthScanMaxSessions = 40

// SessionHealthFindingView is one flagged conversation for the GUI badge and
// the health panel.
type SessionHealthFindingView struct {
	Path        string   `json:"path"`
	Label       string   `json:"label"`
	Codes       []string `json:"codes"`
	Detail      string   `json:"detail"`
	Healed      bool     `json:"healed,omitempty"`
	SuggestHead string   `json:"suggest_head,omitempty"`
}

// sessionHealthFindings 是启动扫描的最近一轮结果（按 path 去重）。
var sessionHealthFindings = struct {
	sync.Mutex
	byPath map[string]SessionHealthFindingView
	round  int64
}{byPath: map[string]SessionHealthFindingView{}}

// startupStartSessionHealthScan arms the startup sweep (检测路 2): one
// delayed, capped, strictly read-only pass over the recent sessions. Findings
// never mutate session data — the repair happens on open (control layer) or
// from the panel button.
func (a *App) startupStartSessionHealthScan() {
	go func() {
		time.Sleep(sessionHealthScanStartupDelay)
		if !a.sessionSelfHealEnabled() {
			return
		}
		findings := a.scanRecentSessionHealth()
		sessionHealthFindings.Lock()
		sessionHealthFindings.byPath = findings
		sessionHealthFindings.round++
		sessionHealthFindings.Unlock()
		if len(findings) > 0 {
			a.emitRuntimeEvent("session-health:findings-v1", map[string]any{
				"count": len(findings), "round": sessionHealthFindings.round,
			})
			slog.Info("session health: startup scan finished", "findings", len(findings))
		}
	}()
}

// scanRecentSessionHealth replays the recent sessions' logs read-only and
// flags the five-criteria failures. Replay cost is capped; a session that
// fails to replay simply doesn't appear (the open path will report it).
func (a *App) scanRecentSessionHealth() map[string]SessionHealthFindingView {
	type recent struct {
		path      string
		updatedAt time.Time
	}
	var candidates []recent
	seen := map[string]struct{}{}
	for _, dir := range recoveryScanDirectories() {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") || strings.HasSuffix(e.Name(), ".events.jsonl") || strings.HasSuffix(e.Name(), ".repairlog.jsonl") {
				continue
			}
			path := filepath.Join(dir, e.Name())
			if _, dup := seen[path]; dup {
				continue
			}
			seen[path] = struct{}{}
			// meta 先行：UpdatedAt 在窗口外的不值得一次重放。
			if meta, ok, err := agent.LoadBranchMeta(path); err == nil && ok {
				if time.Since(meta.UpdatedAt) > sessionHealthScanFreshWindow {
					continue
				}
			}
			info, err := e.Info()
			if err != nil || time.Since(info.ModTime()) > sessionHealthScanFreshWindow {
				continue
			}
			candidates = append(candidates, recent{path: path, updatedAt: info.ModTime()})
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].updatedAt.After(candidates[j].updatedAt) })
	if len(candidates) > sessionHealthScanMaxSessions {
		candidates = candidates[:sessionHealthScanMaxSessions]
	}
	findings := map[string]SessionHealthFindingView{}
	for _, c := range candidates {
		report := agent.InspectSessionHealth(c.path, agent.HealthInspectOptions{})
		if report.Healthy {
			continue
		}
		view := SessionHealthFindingView{
			Path:  c.path,
			Label: recoveryDisplayName(c.path),
		}
		for _, issue := range report.Issues {
			view.Codes = append(view.Codes, issue.Code)
			view.Detail += issue.Code + "{" + issue.Detail + "} "
		}
		view.SuggestHead = report.SuggestedHeadID
		findings[c.path] = view
	}
	return findings
}

// ListSessionHealthFindings returns the latest startup-scan findings.
func (a *App) ListSessionHealthFindings() []SessionHealthFindingView {
	sessionHealthFindings.Lock()
	defer sessionHealthFindings.Unlock()
	out := make([]SessionHealthFindingView, 0, len(sessionHealthFindings.byPath))
	for _, v := range sessionHealthFindings.byPath {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// InspectSessionHealthFor is the GUI's read-only check entry: the repair
// panel runs it first and shows the readings before any repair button lights
// up (误检防线：先读数，后动作，全程零写入).
func (a *App) InspectSessionHealthFor(path string) (agent.SessionHealthReport, error) {
	path, err := a.resolveSessionPathForHeal(path)
	if err != nil {
		return agent.SessionHealthReport{}, err
	}
	return agent.InspectSessionHealth(path, agent.HealthInspectOptions{WindowTokens: a.selfHealWindowTokens()}), nil
}

// RepairSessionFor is the GUI's manual repair entry (修复面板的执行按钮 and
// the doctor-style debugging helper both land here). With the switch off it
// runs the check only and reports selfheal_disabled without touching data —
// the acceptance's off posture (只提示不改数据).
func (a *App) RepairSessionFor(path string, promoteHead, hydrate, diet bool) (agent.SelfHealReport, error) {
	path, err := a.resolveSessionPathForHeal(path)
	if err != nil {
		return agent.SelfHealReport{}, err
	}
	if !a.sessionSelfHealEnabled() {
		report := agent.InspectSessionHealth(path, agent.HealthInspectOptions{WindowTokens: a.selfHealWindowTokens()})
		return agent.SelfHealReport{Path: path, BeforeAfter: report.Issues, Healthy: report.Healthy}, nil
	}
	report, err := agent.SelfHealSession(path, agent.SelfHealOptions{
		PromoteHead: promoteHead, Hydrate: hydrate, Diet: diet,
		WindowTokens: a.selfHealWindowTokens(),
	})
	if err == nil && report.Repaired {
		a.emitProjectTreeChangedForSessionDirs(filepath.Dir(path))
	}
	return report, err
}

// ListSessionRepairRecordsFor exposes the repair log to the health panel.
func (a *App) ListSessionRepairRecordsFor(path string) ([]agent.RepairRecord, error) {
	path, err := a.resolveSessionPathForHeal(path)
	if err != nil {
		return nil, err
	}
	return agent.ListSessionRepairRecordsFor(path)
}

// SessionVersionGroupView is one multi-head conversation for the 需求 E
// panel section: the v3 events-log heads with their readings.
type SessionVersionGroupView struct {
	MainPath  string              `json:"mainPath"`
	MainLabel string              `json:"mainLabel"`
	Directory string              `json:"directory"`
	HeadCount int                 `json:"headCount"`
	Selected  string              `json:"selected"`
	Suggest   string              `json:"suggest,omitempty"`
	Heads     []agent.SessionHead `json:"heads"`
}

// ListSessionVersionGroups lists conversations whose v3 events log holds more
// than one live head, with the "更长且更新" suggestion marked. The listing
// itself stays cheap: meta sidecars carry HeadCount, so a full replay happens
// only for the groups the user actually expands (ListRecoveryChains does the
// per-conversation replay and the preview dialog rides the existing chain
// preview).
func (a *App) ListSessionVersionGroups() ([]SessionVersionGroupView, error) {
	groups := []SessionVersionGroupView{}
	for _, dir := range recoveryScanDirectories() {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") || strings.HasSuffix(e.Name(), ".events.jsonl") || strings.HasSuffix(e.Name(), ".repairlog.jsonl") {
				continue
			}
			path := filepath.Join(dir, e.Name())
			meta, ok, err := agent.LoadBranchMeta(path)
			if err != nil || !ok || meta.HeadCount <= 1 {
				continue
			}
			groups = append(groups, SessionVersionGroupView{
				MainPath:  path,
				MainLabel: recoveryDisplayName(path),
				Directory: dir,
				HeadCount: meta.HeadCount,
				Selected:  meta.HeadID,
			})
		}
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].MainLabel < groups[j].MainLabel })
	return groups, nil
}

// sessionSelfHealEnabled reads the experimental switch live (nil = on).
func (a *App) sessionSelfHealEnabled() bool {
	cfg, err := config.LoadForRootReadOnly(a.activeWorkspaceRoot())
	if err != nil {
		return true
	}
	return cfg.SessionSelfHealEnabled()
}

// selfHealWindowTokens returns the window the overflow judgement runs
// against. The agent default (1M) matches the field incident; a per-model
// window rides the provider config and stays a follow-up.
func (a *App) selfHealWindowTokens() int {
	return 0
}

// errSelfHealPathInvalid refuses paths outside the known session directories:
// the heal writes only where sessions live.
var errSelfHealPathInvalid = errors.New("session path is outside the known session directories")

// resolveSessionPathForHeal validates the path the GUI handed over against
// the known session directories — the heal must never write outside them.
func (a *App) resolveSessionPathForHeal(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errSelfHealPathInvalid
	}
	if _, _, err := a.sessionDirForPath(path); err == nil {
		return path, nil
	}
	if resolved, ok := resolveTranscriptSibling(path); ok {
		if _, _, err := a.sessionDirForPath(resolved); err == nil {
			return resolved, nil
		}
	}
	return "", errSelfHealPathInvalid
}
