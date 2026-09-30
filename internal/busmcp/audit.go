package busmcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// The bus hands write power to external runtimes, so "who did what when" must
// be answerable from files alone, even if the serve process never mentions it
// again. One JSONL line per bus interaction goes to
// <mailDir>/bus-mcp-audit.jsonl. Single-process writer, so a mutex plus
// O_APPEND is enough; the file is not a mailbox and needs no cross-process
// lock.

// audit records one interaction. Failures to write are silently dropped on
// purpose: an audit hiccup must never break a live delivery, and the next
// successful append still leaves a gap the surrounding entries bound.
func (s *Server) audit(role, kind, detail string, ok bool) {
	s.auditMu.Lock()
	defer s.auditMu.Unlock()
	entry := map[string]any{
		"at":    time.Now().UnixMilli(),
		"role":  role,
		"kind":  kind,
		"ok":    ok,
		"event": detail,
	}
	b, err := json.Marshal(entry)
	if err != nil {
		return
	}
	b = append(b, '\n')
	if err := os.MkdirAll(filepath.Dir(s.auditPath), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(s.auditPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(b)
}
