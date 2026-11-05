package control

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"reasonix/internal/agent"
)

// tl1407: snapshot-refusal bounded logging + crash-safety dump.
//
// Before the bound, both refusal branches re-logged their WARN on every
// autosave cycle (30s): the 20261105 field storm logged 471 "refusing to
// snapshot" WARNs across three sessions over fifteen minutes while each
// conversation lived only in memory — one crash away from total loss. Both
// branches now warn at most twice per controller and dump the in-memory
// transcript beside the session dir once, so even a refusal that outlives the
// process leaves the conversation recoverable.

// maxSnapshotRefusalWarns bounds the per-controller refusal WARN count (the
// task's "同一会话同类 WARN ≤ 2" gate). Beyond it the refusal stays silent —
// the condition itself keeps its return value so callers still see the error.
const maxSnapshotRefusalWarns = 2

// noteSnapshotRefusal records one refusal of a save cycle. Callers hold
// snapshotMu (both refusal branches run inside snapshotWithDurability).
// reason is the human-readable WARN body without the "controller: " prefix.
func (c *Controller) noteSnapshotRefusal(s *agent.Session, path, reason string) {
	if c.refusalWarnCount < maxSnapshotRefusalWarns {
		c.refusalWarnCount++
		slog.Warn("controller: "+reason,
			"label", c.Label(), "session_dir", c.SessionDir(), "message_count", s.Len())
	}
	if c.refusalDumpPath == "" {
		c.refusalDumpPath = c.dumpUnsavedTranscript(s, path)
	}
}

// dumpUnsavedTranscript writes the live transcript to a one-shot sidecar
// beside the session dir so a refusal that persists across save cycles cannot
// lose the conversation to a crash. Best effort: any failure is logged and the
// dump abandoned (the next save cycle may retry through the refusal path only
// when the first dump left nothing behind).
func (c *Controller) dumpUnsavedTranscript(s *agent.Session, path string) string {
	msgs := s.Snapshot()
	dir := c.SessionDir()
	if dir == "" || len(msgs) == 0 {
		return ""
	}
	stem := ""
	if trimmed := strings.TrimSpace(path); trimmed != "" {
		stem = strings.TrimSuffix(filepath.Base(trimmed), ".jsonl")
	} else {
		stem = strings.NewReplacer("/", "-", "\\", "-", ":", "-", "<", "-", ">", "-",
			"\"", "-", "|", "-", "?", "-", "*", "-").Replace(c.Label())
		if stem == "" {
			stem = "session"
		}
		stem = fmt.Sprintf("unsaved-%s-%s", stem, time.Now().UTC().Format("20060102T150405"))
	}
	dumpPath := filepath.Join(dir, stem+".unsaved-dump.jsonl")
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, m := range msgs {
		if err := enc.Encode(m); err != nil {
			slog.Error("controller: unsaved-transcript dump encode failed",
				"dump", filepath.Base(dumpPath), "err", err)
			return ""
		}
	}
	tmp := dumpPath + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o600); err != nil {
		slog.Error("controller: unsaved-transcript dump write failed",
			"dump", filepath.Base(dumpPath), "err", err)
		return ""
	}
	if err := os.Rename(tmp, dumpPath); err != nil {
		slog.Error("controller: unsaved-transcript dump rename failed",
			"dump", filepath.Base(dumpPath), "err", err)
		_ = os.Remove(tmp)
		return ""
	}
	slog.Error("controller: unsaved transcript dumped beside the session dir (crash safety)",
		"dump", filepath.Base(dumpPath), "message_count", len(msgs))
	return dumpPath
}
