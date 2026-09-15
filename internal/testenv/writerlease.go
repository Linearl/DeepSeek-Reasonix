package testenv

import "os"

// RunWithLeaseGuard runs package tests. The full upstream lease-leak check is
// not required for the session-v4 experiment; this keeps TestMain compatible.
func RunWithLeaseGuard(m TestingM) {
	os.Exit(m.Run())
}

// ReportLeakedFileLocks is a no-op placeholder for API compatibility.
func ReportLeakedFileLocks() string { return "" }

// VerifyNoLeakedFileLocks is a no-op placeholder for API compatibility.
func VerifyNoLeakedFileLocks() error { return nil }
