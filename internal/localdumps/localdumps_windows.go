//go:build windows

package localdumps

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// Root is the hive handle type on Windows (alias so callers can pass
// registry.LOCAL_MACHINE / registry.CURRENT_USER directly).
type Root = registry.Key

// Ensure writes the three per-application LocalDumps values under the given
// root hive. Callers pass registry.LOCAL_MACHINE for the documented
// machine-wide collection (needs elevation) or registry.CURRENT_USER for the
// task-188 best-effort face; tests pass CURRENT_USER because the value logic
// must be exercisable without admin rights.
//
// DumpFolder is written as REG_EXPAND_SZ, the documented value type, and
// accepts %LOCALAPPDATA%-style values. The call is idempotent.
func Ensure(root Root, imageName string, cfg Config) error {
	key, _, err := registry.CreateKey(root, keyPath(imageName), registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("localdumps: create %s: %w", keyPath(imageName), err)
	}
	defer key.Close()
	if err := key.SetExpandStringValue("DumpFolder", cfg.DumpFolder); err != nil {
		return fmt.Errorf("localdumps: DumpFolder: %w", err)
	}
	if err := key.SetDWordValue("DumpType", cfg.DumpType); err != nil {
		return fmt.Errorf("localdumps: DumpType: %w", err)
	}
	if err := key.SetDWordValue("DumpCount", cfg.DumpCount); err != nil {
		return fmt.Errorf("localdumps: DumpCount: %w", err)
	}
	return nil
}

// Read returns the registration under the given root. ErrNotExist (wrapped)
// means the image has no LocalDumps key there.
func Read(root Root, imageName string) (Config, error) {
	key, err := registry.OpenKey(root, keyPath(imageName), registry.QUERY_VALUE)
	if err != nil {
		return Config{}, fmt.Errorf("localdumps: open %s: %w", keyPath(imageName), err)
	}
	defer key.Close()
	var cfg Config
	if cfg.DumpFolder, _, err = key.GetStringValue("DumpFolder"); err != nil {
		return Config{}, fmt.Errorf("localdumps: DumpFolder: %w", err)
	}
	var dumpType, dumpCount uint64
	if dumpType, _, err = key.GetIntegerValue("DumpType"); err != nil {
		return Config{}, fmt.Errorf("localdumps: DumpType: %w", err)
	}
	if dumpCount, _, err = key.GetIntegerValue("DumpCount"); err != nil {
		return Config{}, fmt.Errorf("localdumps: DumpCount: %w", err)
	}
	cfg.DumpType = uint32(dumpType)
	cfg.DumpCount = uint32(dumpCount)
	return cfg, nil
}

// Delete removes the per-application key. The key must be empty (which it is
// — per-application LocalDumps keys carry only values), and machine-level
// deletion needs the same elevation the write needed.
func Delete(root Root, imageName string) error {
	if err := registry.DeleteKey(root, keyPath(imageName)); err != nil {
		return fmt.Errorf("localdumps: delete %s: %w", keyPath(imageName), err)
	}
	return nil
}

// DefaultDumpFolder resolves %LOCALAPPDATA%\CrashDumps (the WER default
// location), mirroring desktop's task-188 resolution.
func DefaultDumpFolder() (string, error) {
	if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
		return filepath.Join(localAppData, "CrashDumps"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("localdumps: no LOCALAPPDATA and no home: %w", err)
	}
	return filepath.Join(home, "AppData", "Local", "CrashDumps"), nil
}

// IsAccessDenied reports whether err is the registry's access-denied failure
// (the non-elevated write against HKLM).
func IsAccessDenied(err error) bool {
	return errors.Is(err, windows.ERROR_ACCESS_DENIED)
}

func keyPath(imageName string) string {
	return KeyBase + `\` + imageName
}
