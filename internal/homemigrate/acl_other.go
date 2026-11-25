//go:build !windows

package homemigrate

import (
	"errors"
	"os"
)

// RedoEnvACL tightens the copied .env to owner-only (0600) on platforms
// without Windows ACLs. Same contract as the Windows variant: permissions are
// rebuilt from the current identity, never inherited or merged.
func RedoEnvACL(path string) error {
	return os.Chmod(path, 0o600)
}

// errACLProbeWindowsOnly is returned by the non-windows EnvACLOnDisk stub:
// SDDL/protected-DACL introspection has no analogue outside Windows.
var errACLProbeWindowsOnly = errors.New("EnvACLOnDisk is a Windows-only ACL probe (no SDDL on this platform)")

// EnvACLOnDisk mirrors the Windows-only ACL probe so non-windows builds
// compile (tl1808: migrate_test.go references it behind a runtime.GOOS guard,
// but Go still type-checks the call on every platform). The guard means the
// stub is unreachable at runtime; if it ever is reached, fail loudly with a
// named error instead of silently faking an ACL reading.
func EnvACLOnDisk(path string) (sddl string, protected bool, err error) {
	_ = path
	return "", false, errACLProbeWindowsOnly
}
