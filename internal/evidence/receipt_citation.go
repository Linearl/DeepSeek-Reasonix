package evidence

import (
	"slices"
	"strings"
)

// LookupReceipt returns a copy of a receipt by host-issued ID. Arguments are
// dropped: a citation resolves a fact, it never reopens the original call.
func (l *Ledger) LookupReceipt(id string) (Receipt, bool) {
	id = strings.TrimSpace(id)
	if l == nil || id == "" {
		return Receipt{}, false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, r := range l.receipts {
		if r.ID == id {
			r.Args = nil
			return r, true
		}
	}
	return Receipt{}, false
}

// ReceiptRef returns a bounded model-safe receipt projection.
func (l *Ledger) ReceiptRef(id string) (ReceiptRef, bool) {
	r, ok := l.LookupReceipt(id)
	if !ok {
		return ReceiptRef{}, false
	}
	return r.Ref(), true
}

// ReceiptCoversOperation reports whether a cited receipt actually belongs to
// the operation being completed. This replaces command-text matching: shell
// prefixes, quoting, argument order, and working directory stop mattering
// because identity is the ID the host issued, not the text the model retyped.
func (l *Ledger) ReceiptCoversOperation(receiptID, operationID string) bool {
	r, ok := l.LookupReceipt(receiptID)
	if !ok || !r.Success {
		return false
	}
	operationID = strings.TrimSpace(operationID)
	if operationID == "" || r.OperationID == operationID {
		return true
	}
	op, ok := l.Operations().Get(operationID)
	if !ok || len(op.TargetPaths) == 0 || len(r.Paths) == 0 {
		return false
	}
	for _, path := range r.Paths {
		if slices.Contains(op.TargetPaths, path) {
			return true
		}
	}
	return false
}

// CitableReceipts returns up to limit successful receipts from this turn, most
// recent first, so a rejection can list what the model may cite instead of
// asking it to guess a command string.
func (l *Ledger) CitableReceipts(limit int) []ReceiptRef {
	if l == nil || limit <= 0 {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]ReceiptRef, 0, limit)
	for i := len(l.receipts) - 1; i >= 0 && len(out) < limit; i-- {
		r := l.receipts[i]
		if !r.Success || r.ToolName == "complete_step" || r.ToolName == "todo_write" {
			continue
		}
		r.Args = nil
		out = append(out, r.Ref())
	}
	return out
}

// Operations exposes the turn's operation lifecycle, lazily created so every
// existing Ledger construction site keeps working unchanged. Never call it
// while holding l.mu.
func (l *Ledger) Operations() *OperationLedger {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.ops == nil {
		l.ops = NewOperationLedger()
	}
	return l.ops
}
