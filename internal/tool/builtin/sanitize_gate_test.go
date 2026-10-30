package builtin

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// TestPathURLArgsRouteThroughSanitizeHelpers is the migration gate (task 1276
// ③): no builtin source outside sanitize.go may call resolveIn,
// resolveReadablePath, or parse a URL directly. A new tool that reintroduces a
// raw path/URL entry fails here instead of producing a fresh CodeQL taint
// finding (filepath.Clean inside the helpers is the modeled barrier).
func TestPathURLArgsRouteThroughSanitizeHelpers(t *testing.T) {
	const helperFile = "sanitize.go"
	// workspace.go defines resolveIn / resolveReadablePath; the definitions
	// (and only them) live there.
	const resolverFile = "workspace.go"

	// Explicit allowlist for url.Parse outside the helper: config-sourced
	// transport plumbing, not model-controlled tool arguments. Every entry
	// must carry its reason; the corresponding tool must still route its
	// model-facing URL through a sanitize helper (asserted below).
	urlParseAllowlist := map[string]string{
		"webfetch.go": "parses the config-sourced [proxy] URL (netclient.ProxySpec) for the transport dial; the model-controlled fetch URL goes through sanitizeFetchURL",
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		if name == helperFile || name == resolverFile {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		file, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.Ident:
				if v.Name == "resolveIn" || v.Name == "resolveReadablePath" {
					t.Errorf("%s: %s must not be called directly — route the path argument through sanitizePathArg/sanitizeReadPath/sanitizeWriteTarget (internal/tool/builtin/sanitize.go)", name, v.Name)
				}
			case *ast.SelectorExpr:
				if x, ok := v.X.(*ast.Ident); ok && x.Name == "url" && v.Sel.Name == "Parse" {
					if reason, allowed := urlParseAllowlist[name]; !allowed {
						t.Errorf("%s: url.Parse must only be used inside %s — route URL arguments through sanitizeFetchURL", name, helperFile)
					} else if reason == "" {
						t.Errorf("%s: url.Parse allowlist entry needs a documented reason", name)
					}
				}
			}
			return true
		})
		if _, allowed := urlParseAllowlist[name]; allowed {
			if !strings.Contains(string(src), "sanitizeFetchURL(") {
				t.Errorf("%s: allowlisted for url.Parse but does not route its model-facing URL through sanitizeFetchURL", name)
			}
		}
	}
}
