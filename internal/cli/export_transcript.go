package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/provider"
)

// 任务 229 G6（transcript 解析单一信源）: `reasonix export-transcript` turns a
// session into normalized JSONL so external consumers (distill scanner, error
// statistics, ad-hoc scripts) stop maintaining their own transcript parsers.
// The loader (agent.LoadSession) stays the only parser — including the
// legacy event-log migration and damaged-tail salvage — so a host format
// change can never silently fork the parse results (the "双形态教训" behind
// this module). Wire shape per line:
//
//	{seq, role, id?, origin?, tool_name?, tool_call_id?, is_error?, content, timestamp?}
//
// role ∈ user|assistant|system|tool|tool_call|_damaged|_tail_truncated;
// assistant tool_calls expand to one tool_call record each (is_error from the
// durable tool-recovery state) so callers pair call/result by tool_call_id.
type transcriptRecord struct {
	Seq        int64  `json:"seq"`
	Role       string `json:"role"`
	ID         string `json:"id,omitempty"`
	Origin     string `json:"origin,omitempty"`
	ToolName   string `json:"tool_name,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
	IsError    bool   `json:"is_error,omitempty"`
	Content    string `json:"content,omitempty"`
	Timestamp  int64  `json:"timestamp,omitempty"`
}

// transcriptMarkerDamaged / transcriptMarkerTailTruncated are terminal marker
// roles. The loader collapses undecodable rows into its salvage sidecar, so
// the export reports their presence with one marker instead of re-parsing raw
// bytes (a second parser is exactly what G6 removes).
const (
	transcriptMarkerDamaged       = "_damaged"
	transcriptMarkerTailTruncated = "_tail_truncated"
)

func runExportTranscript(args []string) int {
	fs := flag.NewFlagSet("export-transcript", flag.ContinueOnError)
	session := fs.String("session", "", "session transcript path (<id>.jsonl) or bare session id (required)")
	out := fs.String("out", "", "output file (default: stdout)")
	if code, ok := parseCommandFlags(fs, args); !ok {
		return code
	}
	path, err := resolveExportSessionPath(*session)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return verdictExitUsage
	}
	sess, err := agent.LoadSession(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: load session:", err)
		return verdictExitRefuted
	}

	var w io.Writer = os.Stdout
	if strings.TrimSpace(*out) != "" {
		f, err := os.Create(*out)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error: create output:", err)
			return verdictExitRefuted
		}
		defer f.Close()
		w = f
	}
	enc := json.NewEncoder(w)
	for _, rec := range normalizeTranscriptRecords(sess.Snapshot()) {
		if err := enc.Encode(rec); err != nil {
			fmt.Fprintln(os.Stderr, "error: encode:", err)
			return verdictExitRefuted
		}
	}
	for _, rec := range transcriptTrailerMarkers(sess) {
		if err := enc.Encode(rec); err != nil {
			fmt.Fprintln(os.Stderr, "error: encode:", err)
			return verdictExitRefuted
		}
	}
	return verdictExitConfirmed
}

// resolveExportSessionPath accepts an exact transcript path or a bare session
// id resolved against the default sessions directory.
func resolveExportSessionPath(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("--session is required (transcript <id>.jsonl path or session id)")
	}
	if _, err := os.Stat(value); err == nil {
		return value, nil
	}
	if !strings.ContainsRune(value, filepath.Separator) && !strings.ContainsRune(value, '/') && !strings.HasSuffix(value, ".jsonl") {
		candidate := filepath.Join(config.MemoryUserDir(), "sessions", value+".jsonl")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("session transcript not found: %s", value)
}

// normalizeTranscriptRecords maps loader messages onto the wire shape.
// Content prefers the stable provider-visible field and falls back to the
// raw original so user edits never blank an exported line.
func normalizeTranscriptRecords(msgs []provider.Message) []transcriptRecord {
	records := make([]transcriptRecord, 0, len(msgs))
	for _, m := range msgs {
		role := string(m.Role)
		timestamp := m.CreatedAt
		switch m.Role {
		case provider.RoleAssistant:
			content := firstNonEmpty(m.Content, m.RawContent)
			records = append(records, transcriptRecord{Seq: int64(len(records) + 1), Role: role, ID: m.ID, Origin: string(m.Origin), Content: content, Timestamp: timestamp})
			for _, call := range m.ToolCalls {
				records = append(records, transcriptRecord{
					Seq:        int64(len(records) + 1),
					Role:       "tool_call",
					ID:         m.ID,
					ToolName:   call.Name,
					ToolCallID: call.ID,
					IsError:    call.Recovery != nil && call.Recovery.State == provider.ToolRunFailed,
				})
			}
			continue
		case provider.RoleTool:
			records = append(records, transcriptRecord{
				Seq:        int64(len(records) + 1),
				Role:       role,
				ID:         m.ID,
				ToolName:   m.Name,
				ToolCallID: m.ToolCallID,
				IsError:    m.ToolRunState == provider.ToolRunFailed,
				Content:    firstNonEmpty(m.Content, m.RawContent),
				Timestamp:  timestamp,
			})
			continue
		default:
			records = append(records, transcriptRecord{Seq: int64(len(records) + 1), Role: role, ID: m.ID, Origin: string(m.Origin), Content: firstNonEmpty(m.Content, m.RawContent), Timestamp: timestamp})
		}
	}
	return records
}

// transcriptTrailerMarkers appends the integrity markers after the last real
// record, so a consumer that reads to the end always knows whether the stream
// is complete and repair-surviving.
func transcriptTrailerMarkers(sess *agent.Session) []transcriptRecord {
	markers := make([]transcriptRecord, 0, 2)
	if sess.EventLogDamaged() {
		markers = append(markers, transcriptRecord{
			Seq:     int64(sess.Len() + len(markers) + 1),
			Role:    transcriptMarkerDamaged,
			Content: "source event log held undecodable rows; they are salvaged to the .damaged sidecar and absent here",
		})
	}
	if sess.TailTruncated() {
		markers = append(markers, transcriptRecord{
			Seq:     int64(sess.Len() + len(markers) + 1),
			Role:    transcriptMarkerTailTruncated,
			Content: "session was loaded tail-only (very large log); older history is absent here",
		})
	}
	return markers
}
