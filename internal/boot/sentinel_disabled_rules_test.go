package boot

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/config"
	"reasonix/internal/event"
	"reasonix/internal/i18n"
)

// tl1400 前缀说明：[sentinel].disabled_rules 的会话 Notice 按进程去重——
// 同一集合（与顺序、重复项、大小写无关）只在首次装配发一条禁用告知；
// 集合变化（增/删 id）按新集合重发；非空→空发「已恢复」正向通知；空→空
// 静默。装配期 slog WARN 每次非空装配都发，不受去重影响。
//
// 本文件所有用例在开头与 Cleanup 各调一次 resetSentinelDisabledRulesForTest
// 清零进程级告知记录，保证测试顺序无关、go test -race -count=2 复跑成立。

const sentinelDisabledNoticeText = "Sentinel 硬规则已被配置禁用。"
const sentinelDisabledWarnMsg = "disabled_rules opts out of hard-forbidden rules"

// TestBuildWarnsAndNoticesWhenSentinelDisabledRulesSet pins task 896 ①
// (852 调研建议 #2) plus the tl1400 restored state: a non-empty [sentinel]
// disabled_rules must never be silent — assembly emits exactly one slog WARN
// and one in-session notice naming the disabled rules. When the list is
// removed again, the previously announced rules are announced as restored
// (positive notice) instead of silently going quiet.
//
// [sentinel] is user-global (project reasonix.toml values are discarded), so
// the probe config lands in the isolated user config, not the workspace.
func TestBuildWarnsAndNoticesWhenSentinelDisabledRulesSet(t *testing.T) {
	resetSentinelDisabledRulesForTest()
	t.Cleanup(resetSentinelDisabledRulesForTest)

	home := isolateConfigHome(t)
	t.Setenv("REASONIX_HOME", filepath.Join(home, "reasonix-home"))
	userCfg := config.UserConfigPath()
	if err := writeUserConfigWithSentinel(userCfg, `
default_model = "test-model"

[sentinel]
disabled_rules = ["force_push_protected", "exfil_credentials"]

[[providers]]
name = "test-model"
kind = "openai"
base_url = "https://example.invalid"
model = "x"
api_key_env = "REASONIX_TEST_KEY_UNSET"
`); err != nil {
		t.Fatalf("write user config: %v", err)
	}
	project := robustTempDir(t)
	writeFile(t, project, "reasonix.toml", "")

	var logBuf bytes.Buffer
	prevLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(prevLogger) })

	var notices []event.Event
	sink := event.FuncSink(func(e event.Event) {
		if e.Kind == event.Notice {
			notices = append(notices, e)
		}
	})
	build := func() {
		t.Helper()
		ctrl, err := Build(context.Background(), Options{Sink: sink, WorkspaceRoot: project})
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		ctrl.Close()
	}

	// Non-empty disabled_rules: exactly one WARN and one notice, both naming
	// the disabled rules — no silent bottom-line shutdown.
	build()
	if got := strings.Count(logBuf.String(), sentinelDisabledWarnMsg); got != 1 {
		t.Fatalf("WARN count = %d, want 1; log:\n%s", got, logBuf.String())
	}
	matched := 0
	for _, n := range notices {
		if n.Text != sentinelDisabledNoticeText {
			continue
		}
		matched++
		if n.Level != event.LevelWarn {
			t.Errorf("notice level = %v, want warn", n.Level)
		}
		if !strings.Contains(n.Detail, "force_push_protected") || !strings.Contains(n.Detail, "exfil_credentials") {
			t.Errorf("notice must name every disabled rule, got detail: %s", n.Detail)
		}
	}
	if matched != 1 {
		t.Fatalf("disabled-rules notice count = %d, want 1; notices: %+v", matched, notices)
	}

	// Removed disabled_rules: no WARN and no disabled notice — but because a
	// non-empty set was announced before (tl1400), exactly one restored notice
	// names every previously disabled rule at info level.
	if err := writeUserConfigWithSentinel(userCfg, `
default_model = "test-model"

[[providers]]
name = "test-model"
kind = "openai"
base_url = "https://example.invalid"
model = "x"
api_key_env = "REASONIX_TEST_KEY_UNSET"
`); err != nil {
		t.Fatalf("rewrite user config: %v", err)
	}
	logBuf.Reset()
	notices = nil
	build()
	if got := strings.Count(logBuf.String(), sentinelDisabledWarnMsg); got != 0 {
		t.Fatalf("default config must not WARN, got %d; log:\n%s", got, logBuf.String())
	}
	for _, n := range notices {
		if n.Text == sentinelDisabledNoticeText {
			t.Fatalf("default config must not notify the disabled set again, got: %+v", n)
		}
	}
	restored := 0
	for _, n := range notices {
		if n.Text != i18n.M.SentinelDisabledRulesRestored {
			continue
		}
		restored++
		if n.Level != event.LevelInfo {
			t.Errorf("restored notice level = %v, want info", n.Level)
		}
		if !strings.Contains(n.Detail, "force_push_protected") || !strings.Contains(n.Detail, "exfil_credentials") {
			t.Errorf("restored notice must name every previously disabled rule, got detail: %s", n.Detail)
		}
	}
	if restored != 1 {
		t.Fatalf("restored notice count = %d, want 1; notices: %+v", restored, notices)
	}
}

// TestSentinelDisabledRulesNoticeDedupPerProcess pins tl1400: the session
// Notice for a given disabled_rules set fires once per process — the same
// set (reordered, duplicated, case-variant) stays silent while the assembly
// slog WARN keeps firing every build; a changed set re-announces; emptying
// the set announces restoration exactly once; re-disabling announces again.
func TestSentinelDisabledRulesNoticeDedupPerProcess(t *testing.T) {
	resetSentinelDisabledRulesForTest()
	t.Cleanup(resetSentinelDisabledRulesForTest)

	home := isolateConfigHome(t)
	t.Setenv("REASONIX_HOME", filepath.Join(home, "reasonix-home"))
	userCfg := config.UserConfigPath()
	project := robustTempDir(t)
	writeFile(t, project, "reasonix.toml", "")

	var logBuf bytes.Buffer
	prevLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(prevLogger) })

	var notices []event.Event
	sink := event.FuncSink(func(e event.Event) {
		if e.Kind == event.Notice {
			notices = append(notices, e)
		}
	})
	build := func(sentinelSection string) {
		t.Helper()
		body := `
default_model = "test-model"
` + sentinelSection + `
[[providers]]
name = "test-model"
kind = "openai"
base_url = "https://example.invalid"
model = "x"
api_key_env = "REASONIX_TEST_KEY_UNSET"
`
		if err := writeUserConfigWithSentinel(userCfg, body); err != nil {
			t.Fatalf("write user config: %v", err)
		}
		ctrl, err := Build(context.Background(), Options{Sink: sink, WorkspaceRoot: project})
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		ctrl.Close()
	}
	countText := func(text string) int {
		n := 0
		for _, e := range notices {
			if e.Text == text {
				n++
			}
		}
		return n
	}
	warnCount := func() int { return strings.Count(logBuf.String(), sentinelDisabledWarnMsg) }

	const (
		setA         = "\n[sentinel]\ndisabled_rules = [\"force_push_protected\", \"exfil_credentials\"]\n"
		setAReordDup = "\n[sentinel]\ndisabled_rules = [\"exfil_credentials\", \"force_push_protected\", \"exfil_credentials\"]\n"
		setACase     = "\n[sentinel]\ndisabled_rules = [\"Force_Push_Protected\", \"EXFIL_CREDENTIALS\"]\n"
		setB         = "\n[sentinel]\ndisabled_rules = [\"force_push_protected\", \"exfil_credentials\", \"write_system_config\"]\n"
		setEmpty     = ""
	)

	// 首次装配：禁用告知与 WARN 同发。
	build(setA)
	if got := countText(sentinelDisabledNoticeText); got != 1 {
		t.Fatalf("first assembly: disabled notice count = %d, want 1", got)
	}
	if got := warnCount(); got != 1 {
		t.Fatalf("first assembly: WARN count = %d, want 1; log:\n%s", got, logBuf.String())
	}

	// 同集合（顺序不同+重复项/大小写不同）：Notice 静默，WARN 照发——
	// 去重只作用于用户可见的会话 Notice，日志可检索性不受影响。
	build(setAReordDup)
	if got := countText(sentinelDisabledNoticeText); got != 1 {
		t.Fatalf("reordered/duplicated set must stay silent, disabled notice count = %d, want 1", got)
	}
	if got := warnCount(); got != 2 {
		t.Fatalf("reordered set must still WARN once per assembly, WARN count = %d, want 2", got)
	}
	build(setACase)
	if got := countText(sentinelDisabledNoticeText); got != 1 {
		t.Fatalf("case-variant set must stay silent, disabled notice count = %d, want 1", got)
	}
	if got := warnCount(); got != 3 {
		t.Fatalf("case-variant set must still WARN, WARN count = %d, want 3", got)
	}

	// 集合变化（增 id）：按新集合重发禁用告知。
	build(setB)
	if got := countText(sentinelDisabledNoticeText); got != 2 {
		t.Fatalf("changed set must re-announce, disabled notice count = %d, want 2", got)
	}
	lastDisabled := ""
	for _, n := range notices {
		if n.Text == sentinelDisabledNoticeText {
			lastDisabled = n.Detail
		}
	}
	for _, id := range []string{"force_push_protected", "exfil_credentials", "write_system_config"} {
		if !strings.Contains(lastDisabled, id) {
			t.Errorf("re-announced notice must reflect the new set, want %q in detail: %s", id, lastDisabled)
		}
	}
	if got := warnCount(); got != 4 {
		t.Fatalf("changed set must WARN, WARN count = %d, want 4", got)
	}

	// 非空→空：发一条「已恢复」（info 级），点名此前禁用的全部 id；空集合不再 WARN。
	build(setEmpty)
	if got := countText(i18n.M.SentinelDisabledRulesRestored); got != 1 {
		t.Fatalf("non-empty→empty must announce restoration, restored notice count = %d, want 1; notices: %+v", got, notices)
	}
	for _, n := range notices {
		if n.Text != i18n.M.SentinelDisabledRulesRestored {
			continue
		}
		if n.Level != event.LevelInfo {
			t.Errorf("restored notice level = %v, want info", n.Level)
		}
		for _, id := range []string{"force_push_protected", "exfil_credentials", "write_system_config"} {
			if !strings.Contains(n.Detail, id) {
				t.Errorf("restored notice must name previously disabled rule %q, got detail: %s", id, n.Detail)
			}
		}
	}
	if got := warnCount(); got != 4 {
		t.Fatalf("empty set must not WARN, WARN count = %d, want 4", got)
	}

	// 空→空：恢复通知也不重复。
	build(setEmpty)
	if got := countText(i18n.M.SentinelDisabledRulesRestored); got != 1 {
		t.Fatalf("empty→empty must stay silent, restored notice count = %d, want 1", got)
	}
	if got := countText(sentinelDisabledNoticeText); got != 2 {
		t.Fatalf("empty→empty must not re-announce the disabled set, count = %d, want 2", got)
	}

	// 恢复后再次禁用：集合变化，重新告知。
	build(setA)
	if got := countText(sentinelDisabledNoticeText); got != 3 {
		t.Fatalf("re-disabling after restore must re-announce, disabled notice count = %d, want 3", got)
	}
	if got := warnCount(); got != 5 {
		t.Fatalf("re-disabling must WARN, WARN count = %d, want 5", got)
	}
}

// writeUserConfigWithSentinel writes the isolated user config file, creating
// its parent directory first (the path lives under the per-test temp home).
func writeUserConfigWithSentinel(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}
