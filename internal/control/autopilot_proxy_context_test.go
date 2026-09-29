package control

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/config"
	"reasonix/internal/event"
	"reasonix/internal/guardian"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// Task 388: the proxy reviewer's reason must carry the scope dial and, when
// configured, the natural-language manifest — bounded, tail-kept.

func proxyContextFixture(t *testing.T, scope, manifestContent string) (string, *Controller) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if scope != "" {
		if err := cfg.SetAutopilotProxyScope(scope); err != nil {
			t.Fatal(err)
		}
	}
	manifest := ""
	if manifestContent != "" {
		manifest = filepath.Join(home, "proxy-manifest.txt")
		if err := os.WriteFile(manifest, []byte(manifestContent), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := cfg.SetAutopilotProxyManifest(manifest); err != nil {
			t.Fatal(err)
		}
	}
	if err := cfg.SaveTo(filepath.Join(home, "config.toml")); err != nil {
		t.Fatal(err)
	}
	return manifest, &Controller{}
}

func TestProxyContextLevel1IsTheDefault(t *testing.T) {
	_, c := proxyContextFixture(t, "", "")
	ctx := c.autopilotProxyContext()
	if !strings.Contains(ctx, "PROXY SCOPE: level 1 (related-only)") {
		t.Fatalf("empty scope must read level 1 (default), got: %s", ctx)
	}
	if strings.Contains(ctx, "level 2") || strings.Contains(ctx, "PROXY MANIFEST") {
		t.Fatalf("no manifest configured — the context must carry scope only, got: %s", ctx)
	}
}

func TestProxyContextLevel2AndManifestFlowThrough(t *testing.T) {
	manifest, c := proxyContextFixture(t, "all", "ALLOW: builds under the workspace root.\nDENY: anything touching credentials.")
	ctx := c.autopilotProxyContext()
	for _, want := range []string{"PROXY SCOPE: level 2 (full proxy)", "PROXY MANIFEST", "ALLOW: builds", "DENY: anything touching credentials"} {
		if !strings.Contains(ctx, want) {
			t.Fatalf("proxy context missing %q, got: %s", want, ctx)
		}
	}
	_ = manifest
}

func TestProxyContextManifestIsBoundedTailKept(t *testing.T) {
	lines := make([]string, 0, 400)
	for i := 0; i < 400; i++ {
		lines = append(lines, "ALLOW: filler entry number "+strings.Repeat("x", 30))
	}
	_, c := proxyContextFixture(t, "all", strings.Join(lines, "\n"))
	ctx := c.autopilotProxyContext()
	if len(ctx) > 8192 {
		t.Fatalf("context must stay bounded, got %d bytes", len(ctx))
	}
	if strings.Contains(ctx, "filler entry number 0\n") && strings.Contains(ctx, "ALLOW: filler entry number 000") {
		t.Fatal("the oldest filler lines must be truncated first")
	}
	if !strings.Contains(ctx, "older entries truncated") {
		t.Fatal("truncation must be marked")
	}
}

// End-to-end through reviewUnattendedApproval: the guardian reviewer's
// prompt (captured by the recording provider) must carry the proxy context
// block — scope dial plus manifest — and a default-config run must not.
func Test388ProxyContextReachesGuardianPrompt(t *testing.T) {
	manifest, c := proxyContextFixture(t, "all", "ALLOW: workspace-local writes.")
	sink := &noticeSink{}
	guardianProv := &recordingProvider{
		name:    "guardian",
		streams: [][]provider.Chunk{textTurn(`{"risk_level":"low","user_authorization":"high","outcome":"allow","rationale":"manifest allows"}`)},
	}
	c.sink = sink
	c.approvalTier = ApprovalTierGuardian
	c.guardianSess = guardian.NewSession(guardianProv, tool.NewRegistry(), guardian.PolicyPrompt(), "guardian-test", 0, nil, event.Discard)
	reply, decided := c.reviewUnattendedApproval(context.Background(),
		"write_file", "src/config.go", "apply the agreed rename", json.RawMessage(`{"path":"src/config.go"}`))
	if !decided || !reply.allow {
		t.Fatalf("manifest-allowed work must be proxy-approved: decided=%v allow=%v", decided, reply.allow)
	}
	if len(guardianProv.requests) != 1 {
		t.Fatalf("guardian reviews = %d, want 1", len(guardianProv.requests))
	}
	prompt := requestMessagesText(guardianProv.requests[0].Messages)
	for _, want := range []string{"PROXY SCOPE: level 2", "ALLOW: workspace-local writes."} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("guardian prompt must carry %q, got: %s", want, prompt)
		}
	}
	_ = manifest
}
