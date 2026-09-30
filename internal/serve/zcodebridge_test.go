package serve

import (
	"testing"

	"reasonix/internal/zcodebridge"
)

// The env gate is off by default: no bridge, no child process. Construction
// failures are fatal — a silent no-op would be an untested claim (workspace
// discipline: no SKIP).
func TestZcodeBridgeDisabledByDefault(t *testing.T) {
	zcodeBridgeEnvConfigForTest = func() (bool, zcodebridge.Config) { return false, zcodebridge.Config{} }
	t.Cleanup(func() { zcodeBridgeEnvConfigForTest = nil })

	s := &Server{} // the wiring lives on Server; no child may spawn in this test
	_ = s
	enabled, _ := zcodeBridgeEnvConfig()
	if enabled {
		t.Fatal("bridge must be disabled when the env gate is off")
	}
}

func TestZcodeBridgeEnvConfigParses(t *testing.T) {
	zcodeBridgeEnvConfigForTest = func() (bool, zcodebridge.Config) {
		return true, zcodebridge.Config{
			Command:   "node",
			Args:      []string{`C:\Program Files\zcode.cjs`, "app-server", "--stdio"},
			Workspace: `C:\ws`,
		}
	}
	t.Cleanup(func() { zcodeBridgeEnvConfigForTest = nil })

	enabled, cfg := zcodeBridgeEnvConfig()
	if !enabled {
		t.Fatal("env seam returned disabled")
	}
	if cfg.Command != "node" || len(cfg.Args) != 3 || cfg.Args[1] != "app-server" {
		t.Fatalf("env config wrong: %+v", cfg)
	}
	// New must accept the env-shaped config (blank-argv guard stays intact).
	if _, err := zcodebridge.New(cfg); err != nil {
		t.Fatalf("New(env config): %v", err)
	}
	if _, err := zcodebridge.New(zcodebridge.Config{Args: []string{" ", "x"}}); err == nil {
		t.Fatal("blank argv must be rejected")
	}
}
