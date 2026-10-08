package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPersistProjectWriteAccessWritesBothSections(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "reasonix.toml")
	if err := os.WriteFile(path, []byte("# keep\n[permissions]\nallow = [\"Bash(go test:*)\"]\n\n[sandbox]\nbash = \"enforce\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	extra := filepath.Join(dir, ".local")
	if err := PersistProjectWriteAccess(path, []string{extra}, "Edit"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	if !strings.Contains(body, "# keep") {
		t.Fatal("comments must be preserved")
	}
	if !strings.Contains(body, "Bash(go test:*)") || !strings.Contains(body, "Edit") {
		t.Fatalf("permission rules missing: %s", body)
	}
	if !strings.Contains(body, "allow_write") || !strings.Contains(body, extra) && !strings.Contains(body, ".local") {
		t.Fatalf("allow_write missing: %s", body)
	}
}

func TestPersistProjectWriteAccessDoesNotDuplicateAncestor(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "reasonix.toml")
	parent := filepath.Join(dir, "home")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	fixture := "[sandbox]\nallow_write = " + renderStringArray([]string{parent}) + "\n"
	if err := os.WriteFile(path, []byte(fixture), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := PersistProjectWriteAccess(path, []string{filepath.Join(parent, "bin")}, ""); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(raw), "allow_write") != 1 {
		t.Fatalf("unexpected allow_write rewrite: %s", raw)
	}
	if strings.Contains(string(raw), filepath.Join(parent, "bin")) {
		t.Fatalf("child should not be persisted when ancestor exists: %s", raw)
	}
}

func TestSetProjectWriteAccessReplacesListAndRemoves(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "reasonix.toml")
	a := filepath.Join(dir, "a")
	b := filepath.Join(dir, "b")
	if err := os.MkdirAll(a, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(b, 0o755); err != nil {
		t.Fatal(err)
	}
	fixture := "[sandbox]\nallow_write = " + renderStringArray([]string{a, b}) + "\n"
	if err := os.WriteFile(path, []byte(fixture), 0o644); err != nil {
		t.Fatal(err)
	}

	// Replace the whole list with a single entry => b is removed.
	if err := SetProjectWriteAccess(path, []string{a}, ""); err != nil {
		t.Fatal(err)
	}
	reloaded, err := LoadForEditReadOnlyStrict(path)
	if err != nil {
		t.Fatal(err)
	}
	roots := reloaded.AllowWriteRoots()
	if len(roots) != 1 || filepath.Clean(roots[0]) != filepath.Clean(a) {
		t.Fatalf("allow_write after replace = %v, want [%s]", roots, a)
	}
	for _, r := range roots {
		if filepath.Clean(r) == filepath.Clean(b) {
			t.Fatalf("b should have been removed, got %v", roots)
		}
	}

	// Setting an empty list removes everything.
	if err := SetProjectWriteAccess(path, nil, ""); err != nil {
		t.Fatal(err)
	}
	reloaded, err = LoadForEditReadOnlyStrict(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.AllowWriteRoots()) != 0 {
		t.Fatalf("expected empty allow_write, got %v", reloaded.AllowWriteRoots())
	}
}

// 任务 634：桌面端项目写目录列的移除要能落到用户级 [sandbox] allow_write——
// 项目 reasonix.toml 未定义该键时，merged 视图显示的就是用户级列表，只重写
// 项目文件移不掉条目（面板上表现为「x 点击无效」）。
func TestSetUserAllowWriteReplacesListAndRemoves(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	a := filepath.Join(home, "vault-a")
	b := filepath.Join(home, "vault-b")
	for _, dir := range []string{a, b} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfgPath := filepath.Join(home, "config.toml")
	body := "[sandbox]\nbash = \"enforce\"\nallow_write = " + renderStringArray([]string{a, b}) + "\n"
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	// 移除一个条目：其余保持、bash 键不动。
	if err := SetUserAllowWrite([]string{a}); err != nil {
		t.Fatalf("SetUserAllowWrite remove: %v", err)
	}
	cfg, err := LoadUserConfigReadOnly()
	if err != nil {
		t.Fatal(err)
	}
	got := cfg.AllowWriteRoots()
	if len(got) != 1 || got[0] != a {
		t.Fatalf("AllowWriteRoots after removal = %v, want [%s]", got, a)
	}
	if cfg.Sandbox.Bash != "enforce" {
		t.Fatalf("unrelated sandbox keys must survive, got bash=%q", cfg.Sandbox.Bash)
	}

	// 清空：显式空表落盘（替换写支持移除）。
	if err := SetUserAllowWrite(nil); err != nil {
		t.Fatalf("SetUserAllowWrite clear: %v", err)
	}
	cfg, err = LoadUserConfigReadOnly()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.AllowWriteRoots()) != 0 {
		t.Fatalf("AllowWriteRoots after clear = %v, want empty", cfg.AllowWriteRoots())
	}
}

func TestSetUserAllowWriteDeduplicatesUnderAncestor(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	parent := filepath.Join(home, "parent")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := SetUserAllowWrite([]string{parent, filepath.Join(parent, "child")}); err != nil {
		t.Fatalf("SetUserAllowWrite: %v", err)
	}
	cfg, err := LoadUserConfigReadOnly()
	if err != nil {
		t.Fatal(err)
	}
	got := cfg.AllowWriteRoots()
	if len(got) != 1 || got[0] != parent {
		t.Fatalf("AllowWriteRoots = %v, want collapsed to [%s]", got, parent)
	}
}

func TestSetUserAllowWriteCreatesConfigOnlyWhenNeeded(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	// 无配置文件 + 空列表：不落盘、不建文件。
	if err := SetUserAllowWrite(nil); err != nil {
		t.Fatalf("SetUserAllowWrite no-op: %v", err)
	}
	if _, err := os.Stat(UserConfigPath()); !os.IsNotExist(err) {
		t.Fatalf("empty list must not create a user config, stat err=%v", err)
	}
	// 无配置文件 + 有条目：创建仅含 [sandbox] allow_write 的最小配置。
	dir := filepath.Join(home, "created")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := SetUserAllowWrite([]string{dir}); err != nil {
		t.Fatalf("SetUserAllowWrite create: %v", err)
	}
	cfg, err := LoadUserConfigReadOnly()
	if err != nil {
		t.Fatal(err)
	}
	got := cfg.AllowWriteRoots()
	if len(got) != 1 || got[0] != dir {
		t.Fatalf("AllowWriteRoots = %v, want [%s]", got, dir)
	}
}
