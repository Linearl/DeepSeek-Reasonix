package agent

import (
	"context"
	"encoding/json"
)

// readProbe is a read-only tool taking a path, so its receipt is classified the
// way a real reader's is — Read=true with Paths set — which is what progress
// scoring treats as observational evidence. Shared by the run-budget and
// task-budget-gate suites.
//
// The two runaway repros this file once hosted (the read-only wanderer and the
// repeated read tripping the progress-guard notice) pinned the no-progress
// guards retired by task 329 (70ab152b1, upstream #9766/#10223): the guard no
// longer fires any notice and must never stop a read-only run, which
// TestReadOnlyLongZeroGainRoundsNeverStopTheTurn and
// TestOutcomeVersusLegacyOnTheRunawayShape now pin on the replacement
// machinery.
type readProbe struct{}

func (readProbe) Name() string        { return "read_file" }
func (readProbe) Description() string { return "read a file" }
func (readProbe) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`)
}
func (readProbe) ReadOnly() bool { return true }
func (readProbe) Execute(context.Context, json.RawMessage) (string, error) {
	return "package main\n\nfunc main() {}\n", nil
}
