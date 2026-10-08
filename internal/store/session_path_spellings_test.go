package store

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// 任务 475（X6 模式 J 写读路径形态漂移）验收①：路径构造单一来源的 source-level
// 断言。P19 的 imgpack 错位（写 <id>.jsonl.imgpack / 读 <id>.events.jsonl.imgpack）
// 与 task 104 的 x.events.events.jsonl 都源于同一结构：会话路径后缀契约散落在
// 多个包里靠约定维持。本测试把约定变成可执行的仓库级断言：
//
//  1. 常量方向：store 导出的后缀常量必须逐字等于磁盘上的真实拼写——防止有人
//     「顺手改常量」把漂移合法化；
//  2. 仓库方向：除白名单文件外，任何生产代码（非 _test.go）不得手写这些后缀
//     字面量，分类/构造一律引用 store 常量或构造器。
//
// 白名单只有两处，均有结构性理由：
//   - internal/store/session.go：权威本身（常量与构造器的唯一落点）；
//   - internal/agent/migrate.go：v0.x 外来布局导入器，解析的是历史外部文件
//     （.jsonl.bak、外来 .events.jsonl），语义上就是「按历史拼写逐字识别」。
//
// 注释剔除采用「自首个 // 截断该行」的朴素规则：被禁字面量都不含 //，误伤面
// 仅限「同一行内 URL 字符串之后还有代码」的写法，本仓库不存在。
func TestSessionPathSpellingsSingleSource(t *testing.T) {
	// 方向 1：常量必须等于磁盘真实拼写。
	pins := []struct {
		name, got, want string
	}{
		{"SessionTranscriptSuffix", SessionTranscriptSuffix, ".jsonl"},
		{"SessionEventLogSuffix", SessionEventLogSuffix, ".events.jsonl"},
		{"SessionEventLogDamagedSuffix", SessionEventLogDamagedSuffix, ".events.jsonl.damaged"},
		{"SessionEventLogRotatingSuffix", SessionEventLogRotatingSuffix, ".events.jsonl.rotating"},
		{"SessionTurnEventLogSuffix", SessionTurnEventLogSuffix, ".turns.jsonl"},
		{"SessionConflictLogSuffix", SessionConflictLogSuffix, ".conflicts.jsonl"},
		{"SessionEventIndexSuffix", SessionEventIndexSuffix, ".event-index.json"},
		{"SessionDisplayIndexSuffix", SessionDisplayIndexSuffix, ".display-index.json"},
		{"SessionMetaFileSuffix", SessionMetaFileSuffix, ".jsonl.meta"},
		{"SessionLockFileSuffix", SessionLockFileSuffix, ".jsonl.lock"},
		{"SessionLeaseLockSuffix", SessionLeaseLockSuffix, ".jsonl.lease.lock"},
		{"SessionLeaseInfoSuffix", SessionLeaseInfoSuffix, ".jsonl.lease.json"},
	}
	for _, pin := range pins {
		if pin.got != pin.want {
			t.Errorf("store.%s = %q, want the on-disk spelling %q — if the layout really changed, update both sides and re-audit every consumer", pin.name, pin.got, pin.want)
		}
	}

	// 方向 2：仓库级字面量禁令。
	banned := []string{
		".events.jsonl", // 连带覆盖 .damaged / .rotating 两个子尾
		".turns.jsonl",
		".conflicts.jsonl",
		".event-index.json",
		".display-index.json",
		".jsonl.meta",
		".jsonl.lock",
		".jsonl.lease.lock",
		".jsonl.lease.json",
	}
	allowlist := map[string]string{
		filepath.Join("internal", "store", "session.go"): "the authority itself",
		filepath.Join("internal", "agent", "migrate.go"): "legacy v0.x foreign-layout importer (parses historical external spellings verbatim)",
	}

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(thisFile))) // internal/store -> repo root
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("repo root not found from %s: %v", thisFile, err)
	}

	var violations []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && (d.Name() == ".git" || d.Name() == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") || strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if _, allowed := allowlist[rel]; allowed {
			return nil
		}
		b, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for i, line := range strings.Split(string(b), "\n") {
			if idx := strings.Index(line, "//"); idx >= 0 {
				line = line[:idx]
			}
			for _, lit := range banned {
				if strings.Contains(line, lit) {
					violations = append(violations, rel+":"+strconv.Itoa(i+1)+" spells "+lit+" — route through the store constants/constructors (task 475, X6 pattern J)")
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk repo: %v", err)
	}
	for _, v := range violations {
		t.Error(v)
	}
}
