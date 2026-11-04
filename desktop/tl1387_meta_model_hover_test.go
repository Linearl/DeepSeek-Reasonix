package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/agent"
)

// tl1387 (user ruling 20261104 14:0x): the sidebar row hover must stay usable
// when the host cannot see the session's runtime. fillNodeModel keeps the
// live probe (ModelRef/Provider) as-is and stamps MetaModel from the session
// sidecar's creation-time model; the FE annotates that value 「创建时」 and
// never renders either inline.

func TestFillNodeModelMetaModelFallbackTL1387(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "session.jsonl")
	meta := agent.BranchMeta{ID: "meta-tl1387", Model: "mimo-token-plan/mimo-v2.6-flash"}
	if err := agent.SaveBranchMeta(sessionPath, meta); err != nil {
		t.Fatalf("save branch meta: %v", err)
	}
	t.Cleanup(func() { metaModelCache = map[string]string{} })

	app := &App{}
	node := &ProjectNode{Kind: "topic", SessionPath: sessionPath}
	app.fillNodeModel(node)
	if node.ModelRef != "" {
		t.Fatalf("ModelRef = %q without a live runtime, want empty (no guess)", node.ModelRef)
	}
	if node.MetaModel != meta.Model {
		t.Fatalf("MetaModel = %q, want sidecar creation-time model %q", node.MetaModel, meta.Model)
	}

	// The cache answers repeat reads without re-reading the sidecar.
	if got := cachedSessionMetaModel(sessionPath); got != meta.Model {
		t.Fatalf("cachedSessionMetaModel = %q, want %q", got, meta.Model)
	}

	// A session without a sidecar stays unstamped — hover degrades to nothing,
	// never a guess.
	noMeta := &ProjectNode{Kind: "topic", SessionPath: filepath.Join(dir, "absent.jsonl")}
	app.fillNodeModel(noMeta)
	if noMeta.MetaModel != "" || noMeta.ModelRef != "" {
		t.Fatalf("absent sidecar stamped MetaModel=%q ModelRef=%q, want both empty", noMeta.MetaModel, noMeta.ModelRef)
	}
}

// The live probe outranks the sidecar value: a visible runtime stamps
// ModelRef/Provider and the creation-time field stays empty (the FE renders
// the live value without the 「创建时」 annotation). collabSessionInfo needs
// live targets a unit-level App cannot have, so the precedence is pinned at
// the source level: the live branch must return before the fallback stamps.
func TestFillNodeModelLiveProbeOutranksSidecarTL1387(t *testing.T) {
	src := readSourceForTL1387(t, "session_catalog_runtime.go")
	live := strings.Index(src, "node.ModelRef = ref")
	fallback := strings.Index(src, "node.MetaModel = cachedSessionMetaModel(node.SessionPath)")
	if live < 0 || fallback < 0 {
		t.Fatalf("fillNodeModel missing the live stamp or the tl1387 fallback line")
	}
	if live > fallback {
		t.Fatalf("fillNodeModel fallback precedes the live stamp; the ModelRef-then-return precedence is broken")
	}
}

func readSourceForTL1387(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read source: %v", err)
	}
	return string(data)
}
