package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/extension"
	"reasonix/internal/extension/protocol"
	"reasonix/internal/provider"
	"reasonix/internal/taskcontract"
	"reasonix/internal/tool"
)

// namedFileWriter is a write_file stand-in that lands content on disk, the
// shape upstream #12528 reasons about: the tool's own Execute succeeds and the
// bytes are in the workspace before tool.after rules on the result. Fork
// receipts classify writes by tool name (isWriterTool), so no extra interface
// is needed here.
type namedFileWriter struct{ root string }

func (namedFileWriter) Name() string            { return "write_file" }
func (namedFileWriter) Description() string     { return "" }
func (namedFileWriter) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (namedFileWriter) ReadOnly() bool          { return false }
func (w namedFileWriter) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var p struct{ Path, Content string }
	if err := json.Unmarshal(args, &p); err != nil {
		return "", err
	}
	return "wrote", os.WriteFile(filepath.Join(w.root, p.Path), []byte(p.Content), 0o600)
}

// deliveryVerificationAgent builds the agent shape the fork asks for
// verification in: closed-loop delivery scope with a live contract engine.
// Fork has no taskpolicy.VerifyTargeted switch (upstream's lever in the same
// upstream test); the obligations arise from the absorbed write receipt and
// finalReadinessCheckFor only demands them when the turn must close the loop.
func deliveryVerificationAgent(t *testing.T, reg *tool.Registry, root string) *Agent {
	t.Helper()
	a := New(nil, reg, NewSession(""), Options{}, event.Discard)
	a.writeWorkspaceRoot = root
	setTurnConstraints(a, "")
	a.turn.deliveryScopeActive = true
	return a
}

func unsatisfiedVerifyObligations(a *Agent) int {
	var n int
	for _, o := range a.turn.engine.Snapshot().Unsatisfied() {
		if o.Kind == taskcontract.ObligationTargetedVerify || o.Kind == taskcontract.ObligationFullVerify {
			n++
		}
	}
	return n
}

// TestToolAfterRewriteCannotHideTheOnlyWrittenCode pins the fix of upstream
// #12528 (PR12614) on the fork: a named-path write that reached the disk stays
// a write in the evidence ledger when a tool.after extension blocks the result
// or errors in the sidecar, so the verification ask is still owed. Both modes
// fail on the pre-fix code with missing verification = 0.
func TestToolAfterRewriteCannotHideTheOnlyWrittenCode(t *testing.T) {
	for _, mode := range []string{"block", "sidecar error"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			client := &fakeDispatchClient{interceptFn: func(ev protocol.InterceptEvent, _ json.RawMessage) (protocol.InterceptResult, error) {
				if ev != protocol.EventToolAfter {
					return protocol.InterceptResult{Decision: protocol.DecisionContinue}, nil
				}
				if mode == "block" {
					return blockWith("lint rejected the write"), nil
				}
				return protocol.InterceptResult{}, errors.New("sidecar timeout")
			}}
			d := newExtDispatcher(client, true, nil, extension.PointToolAfter)
			reg := tool.NewRegistry()
			reg.Add(namedFileWriter{root: root})
			a := deliveryVerificationAgent(t, reg, root)
			a.svc.extensions = d

			a.executeOne(context.Background(), &a.turn, provider.ToolCall{
				ID: "1", Name: "write_file", Arguments: `{"path":"Program.cs","content":"class Q {}\n"}`,
			})
			if _, err := os.Stat(filepath.Join(root, "Program.cs")); err != nil {
				t.Fatalf("the write did not land: %v", err)
			}
			if got := unsatisfiedVerifyObligations(a); got != 1 {
				t.Errorf("unsatisfied verification obligations = %d, want 1: the written code keeps the check owed", got)
			}
			if check := a.finalReadinessCheckFor(); check.missingVerification != 1 {
				t.Errorf("missing verification = %d, reason %q; want the only written code to keep the check owed",
					check.missingVerification, check.reason)
			}
		})
	}
}

// TestToolAfterRewriteDoesNotResurrectAFailedWrite pins the blast-radius floor
// of the same fix: ranClean records the tool's own outcome, so a write whose
// Execute failed is still a failed write — no verification obligation, no
// readiness ask. The extension ruling on top of a failure changes nothing.
func TestToolAfterRewriteDoesNotResurrectAFailedWrite(t *testing.T) {
	root := t.TempDir()
	client := &fakeDispatchClient{interceptFn: func(ev protocol.InterceptEvent, _ json.RawMessage) (protocol.InterceptResult, error) {
		if ev == protocol.EventToolAfter {
			return blockWith("lint rejected the write"), nil
		}
		return protocol.InterceptResult{Decision: protocol.DecisionContinue}, nil
	}}
	d := newExtDispatcher(client, true, nil, extension.PointToolAfter)
	reg := tool.NewRegistry()
	reg.Add(fakeTool{name: "write_file", readOnly: false, err: errors.New("disk full")})
	a := deliveryVerificationAgent(t, reg, root)
	a.svc.extensions = d

	out := a.executeOne(context.Background(), &a.turn, provider.ToolCall{
		ID: "1", Name: "write_file", Arguments: `{"path":"Program.cs","content":"class Q {}\n"}`,
	})
	if out.errMsg == "" {
		t.Fatalf("outcome = %+v, want the failed write to surface as an error", out)
	}
	if got := unsatisfiedVerifyObligations(a); got != 0 {
		t.Fatalf("unsatisfied verification obligations = %d, want 0: a failed write owes no verification", got)
	}
	if check := a.finalReadinessCheckFor(); check.missingVerification != 0 || check.reason != "" {
		t.Fatalf("missing verification = %d reason %q, want 0 and empty: a failed write is not a write",
			check.missingVerification, check.reason)
	}
}

// TestToolAfterRewriteDoesNotRescueAFailedVerification pins the other side of
// the blast radius: bash receipts are untouched by the ranClean override, so
// an extension failing a test run still fails that verification instead of
// being promoted to success.
func TestToolAfterRewriteDoesNotRescueAFailedVerification(t *testing.T) {
	root := t.TempDir()
	client := &fakeDispatchClient{interceptFn: func(ev protocol.InterceptEvent, _ json.RawMessage) (protocol.InterceptResult, error) {
		if ev == protocol.EventToolAfter {
			return protocol.InterceptResult{}, errors.New("sidecar timeout")
		}
		return protocol.InterceptResult{Decision: protocol.DecisionContinue}, nil
	}}
	d := newExtDispatcher(client, true, nil, extension.PointToolAfter)
	reg := tool.NewRegistry()
	reg.Add(fakeTool{name: "bash", readOnly: true})
	a := deliveryVerificationAgent(t, reg, root)
	a.svc.extensions = d

	out := a.executeOne(context.Background(), &a.turn, provider.ToolCall{
		ID: "1", Name: "bash", Arguments: `{"command":"go test ./..."}`,
	})
	if out.errMsg == "" {
		t.Fatalf("outcome = %+v, want the sidecar failure to fail the verification", out)
	}
	recs := a.task.ledger.Receipts()
	if len(recs) == 0 {
		t.Fatal("no receipt recorded for the failed verification")
	}
	last := recs[len(recs)-1]
	if last.ToolName != "bash" || last.Success {
		t.Fatalf("last receipt = %+v, want a failed bash receipt: the ranClean override must not rescue verification runs", last)
	}
}

// TestToolAfterCleanWriteStaysClean is the control arm: without extension
// interference a successful write records success and owes verification
// exactly as before the ranClean field existed.
func TestToolAfterCleanWriteStaysClean(t *testing.T) {
	root := t.TempDir()
	reg := tool.NewRegistry()
	reg.Add(namedFileWriter{root: root})
	a := deliveryVerificationAgent(t, reg, root)

	out := a.executeOne(context.Background(), &a.turn, provider.ToolCall{
		ID: "1", Name: "write_file", Arguments: `{"path":"Program.cs","content":"class Q {}\n"}`,
	})
	if out.errMsg != "" || out.blocked {
		t.Fatalf("outcome = %+v, want the clean write to execute", out)
	}
	if _, err := os.Stat(filepath.Join(root, "Program.cs")); err != nil {
		t.Fatalf("the write did not land: %v", err)
	}
	if got := unsatisfiedVerifyObligations(a); got != 1 {
		t.Fatalf("unsatisfied verification obligations = %d, want 1 for a clean write", got)
	}
	if check := a.finalReadinessCheckFor(); check.missingVerification != 1 {
		t.Fatalf("missing verification = %d, reason %q; want 1 for a clean write", check.missingVerification, check.reason)
	}
}
