// Package sentinel implements the fixed bottom-line protections of task 410
// (Sentinel 降维版): the hard-forbidden rules and the outbound secret scan.
//
// 设计定位（设计提案 rev2 §三「降维版」）：审批体系（ask/yolo/autopilot）判定
// 「单次动作允不允许」，本包补的是行为底线——「即使 yolo/放行模式下也不允许的
// 操作」。两条底线都零模型参与（纯规则 + 确定性的 git 管道命令），不调用任何
// LLM；zero_model_test.go 在源码层面钉住这一约束。
//
//	1. 硬禁区清单（hard-forbidden）：默认开。保护性机制不是功能，铁律 2。
//	2. 出口 secret 扫描（exit-scan）：默认关（实验开关起步）。外发内容在
//	   离开本机前做 secret 形态扫描，命中即阻断本次操作。
//
// 两者都由 agent 执行链的固定前置检查调用（internal/agent/execute_one.go，
// 在 Auto Guard、MCP 信任快路径与普通审批门之前），因此没有任何审批分支
// 能把它们绕过。
package sentinel

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"sync/atomic"
)

// Verdict is the outcome of the fixed bottom-line check for one tool call.
type Verdict struct {
	// Blocked reports whether the call must not run.
	Blocked bool
	// Rule is the stable rule id that fired ("" when allowed). Audit records
	// and the model-facing reason both carry it, so 拦截记录可查（who/when/
	// which rule）里的 which rule 落在这一字段。
	Rule string
	// Reason is the model-facing explanation. Every block names the boundary
	// and an alternative path (拒绝 + 说明原因 + 给替代路径), e.g. force
	// push → 存 patch/推分支，明早人工确认。
	Reason string
}

// Runtime toggles, set once by the composition root (internal/boot) from the
// user-global [sentinel] config section. Package globals are safe here because
// [sentinel] cannot be overridden per-project (same scoping as [secrets]):
// every concurrent workspace in one process shares the same user setting.
var (
	hardForbiddenEnabled atomic.Bool // default handled by boot (default on)
	exitScanEnabled      atomic.Bool // default off: experimental switch

	protectedBranches atomic.Value // []string
	disabledRules     atomic.Value // []string
	protectedPaths    atomic.Value // []string (absolute, slash-normalized)

	auditPath atomic.Value // string; "" disables the JSONL audit file
)

func init() {
	protectedBranches.Store(defaultProtectedBranches())
	disabledRules.Store([]string(nil))
	protectedPaths.Store([]string(nil))
	auditPath.Store("")
	// Defaults mirror the composition root so direct users of the package
	// (tests, tooling) get the documented posture without boot.
	hardForbiddenEnabled.Store(true)
	exitScanEnabled.Store(false)
}

// defaultProtectedBranches is the v1 protected-branch list. Users extend it
// via [sentinel] protected_branches (config), not by editing this table.
func defaultProtectedBranches() []string {
	return []string{"main", "master", "main-v2-stable"}
}

// SetHardForbidden enables or disables the hard-forbidden rules. The
// composition root passes "config absent => enabled" (nil pointer default on).
func SetHardForbidden(enabled bool) { hardForbiddenEnabled.Store(enabled) }

// SetExitScan enables or disables the outbound secret scan (experimental).
func SetExitScan(enabled bool) { exitScanEnabled.Store(enabled) }

// SetProtectedBranches replaces the force-push protected branch list. Empty
// input restores the built-in defaults.
func SetProtectedBranches(branches []string) {
	cleaned := make([]string, 0, len(branches))
	for _, b := range branches {
		if b = strings.ToLower(strings.TrimSpace(b)); b != "" {
			cleaned = append(cleaned, b)
		}
	}
	if len(cleaned) == 0 {
		cleaned = defaultProtectedBranches()
	}
	protectedBranches.Store(cleaned)
}

// SetDisabledRules opts out of individual hard-forbidden rules by id. Unknown
// ids are ignored silently here; the config layer may warn.
func SetDisabledRules(rules []string) {
	cleaned := make([]string, 0, len(rules))
	for _, r := range rules {
		if r = strings.TrimSpace(r); r != "" {
			cleaned = append(cleaned, strings.ToLower(r))
		}
	}
	disabledRules.Store(cleaned)
}

// SetProtectedPaths registers the absolute system-level paths (Reasonix home,
// user-global config files, the credential store) the system-config and
// critical-deletion rules guard. Empty input clears the list.
func SetProtectedPaths(paths []string) {
	cleaned := make([]string, 0, len(paths))
	for _, p := range paths {
		if p = normalizePath(p); p != "" {
			cleaned = append(cleaned, p)
		}
	}
	protectedPaths.Store(cleaned)
}

// SetAuditPath sets the JSONL file interception records append to. "" disables
// file audit (slog lines still fire).
func SetAuditPath(path string) { auditPath.Store(normalizePath(path)) }

func hardForbidden() bool   { return hardForbiddenEnabled.Load() }
func exitScan() bool        { return exitScanEnabled.Load() }
func auditFile() string     { return auditPath.Load().(string) }
func ruleDisabled(id string) bool {
	for _, r := range disabledRules.Load().([]string) {
		if r == id {
			return true
		}
	}
	return false
}

func protectedBranchList() []string { return protectedBranches.Load().([]string) }
func protectedPathList() []string   { return protectedPaths.Load().([]string) }

// CheckToolCall is the fixed pre-check the agent consults before every other
// approval stage. toolName is the canonical tool name ("bash", "write_file",
// "web_fetch", ...); args is the raw call JSON; workspaceRoot is the session
// workspace (used only by the git plumbing of the exit scan; "" skips it).
//
// The hard-forbidden rules are pure functions over (toolName, args): no I/O,
// no model. The exit scan additionally runs deterministic git plumbing in
// workspaceRoot for git commit/push and fails open when git is unavailable —
// the scan is an experimental switch, so its own unavailability must not
// wedge ordinary work (扫描不可用不阻断，硬禁区规则始终纯参数可判).
func CheckToolCall(toolName string, args json.RawMessage, workspaceRoot string) Verdict {
	if toolName == "" || len(args) == 0 {
		return Verdict{}
	}
	subject := commandSubject(toolName, args)
	if subject == "" {
		return Verdict{}
	}
	if hardForbidden() {
		if v := checkForbidden(toolName, subject); v.Blocked && !ruleDisabled(v.Rule) {
			recordIntercept(toolName, workspaceRoot, v.Rule, subject)
			return v
		}
	}
	if exitScan() {
		if v := checkOutbound(toolName, args, subject, workspaceRoot); v.Blocked {
			recordIntercept(toolName, workspaceRoot, v.Rule, subject)
			return v
		}
	}
	return Verdict{}
}

// commandSubject extracts the human-readable command text of a call for rule
// matching and audit. For bash it is the command line; for path tools the
// primary path; for web tools the URL/query. Empty when the tool exposes no
// matchable text.
func commandSubject(toolName string, args json.RawMessage) string {
	var m struct {
		Command  string `json:"command"`
		URL      string `json:"url"`
		Query    string `json:"query"`
		FilePath string `json:"file_path"`
		Path     string `json:"path"`
		Source   string `json:"source_path"`
		Dest     string `json:"destination_path"`
	}
	if err := json.Unmarshal(args, &m); err != nil {
		return ""
	}
	switch {
	case strings.TrimSpace(m.Command) != "":
		return m.Command
	case strings.TrimSpace(m.URL) != "":
		return m.URL
	case strings.TrimSpace(m.Query) != "":
		return m.Query
	case strings.TrimSpace(m.FilePath) != "":
		return m.FilePath
	case strings.TrimSpace(m.Path) != "":
		return m.Path
	case strings.TrimSpace(m.Source) != "" || strings.TrimSpace(m.Dest) != "":
		return strings.TrimSpace(m.Source + " " + m.Dest)
	default:
		return ""
	}
}

// normalizePath canonicalizes a path for comparisons: forward slashes,
// lowercase drive letters, no trailing separator. Empty stays empty.
func normalizePath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	p = filepath.ToSlash(filepath.Clean(p))
	return p
}
