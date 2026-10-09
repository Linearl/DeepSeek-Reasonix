//go:build !windows

package main

// Non-windows stub for the suspend/resume acceptance harness (task 696): the
// experiment itself needs NtSuspendProcess and a win32 message loop, so it
// only exists on windows. main_test.go consults these hooks portably.

const hangSuspendChildScenarioEnv = "REASONIX_HANG_TEST_SCENARIO"

func hangSuspendChildScenario() string { return "" }

func runHangSuspendChildScenario(string) int { return 0 }
