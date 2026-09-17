package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSessionStorageModeDefaultsToV3Only(t *testing.T) {
	t.Setenv("REASONIX_SESSION_STORAGE", "")
	if got := SessionStorageMode(nil); got != SessionStorageV3Only {
		t.Fatalf("nil config mode = %q, want %s", got, SessionStorageV3Only)
	}
	cfg := Default()
	if got := SessionStorageMode(cfg); got != SessionStorageV3Only {
		t.Fatalf("default config mode = %q, want %s", got, SessionStorageV3Only)
	}
}

// TestSessionStorageModeMigratesLegacyValues pins the settings.toml migration:
// the pre-task-155 spellings keep their exact behaviour on the four-mode scale.
// "v4" used to dual-write and prefer v4 on read, so it maps onto
// dual_write_read_v4 - mapping it onto a read-v3 mode would silently roll a
// user's read side back.
func TestSessionStorageModeMigratesLegacyValues(t *testing.T) {
	t.Setenv("REASONIX_SESSION_STORAGE", "")
	cfg := Default()
	cases := map[string]string{
		"":                            SessionStorageV3Only,
		"legacy":                      SessionStorageV3Only,
		"LEGACY":                      SessionStorageV3Only,
		"v4":                          SessionStorageDualWriteReadV4,
		"V4":                          SessionStorageDualWriteReadV4,
		SessionStorageV3Only:          SessionStorageV3Only,
		SessionStorageDualWriteReadV3: SessionStorageDualWriteReadV3,
		SessionStorageDualWriteReadV4: SessionStorageDualWriteReadV4,
		SessionStorageV4Only:          SessionStorageV4Only,
		"v5":                          SessionStorageV3Only,
	}
	for value, want := range cases {
		cfg.SessionStorage = value
		if got := SessionStorageMode(cfg); got != want {
			t.Fatalf("stored %q mode = %q, want %q", value, got, want)
		}
	}
}

func TestSessionStorageModeEnvAndConfig(t *testing.T) {
	cfg := Default()
	cfg.SessionStorage = "v4"
	if got := SessionStorageMode(cfg); got != SessionStorageDualWriteReadV4 {
		t.Fatalf("config v4 mode = %q, want %s", got, SessionStorageDualWriteReadV4)
	}
	t.Setenv("REASONIX_SESSION_STORAGE", "legacy")
	if got := SessionStorageMode(cfg); got != SessionStorageV3Only {
		t.Fatalf("env override = %q, want %s", got, SessionStorageV3Only)
	}
	t.Setenv("REASONIX_SESSION_STORAGE", "dual_write_read_v3")
	if got := SessionStorageMode(cfg); got != SessionStorageDualWriteReadV3 {
		t.Fatalf("env staged = %q, want %s", got, SessionStorageDualWriteReadV3)
	}
	t.Setenv("REASONIX_SESSION_STORAGE", "v4_only")
	if got := SessionStorageMode(cfg); got != SessionStorageV4Only {
		t.Fatalf("env v4_only = %q, want %s", got, SessionStorageV4Only)
	}
}

func TestActiveSessionDirFollowsMode(t *testing.T) {
	t.Setenv("REASONIX_SESSION_STORAGE", "")
	cfg := Default()
	legacy := ActiveSessionDir(cfg)
	cfg.SessionStorage = SessionStorageDualWriteReadV4
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
	// A staged mode still writes the mirror, so the v4 root stays the target.
	cfg.SessionStorage = SessionStorageDualWriteReadV3
	if got := ActiveSessionDir(cfg); got != v4 {
		t.Fatalf("staged mode dir = %q, want %q", got, v4)
	}
}

func TestSessionStorageGatesSplitReadAndWrite(t *testing.T) {
	t.Setenv("REASONIX_SESSION_STORAGE", "")
	cfg := Default()
	type gate struct {
		mode       string
		writesV4   bool
		readsV4    bool
		v3IsFrozen bool
	}
	gates := []gate{
		{SessionStorageV3Only, false, false, false},
		{SessionStorageDualWriteReadV3, true, false, false},
		{SessionStorageDualWriteReadV4, true, true, false},
		{SessionStorageV4Only, true, true, true},
	}
	for _, g := range gates {
		cfg.SessionStorage = g.mode
		if got := SessionV4WritesEnabled(cfg); got != g.writesV4 {
			t.Fatalf("%s writes v4 = %v, want %v", g.mode, got, g.writesV4)
		}
		if got := SessionV4ReadsEnabled(cfg); got != g.readsV4 {
			t.Fatalf("%s reads v4 = %v, want %v", g.mode, got, g.readsV4)
		}
		if got := SessionV3Frozen(cfg); got != g.v3IsFrozen {
			t.Fatalf("%s freezes v3 = %v, want %v", g.mode, got, g.v3IsFrozen)
		}
	}
}

func TestSessionStorageModesAreOrdered(t *testing.T) {
	want := []string{
		SessionStorageV3Only,
		SessionStorageDualWriteReadV3,
		SessionStorageDualWriteReadV4,
		SessionStorageV4Only,
	}
	if len(SessionStorageModes) != len(want) {
		t.Fatalf("mode count = %d, want %d", len(SessionStorageModes), len(want))
	}
	for i, mode := range want {
		if SessionStorageModes[i] != mode {
			t.Fatalf("mode[%d] = %q, want %q", i, SessionStorageModes[i], mode)
		}
		if got := SessionStorageModeIndex(mode); got != i {
			t.Fatalf("index(%s) = %d, want %d", mode, got, i)
		}
	}
}

func TestValidateSessionStorageTransition(t *testing.T) {
	cases := []struct {
		name    string
		current string
		target  string
		history []string
		wantErr bool
	}{
		{"same mode", SessionStorageV3Only, SessionStorageV3Only, nil, false},
		{"first stage", SessionStorageV3Only, SessionStorageDualWriteReadV3, nil, false},
		{"skips the dual-write stage", SessionStorageV3Only, SessionStorageV4Only, nil, true},
		{"second stage", SessionStorageDualWriteReadV3, SessionStorageDualWriteReadV4, nil, false},
		{"stage after staged history", SessionStorageDualWriteReadV4, SessionStorageV4Only, nil, false},
		{"v4_only after staged history", SessionStorageV3Only, SessionStorageV4Only,
			[]string{SessionStorageDualWriteReadV3, SessionStorageDualWriteReadV4}, false},
		{"downgrade from v4_only", SessionStorageV4Only, SessionStorageDualWriteReadV4, nil, false},
		{"downgrade to v3_only", SessionStorageDualWriteReadV4, SessionStorageV3Only, nil, false},
		{"legacy alias target", SessionStorageDualWriteReadV3, "v4", nil, false},
		{"unknown target", SessionStorageDualWriteReadV3, "v9", nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateSessionStorageTransition(tc.current, tc.target, tc.history)
			if tc.wantErr && err == nil {
				t.Fatalf("%s -> %s allowed, want refusal", tc.current, tc.target)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("%s -> %s refused: %v", tc.current, tc.target, err)
			}
		})
	}
}

func TestSessionStorageNeedsRestart(t *testing.T) {
	cases := []struct {
		from, to string
		want     bool
	}{
		{SessionStorageDualWriteReadV3, SessionStorageDualWriteReadV4, false},
		{SessionStorageDualWriteReadV4, SessionStorageDualWriteReadV3, false},
		{SessionStorageDualWriteReadV3, SessionStorageDualWriteReadV3, false},
		{SessionStorageV3Only, SessionStorageDualWriteReadV3, true},
		{SessionStorageDualWriteReadV4, SessionStorageV4Only, true},
		{SessionStorageV4Only, SessionStorageDualWriteReadV4, true},
	}
	for _, tc := range cases {
		if got := SessionStorageNeedsRestart(tc.from, tc.to); got != tc.want {
			t.Fatalf("restart(%s -> %s) = %v, want %v", tc.from, tc.to, got, tc.want)
		}
	}
}

// TestResolveSafeSessionStorageMode is the boot-time guard for a hand-edited
// settings.toml: v4_only without a dual-write stage in the audit trail is
// clamped instead of starting on an unproven transition.
func TestResolveSafeSessionStorageMode(t *testing.T) {
	mode, adjusted := ResolveSafeSessionStorageMode(SessionStorageV4Only, nil)
	if !adjusted || mode != SessionStorageDualWriteReadV4 {
		t.Fatalf("v4_only without history = %q adjusted=%v, want %s adjusted", mode, adjusted, SessionStorageDualWriteReadV4)
	}
	mode, adjusted = ResolveSafeSessionStorageMode(SessionStorageV4Only, []string{SessionStorageDualWriteReadV3})
	if adjusted || mode != SessionStorageV4Only {
		t.Fatalf("v4_only with staged history = %q adjusted=%v, want unchanged", mode, adjusted)
	}
	mode, adjusted = ResolveSafeSessionStorageMode(SessionStorageDualWriteReadV3, nil)
	if adjusted || mode != SessionStorageDualWriteReadV3 {
		t.Fatalf("staged mode = %q adjusted=%v, want unchanged", mode, adjusted)
	}
	mode, adjusted = ResolveSafeSessionStorageMode("nonsense", nil)
	if !adjusted || mode != SessionStorageV3Only {
		t.Fatalf("unknown mode = %q adjusted=%v, want %s adjusted", mode, adjusted, SessionStorageV3Only)
	}
	mode, adjusted = ResolveSafeSessionStorageMode("legacy", nil)
	if adjusted || mode != SessionStorageV3Only {
		t.Fatalf("legacy alias = %q adjusted=%v, want %s unchanged", mode, adjusted, SessionStorageV3Only)
	}
}

func TestSetSessionStorageNormalizesAndRefusesUnknown(t *testing.T) {
	cfg := Default()
	if err := cfg.SetSessionStorage("v4"); err != nil {
		t.Fatalf("SetSessionStorage(v4): %v", err)
	}
	if cfg.SessionStorage != SessionStorageDualWriteReadV4 {
		t.Fatalf("stored mode = %q, want %s", cfg.SessionStorage, SessionStorageDualWriteReadV4)
	}
	if err := cfg.SetSessionStorage("legacy"); err != nil {
		t.Fatalf("SetSessionStorage(legacy): %v", err)
	}
	if cfg.SessionStorage != SessionStorageV3Only {
		t.Fatalf("stored legacy = %q, want %s", cfg.SessionStorage, SessionStorageV3Only)
	}
	if err := cfg.SetSessionStorage("v9"); err == nil {
		t.Fatal("unknown mode accepted")
	}
}

func TestSessionStorageChangeLogRoundTrip(t *testing.T) {
	t.Setenv("REASONIX_STATE_HOME", t.TempDir())
	if changes, err := ReadSessionStorageChanges(); err != nil || len(changes) != 0 {
		t.Fatalf("empty log = %v err=%v", changes, err)
	}
	if err := AppendSessionStorageChange(SessionStorageChange{
		From: SessionStorageV3Only, To: SessionStorageDualWriteReadV3, Source: "settings-ui", Restart: true,
	}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := AppendSessionStorageChange(SessionStorageChange{
		From: SessionStorageDualWriteReadV3, To: SessionStorageV4Only, Source: "settings-ui", Restart: true,
	}); err != nil {
		t.Fatalf("append: %v", err)
	}
	changes, err := ReadSessionStorageChanges()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(changes) != 2 {
		t.Fatalf("changes = %d, want 2", len(changes))
	}
	if changes[0].From != SessionStorageV3Only || changes[0].To != SessionStorageDualWriteReadV3 {
		t.Fatalf("first change = %+v", changes[0])
	}
	if changes[1].At.IsZero() {
		t.Fatal("change timestamp not stamped")
	}
	history := SessionStorageHistoryModes()
	if len(history) != 4 {
		t.Fatalf("history = %v, want 4 entries", history)
	}
	mode, adjusted := ResolveSafeSessionStorageMode(SessionStorageV4Only, history)
	if adjusted || mode != SessionStorageV4Only {
		t.Fatalf("v4_only after logged dual write = %q adjusted=%v", mode, adjusted)
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

// TestSessionStorageSnapshotCountsBothStores pins the pre-switch inventory that
// every recorded mode change carries (task 155 "dump before switching").
func TestSessionStorageSnapshotCountsBothStores(t *testing.T) {
	t.Setenv("REASONIX_STATE_HOME", t.TempDir())
	t.Setenv("REASONIX_SESSION_STORAGE", "")
	v3Dir := SessionDir()
	v4Dir := SessionStoreDir()
	if v3Dir == "" || v4Dir == "" {
		t.Fatal("session directories unavailable")
	}
	if err := os.MkdirAll(v3Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(v4Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(v3Dir, "a.jsonl"), []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(v3Dir, "b.jsonl"), []byte("123"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(v4Dir, "manifest.json"), []byte("12"), 0o600); err != nil {
		t.Fatal(err)
	}
	snap := SessionStorageSnapshot()
	if snap.V3Count != 2 || snap.V3Bytes != 8 {
		t.Fatalf("v3 snapshot = %d files/%d bytes, want 2/8", snap.V3Count, snap.V3Bytes)
	}
	if snap.V4Count != 1 || snap.V4Bytes != 2 {
		t.Fatalf("v4 snapshot = %d files/%d bytes, want 1/2", snap.V4Count, snap.V4Bytes)
	}
}
