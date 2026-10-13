//go:build !windows

package homemigrate

import "os"

// RedoEnvACL tightens the copied .env to owner-only (0600) on platforms
// without Windows ACLs. Same contract as the Windows variant: permissions are
// rebuilt from the current identity, never inherited or merged.
func RedoEnvACL(path string) error {
	return os.Chmod(path, 0o600)
}
