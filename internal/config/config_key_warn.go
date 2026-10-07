package config

import (
	"log/slog"
	"maps"
	"slices"
	"strings"

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
		slog.Warn("config: unknown key ignored (typo, leftover, or key added by a newer version?)", "path", path, "key", name)
	}
}
