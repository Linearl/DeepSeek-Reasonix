package main

import (
	"os"
	"strings"
	"testing"
	"time"
)

// tl1725 手改自愈 + UI 整表写校验对齐的验收钉。
//
// 背景（20261028 实害）：手工编辑 heartbeat-tasks.json 绕过工具/UI 两条写
// 路径的全部校验，一个 enabled 任务写了非法 interval 后调度器永不运行
// （heartbeatTaskDueAt 对解析失败的 interval 一律返回 false），而界面看着
// 正常。本文件钉死加载侧契约：
//   - 坏值加载时被修复/禁用（sanitizeHeartbeatTasks，读咽喉 readConfigSnapshot）
//   - 修复通过 configHealthErr → panel 常驻横幅可见，WARN 每个 digest 一次
//   - panel 整表保存对 enabled 任务执行与 agent 工具同款校验
//     （validateHeartbeatPanelSave），disabled 行放行以便自愈落盘
//   - 合法配置加载零改动零告警（合法路径零回退）

const heartbeatHandEditInvalidInterval = `{
  "schemaVersion": 4,
  "revision": 7,
  "tasks": [
    {"id": "bad-interval", "title": "坏interval任务", "prompt": "p", "interval": "invalid-interval", "enabled": true, "createdAt": 1700000000000},
    {"id": "good", "title": "正常任务", "prompt": "p", "interval": "30m", "enabled": true, "createdAt": 1700000000000}
  ]
}`

// TestHeartbeatSanitizeInvalidIntervalDisablesOnReload 是 20261028 实害的
// end-to-end 钉：手写坏 interval JSON → 加载 → WARN 可见 + 内存禁用 +
// 任务仍可见 + 磁盘文件原样（加载路径无写副作用）。
func TestHeartbeatSanitizeInvalidIntervalDisablesOnReload(t *testing.T) {
	isolateDesktopUserDirs(t)
	engine := newHeartbeatEngine(nil)
	writeHeartbeatConfigFile(t, engine, heartbeatHandEditInvalidInterval)

	view := engine.ReloadConfig()
	if len(view.Tasks) != 2 {
		t.Fatalf("tasks=%d, want 2 (sanitized rows stay visible, nothing silently dropped)", len(view.Tasks))
	}
	bad := view.Tasks[0]
	if bad.ID != "bad-interval" {
		t.Fatalf("first task id=%q, want the damaged row", bad.ID)
	}
	if bad.Enabled {
		t.Fatal("damaged row still enabled in memory — the load must turn the fake-runnable state into an honest disable")
	}
	if view.Tasks[1].ID != "good" || !view.Tasks[1].Enabled {
		t.Fatalf("healthy row altered: %+v", view.Tasks[1])
	}
	// 可见性：横幅点名任务与字段，但不阻塞保存（ETag 正常，与 823② 降级视图区分）。
	if view.ConfigError == "" {
		t.Fatal("ConfigError empty after sanitizing a damaged hand edit")
	}
	if !strings.Contains(view.ConfigError, "bad-interval") || !strings.Contains(view.ConfigError, "interval") {
		t.Fatalf("ConfigError=%q, want it to name the task and the field", view.ConfigError)
	}
	if view.ETag == "" {
		t.Fatal("sanitized view must keep its ETag — the panel must be able to save the repair, unlike the 823② degraded view")
	}
	// 内存态行为正确：坏 interval 任务投影为 disabled 而非 invalid-interval。
	if _, hint := nextHeartbeatRunAt(view.Tasks[0], time.Now()); hint != "disabled" {
		t.Fatalf("next-run hint=%q, want \"disabled\"", hint)
	}
	// 磁盘原样：加载路径不带写副作用。
	raw, err := os.ReadFile(engine.configPath())
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != heartbeatHandEditInvalidInterval {
		t.Fatal("load-time sanitize must not rewrite the file on disk")
	}
	engine.mu.Lock()
	health := engine.configHealthErr
	engine.mu.Unlock()
	if health == "" {
		t.Fatal("engine.configHealthErr empty after sanitized load")
	}
}

// TestHeartbeatSanitizeAgentListSeesDisabledHint 钉 agent 列表视角：ListTasks
// 是加载路径之一，自愈后 agent 看到的是修复后的行（hint=disabled），
// 而不是 20261028 实害里 enabled+invalid-interval 的假象。
func TestHeartbeatSanitizeAgentListSeesDisabledHint(t *testing.T) {
	a, engine := newTestAdapter(t)
	writeHeartbeatConfigFile(t, engine, heartbeatHandEditInvalidInterval)

	view := mustList(t, a)
	if len(view.Tasks) != 2 {
		t.Fatalf("agent list tasks=%d, want 2", len(view.Tasks))
	}
	if view.Tasks[0].Enabled {
		t.Fatal("agent list shows the damaged row enabled; load-time sanitize did not apply")
	}
	if view.Tasks[0].NextRunHint != "disabled" {
		t.Fatalf("agent NextRunHint=%q, want \"disabled\"", view.Tasks[0].NextRunHint)
	}
}

// TestHeartbeatSanitizeFieldRepairsNormalizeSafeDefaults 钉有安全默认值的字段：
// 加载归一化回默认 + 记 note，任务保持 enabled（字段级修复不禁用）。
func TestHeartbeatSanitizeFieldRepairsNormalizeSafeDefaults(t *testing.T) {
	isolateDesktopUserDirs(t)
	engine := newHeartbeatEngine(nil)
	writeHeartbeatConfigFile(t, engine, `{
  "schemaVersion": 4,
  "tasks": [
    {"id": "fixable", "title": "可修复", "prompt": "p", "interval": "1h", "enabled": true,
     "scope": "bogus", "approvalMode": "superuser", "timeWindowStart": "25:99", "timeWindowEnd": "abc"}
  ]
}`)

	view := engine.ReloadConfig()
	if len(view.Tasks) != 1 {
		t.Fatalf("tasks=%d, want 1", len(view.Tasks))
	}
	got := view.Tasks[0]
	if !got.Enabled {
		t.Fatal("field-level repairs must not disable the task")
	}
	if got.Scope != "" {
		t.Fatalf("scope=%q, want normalized to global (empty)", got.Scope)
	}
	if got.ApprovalMode != "yolo" {
		t.Fatalf("approvalMode=%q, want \"yolo\"", got.ApprovalMode)
	}
	if got.TimeWindowStart != "" || got.TimeWindowEnd != "" {
		t.Fatalf("time windows (%q, %q) not cleared; invalid clock strings have no meaning",
			got.TimeWindowStart, got.TimeWindowEnd)
	}
	if view.ConfigError == "" {
		t.Fatal("ConfigError empty although three fields were repaired")
	}
}

// TestHeartbeatSanitizeDisableWithoutSafeDefault 钉无安全默认值的字段：
// title/prompt 空、project 缺 workspaceRoot、maxRuns 负数 → 内存禁用 + note。
func TestHeartbeatSanitizeDisableWithoutSafeDefault(t *testing.T) {
	cases := []struct {
		name     string
		taskJSON string
		wantNote string
	}{
		{"empty title", `{"id":"t","title":"","prompt":"p","interval":"1h","enabled":true}`, "empty title"},
		{"empty prompt", `{"id":"t","title":"x","prompt":"  ","interval":"1h","enabled":true}`, "empty prompt"},
		{"project without root", `{"id":"t","title":"x","prompt":"p","interval":"1h","enabled":true,"scope":"project"}`, "workspaceRoot"},
		{"negative maxRuns", `{"id":"t","title":"x","prompt":"p","interval":"1h","enabled":true,"maxRuns":-2}`, "maxRuns"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolateDesktopUserDirs(t)
			engine := newHeartbeatEngine(nil)
			writeHeartbeatConfigFile(t, engine,
				`{"schemaVersion": 4, "tasks": [`+tc.taskJSON+`]}`)

			view := engine.ReloadConfig()
			if len(view.Tasks) != 1 || view.Tasks[0].Enabled {
				t.Fatalf("task not disabled in memory: %+v", view.Tasks)
			}
			if !strings.Contains(view.ConfigError, tc.wantNote) {
				t.Fatalf("ConfigError=%q, want it to mention %q", view.ConfigError, tc.wantNote)
			}
		})
	}
}

// TestHeartbeatSanitizeDropsEmptyIDRow 钉 id 空的废行：加载剔除 + note。
func TestHeartbeatSanitizeDropsEmptyIDRow(t *testing.T) {
	isolateDesktopUserDirs(t)
	engine := newHeartbeatEngine(nil)
	writeHeartbeatConfigFile(t, engine, `{
  "schemaVersion": 4,
  "tasks": [
    {"id": "", "title": "废行", "prompt": "p", "interval": "1h", "enabled": true},
    {"id": "kept", "title": "保留", "prompt": "p", "interval": "1h", "enabled": true}
  ]
}`)

	view := engine.ReloadConfig()
	if len(view.Tasks) != 1 || view.Tasks[0].ID != "kept" {
		t.Fatalf("tasks=%+v, want only the addressable row", view.Tasks)
	}
	if !strings.Contains(view.ConfigError, "empty id") {
		t.Fatalf("ConfigError=%q, want it to report the dropped row", view.ConfigError)
	}
}

// TestHeartbeatSanitizeDisabledRowKeepsNotes 钉 disabled 行的坏值：无行为实害，
// 不重复禁用动作，但仍记 note + 横幅（用户该知道它为什么一直没跑）。
func TestHeartbeatSanitizeDisabledRowKeepsNotes(t *testing.T) {
	isolateDesktopUserDirs(t)
	engine := newHeartbeatEngine(nil)
	writeHeartbeatConfigFile(t, engine,
		`{"schemaVersion": 4, "tasks": [{"id":"t","title":"x","prompt":"p","interval":"nope","enabled":false}]}`)

	view := engine.ReloadConfig()
	if len(view.Tasks) != 1 || view.Tasks[0].Enabled {
		t.Fatalf("tasks=%+v, want the row untouched and still disabled", view.Tasks)
	}
	if view.ConfigError == "" || !strings.Contains(view.ConfigError, "interval") {
		t.Fatalf("ConfigError=%q, want the interval note even for a disabled row", view.ConfigError)
	}
}

// TestHeartbeatSanitizeWarnOncePerDigest 钉 WARN 节流：同一 digest 只
// slog.Warn 一次（返回值即是否发出），digest 变化（又一次手改）再发。
func TestHeartbeatSanitizeWarnOncePerDigest(t *testing.T) {
	isolateDesktopUserDirs(t)
	engine := newHeartbeatEngine(nil)
	writeHeartbeatConfigFile(t, engine, heartbeatHandEditInvalidInterval)

	engine.mu.Lock()
	snap1, err := engine.readConfigSnapshot()
	if err != nil {
		engine.mu.Unlock()
		t.Fatal(err)
	}
	first := engine.noteSanitizedConfigLocked(snap1)
	second := engine.noteSanitizedConfigLocked(snap1)
	engine.mu.Unlock()
	if !first {
		t.Fatal("first sanitized load must WARN")
	}
	if second {
		t.Fatal("same digest must not WARN twice (log storm guard)")
	}

	// 又一次手改 → 新 digest → 再 WARN。
	writeHeartbeatConfigFile(t, engine, heartbeatHandEditInvalidInterval+" ")
	engine.mu.Lock()
	snap2, err := engine.readConfigSnapshot()
	if err != nil {
		engine.mu.Unlock()
		t.Fatal(err)
	}
	third := engine.noteSanitizedConfigLocked(snap2)
	engine.mu.Unlock()
	if !third {
		t.Fatal("a changed digest (new hand edit) must WARN again")
	}
}

// TestHeartbeatSanitizeClearsAfterFix 钉修复闭环：修好 JSON 后下一轮加载
// 横幅清空、任务恢复原样。
func TestHeartbeatSanitizeClearsAfterFix(t *testing.T) {
	isolateDesktopUserDirs(t)
	engine := newHeartbeatEngine(nil)
	writeHeartbeatConfigFile(t, engine, heartbeatHandEditInvalidInterval)
	if view := engine.ReloadConfig(); view.ConfigError == "" {
		t.Fatal("precondition failed: damaged config did not surface the banner")
	}

	fixed := strings.Replace(heartbeatHandEditInvalidInterval, `"invalid-interval"`, `"45m"`, 1)
	writeHeartbeatConfigFile(t, engine, fixed)
	view := engine.ReloadConfig()
	if view.ConfigError != "" {
		t.Fatalf("ConfigError=%q after the fix, want empty", view.ConfigError)
	}
	if len(view.Tasks) != 2 || !view.Tasks[0].Enabled {
		t.Fatalf("tasks=%+v after the fix, want both rows enabled", view.Tasks)
	}
}

// TestHeartbeatPanelSaveRejectsEnabledInvalidTask 钉 UI 整表写校验（C1）：
// enabled 坏值任务的整表保存被两个口（ReplaceTasks / ReplaceConfig）拒绝，
// 错误点名任务与字段，磁盘不变——与 agent 工具同款校验。
func TestHeartbeatPanelSaveRejectsEnabledInvalidTask(t *testing.T) {
	isolateDesktopUserDirs(t)
	engine := newHeartbeatEngine(nil)
	if err := engine.saveTasks([]HeartbeatTask{
		{ID: "ok", Title: "正常", Prompt: "p", Interval: "30m", Enabled: true},
	}); err != nil {
		t.Fatal(err)
	}
	base := mustList(t, heartbeatManagerAdapter{engine: engine})

	// ReplaceTasks 口。
	bad := append([]HeartbeatTask(nil), engine.ListTasks()...)
	bad[0].Interval = "invalid-interval"
	if err := engine.ReplaceTasks(bad); err == nil {
		t.Fatal("ReplaceTasks accepted an enabled task with a broken interval")
	} else if !strings.Contains(err.Error(), "invalid-interval") || !strings.Contains(err.Error(), "ok") {
		t.Fatalf("ReplaceTasks error=%v, want it to name the task and the bad value", err)
	}

	// ReplaceConfig 口（带 revision/ETag 的前端保存链路）。
	bad2 := append([]HeartbeatTask(nil), engine.ListTasks()...)
	bad2[0].Prompt = ""
	if _, err := engine.ReplaceConfig(HeartbeatConfigUpdate{
		Revision: base.Revision, ETag: base.ETag, Tasks: bad2,
	}); err == nil {
		t.Fatal("ReplaceConfig accepted an enabled task with an empty prompt")
	} else if !strings.Contains(err.Error(), "prompt") {
		t.Fatalf("ReplaceConfig error=%v, want it to name the field", err)
	}

	// 磁盘不变：两次被拒的保存都没有落盘。
	got := mustList(t, heartbeatManagerAdapter{engine: engine})
	if got.Revision != base.Revision || len(got.Tasks) != 1 || got.Tasks[0].Interval != "30m" {
		t.Fatalf("disk changed after rejected saves: revision=%d tasks=%+v", got.Revision, got.Tasks)
	}
}

// TestHeartbeatPanelSavePersistsSanitizedDisabledRow 钉 C2：disabled 坏值行
// 放行整表保存——这是自愈从内存落到磁盘的通道（panel 一次保存即固化修复）。
func TestHeartbeatPanelSavePersistsSanitizedDisabledRow(t *testing.T) {
	isolateDesktopUserDirs(t)
	engine := newHeartbeatEngine(nil)
	writeHeartbeatConfigFile(t, engine, heartbeatHandEditInvalidInterval)

	view := engine.ReloadConfig()
	if view.Tasks[0].Enabled {
		t.Fatal("precondition failed: damaged row should have been sanitized to disabled")
	}

	// panel 原样保存（用户改的是别的行）：disabled 坏值行随整表落盘。
	if _, err := engine.ReplaceConfig(HeartbeatConfigUpdate{
		Revision: view.Revision, ETag: view.ETag, Tasks: view.Tasks,
	}); err != nil {
		t.Fatalf("panel save with a sanitized disabled row failed: %v", err)
	}

	// 自愈已固化：磁盘上该行现在是 disabled（假运行状态消失）。interval
	// 本身的坏值仍在（自愈不猜语义、不动格式），所以新加载仍报 interval
	// note——诚实地指向用户唯一剩余动作：把 interval 改对。
	fresh := newHeartbeatEngine(nil)
	reloaded := fresh.ReloadConfig()
	if len(reloaded.Tasks) != 2 || reloaded.Tasks[0].Enabled {
		t.Fatalf("persisted state=%+v, want the damaged row disabled on disk", reloaded.Tasks)
	}
	if reloaded.ConfigError == "" || !strings.Contains(reloaded.ConfigError, "interval") {
		t.Fatalf("ConfigError=%q after persisting the disable, want the residual interval note", reloaded.ConfigError)
	}

	// 用户随后把 interval 改对（模拟手改修好）→ 下一轮加载零告警闭环。
	repaired := strings.Replace(heartbeatHandEditInvalidInterval, `"invalid-interval"`, `"45m"`, 1)
	writeHeartbeatConfigFile(t, fresh, repaired)
	final := fresh.ReloadConfig()
	if final.ConfigError != "" {
		t.Fatalf("ConfigError=%q after the user fixed the interval, want empty", final.ConfigError)
	}
}

// TestHeartbeatSanitizeCleanConfigUntouched 钉 A10：合法配置加载零 note、
// 零告警、零改动（合法路径零回退的直证）。
func TestHeartbeatSanitizeCleanConfigUntouched(t *testing.T) {
	isolateDesktopUserDirs(t)
	engine := newHeartbeatEngine(nil)
	writeHeartbeatConfigFile(t, engine,
		`{"schemaVersion": 4, "revision": 3, "tasks": [{"id":"t","title":"x","prompt":"p","interval":"0 9 * * 1-5","enabled":true,"approvalMode":"ask","scope":"global","timeWindowStart":"09:00","timeWindowEnd":"17:00","maxRuns":3}]}`)

	engine.mu.Lock()
	snap, err := engine.readConfigSnapshot()
	if err != nil {
		engine.mu.Unlock()
		t.Fatal(err)
	}
	warned := engine.noteSanitizedConfigLocked(snap)
	engine.mu.Unlock()
	if len(snap.sanitizeNotes) != 0 {
		t.Fatalf("clean config produced notes: %v", snap.sanitizeNotes)
	}
	if warned {
		t.Fatal("clean config must not WARN")
	}

	view := engine.ReloadConfig()
	if view.ConfigError != "" {
		t.Fatalf("ConfigError=%q on a clean config, want empty", view.ConfigError)
	}
	if len(view.Tasks) != 1 || !view.Tasks[0].Enabled ||
		view.Tasks[0].TimeWindowStart != "09:00" || view.Tasks[0].ApprovalMode != "ask" {
		t.Fatalf("clean task altered: %+v", view.Tasks)
	}
}
