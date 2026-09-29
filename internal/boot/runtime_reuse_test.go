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

// TestRuntimeAssemblyReuseFeedsDiscovery is the task-363A behavior proof: a
// second build whose Options carry the first build's ReusedAssembly skips
// prompt reassembly — the sentinel prompt flows through untouched — and the
// stage-timings line reports the finer agent segments. Gate-off callers just
// leave ReuseAssembly nil and get the legacy path.
func TestRuntimeAssemblyReuseFeedsDiscovery(t *testing.T) {
	isolateConfigHome(t)
	dir := robustTempDir(t)
	t.Chdir(dir)

	var hs atomic.Int32
	srv := handshakeStub(t, "tele-a", 0, &hs)
	defer srv.Close()
	writeFile(t, dir, "reasonix.toml", mcpCapabilityTestProviderConfig+`
[[plugins]]
name = "tele-a"
type = "http"
url = "`+srv.URL+`"
auto_start = true
`)
	registerBootTokenProfileTestProvider()
	setBootTokenProfileTestProvider(t, testutil.NewMock("mcp-runtime-reuse", testutil.Turn{Text: "ok"}))

	var logBuf bytes.Buffer
	prevLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(prevLogger) })

	baseOpts := func() Options {
		return Options{Sink: event.Discard, MCPHostProfile: plugin.HostProfileInteractive}
	}

	res1, err := BuildRuntime(context.Background(), baseOpts())
	if err != nil {
		t.Fatalf("round1 BuildRuntime: %v", err)
	}
	if res1.Assembly == nil {
		t.Fatal("round1 returned nil Assembly; BuildWithAssembly would store nothing")
	}
	firstPrompt := ""
	if res1.Snapshot != nil {
		firstPrompt = res1.Snapshot.SystemPrompt()
	}
	if strings.TrimSpace(firstPrompt) == "" {
		t.Fatal("round1 produced an empty system prompt; nothing to prove reuse against")
	}

	// Second round: hand back the assembly with a sentinel prompt. If the
	// reuse path is live, the sentinel wins verbatim.
	assembly := res1.Assembly
	assembly.SystemPrompt = "SENTINEL-PROMPT-FROM-POOL"
	logBuf.Reset()
	opts2 := baseOpts()
	opts2.RuntimeReload = RuntimeReload{ReuseAssembly: assembly, PreviousPlan: FullReusePlan()}
	res2, err := BuildRuntime(context.Background(), opts2)
	if err != nil {
		t.Fatalf("round2 BuildRuntime: %v", err)
	}
	if res2.Snapshot == nil {
		t.Fatal("round2 returned nil Snapshot")
	}
	if got := res2.Snapshot.SystemPrompt(); got != "SENTINEL-PROMPT-FROM-POOL" {
		t.Fatalf("reuse path did not feed the sentinel prompt through (len=%d first60=%q)", len(got), first60(got))
	}
	if logs := logBuf.String(); !strings.Contains(logs, "agent:tools=") {
		t.Fatalf("stage timings missing agent:tools segment, got:\n%s", logs)
	}
}

func first60(s string) string {
	if len(s) < 60 {
		return s
	}
	return s[:60]
}
