package agent

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// tl1652 门禁：pendingStateManifest（pending_state_manifest_tl1652.go）与仓库
// 实测的 pending/drain/deferred 型结构体字段集合做双向差集断言。
//
//   - 实测 ⊄ 清单 → 有人新增了 pending 型内存状态但没登记处置决定（关机会丢
//     什么？丢失后果？该完成落盘还是关机 flush 还是显式放弃？）——先登记再合入；
//   - 清单 ⊄ 实测 → 清单过期（字段已删/改名），把过期项清掉。
//
// 扫描规则与 20261117 基线扫描（tmp/tl1652/enum_pending.go）一致：非测试 Go
// 文件的全部 struct 字段，字段名（小写化）含命名族子串即命中。排除域：
// desktop/third_party、cmd/e2ebench、*_test.go、生成代码。
func TestPendingStateManifestCoversRepository(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "go.mod")); statErr != nil {
		// 保险丝：模块根上必须有 go.mod，否则说明目录结构变了，直接失败让人
		// 重新定位扫描根，绝不静默扫错树。
		t.Fatalf("scan root %q has no go.mod — locate the reasonix module root first", root)
	}
	scanned := scanPendingFields(t, root)

	manifest := map[string]bool{} // "loc\tsym"
	for _, entry := range pendingStateManifest {
		manifest[entry.Location+"\t"+entry.Symbol] = true
	}

	// 方向一：实测 ⊆ 清单。新增未登记 = 红。
	var unregistered []string
	for key := range scanned {
		if !manifest[key] {
			unregistered = append(unregistered, key)
		}
	}
	sort.Strings(unregistered)
	if len(unregistered) > 0 {
		var b strings.Builder
		b.WriteString("found pending/drain/deferred-type struct fields NOT in the tl1652 manifest;\n")
		b.WriteString("register a disposition (durable-at-write / shutdown-flush / explicit-drop / persisted-elsewhere)\n")
		b.WriteString("in pending_state_manifest_tl1652.go before merging:\n")
		for _, k := range unregistered {
			b.WriteString("  + " + strings.ReplaceAll(k, "\t", "  ") + "\n")
		}
		t.Fatal(b.String())
	}

	// 方向二：清单 ⊆ 实测。过期项 = 红。
	var stale []string
	for _, entry := range pendingStateManifest {
		if !scanned[entry.Location+"\t"+entry.Symbol] {
			stale = append(stale, entry.Location+"  "+entry.Symbol)
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		var b strings.Builder
		b.WriteString("manifest entries no longer present in code; prune or update them:\n")
		for _, k := range stale {
			b.WriteString("  - " + k + "\n")
		}
		t.Fatal(b.String())
	}

	// 方向三（固定语义）：tl1652 的两个 durable-at-write 项必须在场。
	// 归因完成即落盘是本件的核心交付，有人删掉 Session.pendingAttribution
	// 或改回纯内存队列时，这条把语义红出来，而不只是清单对齐。
	var durable int
	for _, entry := range pendingStateManifest {
		if entry.Disposition == dispDurableAtWrite {
			durable++
			if entry.Symbol != "Session.pendingContentReasons" && entry.Symbol != "Session.pendingAttribution" {
				t.Errorf("unexpected durable-at-write entry %s — review its semantics before changing this pin", entry.Symbol)
			}
		}
	}
	if durable != 2 {
		t.Fatalf("durable-at-write entries = %d, want exactly 2 (Session.pendingContentReasons + Session.pendingAttribution)", durable)
	}
}

// scanPendingFields mirrors tmp/tl1652/enum_pending.go's sweep rules.
func scanPendingFields(t *testing.T, root string) map[string]bool {
	t.Helper()
	nameFamilies := []string{
		"pending", "drain", "deferred", "flush", "dirty", "buffered",
		"queued", "queue", "awaiting", "staged", "unsaved", "unflushed", "unpersisted",
	}
	match := func(name string) bool {
		ln := strings.ToLower(name)
		for _, fam := range nameFamilies {
			if strings.Contains(ln, fam) {
				return true
			}
		}
		return false
	}
	excluded := func(rel string) bool {
		return strings.HasPrefix(rel, "desktop"+string(os.PathSeparator)+"third_party") ||
			strings.HasPrefix(rel, "cmd"+string(os.PathSeparator)+"e2ebench")
	}

	out := map[string]bool{}
	fset := token.NewFileSet()
	walkErr := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // unreadable entries are not manifest data
		}
		if info.IsDir() {
			name := info.Name()
			if path != root && (name == ".git" || name == "node_modules" || strings.HasPrefix(name, "wt-") ||
				name == "worktrees" || name == "testdata" || name == "_archive") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if strings.Contains(path, ".gen.go") || strings.Contains(path, "generated") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil || excluded(rel) {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return nil
		}
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, spec := range gd.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				st, ok := ts.Type.(*ast.StructType)
				if !ok {
					continue
				}
				for _, fld := range st.Fields.List {
					if len(fld.Names) == 0 {
						continue // embedded: not a named state field
					}
					fname := fld.Names[0].Name
					if !match(fname) {
						continue
					}
					out[rel+":"+itoa(fset.Position(fld.Pos()).Line)+"\t"+ts.Name.Name+"."+fname] = true
				}
			}
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk repo: %v", walkErr)
	}
	if len(out) == 0 {
		t.Fatal("scanner found nothing — the sweep rules or the scan root are broken, do not trust this green")
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
