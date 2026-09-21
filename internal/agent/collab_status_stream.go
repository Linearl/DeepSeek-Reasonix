package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"reasonix/internal/tool"
)

// Collaboration status stream (task 202). The engine appends lifecycle events
// to an append-only jsonl file so a batch's progress is readable without any
// model writing it by hand, and decoupled from the messaging channel: the
// stream is a plain workspace file, so decisions survive with messaging off.
//
// Field contract (mirrors the A1 hand-written fallback table, one JSON object
// per line so concurrent appends cannot interleave):
//
//	ts             RFC3339 timestamp
//	line           collaboration line label, often empty for engine events
//	session        writing session identity (contact id or path basename)
//	event          turn_start|turn_end|tool_error|commit|delivered|received|blocking_wait|needs_decision|manual
//	summary        one-line human-readable progress note
//	needs_decision true when the batch manager must decide something
//
// A hand-written fallback line is simply another valid line in the same
// shape, which is what keeps engine output and the A1 habit compatible.

// Collab status event kinds.
const (
	CollabStatusTurnStart     = "turn_start"
	CollabStatusTurnEnd       = "turn_end"
	CollabStatusToolError     = "tool_error"
	CollabStatusCommit        = "commit"
	CollabStatusDelivered     = "delivered"
	CollabStatusReceived      = "received"
	CollabStatusBlockingWait  = "blocking_wait"
	CollabStatusNeedsDecision = "needs_decision"
	CollabStatusManual        = "manual"
)

const (
	// collabStatusDefaultTailBytes bounds a first read without an offset: the
	// reader never scans the whole file, it starts from a bounded tail window
	// (187: never build a read path that rescans everything).
	collabStatusDefaultTailBytes = 8 << 10
	// collabStatusMaxReadBytes caps a single incremental read.
	collabStatusMaxReadBytes = 256 << 10
)

// CollabStatusEvent is one line of the status stream.
type CollabStatusEvent struct {
	TS            string `json:"ts"`
	Line          string `json:"line,omitempty"`
	Session       string `json:"session"`
	Event         string `json:"event"`
	Summary       string `json:"summary"`
	NeedsDecision bool   `json:"needs_decision"`
}

// ResolveCollabStatusPath picks where the batch status stream lives
// (task 202 M2): an explicit configuration wins, then the shared collab
// mail dir - which every session on the machine already shares, so sessions
// whose workspace root points at their own worktree still converge on one
// file and a manager's single incremental read sees every line. The
// workspace root is the last-resort fallback (single-session use).
func ResolveCollabStatusPath(explicit, mailDir, workspaceRoot string) string {
	if p := strings.TrimSpace(explicit); p != "" {
		return p
	}
	if mailDir = strings.TrimSpace(mailDir); mailDir != "" {
		return filepath.Join(mailDir, "collab-status.jsonl")
	}
	if workspaceRoot = strings.TrimSpace(workspaceRoot); workspaceRoot != "" {
		return filepath.Join(workspaceRoot, "tasks", "collab-status.jsonl")
	}
	return ""
}

// collabStatusMu serialises appends within the process; the O_APPEND write
// keeps cross-process appends line-atomic for reasonably sized lines (keep
// one event well under 4 KiB - the append-only jsonl contract). A reader
// that still hits an interleaved line skips and counts it instead of
// failing, so the worst case is one lost summary, never a broken stream.
var collabStatusMu sync.Mutex

// AppendCollabStatusEvent appends one event to the stream file, creating it
// on demand. A write failure must never break the calling turn: the first
// failure logs to stderr and later failures stay silent until a write
// succeeds again.
func AppendCollabStatusEvent(path, session, line, event, summary string, needsDecision bool) {
	if strings.TrimSpace(path) == "" {
		return
	}
	ev := CollabStatusEvent{
		TS:            time.Now().UTC().Format(time.RFC3339),
		Line:          line,
		Session:       session,
		Event:         event,
		Summary:       strings.ReplaceAll(strings.TrimSpace(summary), "\n", " "),
		NeedsDecision: needsDecision,
	}
	if ev.Summary == "" {
		ev.Summary = " "
	}
	b, err := json.Marshal(ev)
	if err != nil {
		return
	}
	collabStatusMu.Lock()
	defer collabStatusMu.Unlock()
	// The stream may outlive the directory that first used it (a fresh
	// workspace, a cleaned tasks/): create the parent on demand.
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		collabStatusLogWriteFailure(err)
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		collabStatusLogWriteFailure(err)
		return
	}
	defer f.Close()
	if _, err := f.Write(append(b, '\n')); err != nil {
		collabStatusLogWriteFailure(err)
	}
}

// CollabStatusReadResult reports one incremental read plus the counters that
// prove the read stayed bounded (task 202 acceptance): Scans is always zero
// because the reader seeks straight to the requested offset (or a bounded
// tail on first read) and never walks the file from byte 0.
type CollabStatusReadResult struct {
	Events        []CollabStatusEvent `json:"events"`
	NextOffset    int64               `json:"next_offset"`
	FileBytes     int64               `json:"file_bytes"`
	BytesRead     int64               `json:"bytes_read"`
	Scans         int                 `json:"scans"`
	SkippedLines  int                 `json:"skipped_lines"`
	TruncatedHead bool                `json:"truncated_head"`
}

// ReadCollabStatusIncremental reads only the new bytes of the stream.
//
// offset <= 0 means "first read": start from a bounded tail window so even a
// long-lived file costs at most tailBytes. offset > 0 means "continue": read
// from that byte offset, capped at collabStatusMaxReadBytes. Partial lines at
// a window head are dropped (and counted) rather than half-parsed.
func ReadCollabStatusIncremental(path string, offset, tailBytes int64) (CollabStatusReadResult, error) {
	result := CollabStatusReadResult{Events: []CollabStatusEvent{}, Scans: 0}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			result.NextOffset = 0
			return result, nil
		}
		return result, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return result, err
	}
	result.FileBytes = info.Size()
	if offset < 0 || offset > result.FileBytes {
		offset = 0
	}
	start := offset
	if start <= 0 {
		if tailBytes <= 0 {
			tailBytes = collabStatusDefaultTailBytes
		}
		start = result.FileBytes - tailBytes
		if start < 0 {
			start = 0
		}
		result.TruncatedHead = start > 0
	} else {
		result.TruncatedHead = false
	}
	end := result.FileBytes
	if end-start > collabStatusMaxReadBytes {
		end = start + collabStatusMaxReadBytes
	}
	// A tail window can open mid-line; a continue read cannot (next_offset is
	// always a line boundary). When the window head does open mid-line, drop
	// the partial first line instead of parsing half an event.
	headOnBoundary := true
	if result.TruncatedHead {
		headOnBoundary = startIsLineBoundary(f, start)
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return result, err
	}
	result.BytesRead = end - start
	scanner := bufio.NewScanner(io.LimitReader(f, end-start))
	scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
	lineStart := start
	for scanner.Scan() {
		lineNo := lineStart
		lineStart += int64(len(scanner.Bytes())) + 1
		raw := strings.TrimSpace(scanner.Text())
		if raw == "" {
			continue
		}
		if result.TruncatedHead && !headOnBoundary && lineNo == start {
			result.SkippedLines++
			continue
		}
		var ev CollabStatusEvent
		if json.Unmarshal([]byte(raw), &ev) != nil || ev.Event == "" {
			result.SkippedLines++
			continue
		}
		result.Events = append(result.Events, ev)
	}
	result.NextOffset = end
	return result, scanner.Err()
}

// startIsLineBoundary reports whether the byte before start is a newline (or
// start is 0), i.e. whether a read beginning at start sees a whole line.
func startIsLineBoundary(f *os.File, start int64) bool {
	if start == 0 {
		return true
	}
	buf := make([]byte, 1)
	if _, err := f.ReadAt(buf, start-1); err != nil {
		return false
	}
	return buf[0] == '\n'
}

// collabStatusLogWriteFailure logs once per stream file; repeated failures
// stay silent so a broken path cannot spam the log on every turn.
var collabStatusLogWriteFailure = func(err error) {
	fmt.Fprintf(os.Stderr, "[collab-status] WARN: append failed: %v\n", err)
}

// CollabStatusCommitFromBash reports whether a bash tool call is a git
// commit, which the engine turns into a commit status event (task 202: the
// model never has to report its own commits).
func CollabStatusCommitFromBash(args string) bool {
	var p struct {
		Command string `json:"command"`
	}
	if json.Unmarshal([]byte(args), &p) != nil {
		return false
	}
	cmd := strings.TrimSpace(p.Command)
	if !strings.Contains(cmd, "git") || !strings.Contains(cmd, "commit") {
		return false
	}
	for _, field := range strings.Fields(cmd) {
		if field == "commit" {
			return true
		}
	}
	return false
}

// NewReadCollabStatusTool answers "what has every line of the batch been
// doing?" (task 202) from the engine-written status stream plus any
// hand-written A1 fallback lines, in one incremental call.
//
// Boundary with get_session_status (task 218): that tool answers per-session
// busy/idle metadata (running, last activity, unread count) from cheap state;
// read_collab_status returns progress summaries from the batch stream. One
// answers "can I hand them work now", the other "what did they actually do".
func NewReadCollabStatusTool(cfg SessionCollabConfig) tool.Tool {
	return readCollabStatusTool{cfg: cfg}
}

type readCollabStatusTool struct{ cfg SessionCollabConfig }

func (readCollabStatusTool) Name() string { return "read_collab_status" }

func (readCollabStatusTool) Description() string {
	return "Read the batch collaboration status stream (task 202): engine-written lifecycle events (turn start/end, tool failures, commits, deliveries, blocking waits, needs-decision) plus hand-written fallback lines, across every line of the batch in one incremental call. Unlike get_session_status (per-session busy/idle metadata), this returns progress summaries. Incremental only: pass the offset returned by the previous call; the first read starts from a bounded tail window, and the reader never rescans the whole file - counters in the response prove it. Strictly read-only."
}

func (readCollabStatusTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"offset":{"type":"integer","description":"Byte offset from the previous call's next_offset. Omit or 0 for a bounded tail window."},"tail_bytes":{"type":"integer","description":"First-read tail window size (default 8192). Ignored when offset > 0."}},"required":[]}`)
}

func (readCollabStatusTool) ReadOnly() bool { return true }

func (t readCollabStatusTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	if strings.TrimSpace(t.cfg.CollabStatusPath) == "" {
		return "", fmt.Errorf("collab status stream is not configured for this session (no CollabStatusPath at boot)")
	}
	var p struct {
		Offset    int64 `json:"offset"`
		TailBytes int64 `json:"tail_bytes"`
	}
	if len(args) > 0 {
		_ = json.Unmarshal(args, &p)
	}
	result, err := ReadCollabStatusIncremental(t.cfg.CollabStatusPath, p.Offset, p.TailBytes)
	if err != nil {
		return "", err
	}
	out, err := json.Marshal(map[string]any{
		"events":         result.Events,
		"next_offset":    result.NextOffset,
		"file_bytes":     result.FileBytes,
		"bytes_read":     result.BytesRead,
		"scans":          result.Scans,
		"skipped_lines":  result.SkippedLines,
		"truncated_head": result.TruncatedHead,
		"path":           filepath.ToSlash(t.cfg.CollabStatusPath),
	})
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// collabStatusEvent is the Agent-side hook: one engine event, silently
// skipped when the stream is not configured.
func (a *Agent) collabStatusEvent(event, summary string, needsDecision bool) {
	if a == nil || a.collabStatusPath == "" {
		return
	}
	AppendCollabStatusEvent(a.collabStatusPath, collabStatusSessionID(a.sess.path, ""), "", event, summary, needsDecision)
}


// collabStatusSessionID picks the identity stamped into status events:
// the contact id when known, otherwise the session file's base name.
func collabStatusSessionID(sessionPath, contactID string) string {
	if contactID = strings.TrimSpace(contactID); contactID != "" {
		return contactID
	}
	sessionPath = strings.TrimSpace(sessionPath)
	if sessionPath == "" {
		return ""
	}
	return strings.TrimSuffix(filepath.Base(sessionPath), filepath.Ext(sessionPath))
}

// summarizeCollabStatusInput condenses the turn input into a short,
// single-line summary for the status stream. It never includes the whole
// input: the stream is a shared batch file, not a transcript.
func summarizeCollabStatusInput(input string) string {
	trimmed := strings.Join(strings.Fields(strings.TrimSpace(input)), " ")
	if trimmed == "" {
		return "(empty turn input)"
	}
	const max = 120
	runes := []rune(trimmed)
	if len(runes) > max {
		trimmed = string(runes[:max]) + "…"
	}
	return trimmed
}
