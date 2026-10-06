package sentinel

// Interception audit (task 410 acceptance: 拦截记录可查 who/when/which rule).
// Every block appends one JSONL line to the configured audit file (best
// effort — audit failure must never change the verdict) and emits a slog
// line. The recorded subject excerpt is redacted through internal/secrets so
// the audit trail never becomes itself a secret store.

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"reasonix/internal/secrets"
)

// auditMaxSubject caps the recorded command text per line.
const auditMaxSubject = 400

var auditMu sync.Mutex

// InterceptRecord is one blocked call. "who" is the workspace root + tool
// (sessions are identified by their workspace in v1); "when" is the UTC
// timestamp; "which rule" is the rule id.
type InterceptRecord struct {
	Time          string `json:"time"`
	WorkspaceRoot string `json:"workspace_root"`
	Tool          string `json:"tool"`
	Rule          string `json:"rule"`
	Subject       string `json:"subject"`
}

func recordIntercept(tool, workspaceRoot, rule, subject string) {
	subject = secrets.Redact(subject)
	if len(subject) > auditMaxSubject {
		subject = subject[:auditMaxSubject] + "…"
	}
	rec := InterceptRecord{
		Time:          time.Now().UTC().Format(time.RFC3339),
		WorkspaceRoot: workspaceRoot,
		Tool:          tool,
		Rule:          rule,
		Subject:       subject,
	}
	slog.Warn("sentinel.blocked",
		"tool", tool,
		"workspace_root", workspaceRoot,
		"rule", rule,
		"subject", subject,
	)
	if path := auditFile(); path != "" {
		appendAudit(path, rec)
	}
}

// appendAudit appends one JSONL line, creating the directory on first use.
// Errors are logged, never propagated: 留痕是尽力而为，拦截判定不受影响。
func appendAudit(path string, rec InterceptRecord) {
	auditMu.Lock()
	defer auditMu.Unlock()
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			slog.Warn("sentinel.audit_mkdir_failed", "path", dir, "err", err)
			return
		}
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		slog.Warn("sentinel.audit_open_failed", "path", path, "err", err)
		return
	}
	defer f.Close()
	line, err := json.Marshal(rec)
	if err != nil {
		slog.Warn("sentinel.audit_marshal_failed", "err", err)
		return
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		slog.Warn("sentinel.audit_write_failed", "path", path, "err", err)
	}
}
