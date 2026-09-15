package config

import "testing"

func TestSessionStorageModeDefaultsToLegacy(t *testing.T) {
	t.Setenv("REASONIX_SESSION_STORAGE", "")
	if got := SessionStorageMode(nil); got != "legacy" {
		t.Fatalf("nil config mode = %q, want legacy", got)
	}
	cfg := Default()
	if got := SessionStorageMode(cfg); got != "legacy" {
		t.Fatalf("default config mode = %q, want legacy", got)
	}
}

func TestSessionStorageModeEnvAndConfig(t *testing.T) {
	cfg := Default()
	cfg.SessionStorage = "v4"
	if got := SessionStorageMode(cfg); got != "v4" {
		t.Fatalf("config v4 mode = %q, want v4", got)
	}
	t.Setenv("REASONIX_SESSION_STORAGE", "legacy")
	if got := SessionStorageMode(cfg); got != "legacy" {
		t.Fatalf("env override = %q, want legacy", got)
	}
	t.Setenv("REASONIX_SESSION_STORAGE", "v4")
	if got := SessionStorageMode(cfg); got != "v4" {
		t.Fatalf("env v4 = %q, want v4", got)
	}
}

func TestActiveSessionDirFollowsMode(t *testing.T) {
	cfg := Default()
	legacy := ActiveSessionDir(cfg)
	cfg.SessionStorage = "v4"
	t.Setenv("REASONIX_SESSION_STORAGE", "")
	v4 := ActiveSessionDir(cfg)
	if legacy == "" || v4 == "" {
		t.Fatal("empty session dirs")
	}
	if legacy == v4 {
		t.Fatalf("legacy and v4 dirs should differ: %q", legacy)
	}
	if filepathBase(v4) != "sessions-v4" || filepathBase(legacy) != "sessions" {
		t.Fatalf("unexpected dirs legacy=%q v4=%q", legacy, v4)
	}
}

func filepathBase(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' || p[i] == '\\' {
			return p[i+1:]
		}
	}
	return p
}
