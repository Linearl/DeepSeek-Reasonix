//go:build windows

package localdumps

import (
	"errors"
	"testing"

	"golang.org/x/sys/windows/registry"
)

// The value logic must be fully exercisable without admin rights, so the tests
// run the exact Ensure/Read/Delete path against CURRENT_USER — the same code
// EnsureMachine drives against LOCAL_MACHINE in production.
func TestEnsureReadDeleteRoundtrip(t *testing.T) {
	const image = "reasonix-localdumps-selftest.exe"
	cfg := DesktopConfig(`C:\Users\nobody\AppData\Local\CrashDumps`)
	if err := Ensure(registry.CURRENT_USER, image, cfg); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	t.Cleanup(func() { _ = Delete(registry.CURRENT_USER, image) })

	got, err := Read(registry.CURRENT_USER, image)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got != cfg {
		t.Fatalf("read = %+v, want %+v", got, cfg)
	}
	// DumpFolder must land as REG_EXPAND_SZ, the documented value type.
	key, err := registry.OpenKey(registry.CURRENT_USER, KeyBase+`\`+image, registry.QUERY_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	defer key.Close()
	if _, kind, _ := key.GetStringValue("DumpFolder"); kind != registry.EXPAND_SZ {
		t.Fatalf("DumpFolder value kind = %v, want ExpandString", kind)
	}

	if err := Delete(registry.CURRENT_USER, image); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := Read(registry.CURRENT_USER, image); !errors.Is(err, registry.ErrNotExist) {
		t.Fatalf("read after delete = %v, want ErrNotExist", err)
	}
}

func TestDesktopConfigValues(t *testing.T) {
	cfg := DesktopConfig("X")
	// Task-188 semantics: minidump, capped rolling set.
	if cfg.DumpType != 2 || cfg.DumpCount != 10 {
		t.Fatalf("DesktopConfig = %+v, want DumpType=2 DumpCount=10", cfg)
	}
}
