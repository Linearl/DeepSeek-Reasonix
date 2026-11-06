package control

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"reasonix/internal/evidence"
	"reasonix/internal/fileutil"
)

// goalMachineSnapshot is an in-memory rollback point for durable Goal updates.
// Persistence paths and mutexes are deliberately excluded.
type goalMachineSnapshot struct {
	goal                   string
	status                 string
	scopeID                string
	deliveryCheckpoint     evidence.DeliveryCheckpoint
	block                  string
	strict                 bool
	budgetClass            string
	turnsUsed              int
	turnsLimit             int
	tokensUsed             int
	requestsUsed           int
	workDurationMs         int64
	tokensLimit            int
	noProgressTurns        int
	noProgressLimit        int
	lastContinuationReason string
	lastEvaluatorReason    string
	stopCause              string
	lastStopCause          string
	budgetExtensions       int
	progressEvidence       []string
	stateExtra             map[string]json.RawMessage
}

func (g *goalMachine) capture() goalMachineSnapshot {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.captureLocked()
}

func (g *goalMachine) captureLocked() goalMachineSnapshot {
	return goalMachineSnapshot{
		goal: g.goal, status: g.status,
		scopeID: g.scopeID, deliveryCheckpoint: g.deliveryCheckpoint,
		block: g.block, strict: g.strict,
		budgetClass: g.budgetClass, turnsUsed: g.turnsUsed,
		turnsLimit: g.turnsLimit, tokensUsed: g.tokensUsed,
		requestsUsed:   g.requestsUsed,
		workDurationMs: g.workDurationMs,
		tokensLimit:    g.tokensLimit, noProgressTurns: g.noProgressTurns,
		noProgressLimit:        g.noProgressLimit,
		lastContinuationReason: g.lastContinuationReason,
		lastEvaluatorReason:    g.lastEvaluatorReason,
		stopCause:              g.stopCause, budgetExtensions: g.budgetExtensions,
		lastStopCause:    g.lastStopCause,
		progressEvidence: append([]string(nil), g.progressEvidence...),
		stateExtra:       cloneGoalStateExtra(g.stateExtra),
	}
}

func (g *goalMachine) restore(snapshot goalMachineSnapshot) {
	g.mu.Lock()
	g.goal, g.status = snapshot.goal, snapshot.status
	g.scopeID = snapshot.scopeID
	g.deliveryCheckpoint, g.block = snapshot.deliveryCheckpoint, snapshot.block
	g.strict = snapshot.strict
	g.budgetClass = snapshot.budgetClass
	g.turnsUsed, g.turnsLimit = snapshot.turnsUsed, snapshot.turnsLimit
	g.tokensUsed, g.tokensLimit = snapshot.tokensUsed, snapshot.tokensLimit
	g.requestsUsed = snapshot.requestsUsed
	g.workDurationMs = snapshot.workDurationMs
	g.noProgressTurns, g.noProgressLimit = snapshot.noProgressTurns, snapshot.noProgressLimit
	g.lastContinuationReason = snapshot.lastContinuationReason
	g.lastEvaluatorReason = snapshot.lastEvaluatorReason
	g.stopCause = snapshot.stopCause
	g.lastStopCause = snapshot.lastStopCause
	g.budgetExtensions = snapshot.budgetExtensions
	g.progressEvidence = append([]string(nil), snapshot.progressEvidence...)
	g.stateExtra = cloneGoalStateExtra(snapshot.stateExtra)
	g.bumpEpochLocked(goalEpochRollback)
	g.mu.Unlock()
}

func (g *goalMachine) writeStateErr(path string, data []byte) error {
	if path == "" || data == nil {
		return nil
	}
	g.writeMu.Lock()
	defer g.writeMu.Unlock()
	return writeGoalStateData(path, data)
}

func (g *goalMachine) writeStateAtEpoch(epoch uint64, todos []evidence.TodoItem) (bool, error) {
	g.writeMu.Lock()
	defer g.writeMu.Unlock()
	g.mu.Lock()
	if g.continuationEpoch != epoch {
		g.mu.Unlock()
		return false, nil
	}
	path, data, ok := g.buildStateLocked(todos)
	g.mu.Unlock()
	if !ok {
		return true, nil
	}
	return true, writeGoalStateData(path, data)
}

func writeGoalStateData(path string, data []byte) error {
	// tl1317: fail fast on bytes that are not valid JSON — persisting them
	// would poison the sidecar itself and every later marshalGoalState merge
	// that reads its unknown fields back. Callers treat the error as a
	// best-effort warn, so failing here loses nothing that worked before.
	if len(data) > 0 && !json.Valid(data) {
		return fmt.Errorf("control: refusing to write goal state %s: data is not valid JSON", filepath.Base(path))
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return fileutil.AtomicWriteFile(path, data, 0o644)
}
