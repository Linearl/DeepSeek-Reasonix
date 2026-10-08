// Package localdumps owns the WER LocalDumps registration for the Reasonix
// desktop (task 404 face 1: "下次静默崩溃必留 native dump").
//
// Why this package exists next to desktop's task-188 registration: 188 writes
// HKCU — user-level, no elevation needed. The 2026-10-08 verification on the
// incident machine (task 404) showed HKCU registration does NOT collect
// dumps: four probe crashes (Go fail-fast and raw native access violation,
// one spawned by Task Scheduler outside any agent process tree) left zero
// dumps and zero WER events, while WER itself demonstrably processed other
// applications' crashes the same week. Microsoft's documented location for
// LocalDumps is HKLM, and per-user registration is not part of that contract.
// Machine-level registration therefore needs one elevated step, which this
// package's caller (the launcher `localdumps` subcommand) surfaces as an
// explicit, reversible user action instead of silently pretending success.
package localdumps

import "errors"

// KeyBase is the WER LocalDumps registry base key (relative to a root hive).
const KeyBase = `SOFTWARE\Microsoft\Windows\Windows Error Reporting\LocalDumps`

// Config mirrors the three per-application LocalDumps values. The desktop's
// task-188 semantics apply unchanged: minidumps only (DumpType 2 — enough for
// stacks/symbols, small enough that a crash loop cannot fill the disk) and a
// capped rolling set.
type Config struct {
	DumpFolder string
	DumpType   uint32
	DumpCount  uint32
}

// DesktopConfig is the registration the Reasonix desktop ships with (the
// task-188 values, machine-level scope).
func DesktopConfig(dumpFolder string) Config {
	return Config{DumpFolder: dumpFolder, DumpType: 2, DumpCount: 10}
}

// ErrUnsupported is returned on platforms without the WER facility.
var ErrUnsupported = errors.New("localdumps: only supported on windows")

// DesktopImageName is the executable whose crashes the dump face must cover.
const DesktopImageName = "reasonix-desktop.exe"

// VerifyImageName is the throwaway probe image used by the launcher's
// `localdumps verify` acceptance: a copy of the launcher binary itself under
// this name, so the probe's LocalDumps key never collides with a shipped
// executable and the whole probe (key, exe copy, dump) is reversible.
const VerifyImageName = "reasonix-localdumps-probe.exe"
