package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoadDoesNotMaterializeDeletedProvider (tl1654 round 3) is the load-path
// regression pin for the "MiMo API" group that kept coming back after the user
// deleted the connection.
//
// The machine that reported it had no `[[providers]]` section for the deleted
// connection, but its access list still named it — a project-level
// reasonix.toml entry the settings page can never rewrite. The loader used to
// treat such a name as a request to rebuild the connection, so the deleted
// provider came back with the credential still on the machine, showed up in
// the composer dropdown under its old label, and kept historical sessions
// pinned to it.
//
// The pin uses only the public load path so it can be replayed against the
// pre-fix baseline.
func TestLoadDoesNotMaterializeDeletedProvider(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	t.Setenv("REASONIX_CREDENTIALS_STORE", "file")
	body := `language = "zh"
default_model = "kept/kept-model"

[desktop]
provider_access = ["kept", "mimo-api"]

[[providers]]
name = "kept"
kind = "openai"
base_url = "http://127.0.0.1:11434/v1"
models = ["kept-model"]
`
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	// The credential of the deleted connection is still on the machine.
	if err := os.WriteFile(filepath.Join(home, ".env"), []byte("MIMO_API_KEY=sk-test\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	c, err := LoadForRoot(t.TempDir())
	if err != nil {
		t.Fatalf("LoadForRoot: %v", err)
	}
	if _, ok := c.Provider("mimo-api"); ok {
		t.Fatal("a deleted provider must not be rebuilt from a stale access reference")
	}
	if _, ok := c.ResolveModel("mimo-api/mimo-v2.5-pro"); ok {
		t.Fatal("a deleted provider's model ref must not resolve")
	}
	resolved, fallback, ok := c.ResolveModelWithFallback("mimo-api/mimo-v2.5-pro")
	if !ok || !fallback || resolved != "kept/kept-model" {
		t.Fatalf("historical ref fallback = %q (fallback=%v ok=%v), want kept/kept-model", resolved, fallback, ok)
	}
	if _, ok := c.Provider("kept"); !ok {
		t.Fatal("the configured provider must survive")
	}
}
