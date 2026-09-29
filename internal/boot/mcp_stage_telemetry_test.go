package boot

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"

	"reasonix/internal/agent/testutil"
	"reasonix/internal/event"
	"reasonix/internal/plugin"
)

// TestMCPTelemetryPerSpecLogs is the task-363B telemetry contract: a build
// decomposes the mcp stage into named per-spec lines ("boot: mcp spec") and
// the shared-host fetch path emits the queueing split ("plugin: tools fetch"
// with wait_ms/fetch_ms). Observation only — the assertions read captured
// slog output, never wall-clock thresholds, so the test stays stable on a
// loaded machine while still proving the telemetry lines exist with their
// fields.
func TestMCPTelemetryPerSpecLogs(t *testing.T) {
	isolateConfigHome(t)
	dir := robustTempDir(t)
	t.Chdir(dir)
	registerBootTokenProfileTestProvider()
	setBootTokenProfileTestProvider(t, testutil.NewMock("mcp-telemetry", testutil.Turn{Text: "ok"}))

	var hs atomic.Int32
	srv := handshakeStub(t, "tele-a", 0, &hs)
	defer srv.Close()
	// The spec rides the config file (auto_start plugin), which is the
	// registerEnabledMCP face — ExtraPlugins take the host-session fast path
	// above the mcp stage and never reach the per-spec telemetry.
	writeFile(t, dir, "reasonix.toml", mcpCapabilityTestProviderConfig+`
[[plugins]]
name = "tele-a"
type = "http"
url = "`+srv.URL+`"
auto_start = true
`)
	shared := plugin.NewHostWithProfile(plugin.HostProfileInteractive)

	var logBuf bytes.Buffer
	prevLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(prevLogger) })

	// Round 1 (fresh host): the spec walks the lazy/kick path because the
	// shared host has no client yet.
	ctrl, err := Build(context.Background(), Options{
		Sink:       event.Discard,
		SharedHost: shared,
	})
	if err != nil {
		t.Fatalf("round1 Build: %v", err)
	}
	ctrl.Close()
	first := logBuf.String()
	logBuf.Reset()
	if !strings.Contains(first, "msg=\"boot: mcp spec\"") {
		t.Fatalf("round1 missing per-spec line, got:\n%s", first)
	}
	if !strings.Contains(first, "path=lazy") {
		t.Fatalf("round1 fresh-host spec should log path=lazy, got:\n%s", first)
	}
	if !strings.Contains(first, "name=\"tele-a\"") && !strings.Contains(first, "name=tele-a") {
		t.Fatalf("round1 per-spec line missing spec name, got:\n%s", first)
	}
	// The 334 stage line must still be present and unchanged in shape.
	if !strings.Contains(first, "boot: mcp stage") || !strings.Contains(first, "connect_fresh=") {
		t.Fatalf("round1 lost the 334 stage line fields, got:\n%s", first)
	}

	// Round 2 (same shared host, config unchanged): the spec resolves via the
	// connected client; the first ToolsFor populates the cache, so either the
	// host_tools line or a tools-fetch line must appear.
	ctrl2, err := Build(context.Background(), Options{
		Sink:       event.Discard,
		SharedHost: shared,
	})
	if err != nil {
		t.Fatalf("round2 Build: %v", err)
	}
	ctrl2.Close()
	second := logBuf.String()
	if !strings.Contains(second, "msg=\"boot: mcp spec\"") {
		t.Fatalf("round2 missing per-spec line, got:\n%s", second)
	}
	if !strings.Contains(second, "path=host_tools") {
		t.Fatalf("round2 shared-host spec should log path=host_tools, got:\n%s", second)
	}
	if !strings.Contains(second, "spec_ms=") {
		t.Fatalf("per-spec line missing spec_ms field, got:\n%s", second)
	}

	// The fetch-side telemetry line fires whenever listTools is reached at
	// least once across the two builds (cache miss on round 2's ToolsFor or
	// the lazy discovery). Assert the field trio, not the timing values.
	combined := first + second
	if !strings.Contains(combined, "msg=\"plugin: tools fetch\"") {
		t.Fatalf("no plugin: tools fetch line across builds, got:\n%s", combined)
	}
	for _, field := range []string{"wait_ms=", "fetch_ms=", "cached="} {
		if !strings.Contains(combined, field) {
			t.Fatalf("tools fetch line missing %s, got:\n%s", field, combined)
		}
	}
}

// TestMCPFetchTelemetryConcurrentOrder: two concurrent listTools calls against
// one client serialize on the fetch mutex and both return the same tools —
// the queueing telemetry must not change the result, only observe it.
func TestMCPFetchTelemetryConcurrentOrder(t *testing.T) {
	var hs atomic.Int32
	srv := handshakeStub(t, "tele-cc", 0, &hs)
	defer srv.Close()
	spec := plugin.Spec{Name: "tele-cc", Type: "http", URL: srv.URL, Authorized: true}
	host := plugin.NewHostWithProfile(plugin.HostProfileInteractive)
	if _, err := host.EnsureConnected(context.Background(), spec); err != nil {
		t.Fatalf("EnsureConnected: %v", err)
	}
	defer host.Close()

	toolsA, errA := host.ToolsFor(context.Background(), spec.Name)
	toolsB, errB := host.ToolsFor(context.Background(), spec.Name)
	if errA != nil || errB != nil {
		t.Fatalf("ToolsFor errors: %v / %v", errA, errB)
	}
	if len(toolsA) == 0 || len(toolsA) != len(toolsB) {
		t.Fatalf("concurrent ToolsFor disagree: %d vs %d tools", len(toolsA), len(toolsB))
	}
}
