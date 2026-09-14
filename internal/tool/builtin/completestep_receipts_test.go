package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"reasonix/internal/evidence"
	"reasonix/internal/tool"
)

func recordReceiptID(t *testing.T, ledger *evidence.Ledger, r evidence.Receipt) string {
	t.Helper()
	stored := ledger.Record(r)
	if stored.ID == "" {
		t.Fatal("ledger issued no receipt id")
	}
	return stored.ID
}

func TestCompleteStepAcceptsReceiptIDInsteadOfCommandText(t *testing.T) {
	ledger := evidence.NewLedger()
	// The command as it really ran: a cd prefix, a pipe, a different quoting
	// style. None of it matters once the citation is the host's own id.
	id := recordReceiptID(t, ledger, evidence.Receipt{
		ToolName: "bash", Success: true,
		Command: `cd /repo && go test ./internal/auth -count=1 2>&1 | tail -5`,
	})
	ctx := evidence.WithLedger(context.Background(), ledger)

	out, err := completeStep{}.Execute(ctx, json.RawMessage(`{
		"step":"Add auth","result":"auth done",
		"receipt_ids":["`+id+`"],
		"evidence":[{"kind":"verification","summary":"auth tests pass"}]}`))
	if err != nil {
		t.Fatalf("receipt citation rejected: %v", err)
	}
	if !strings.Contains(out, "host-verified 1") {
		t.Fatalf("ack should count the receipt as host verification, got %q", out)
	}
}

func TestCompleteStepAcceptsUnclassifiedCommandReceipt(t *testing.T) {
	ledger := evidence.NewLedger()
	id := recordReceiptID(t, ledger, evidence.Receipt{ToolName: "bash", Success: true, Command: "./company-ci.sh"})
	ctx := evidence.WithLedger(context.Background(), ledger)

	if _, err := (completeStep{}).Execute(ctx, json.RawMessage(`{
		"step":"Run company CI","result":"pipeline green",
		"receipt_ids":["`+id+`"],
		"evidence":[{"kind":"verification","summary":"company CI passed"}]}`)); err != nil {
		t.Fatalf("a successful project-specific verifier must be citable: %v", err)
	}
}

func TestCompleteStepRejectsUnknownReceiptWithAvailableIDs(t *testing.T) {
	ledger := evidence.NewLedger()
	known := recordReceiptID(t, ledger, evidence.Receipt{ToolName: "bash", Success: true, Command: "go test ./..."})
	ctx := evidence.WithLedger(context.Background(), ledger)

	_, err := completeStep{}.Execute(ctx, json.RawMessage(`{
		"step":"x","result":"y",
		"receipt_ids":["r_invented"],
		"evidence":[{"kind":"verification","summary":"claimed"}]}`))
	if err == nil {
		t.Fatal("an invented receipt id must be rejected")
	}
	var operationErr *tool.OperationError
	if !errors.As(err, &operationErr) {
		t.Fatalf("rejection should be a structured operation error, got %T", err)
	}
	d := operationErr.Diagnostic
	if d.Code != tool.VerificationReceiptMissing {
		t.Fatalf("code = %q, want VERIFICATION_RECEIPT_MISSING", d.Code)
	}
	if len(d.AvailableReceipts) == 0 || d.AvailableReceipts[0] != known {
		t.Fatalf("available receipts = %v, want the id that exists", d.AvailableReceipts)
	}
	if !strings.Contains(strings.Join(d.AllowedRecovery, " "), "use_receipt:"+known) {
		t.Fatalf("allowed recovery = %v, want a use_receipt action", d.AllowedRecovery)
	}
}

func TestCompleteStepRejectsFailedReceiptCitation(t *testing.T) {
	ledger := evidence.NewLedger()
	id := recordReceiptID(t, ledger, evidence.Receipt{ToolName: "bash", Success: false, Command: "go test ./..."})
	ctx := evidence.WithLedger(context.Background(), ledger)

	if _, err := (completeStep{}).Execute(ctx, json.RawMessage(`{
		"step":"x","result":"y",
		"receipt_ids":["`+id+`"],
		"evidence":[{"kind":"verification","summary":"claimed"}]}`)); err == nil {
		t.Fatal("a receipt for a failed call proves nothing and must be rejected")
	}
}

func TestCompleteStepReceiptIDCoversDiffPaths(t *testing.T) {
	ledger := evidence.NewLedger()
	id := recordReceiptID(t, ledger, evidence.Receipt{
		ToolName: "write_file", Success: true, Write: true, Paths: []string{"internal/auth/login.go"},
	})
	ctx := evidence.WithLedger(context.Background(), ledger)

	if _, err := (completeStep{}).Execute(ctx, json.RawMessage(`{
		"step":"x","result":"y",
		"receipt_ids":["`+id+`"],
		"evidence":[{"kind":"diff","summary":"login rewritten","paths":["internal/auth/login.go"]}]}`)); err != nil {
		t.Fatalf("a cited mutation receipt should cover its own paths: %v", err)
	}
}

func TestCompleteStepMissingVerificationListsReceiptIDs(t *testing.T) {
	ledger := evidence.NewLedger()
	known := recordReceiptID(t, ledger, evidence.Receipt{ToolName: "bash", Success: true, Command: "make check"})
	ctx := evidence.WithLedger(context.Background(), ledger)
	ctx = evidence.WithClosedLoopExecution(ctx)

	_, err := completeStep{}.Execute(ctx, json.RawMessage(`{
		"step":"x","result":"y",
		"evidence":[{"kind":"verification","summary":"claimed","command":"go test ./nowhere/..."}]}`))
	if err == nil {
		t.Fatal("an uncited verification command should still be rejected")
	}
	if !strings.Contains(err.Error(), known) {
		t.Fatalf("rejection should offer the receipt ids that exist, got %v", err)
	}
	var operationErr *tool.OperationError
	if !errors.As(err, &operationErr) || operationErr.Diagnostic.RetryBudget != 1 {
		t.Fatalf("rejection should carry a bounded retry budget, got %v", err)
	}
}
