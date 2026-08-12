package agent

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/provider"
)

func TestSaveConflictRecoveryBranchAtCapStaysBounded(t *testing.T) {
	dir := t.TempDir()
	path, stale := divergedSessionPair(t, dir, "session.jsonl")
	stampRecoveryMeta(t, path, SessionRecoveryMaxDepth)

	// Depth-cap path once per autosave tick with a growing transcript (#8342).
	current := path
	for i := range 6 {
		stale.Add(provider.Message{Role: provider.RoleUser, Content: fmt.Sprintf("tick %d", i)})
		if _, err := stale.SaveRecoveryBranch(RecoveryBranchOptions{OriginalPath: current}); !errors.Is(err, ErrSessionRecoveryDepthExceeded) {
			t.Fatalf("tick %d: SaveRecoveryBranch err = %v, want ErrSessionRecoveryDepthExceeded", i, err)
		}
		info, err := stale.SaveConflictRecoveryBranch(RecoveryBranchOptions{OriginalPath: current})
		if err != nil {
			t.Fatalf("tick %d: SaveConflictRecoveryBranch: %v", i, err)
		}
		current = info.Path
		if i == 0 {
			// In-place rewrites must preserve markers the controller does not re-transplant.
			if err := SetSessionInFlightTurn(current, InFlightTurnMeta{StartMessageIndex: 2}); err != nil {
				t.Fatalf("SetSessionInFlightTurn: %v", err)
			}
		}
	}

	meta, ok, err := LoadBranchMeta(current)
	if err != nil || !ok {
		t.Fatalf("LoadBranchMeta ok=%v err=%v", ok, err)
	}
	if meta.InFlightTurn == nil || meta.InFlightTurn.StartMessageIndex != 2 {
		t.Fatalf("in-flight turn marker = %+v, want StartMessageIndex 2", meta.InFlightTurn)
	}

	matches, err := filepath.Glob(filepath.Join(dir, "*-recovery-*.jsonl"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	var isolated []string
	for _, m := range matches {
		if !strings.HasSuffix(m, ".events.jsonl") {
			isolated = append(isolated, m)
		}
	}
	if len(isolated) != 1 {
		t.Fatalf("isolated copies = %d, want 1: %v", len(isolated), isolated)
	}
}

// Sibling lineages get distinct isolated copies; a copy is a fixed point.
func TestFixedWriterRecoverySessionPathSeparatesSiblingLineages(t *testing.T) {
	dir := t.TempDir()
	root := fixedWriterRecoverySessionPath(filepath.Join(dir, "session.jsonl"))
	fork := fixedWriterRecoverySessionPath(filepath.Join(dir, "session-recovery-abcd1234abcd1234.jsonl"))
	if root == fork {
		t.Fatalf("session and its fork share an isolated copy: %s", root)
	}
	if again := fixedWriterRecoverySessionPath(root); again != root {
		t.Fatalf("isolated copy is not a fixed point: %s -> %s", root, again)
	}
}
