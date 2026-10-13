package homemigrate

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
)

// EnvFileName is the credential file's name inside the Reasonix home.
const EnvFileName = ".env"

// CopyEnvFile copies the credential .env to target and REDOES its access
// permissions from scratch (task 846: 凭据 .env 搬运重做 ACL). A plain byte
// copy would either inherit the target directory's permissive ACEs (Windows)
// or, with some copy strategies, carry stale ownership metadata. The migration
// must end with permissions rebuilt from the current identity:
//
//	Windows: DACL replaced with a protected descriptor granting FULL control
//	         to the current user + SYSTEM only, inheritance OFF (so a parent
//	         dir's "Everyone" ACEs can never reappear over the copy).
//	Other:   chmod 0600 (owner rw, nothing else).
//
// The source file is only ever read.
func CopyEnvFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if err := writeFileSync(dst, data, info.Mode().Perm()); err != nil {
		return err
	}
	return RedoEnvACL(dst)
}

// EnvFileFingerprint is the SHA-256 hex digest used by the verify phase to
// pin the credential copy.
func EnvFileFingerprint(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
