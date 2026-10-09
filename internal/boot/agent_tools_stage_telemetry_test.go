package boot

import (
	"bytes"
	"context"
	"log/slog"
	"regexp"
	"strings"
	"testing"

	"reasonix/internal/agent/testutil"
	"reasonix/internal/event"
	"reasonix/internal/plugin"
)

// agentToolsStageLine matches the task-363 ⑦ decomposition line and captures
// its key=value fields, e.g. msg="boot: agent:tools stage" prep_ms=2
// commands_ms=31 skills_ms=4 capability_ms=1 executor_ms=5 stage_ms=43.
var agentToolsStageLine = regexp.MustCompile(
	`msg="boot: agent:tools stage"` +
		`.*?prep_ms=(\d+)` +
		`.*?commands_ms=(\d+)` +
		`.*?skills_ms=(\d+)` +
		`.*?capability_ms=(\d+)` +
		`.*?executor_ms=(\d+)` +
		`.*?stage_ms=(\d+)`)

// TestAgentToolsStageTelemetry is the task-363 ⑦ contract: every build emits
// the agent:tools decomposition line with all five named faces plus the stage
// wall clock, on both the cold path and the 363A reuse path (where
// commands_ms reads 0 — LoadRoots is skipped by the reused assembly). Like the
// 363B test, assertions read captured slog output and never wall-clock
// thresholds, so the test stays stable on a loaded machine.
func TestAgentToolsStageTelemetry(t *testing.T) {
	isolateConfigHome(t)
	dir := robustTempDir(t)
	t.Chdir(dir)
	registerBootTokenProfileTestProvider()
	setBootTokenProfileTestProvider(t, testutil.NewMock("agent-tools-telemetry", testutil.Turn{Text: "ok"}))
	writeFile(t, dir, "reasonix.toml", mcpCapabilityTestProviderConfig)

	var logBuf bytes.Buffer
	prevLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(prevLogger) })

	res1, err := BuildRuntime(context.Background(), Options{
		Sink:           event.Discard,
		MCPHostProfile: plugin.HostProfileInteractive,
	})
	if err != nil {
		t.Fatalf("round1 BuildRuntime: %v", err)
	}
	if res1.Controller == nil {
		t.Fatal("round1 returned nil Controller")
	}
	cold := logBuf.String()
	logBuf.Reset()

	m := agentToolsStageLine.FindStringSubmatch(cold)
	if m == nil {
		t.Fatalf("cold build missing boot: agent:tools stage line, got:\n%s", cold)
	}
	if !strings.Contains(cold, "msg=\"boot: stage timings\"") || !strings.Contains(cold, "agent:tools=") {
		t.Fatalf("cold build lost the stage-timings summary shape, got:\n%s", cold)
	}

	// Round 2: the 363A reuse path. The assembly feeds skills/commands/hooks
	// back in, so commands_ms must read 0 (LoadRoots skipped); every other
	// face still reports its wall clock.
	assembly := res1.Assembly
	if assembly == nil {
		t.Fatal("round1 returned nil Assembly; reuse path would store nothing")
	}
	opts2 := Options{
		Sink:           event.Discard,
		MCPHostProfile: plugin.HostProfileInteractive,
		RuntimeReload:  RuntimeReload{ReuseAssembly: assembly, PreviousPlan: FullReusePlan()},
	}
	res2, err := BuildRuntime(context.Background(), opts2)
	if err != nil {
		t.Fatalf("round2 BuildRuntime: %v", err)
	}
	if res2.Controller == nil {
		t.Fatal("round2 returned nil Controller")
	}
	reuse := logBuf.String()
	m2 := agentToolsStageLine.FindStringSubmatch(reuse)
	if m2 == nil {
		t.Fatalf("reuse build missing boot: agent:tools stage line, got:\n%s", reuse)
	}
	if m2[2] != "0" {
		t.Fatalf("reuse build commands_ms=%s, want 0 (LoadRoots must be skipped by the reused assembly)", m2[2])
	}

	// Task 363 ① evidence, not assertion: log both rounds' stage-timings
	// summaries so the cold-vs-reuse savings stay visible in test output
	// (-v) without a wall-clock threshold that would flake on a loaded
	// machine. The <1s field acceptance is judged from desktop.log once the
	// experimental_runtime_reuse gate runs in the app; this line documents
	// what the reuse path already removes in test conditions.
	logStageTimings := func(label, logs string) {
		for _, line := range strings.Split(logs, "\n") {
			if strings.Contains(line, "boot: stage timings") {
				t.Logf("%s: %s", label, strings.TrimSpace(line))
			}
		}
	}
	logStageTimings("cold", cold)
	logStageTimings("reuse", reuse)
}
