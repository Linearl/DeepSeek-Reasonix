package agent

import (
	"context"
	"errors"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// Task 283: the readiness gate's pause branch must only stop turns a human can
// rescue. Before this, only the agent-level autopilot flag counted as
// unattended, so an auto/yolo session (approval already delegated to policy)
// with an unmet delivery contract returned FinalReadinessError on the first
// final answer and parked on the "delivery incomplete" recovery card — a card
// nobody was going to press. These cases pin the widened family against the
// unchanged ask/interactive behavior.
func TestReadinessGateUnattendedFamilyDoesNotStopTheRun(t *testing.T) {
	newReg := func() *tool.Registry {
		reg := tool.NewRegistry()
		reg.Add(fakeReadFileTool{})
		reg.Add(fakeWriterTool{}) // writer-capable registry keeps mutation expected
		return reg
	}
	finalText := []provider.Chunk{{Type: provider.ChunkText, Text: "done, all fixed"}, {Type: provider.ChunkDone}}
	// Two write+final cycles plus one extra final: the advisory path spends up
	// to maxReadinessAdvisories (2) retry turns before the switch falls
	// through and the run ends without a FinalReadinessError.
	turns := func() [][]provider.Chunk {
		return [][]provider.Chunk{
			{toolCallChunk("w1", "fake_write", `{"path":"a.go","content":"package a"}`), {Type: provider.ChunkDone}},
			finalText,
			{toolCallChunk("w2", "fake_write", `{"path":"a.go","content":"package b"}`), {Type: provider.ChunkDone}},
			finalText,
			finalText,
		}
	}

	cases := []struct {
		name        string
		ctx         func() context.Context
		wantStopped bool
	}{
		{name: "ask stays interactive", ctx: func() context.Context {
			return WithToolApprovalMode(context.Background(), "ask")
		}, wantStopped: true},
		{name: "no mode stays interactive", ctx: context.Background, wantStopped: true},
		{name: "auto delegates to policy", ctx: func() context.Context {
			return WithToolApprovalMode(context.Background(), "auto")
		}},
		{name: "yolo delegates to policy", ctx: func() context.Context {
			return WithToolApprovalMode(context.Background(), "yolo")
		}},
		{name: "unattended host run", ctx: func() context.Context {
			return WithUnattendedRun(WithToolApprovalMode(context.Background(), "ask"))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prov := &scriptedProvider{name: "p", turns: turns()}
			a := New(prov, newReg(), NewSession("sys"), Options{}, event.Discard)
			err := a.Run(withClosedLoopContext(tc.ctx()), "fix the crash in a.go")
			var readinessErr *FinalReadinessError
			gotReadiness := errors.As(err, &readinessErr)
			if tc.wantStopped {
				if !gotReadiness {
					t.Fatalf("interactive posture must keep the recovery card, err = %v", err)
				}
				if readinessErr.Attempts != 1 {
					t.Fatalf("attempts = %d, want 1 (existing no-hidden-retry contract)", readinessErr.Attempts)
				}
				return
			}
			if gotReadiness {
				t.Fatalf("unattended posture stopped on the card: %v", readinessErr)
			}
			if err != nil {
				t.Fatalf("run failed outside the readiness card: %v", err)
			}
			// Honest failure, not fake delivery: the run ends without a card,
			// but the unmet contract is still retained for the next turn and
			// after-the-fact review. (The durable LocalOnly marker is
			// intentionally stale once a later retry turn lands — see
			// pendingFinalReadinessRecovery — so the live carrier is the
			// pending bit plus the ReadinessAdvised audit the advisory branch
			// records every time it fires.)
			if !a.pending.finalReadinessRecovery {
				t.Fatal("advisory fall-through dropped the pending gap bit — unmet contract must stay visible")
			}
		})
	}
}
