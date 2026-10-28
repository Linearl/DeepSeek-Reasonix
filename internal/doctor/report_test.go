package doctor

import (
	"encoding/json"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/config"
)

func TestRedactHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)        // os.UserHomeDir on unix
	t.Setenv("USERPROFILE", home) // os.UserHomeDir on windows
	sep := string(os.PathSeparator)

	if got := redactHome(home); got != "~" {
		t.Fatalf("home itself: got %q, want ~", got)
	}
	under := filepath.Join(home, "projects", "x")
	if got, want := redactHome(under), "~"+sep+"projects"+sep+"x"; got != want {
		t.Fatalf("under home: got %q, want %q", got, want)
	}
	outside := filepath.Join(t.TempDir(), "elsewhere") // sibling temp, not under home
	if got := redactHome(outside); got != outside {
		t.Fatalf("outside home must be unchanged: got %q", got)
	}
	if got := redactHome(""); got != "" {
		t.Fatalf("empty must stay empty: got %q", got)
	}
}

func TestCollectReportRedactsSecrets(t *testing.T) {
	t.Setenv("REASONIX_TEST_SECRET", "sk-live-secret")

	cfg := config.Default()
	cfg.DefaultModel = "custom"
	cfg.Providers = []config.ProviderEntry{{
		Name:      "custom",
		Kind:      "openai",
		BaseURL:   "https://api.example.com/v1?token=secret-query",
		Model:     "model-a",
		APIKeyEnv: "REASONIX_TEST_SECRET",
	}}
	cfg.Plugins = []config.PluginEntry{{
		Name:    "remote",
		Type:    "http",
		URL:     "https://mcp.example.com/path?api_key=secret-query",
		Headers: map[string]string{"Authorization": "Bearer sk-live-secret"},
	}}
	cfg.Network = config.NetworkConfig{
		ProxyMode: "custom",
		Proxy: config.NetworkProxyConfig{
			Type:     "socks5",
			Server:   "proxy.example.com",
			Port:     1080,
			Username: "proxy-user",
			Password: "proxy-secret",
		},
	}

	report := Collect(Options{Version: "test-version", Config: cfg})
	text := RenderText(report)
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	combined := text + "\n" + string(raw)

	for _, secret := range []string{"sk-live-secret", "secret-query", "Authorization", "proxy-secret"} {
		if strings.Contains(combined, secret) {
			t.Fatalf("doctor report leaked %q:\n%s", secret, combined)
		}
	}
	if !strings.Contains(combined, "api.example.com") || !strings.Contains(combined, "mcp.example.com") {
		t.Fatalf("doctor report should keep useful host diagnostics:\n%s", combined)
	}
}

func TestCollectReportDoesNotRequireAPIKey(t *testing.T) {
	t.Setenv("REASONIX_HOME", filepath.Join(t.TempDir(), "reasonix"))
	t.Setenv("DEEPSEEK_API_KEY", "")

	cfg := config.Default()
	report := Collect(Options{Version: "1.2.3", Config: cfg})
	text := RenderText(report)

	if report.Version != "1.2.3" {
		t.Fatalf("version = %q, want 1.2.3", report.Version)
	}
	if len(report.Providers) == 0 {
		t.Fatal("expected built-in providers in report")
	}
	if report.Providers[0].KeyPresent {
		t.Fatal("provider key should be reported missing when env is empty")
	}
	if !strings.Contains(text, "reasonix 1.2.3 doctor") {
		t.Fatalf("text report missing header:\n%s", text)
	}
	if !strings.Contains(text, "missing") {
		t.Fatalf("text report should mention missing key state:\n%s", text)
	}
}

func TestCollectRecoveryLifecycleAggregatesWithoutLeakingSessionData(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "private-session.conflicts.jsonl")
	body := strings.Join([]string{
		`{"outcome":"forked_recovery_branch","occurrence":1,"topic_title":"secret title","path":"/private/user/session.jsonl"}`,
		`{"outcome":"forked_file_lock_recovery","existing_recovery":true,"occurrence":2,"repeated_in_process":true,"preview":"secret message"}`,
		`{"outcome":"adopted_newer_disk_transcript","occurrence":3,"repeated_in_process":true}`,
		`{"outcome":"classified_covered"}`,
		`{"outcome":"classified_adopted"}`,
		`{"outcome":"classified_preferred"}`,
		`{"outcome":"classified_diverged"}`,
		`{"outcome":"cleanup_moved"}`,
		`{"outcome":"cleanup_kept"}`,
		`{"outcome":"cleanup_skipped_in_use"}`,
		`{"outcome":"cleanup_revalidation_failed"}`,
		`not-json`,
	}, "\n") + "\n"
	if err := os.WriteFile(logPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	report := collectSessions(dir)
	if report.Recovery.Events != 11 || report.Recovery.PhysicalVersionsCreated != 1 ||
		report.Recovery.DiskAdoptions != 1 || report.Recovery.ShutdownRecoveries != 1 ||
		report.Recovery.RepeatedEvents != 2 || report.Recovery.MaxTopicOccurrences != 3 ||
		report.Recovery.ClassifiedCovered != 1 || report.Recovery.ClassifiedAdopted != 1 ||
		report.Recovery.ClassifiedPreferred != 1 || report.Recovery.ClassifiedDiverged != 1 ||
		report.Recovery.CleanupMoved != 1 || report.Recovery.CleanupKept != 1 ||
		report.Recovery.CleanupSkippedInUse != 1 || report.Recovery.CleanupRevalidationFailed != 1 ||
		report.Recovery.InvalidRecords != 1 {
		t.Fatalf("recovery lifecycle = %+v", report.Recovery)
	}
	raw, err := json.Marshal(report.Recovery)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"secret title", "secret message", "/private/user", "private-session"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("aggregate recovery diagnostics leaked %q: %s", secret, raw)
		}
	}
}

func TestRenderTextSurfacesWarningsUpTop(t *testing.T) {
	text := RenderText(Report{Warnings: []string{"config reasonix.toml: parse boom"}})
	w := strings.Index(text, "parse boom")
	if w < 0 {
		t.Fatalf("warning missing from report:\n%s", text)
	}
	if p := strings.Index(text, "\nproviders\n"); p >= 0 && w > p {
		t.Fatalf("warning should appear before the providers section, not buried below:\n%s", text)
	}
}

func TestRenderTextFlagsUnavailableSandboxAsFailClosed(t *testing.T) {
	inactive := RenderText(Report{Sandbox: SandboxReport{Bash: "enforce", Available: false}})
	if !strings.Contains(inactive, "bash execution is refused") {
		t.Fatalf("enforce without an OS sandbox should report fail-closed bash behavior:\n%s", inactive)
	}
	if strings.Contains(inactive, "runs unconfined") {
		t.Fatalf("enforce without an OS sandbox should not claim bash runs unconfined:\n%s", inactive)
	}

	active := RenderText(Report{Sandbox: SandboxReport{Bash: "enforce", Available: true}})
	if strings.Contains(active, "bash execution is refused") {
		t.Fatalf("enforce with an OS sandbox should not be flagged unavailable:\n%s", active)
	}
}

// TestCollectFlagsIgnoredEnforceConfig pins the visibility contract for the
// platform force-off: when the config file says enforce but the effective mode
// resolves to off (Windows), doctor must say so in both the warnings list and
// the sandbox bash line instead of silently reporting "off".
func TestCollectFlagsIgnoredEnforceConfig(t *testing.T) {
	t.Setenv("REASONIX_HOME", filepath.Join(t.TempDir(), "reasonix"))

	cfg := config.Default()
	cfg.Sandbox.Bash = "enforce"
	report := Collect(Options{Version: "test", Config: cfg})

	ignored := cfg.BashMode() == "off"
	if report.Sandbox.BashConfigIgnored != ignored {
		t.Fatalf("BashConfigIgnored = %v, want %v (BashMode %q)", report.Sandbox.BashConfigIgnored, ignored, cfg.BashMode())
	}

	text := RenderText(Report{Sandbox: SandboxReport{Bash: "off", BashConfigIgnored: true}})
	if !strings.Contains(text, `config requests "enforce", ignored`) {
		t.Fatalf("ignored enforce should be flagged on the bash line:\n%s", text)
	}
	plain := RenderText(Report{Sandbox: SandboxReport{Bash: "off"}})
	if strings.Contains(plain, "ignored") {
		t.Fatalf("plain off must not claim the config was ignored:\n%s", plain)
	}
}

func TestHomeIsolationWarningDetectsMismatch(t *testing.T) {
	// Prefer a synthetic mismatch without requiring root privileges.
	acct, err := user.Current()
	if err != nil || acct == nil || strings.TrimSpace(acct.HomeDir) == "" {
		t.Skip("account home unavailable")
	}
	t.Setenv("REASONIX_HOME", "")
	serviceHome := filepath.Join(t.TempDir(), "service-home")
	t.Setenv("HOME", serviceHome)
	t.Setenv("USERPROFILE", serviceHome)
	got := homeIsolationWarning()
	if got == "" {
		t.Fatal("expected HOME mismatch warning")
	}
	if !strings.Contains(got, "REASONIX_HOME") {
		t.Fatalf("warning = %q, want REASONIX_HOME guidance", got)
	}
	// Shareable output must not embed either absolute home path.
	if strings.Contains(got, serviceHome) || strings.Contains(got, acct.HomeDir) {
		t.Fatalf("warning leaked a home path: %q", got)
	}
	t.Setenv("REASONIX_HOME", t.TempDir())
	if got := homeIsolationWarning(); got != "" {
		t.Fatalf("REASONIX_HOME set should silence warning, got %q", got)
	}
}

// tl1173s2 落点②：doctor 报告标注会话日志形态——每个会话的 events 侧车要么是
// 扁平 schema-1（现役默认），要么是 schema-2 DAG 封存残留（tl1227 读退役后读
// 路径只服务扁平 JSONL）。报告只给聚合计数（dag_logs），不带任何路径。
func TestCollectSessionsReportsLogFormatDAGLeftovers(t *testing.T) {
	dir := t.TempDir()
	writeLog := func(name, head string) {
		t.Helper()
		body := head + "\n" + `{"schema_version":1,"type":"append","at":"2026-01-01T00:00:00Z","message_index":1,"messages":[]}` + "\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeLog("flat-one.events.jsonl", `{"schema_version":1,"type":"replace","at":"2026-01-01T00:00:00Z","message_index":0,"messages":[]}`)
	writeLog("sealed-dag.events.jsonl", `{"schema_version":2,"type":"dag_append","at":"2026-01-01T00:00:00Z"}`)
	writeLog("flat-two.events.jsonl", `{"schema_version":1,"type":"append","at":"2026-01-01T00:00:00Z","message_index":0,"messages":[]}`)

	report := collectSessions(dir)
	if report.DAGLogs != 1 {
		t.Fatalf("DAGLogs = %d, want 1 (one sealed schema-2 leftover among flat logs)", report.DAGLogs)
	}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"flat-one", "sealed-dag", "flat-two", dir} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("log-format marker leaked a machine-local name %q: %s", secret, raw)
		}
	}

	text := RenderText(Report{Sessions: SessionsReport{DAGLogs: report.DAGLogs}})
	if !strings.Contains(text, "log format   dag:1") {
		t.Fatalf("text report missing the log format line:\n%s", text)
	}
	if !strings.Contains(text, "flat JSONL") {
		t.Fatalf("log format line should say reads serve the flat JSONL:\n%s", text)
	}

	allFlat := RenderText(Report{Sessions: SessionsReport{}})
	if !strings.Contains(allFlat, "log format   dag:0") {
		t.Fatalf("all-flat dir should read dag:0:\n%s", allFlat)
	}
}
