package agent

import (
	"context"
	"fmt"
	"strings"

	"reasonix/internal/provider"
)

// MigrationMessageStream is the bounded legacy migration result.
type MigrationMessageStream struct {
	Messages   int
	FromEvents bool
}

// LoadSessionModelSelection reads the session model from the branch sidecar.
func LoadSessionModelSelection(sessionPath string) (string, string, bool) {
	meta, ok, err := LoadBranchMeta(sessionPath)
	if err != nil || !ok {
		return "", "", false
	}
	model := strings.TrimSpace(meta.Model)
	if model == "" {
		return "", "", false
	}
	return model, meta.ModelIdentity, true
}

// StreamSessionMessagesForMigration emits a frozen legacy transcript for v4 import.
func StreamSessionMessagesForMigration(ctx context.Context, path, headID string, reset func() error, emit func(provider.Message) error) (MigrationMessageStream, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if reset == nil || emit == nil {
		return MigrationMessageStream{}, fmt.Errorf("session migration stream requires reset and emit callbacks")
	}
	if err := reset(); err != nil {
		return MigrationMessageStream{}, err
	}

	// tl1280: the DAG head-materialization entry (LoadSessionHeadForMigration)
	// left with the write-path slow lane. A frozen legacy headID used to replay
	// one head of a schema-2 log; with the slow lane gone the v4 bridge can only
	// import the flat transcript the .jsonl checkpoint already holds, so the
	// headID argument is accepted and ignored (the target ID still keys off it,
	// preserving the per-head migration map rows).
	loaded, err := LoadSession(path)
	if err != nil {
		return MigrationMessageStream{}, err
	}

	messages := loaded.Snapshot()
	fromEvents := false
	if _, ok := loaded.Head(); ok {
		fromEvents = true
	}
	for _, message := range messages {
		if err := ctx.Err(); err != nil {
			return MigrationMessageStream{FromEvents: fromEvents}, err
		}
		if err := emit(message); err != nil {
			return MigrationMessageStream{FromEvents: fromEvents}, err
		}
	}
	return MigrationMessageStream{Messages: len(messages), FromEvents: fromEvents}, nil
}
