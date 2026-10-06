package sentinel

// Zero-model participation, pinned at source level (task 410 acceptance:
// 「规则引擎零模型参与（源码级断言：不调用 LLM）」). The whole package parses
// its own imports and refuses any dependency that could route a decision
// through a model. The allowlist is closed on purpose: adding a dependency
// means editing this test, i.e. a reviewed decision, never a silent drift.

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// allowedImports is the complete non-stdlib import surface the sentinel
// package may have. shellparse: bash command segmentation. secrets: audit
// redaction. Nothing else — in particular nothing from internal/agent,
// internal/provider, internal/boundedllm, internal/guardian or any other
// package that talks to a model.
var allowedImports = map[string]bool{
	"reasonix/internal/shellparse": true,
	"reasonix/internal/secrets":    true,
}

func TestSentinelPackageImportsAreModelFree(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	fset := token.NewFileSet()
	seen := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") {
			continue
		}
		seen++
		f, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, imp := range f.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if allowedImports[path] {
				continue
			}
			if !strings.Contains(path, ".") { // stdlib: no dot in the first path segment
				continue
			}
			t.Errorf("%s imports %q — outside the sentinel allowlist; the sentinel rule engine must stay model-free (零模型参与)", name, path)
		}
	}
	if seen < 4 {
		t.Fatalf("expected the full package source set, only parsed %d files", seen)
	}
}

// TestAuditSubjectRedacted proves the audit trail cannot become a secret
// store: the recorded subject goes through secrets.Redact, so a fake key in
// the command text is masked on disk.
func TestAuditSubjectRedacted(t *testing.T) {
	withSentinelState(t, func(t *testing.T) {
		SetHardForbidden(true)
		audit := filepath.Join(t.TempDir(), "intercepts.jsonl")
		SetAuditPath(audit)

		secret := "sk-proj-abc123defGHIjklMNOpq"
		tool, args := bashCall(t, "git commit -m "+secret+" && git push --force origin main")
		expectBlocked(t, tool, args, RuleForcePushProtected)

		data, err := os.ReadFile(audit)
		if err != nil {
			t.Fatalf("audit file must exist: %v", err)
		}
		if strings.Contains(string(data), secret) {
			t.Error("audit record must not contain the raw secret")
		}
		if !strings.Contains(string(data), "****") {
			t.Errorf("audit record should show a masked value, got: %s", data)
		}
	})
}
