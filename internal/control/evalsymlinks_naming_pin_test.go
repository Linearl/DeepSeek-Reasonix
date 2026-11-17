package control

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// TestNoSharedEvalSymlinksAllowMissingName 是 tl1617 的 grep 钉：历史上
// config/path_access.go 与本包各有一份同名 `evalSymlinksAllowMissing`，边界
// 语义已分叉（config 版坏叶子报错防写穿透 vs 本包版坏叶子当缺失上溯重接）——
// 「同一语义两套真相源必有一套腐化」的现行案例。tl1617 显式双命名：
// evalSymlinksStrictForWrite（config，Lstat 判存在、坏叶子报错）/
// evalSymlinksReattachMissing（control，EvalSymlinks 判存在、坏叶子重接）。
// 本钉保证旧同名不再回归；新增第三份同语义手写解析同样会撞这里的钉。
func TestNoSharedEvalSymlinksAllowMissingName(t *testing.T) {
	banned := "evalSymlinksAllowMissing"

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(thisFile))) // internal/control -> repo root
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
			// _test.go 与判例（store/session_path_spellings_test.go）一致排除：
			// 本测试文件自身必然携带被禁字符串（钉面在生产码）。
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		b, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for i, line := range strings.Split(string(b), "\n") {
			if strings.Contains(line, banned) {
				violations = append(violations, rel+":"+strconv.Itoa(i+1)+" uses the forked twin name "+banned+" — name it by its edge semantics: evalSymlinksStrictForWrite (config) or evalSymlinksReattachMissing (control), see tl1617")
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
