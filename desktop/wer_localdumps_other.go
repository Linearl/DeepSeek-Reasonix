//go:build !windows

package main

// installWERLocalDumps is a no-op off Windows: WER LocalDumps is a Windows
// facility (task 188). Crash visibility elsewhere relies on
// debug.SetCrashOutput plus the rolling log, both platform-neutral.
func installWERLocalDumps() {}

func readWERLocalDumpsFolder() (string, error) { return "", nil }
