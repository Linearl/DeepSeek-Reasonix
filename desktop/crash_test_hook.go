package main

import (
	"log"
	"os"
)

const (
	// crashTestModeEnv asks the process to crash deliberately (task 188): the
	// automated acceptance for "a human-triggered panic leaves evidence in the
	// log/dump channels" instead of waiting for the next real production crash.
	crashTestModeEnv = "REASONIX_CRASH_TEST"
	// crashTestFileEnv pins the SetCrashOutput destination so a re-exec'd test
	// binary writes its death artifact somewhere the parent can read.
	crashTestFileEnv = "REASONIX_CRASH_TEST_FILE"
)

// runCrashTest triggers the deliberate crash the acceptance calls for. Empty
// (the default everywhere) is a no-op, so production behavior is byte-for-byte
// unchanged. "panic" raises an unrecovered panic in the calling goroutine: the
// runtime mirrors it through debug.SetCrashOutput, which is the channel that
// must be proven to work. An unknown mode logs and returns — never crash a
// process on a typo.
func runCrashTest(mode string) {
	switch mode {
	case "":
		return
	case "panic":
		panic("reasonix crash test (task 188): deliberate unrecovered panic")
	default:
		log.Printf("[crash] %s=%q unknown mode, ignoring", crashTestModeEnv, mode)
	}
}

// crashTestMode reads the deliberate-crash mode, or "" when unset.
func crashTestMode() string {
	return os.Getenv(crashTestModeEnv)
}
