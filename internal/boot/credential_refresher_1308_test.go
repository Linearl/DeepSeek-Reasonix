package boot

import (
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/config"
)

// TestProviderCredentialRefresherReadsRotatedStore pins the 任务1308 boot
// wiring: the re-auth hook stamped into provider Extra re-reads the global
// credentials store fresh on every call, so a credential rotated after boot —
// the exact mimo-token-plan incident — is picked up without any runtime
// rebuild. Entries without an api_key_env get no hook.
func TestProviderCredentialRefresherReadsRotatedStore(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("AppData", filepath.Join(home, "AppData"))

	credPath := config.UserCredentialsPath()
	if credPath == "" {
		t.Skip("user credentials path unresolved on this platform")
	}
	if err := os.MkdirAll(filepath.Dir(credPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(credPath, []byte("TL1308_BOOT_KEY=token-at-boot\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	entry := &config.ProviderEntry{Name: "mimo", Kind: "openai", BaseURL: "https://example.invalid/v1", Model: "mimo-test", APIKeyEnv: "TL1308_BOOT_KEY"}
	entry.ResolveAPIKeyForRoot(".")
	if entry.APIKey() != "token-at-boot" {
		t.Fatalf("entry.APIKey() = %q, want the boot-time value", entry.APIKey())
	}

	refresher := providerCredentialRefresher(entry)
	if refresher == nil {
		t.Fatal("entry with api_key_env must get a refresher")
	}

	// Rotation behind the running runtime: the store file changes on disk.
	if err := os.WriteFile(credPath, []byte("TL1308_BOOT_KEY=token-rotated\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, ok := refresher()
	if !ok || got != "token-rotated" {
		t.Fatalf("refresher after rotation = (%q, %v), want the rotated value", got, ok)
	}

	// The store dropping the key reports not-found, never the stale value.
	if err := os.WriteFile(credPath, []byte("# key removed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := refresher(); ok {
		t.Fatal("refresher must report not-found once the store drops the key")
	}
}

// TestProviderCredentialRefresherNilWithoutKeyEnv pins the off-state: no
// api_key_env (or nil entry) means no hook — custom setups keep today's
// fixed build-time key behaviour.
func TestProviderCredentialRefresherNilWithoutKeyEnv(t *testing.T) {
	if got := providerCredentialRefresher(nil); got != nil {
		t.Fatal("nil entry must yield no refresher")
	}
	if got := providerCredentialRefresher(&config.ProviderEntry{Name: "keyless"}); got != nil {
		t.Fatal("entry without api_key_env must yield no refresher")
	}
}
