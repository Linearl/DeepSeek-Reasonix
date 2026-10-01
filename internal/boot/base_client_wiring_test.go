package boot

import (
	"context"
	"reflect"
	"testing"

	"reasonix/internal/baseproc"
)

// TestBuildWiresInlineBaseClientByDefault is the S1b boot 消费点对照测试：
// the default build carries a live BaseClient whose mode is inline (the
// switch ships off — R4), whose hello reports the tools capability because
// boot attached the just-built registry as its surface, and whose catalog
// answer is the same registry content the controller's gate serves locally.
// 开关关=现行为，由「同一注册表、进程内回答」两个断言钉住。
//
// The Mode assertion also holds if someone flips the switch on a dev machine:
// the test binary cannot serve `base serve --stdio`, so Start's R1 fallback
// returns inline anyway.
func TestBuildWiresInlineBaseClientByDefault(t *testing.T) {
	res, err := BuildRuntime(context.Background(), Options{})
	if err != nil {
		t.Fatalf("BuildRuntime: %v", err)
	}
	ctrl := res.Controller
	if ctrl == nil {
		t.Fatal("BuildRuntime returned no controller")
	}
	t.Cleanup(func() { ctrl.Close() })

	if res.BaseClient == nil {
		t.Fatal("BuildResult.BaseClient is nil: the S1b boot consumption point is not wired")
	}
	if mode := res.BaseClient.Mode(); mode != baseproc.ModeInline {
		t.Fatalf("BaseClient mode = %q, want inline with the switch off", mode)
	}
	hello, err := res.BaseClient.Hello(context.Background(), baseproc.HelloParams{
		ProtocolVersion: baseproc.ProtocolVersion, ClientPID: 1,
	})
	if err != nil {
		t.Fatalf("inline hello: %v", err)
	}
	capable := false
	for _, c := range hello.Capabilities {
		if c == baseproc.CapTools {
			capable = true
		}
	}
	if !capable {
		t.Fatalf("inline hello capabilities = %v, want %q (boot attached the registry surface)",
			hello.Capabilities, baseproc.CapTools)
	}

	// Same-source对照: the inline surface and the controller's local gate read
	// the same registry, so both catalog answers must be identical.
	inlineCatalog, err := res.BaseClient.ToolCatalog(context.Background(), baseproc.ToolCatalogParams{
		Scope: baseproc.ScopeProvider,
	})
	if err != nil {
		t.Fatalf("inline toolCatalog: %v", err)
	}
	gateEntries := ctrl.ToolContractEntries()
	if len(inlineCatalog.Tools) != len(gateEntries) {
		t.Fatalf("inline catalog has %d tools, controller gate has %d — not the same source",
			len(inlineCatalog.Tools), len(gateEntries))
	}
	for i, d := range inlineCatalog.Tools {
		e := gateEntries[i]
		if d.Name != e.Name || d.Description != e.Description || d.ReadOnly != e.ReadOnly ||
			!reflect.DeepEqual([]byte(d.Schema), []byte(e.Schema)) {
			t.Fatalf("entry %d diverges: catalog=%+v gate=%+v", i, d, e)
		}
	}
	if len(gateEntries) == 0 {
		t.Fatal("built registry has no tools — cannot prove the catalog gate is live")
	}
}
