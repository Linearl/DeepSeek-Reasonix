package config

// 任务 846：配置路径可更改（B 档）——home 指针文件。
//
// 设计（843 调研报告 ③段）：底层 REASONIX_HOME 环境变量覆盖机制早已完整
// 存在（paths.go），本件补的是「指针文件」这一层：把用户通过设置页向导
// 选定的自定义 home 记录在 OS 默认 home 下的 home-pointer.json，进程启动
// 早期读它并把 REASONIX_HOME 指过去。三条不变量：
//
//  1. 指针文件永远住在 OS 默认 home（不跟随 REASONIX_HOME env，也不跟随
//     指针自身）——否则「指针在哪个 home」本身就成了鸡生蛋问题，回落语义
//     失去锚点。
//  2. 显式环境变量永远优先：REASONIX_HOME 已设置时指针被忽略（测试/CI/
//     便携安装的既有契约不受影响，IsolatedHomeDir 语义不变）。
//  3. 缺省零行为：指针文件不存在/损坏/目标目录缺失 ⇒ 一律静默回落 OS
//     默认 home，与引入本文件之前逐字节一致（铁律 2）。写入侧另有实验
//     开关门禁（experimental_home_pointer，只有向导可达），损坏回落是
//     读侧兜底——用户清掉指针文件即可恢复默认，无需改配置。

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// HomePointerFileName is the pointer file's name inside the OS-default home.
const HomePointerFileName = "home-pointer.json"

// homePointerVersion is the current pointer schema version. Readers refuse
// unknown versions instead of guessing at an unfamiliar shape.
const homePointerVersion = 1

// ErrHomePointerInvalid marks a pointer file that exists but cannot be honored
// (bad JSON, unknown version, relative or empty home). Callers fall back to
// the OS-default home; the error text is for diagnostics only.
var ErrHomePointerInvalid = errors.New("home pointer invalid")

// HomePointerFile is the on-disk shape of home-pointer.json.
type HomePointerFile struct {
	Version   int    `json:"version"`
	Home      string `json:"home"`
	UpdatedAt string `json:"updated_at,omitempty"` // RFC3339, advisory only
	// Task846 records the task line that introduced the pointer, so a future
	// reader can tell a hand-crafted file from a wizard-written one.
	Task846 bool `json:"task846,omitempty"`
}

// osDefaultHomeDir resolves the Reasonix home the OS layout implies, ignoring
// the REASONIX_HOME environment variable entirely. This is the anchor the
// pointer file lives under and falls back to. Empty when the OS dirs cannot
// be resolved (callers must tolerate that, as everywhere else in this package).
func osDefaultHomeDir() string {
	if runtimeGOOS == "windows" {
		if dir := osUserConfigDir(); dir != "" {
			return filepath.Join(dir, "reasonix")
		}
		if home, err := osUserHomeDir(); err == nil && home != "" {
			return filepath.Join(home, "AppData", "Roaming", "reasonix")
		}
		return ""
	}
	if home, err := osUserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".reasonix")
	}
	if dir := osUserConfigDir(); dir != "" {
		return filepath.Join(dir, "reasonix")
	}
	return ""
}

// DefaultHomePointerPath is where the pointer file always lives: the OS-default
// home. Deliberately not routed through reasonixHomeDir — see the invariants
// in the file comment.
func DefaultHomePointerPath() string {
	dir := osDefaultHomeDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, HomePointerFileName)
}

// OsDefaultHomeDir exposes the OS-implied Reasonix home (REASONIX_HOME
// ignored). The home-migration wizard and its lock file anchor on it: the
// pointer and the lock must live where every future launch can find them,
// regardless of where a previous pointer pointed.
func OsDefaultHomeDir() string { return osDefaultHomeDir() }

// HomeMigrationLockPath is the cross-process migration mutex location: the
// OS-default home (task 846). While a live lock exists, second Reasonix
// processes refuse to start — the home tree may be mid-copy.
func HomeMigrationLockPath() string {
	dir := osDefaultHomeDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "home-migrate.lock")
}

// NormalizeHomePointerTarget validates and normalizes a user-supplied target
// directory before it may be written into the pointer file. It must be an
// absolute path that resolves outside the default home's subtree, and it must
// not be the default home itself. Relative paths are rejected outright: the
// pointer is read at process start before any meaningful cwd exists, so a
// recorded relative home could never be honored faithfully anyway. The
// directory does NOT need to exist yet (the migration wizard creates it).
func NormalizeHomePointerTarget(target string) (string, error) {
	trimmed := strings.TrimSpace(target)
	if trimmed == "" {
		return "", fmt.Errorf("%w: target path is empty", ErrHomePointerInvalid)
	}
	if !filepath.IsAbs(trimmed) {
		return "", fmt.Errorf("%w: target path %q is not absolute", ErrHomePointerInvalid, trimmed)
	}
	trimmed = filepath.Clean(trimmed)
	def := osDefaultHomeDir()
	if def != "" && samePath(trimmed, def) {
		return "", fmt.Errorf("%w: target equals the default home", ErrHomePointerInvalid)
	}
	return trimmed, nil
}

// ReadHomePointerFile loads and validates the pointer file at path. A missing
// file returns (nil, nil) — absence is the default, not an error. A present
// but malformed file returns ErrHomePointerInvalid so diagnostics can tell the
// two apart; callers still fall back either way.
func ReadHomePointerFile(path string) (*HomePointerFile, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read home pointer: %w", err)
	}
	var pointer HomePointerFile
	if err := json.Unmarshal(raw, &pointer); err != nil {
		return nil, fmt.Errorf("%w: %s is not valid JSON: %v", ErrHomePointerInvalid, filepath.Base(path), err)
	}
	if pointer.Version != homePointerVersion {
		return nil, fmt.Errorf("%w: version %d unsupported (want %d)", ErrHomePointerInvalid, pointer.Version, homePointerVersion)
	}
	home, err := NormalizeHomePointerTarget(pointer.Home)
	if err != nil {
		return nil, fmt.Errorf("%w: %s records an unusable home: %v", ErrHomePointerInvalid, filepath.Base(path), err)
	}
	pointer.Home = home
	return &pointer, nil
}

// WriteHomePointerFile atomically writes the pointer: temp file in the same
// directory + rename, so a crash mid-write can never leave a half-written
// pointer that would send the next launch somewhere unintended (risk 2:
// 指针损坏回落 — the old pointer either survives whole or is replaced whole).
func WriteHomePointerFile(path string, home string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("home pointer path unavailable")
	}
	normalized, err := NormalizeHomePointerTarget(home)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create home pointer dir: %w", err)
	}
	payload, err := json.MarshalIndent(HomePointerFile{
		Version:   homePointerVersion,
		Home:      normalized,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
		Task846:   true,
	}, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, payload, 0o644); err != nil {
		return fmt.Errorf("write home pointer temp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("swap home pointer: %w", err)
	}
	return nil
}

// HomePointerApplyStatus tells callers what the startup pointer application
// did. Zero-behaviour default is StatusNone.
type HomePointerApplyStatus string

const (
	// HomePointerStatusNone: no pointer file (or nothing to apply) — the
	// process behaves exactly as before task 846.
	HomePointerStatusNone HomePointerApplyStatus = "none"
	// HomePointerStatusEnvOverride: REASONIX_HOME was set explicitly; the
	// pointer (if any) is ignored.
	HomePointerStatusEnvOverride HomePointerApplyStatus = "env_override"
	// HomePointerStatusApplied: the pointer was honored and REASONIX_HOME
	// was set to the recorded home.
	HomePointerStatusApplied HomePointerApplyStatus = "applied"
	// HomePointerStatusFallback: a pointer file exists but could not be
	// honored (corrupt, unknown version, target directory missing) — the
	// process falls back to the OS-default home (risk 2 containment).
	HomePointerStatusFallback HomePointerApplyStatus = "fallback"
)

// ApplyHomePointerEnv runs at the very start of the desktop/CLI process, before
// any path-dependent subsystem (logging included) resolves the home. It is the
// only writer of REASONIX_HOME from the pointer file:
//
//	env set            -> ignore pointer (StatusEnvOverride)
//	no pointer         -> no-op (StatusNone; the default)
//	pointer + live dir -> set REASONIX_HOME (StatusApplied)
//	pointer, unusable  -> ignore (StatusFallback; default home wins)
func ApplyHomePointerEnv() (HomePointerApplyStatus, string) {
	path := DefaultHomePointerPath()
	if path == "" {
		return HomePointerStatusNone, ""
	}
	if IsolatedHomeDir() != "" {
		return HomePointerStatusEnvOverride, ""
	}
	pointer, err := ReadHomePointerFile(path)
	if err != nil {
		return HomePointerStatusFallback, err.Error()
	}
	if pointer == nil {
		return HomePointerStatusNone, ""
	}
	if info, statErr := os.Stat(pointer.Home); statErr != nil || !info.IsDir() {
		return HomePointerStatusFallback, fmt.Sprintf("pointer home %q is missing", pointer.Home)
	}
	if err := os.Setenv("REASONIX_HOME", pointer.Home); err != nil {
		return HomePointerStatusFallback, err.Error()
	}
	return HomePointerStatusApplied, pointer.Home
}
