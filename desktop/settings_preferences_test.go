package main

// Task 262 install-fix: the intake batch landed its config layer while the
// Wails App-method layer was missed — the frontend called into a method that
// did not exist, every call failed at runtime, and the switch clicked without
// ever saving (the installed user's "cannot turn quick commands on" report).
// These interface assertions are compile-time pins: deleting any wrapper below
// fails the build instead of resurfacing as a silently-dead switch. The value
// round trip itself is pinned in internal/config (render tests cover the
// nil-means-on defaults and the explicit off/on saves).
type labIntakeSwitches interface {
	SetExperimentalQuickCommands(bool) error
	SetExperimentalCompactionParallel(bool) error
	SetExperimentalContextBudget(bool) error
	SetExperimentalResearchBudget(bool) error
	SetExperimentalQuestionSearch(bool) error
	SetExperimentalSubagentPolicy(bool) error
	SetExperimentalSubagentTps(bool) error
	SetExperimentalCompletionSummary(bool) error
}

var _ labIntakeSwitches = (*App)(nil)
