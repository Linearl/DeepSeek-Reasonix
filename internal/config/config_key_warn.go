package config

import (
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/BurntSushi/toml"
)

// deprecatedConfigKeys lists dotted TOML keys that once existed in config.toml
// but are retired: they either no longer decode (removed from the struct) or
// decode only so old files keep loading and are normalized away afterwards
// (see normalizeRetiredAutoPlan and the other legacy normalizers). Each entry
// maps the key to a short hint about where the behavior went.
//
// The list is sensing only: warnUnknownConfigKeys surfaces these keys at warn
// level so a leftover is visible instead of silently ignored. Loading never
// blocks and never rewrites a file because of it.
var deprecatedConfigKeys = map[string]string{
	"agent.auto_plan":              "automatic plan mode was removed; planning now starts explicitly (e.g. Shift+Tab)",
	"agent.auto_plan_classifier":   "the automatic plan classifier was removed together with auto_plan",
	"agent.max_steps":              "retired step limits; the adaptive progress policy replaced them",
	"agent.planner_max_steps":      "retired step limits; the adaptive progress policy replaced them",
	"agent.soft_compact_ratio":     "retired multi-threshold compaction key; agent.compact_ratio is the single knob",
	"agent.tool_result_snip_ratio": "retired multi-threshold compaction key; agent.compact_ratio is the single knob",
	"agent.compact_force_ratio":    "retired multi-threshold compaction key; agent.compact_ratio is the single knob",
	"agent.cold_resume_prune":      "retired key; stale tool results are no longer elided through a config switch",
	"agent.context_editing":        "retired key; native tool clearing is no longer an auto path",
	// tl1487 四组：OpenCode Go 用量「不提供开关、始终打开」——用量查询不再
	// 有门，本键退役（agent 段 task-163 键）。
	"agent.experimental_opencode_go_usage": "retired key (tl1487): the OpenCode Go usage query is now always on — the switch was removed",
	// tl1487 七组③：路径规则（task-134）整卡移除——过时技术，生产沿用
	// confine + allow_write + 写入审批；机制存档 docs/archive/。
	"desktop.experimental_path_rules": "retired key (tl1487): the task-134 path-scope rules card was removed (never wired; confine + allow_write + approval unchanged)",
	// tl1741 实验室瘦身（用户 20261121 三拍）：8 项 stable 卡撤开关——稳定档
	// 不再提供开关，功能常开（非写死 true：false 分支与 config 读取项一并清）。
	// fullAccess 保留（外部目录访问控制有真实需求，手写 false 仍生效）；
	// session_storage 是 916 特例：键保留（宽容往返防替升级用户做降级决定），
	// 只撤实验室 UI 行，不在此登记。
	"desktop.autopilot":                          "retired key (tl1741): new desktop sessions always default to autopilot (requires autopilot_max_runtime); per-session autopilot state is unaffected",
	"agent.experimental_session_collab":          "retired key (tl1741): multi-session collaboration is now always on — the switch was removed",
	"desktop.experimental_session_collab":        "retired key (tl1741): [desktop] mirror of the collaboration switch; the [agent] key retired with it",
	"desktop.experimental_collab_hub":            "retired key (tl1741): the collaboration-hub entry is now always shown — the switch was removed",
	"desktop.experimental_workflow_panel":        "retired key (tl1741): the workflow panel is now always available — the switch was removed",
	"desktop.experimental_workflow_templates":    "retired key (tl1741): workflow templates are now always available — the switch was removed",
	"desktop.experimental_cdp_debug_port":        "retired key (tl1741): the WebView2 CDP debug endpoint (loopback-only random port) now starts unconditionally",
	"agent.experimental_runtime_reuse":           "retired key (tl1741): the runtime assembly reuse pool is now always on — the switch was removed",
	"desktop.experimental_runtime_reuse":         "retired key (tl1741): [desktop] mirror of the runtime-reuse switch; the [agent] key retired with it",
	// tl1796 r2（装机实测打回）：一期 opt-in 键随撤开关退役——composer 模式条
	// autopilot 档恒显（用户口径「任何时候都应该出现」），存量手写键
	// （true/false 皆然）由本清单承接：WARN 可见不阻断，fixed-key-set 渲染
	// 下次保存清行。
	"desktop.experimental_autopilot_composer": "retired key (tl1796 r2): the composer mode bar always offers the autopilot tier — the opt-in switch was removed",
	// tl1855（用户 20261127 实验室清理令件①）：启动遮罩开关退役——遮罩恒显。
	"desktop.experimental_startup_splash": "retired key (tl1855): the DSH-style startup splash is now always shown — the switch was removed",
}

// unknownKeyWarned tracks which unrecognized keys this process has already
// warned about, so a long-lived desktop process re-loading the same config
// file thousands of times (settings reads, heartbeat ticks, session rebuilds)
// emits one line per key instead of one line per load. tl 1113: the raw
// per-load WARN was 209,197 of 403,038 lines (52.5%) in 25 rotated desktop
// logs — a single leftover key in the user's config.toml, re-warned on every
// decode.
//
// Only the unrecognized-key branch deduplicates: deprecated keys keep their
// per-load WARN because each carries a migration hint the user should still
// see, and there are at most a handful of them.
var unknownKeyWarned sync.Map // key string -> struct{}

// resetUnknownKeyWarnDedupeForTest clears the process-level dedupe so tests
// can assert first-report and repeat-load behaviour independently.
func resetUnknownKeyWarnDedupeForTest() {
	unknownKeyWarned = sync.Map{}
}

// warnUnknownConfigKeys reports retired and unrecognized keys found in one
// config TOML file at warn level. It is diagnostic only: it never fails the
// load and never mutates the file, so a misspelled key, a leftover from an
// older release, or a key newly added upstream becomes visible without any
// behavioral change for configs that only use known keys.
func warnUnknownConfigKeys(meta toml.MetaData, path string) {
	if len(meta.Keys()) == 0 {
		return
	}
	deprecatedSeen := make(map[string]bool, len(deprecatedConfigKeys))
	for _, key := range slices.Sorted(maps.Keys(deprecatedConfigKeys)) {
		if !meta.IsDefined(strings.Split(key, ".")...) {
			continue
		}
		slog.Warn("config: deprecated key ignored (no longer read)", "path", path, "key", key, "hint", deprecatedConfigKeys[key])
		deprecatedSeen[key] = true
	}
	reportedUnknown := make(map[string]bool)
	for _, key := range meta.Undecoded() {
		name := key.String()
		if reportedUnknown[name] {
			continue
		}
		reportedUnknown[name] = true
		if hint, ok := deprecatedConfigKeys[name]; ok {
			slog.Warn("config: deprecated key ignored (no longer read)", "path", path, "key", name, "hint", hint)
			continue
		}
		if _, loaded := unknownKeyWarned.LoadOrStore(name, struct{}{}); loaded {
			continue
		}
		slog.Warn("config: unknown key ignored (typo, leftover, or key added by a newer version?)", "path", path, "key", name)
	}
}
