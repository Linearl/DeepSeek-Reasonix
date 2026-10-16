package main

// 任务 909（验收自动化一期）：切换模型耗时读数——fast path 的
// "desktop: model switch" 行必须带 elapsed_ms（task 148 的 effort 侧
// 同款；模型侧此前只有 path/model，装机后无法回答「切换模型花了多久」，
// 验收清单里该项只能靠人肉感受）。本测钉读数存在且为非负整数，
// 让 benchmarks/experience 的 switch-cost 门有稳定的数据源契约。
//
// 纯观测断言：不设 magnitude 门（快慢归 bench 阈值管），只钉「读数会落行」。

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestModelSwitchFastPathLogsElapsedMs: same-provider fast path 切换后，
// 日志行必须携带 elapsed_ms——bench 侧解析器（experience collect）以
// elapsed_ms 为样本字段，缺字段=样本静默丢失=门假 N/A。
func TestModelSwitchFastPathLogsElapsedMs(t *testing.T) {
	modelFastPathTestConfig(t)
	buf := captureSlog(t)
	app, _, _, _, _ := activeTabApp(t, provAStubProvider{}, savedConfigResolver(t))

	if err := app.SetModelForTab("test", "prov-a/model-a2"); err != nil {
		t.Fatalf("fast path switch failed: %v", err)
	}

	var line string
	for _, l := range strings.Split(buf.String(), "\n") {
		if strings.Contains(l, "desktop: model switch") && strings.Contains(l, "fast-per-request") {
			line = l
			break
		}
	}
	if line == "" {
		t.Fatalf("no fast-per-request model switch line emitted:\n%s", buf.String())
	}
	// JSON handler 形态：字段以 "elapsed_ms":N 出现；文本 handler 形态：
	// elapsed_ms=N。两种都认。
	ms, ok := extractElapsedMS(line)
	if !ok {
		t.Fatalf("model switch line lacks elapsed_ms: %s", line)
	}
	if ms < 0 {
		t.Fatalf("elapsed_ms = %d, must be non-negative", ms)
	}
}

// TestModelSwitchRepeatedFastPathKeepsElapsedMs: 第二次热切换（tab.model
// 已变、override 重挂）同样落带读数的行——读数不是首切专属。
func TestModelSwitchRepeatedFastPathKeepsElapsedMs(t *testing.T) {
	modelFastPathTestConfig(t)
	buf := captureSlog(t)
	app, _, _, _, _ := activeTabApp(t, provAStubProvider{}, savedConfigResolver(t))

	if err := app.SetModelForTab("test", "prov-a/model-a1"); err != nil {
		t.Fatalf("warm switch failed: %v", err)
	}
	buf.Reset()
	if err := app.SetModelForTab("test", "prov-a/model-a2"); err != nil {
		t.Fatalf("second fast path switch failed: %v", err)
	}
	var line string
	for _, l := range strings.Split(buf.String(), "\n") {
		if strings.Contains(l, "desktop: model switch") {
			line = l
			break
		}
	}
	if line == "" {
		t.Fatalf("no model switch line on second hop:\n%s", buf.String())
	}
	if _, ok := extractElapsedMS(line); !ok {
		t.Fatalf("second-hop model switch line lacks elapsed_ms: %s", line)
	}
}

// extractElapsedMS 从一行 slog（Text 或 JSON handler 形态）取出 elapsed_ms。
func extractElapsedMS(line string) (int64, bool) {
	// JSON 形态："elapsed_ms":123
	if i := strings.Index(line, `"elapsed_ms":`); i >= 0 {
		rest := line[i+len(`"elapsed_ms":`):]
		end := 0
		for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
			end++
		}
		if end == 0 {
			return 0, false
		}
		var v int64
		if err := json.Unmarshal([]byte(rest[:end]), &v); err == nil {
			return v, true
		}
		return 0, false
	}
	// Text 形态：elapsed_ms=123
	if i := strings.Index(line, "elapsed_ms="); i >= 0 {
		rest := line[i+len("elapsed_ms="):]
		end := 0
		for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
			end++
		}
		if end == 0 {
			return 0, false
		}
		var v int64
		if err := json.Unmarshal([]byte(rest[:end]), &v); err == nil {
			return v, true
		}
	}
	return 0, false
}
