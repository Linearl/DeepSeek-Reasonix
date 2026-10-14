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
)

// TestBuildWarnsAndNoticesWhenSentinelDisabledRulesSet pins task 896 ①
// (852 调研建议 #2): a non-empty [sentinel] disabled_rules must never be
// silent — assembly emits exactly one slog WARN and one in-session notice
// naming the disabled rules; an absent list emits neither.
//
// [sentinel] is user-global (project reasonix.toml values are discarded), so
// the probe config lands in the isolated user config, not the workspace.
func TestBuildWarnsAndNoticesWhenSentinelDisabledRulesSet(t *testing.T) {
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

	const warnMsg = "disabled_rules opts out of hard-forbidden rules"
	const noticeText = "Sentinel 硬规则已被配置禁用。"

	// Non-empty disabled_rules: exactly one WARN and one notice, both naming
	// the disabled rules — no silent bottom-line shutdown.
	build()
	if got := strings.Count(logBuf.String(), warnMsg); got != 1 {
		t.Fatalf("WARN count = %d, want 1; log:\n%s", got, logBuf.String())
	}
	matched := 0
	for _, n := range notices {
		if n.Text != noticeText {
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

	// Absent disabled_rules: neither signal — no noise on a default config.
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
	if got := strings.Count(logBuf.String(), warnMsg); got != 0 {
		t.Fatalf("default config must not WARN, got %d; log:\n%s", got, logBuf.String())
	}
	for _, n := range notices {
		if n.Text == noticeText {
			t.Fatalf("default config must not notify, got: %+v", n)
		}
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
