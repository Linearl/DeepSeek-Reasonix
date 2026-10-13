package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// redirectOSDefaultHome points the OS-default home hooks at a temp dir so the
// pointer-file tests never touch the real per-user Reasonix home. Replaces
// package-level vars (same pattern paths_test.go uses); restores via cleanup.
func redirectOSDefaultHome(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	origConfigDir := osUserConfigDir
	origHomeDir := osUserHomeDir
	osUserConfigDir = func() string { return root }
	osUserHomeDir = func() (string, error) { return filepath.Join(root, "home"), nil }
	t.Cleanup(func() {
		osUserConfigDir = origConfigDir
		osUserHomeDir = origHomeDir
	})
	return root
}

func TestDefaultHomePointerPathLivesUnderOSDefaultHome(t *testing.T) {
	root := redirectOSDefaultHome(t)
	want := filepath.Join(root, "reasonix", HomePointerFileName)
	if got := DefaultHomePointerPath(); got != want {
		t.Fatalf("DefaultHomePointerPath() = %q, want %q", got, want)
	}
	// The pointer must NOT follow REASONIX_HOME: it anchors on the OS default
	// so a corrupted pointer always has a known address to recover at.
	t.Setenv("REASONIX_HOME", filepath.Join(root, "custom"))
	if got := DefaultHomePointerPath(); got != want {
		t.Fatalf("pointer path moved with REASONIX_HOME set: %q, want %q", got, want)
	}
}

func TestHomePointerFileRoundTrip(t *testing.T) {
	root := redirectOSDefaultHome(t)
	path := DefaultHomePointerPath()
	target := filepath.Join(root, "vault", "reasonix-home")

	if pointer, err := ReadHomePointerFile(path); err != nil || pointer != nil {
		t.Fatalf("missing pointer should read as (nil, nil), got (%v, %v)", pointer, err)
	}

	if err := WriteHomePointerFile(path, target); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Atomic write leaves no temp behind (crash-mid-write cannot half-land).
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("temp file survived the rename: %v", err)
	}

	pointer, err := ReadHomePointerFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if pointer == nil {
		t.Fatal("pointer missing after write")
	}
	if pointer.Version != 1 {
		t.Fatalf("version = %d, want 1", pointer.Version)
	}
	if !samePath(pointer.Home, target) {
		t.Fatalf("home = %q, want %q", pointer.Home, target)
	}
}

func TestHomePointerFileRejectsUnusableContent(t *testing.T) {
	redirectOSDefaultHome(t)
	path := DefaultHomePointerPath()
	cases := map[string]string{
		"not json":        `{ this is not json`,
		"unknown version": `{"version":99,"home":"C:/x"}`,
		"relative home":   `{"version":1,"home":"relative/path"}`,
		"empty home":      `{"version":1,"home":"  "}`,
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
				t.Fatal(err)
			}
			pointer, err := ReadHomePointerFile(path)
			if pointer != nil || !errors.Is(err, ErrHomePointerInvalid) {
				t.Fatalf("read = (%v, %v), want (nil, ErrHomePointerInvalid)", pointer, err)
			}
		})
	}
}

// 验收 1（默认零行为）的读侧对位：无指针文件 ⇒ Apply 不动任何东西。
func TestApplyHomePointerEnvNoPointerIsZeroBehaviour(t *testing.T) {
	redirectOSDefaultHome(t)
	t.Setenv("REASONIX_HOME", "")
	os.Unsetenv("REASONIX_HOME")

	status, detail := ApplyHomePointerEnv()
	if status != HomePointerStatusNone || detail != "" {
		t.Fatalf("ApplyHomePointerEnv() = (%q, %q), want (none, \"\")", status, detail)
	}
	if dir := IsolatedHomeDir(); dir != "" {
		t.Fatalf("REASONIX_HOME was set to %q by a no-pointer apply", dir)
	}
}

// 显式环境变量优先：指针存在也被忽略（测试/CI/便携契约不因 846 变化）。
func TestApplyHomePointerEnvExplicitEnvWins(t *testing.T) {
	root := redirectOSDefaultHome(t)
	envHome := filepath.Join(root, "env-home")
	t.Setenv("REASONIX_HOME", envHome)
	if err := WriteHomePointerFile(DefaultHomePointerPath(), filepath.Join(root, "pointer-home")); err != nil {
		t.Fatal(err)
	}

	status, _ := ApplyHomePointerEnv()
	if status != HomePointerStatusEnvOverride {
		t.Fatalf("status = %q, want %q", status, HomePointerStatusEnvOverride)
	}
	if dir := IsolatedHomeDir(); !samePath(dir, envHome) {
		t.Fatalf("REASONIX_HOME = %q, want untouched %q", dir, envHome)
	}
}

// 指针生效路径：有效指针+存在的目标目录 ⇒ REASONIX_HOME 被指过去。
func TestApplyHomePointerEnvAppliesLivePointer(t *testing.T) {
	root := redirectOSDefaultHome(t)
	target := filepath.Join(root, "migrated-home")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REASONIX_HOME", "")
	os.Unsetenv("REASONIX_HOME")
	if err := WriteHomePointerFile(DefaultHomePointerPath(), target); err != nil {
		t.Fatal(err)
	}

	status, detail := ApplyHomePointerEnv()
	if status != HomePointerStatusApplied {
		t.Fatalf("status = %q (detail %q), want %q", status, detail, HomePointerStatusApplied)
	}
	if dir := IsolatedHomeDir(); !samePath(dir, target) {
		t.Fatalf("REASONIX_HOME = %q, want %q", dir, target)
	}
}

// 风险 2 的读侧兜底：指针损坏/目标缺失 ⇒ 回落默认 home，而不是带病启动。
func TestApplyHomePointerEnvFallbackOnBrokenPointer(t *testing.T) {
	cases := []struct {
		name    string
		payload string
	}{
		{"corrupt json", `{"version":1,"home":`},
		{"bad version", `{"version":42,"home":"C:/x"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			redirectOSDefaultHome(t)
			path := DefaultHomePointerPath()
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(tc.payload), 0o644); err != nil {
				t.Fatal(err)
			}
			t.Setenv("REASONIX_HOME", "")
			os.Unsetenv("REASONIX_HOME")

			status, detail := ApplyHomePointerEnv()
			if status != HomePointerStatusFallback {
				t.Fatalf("status = %q, want %q", status, HomePointerStatusFallback)
			}
			if !strings.Contains(detail, "invalid") && !strings.Contains(detail, "unsupported") {
				t.Fatalf("detail should carry the reason, got %q", detail)
			}
			if dir := IsolatedHomeDir(); dir != "" {
				t.Fatalf("REASONIX_HOME set despite broken pointer: %q", dir)
			}
		})
	}

	t.Run("target missing", func(t *testing.T) {
		root := redirectOSDefaultHome(t)
		if err := WriteHomePointerFile(DefaultHomePointerPath(), filepath.Join(root, "vanished")); err != nil {
			t.Fatal(err)
		}
		t.Setenv("REASONIX_HOME", "")
		os.Unsetenv("REASONIX_HOME")

		status, detail := ApplyHomePointerEnv()
		if status != HomePointerStatusFallback {
			t.Fatalf("status = %q, want %q", status, HomePointerStatusFallback)
		}
		if detail == "" {
			t.Fatal("fallback should carry a reason")
		}
	})
}

// 写入门禁：相对路径与默认 home 自身不允许成为指针目标。
func TestWriteHomePointerFileRejectsDefaultHomeAndRelative(t *testing.T) {
	root := redirectOSDefaultHome(t)
	path := DefaultHomePointerPath()
	if err := WriteHomePointerFile(path, root+string(filepath.Separator)+"reasonix"); err == nil {
		t.Fatal("writing a pointer at the default home should fail")
	}
	if err := WriteHomePointerFile(path, "relative/dir"); err == nil {
		t.Fatal("writing a pointer at a relative path should fail")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("failed writes must not leave a pointer behind: %v", err)
	}
}
