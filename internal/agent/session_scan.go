package agent

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// This file hosts the session-transcript scanner shared by the in-process
// extraction passes (decision-pattern extraction, task 801). It was
// originally built for the task-115 dream/distill curation tools; tl1453
// retired those tools (constitution Q1) and the scanner stayed for its
// surviving consumer.

// sessionUserLine is one scanned user utterance from a project session file.
type sessionUserLine struct {
	Session string
	Text    string
	At      time.Time
}

// sessionToolCall is one tool invocation observed in a project session.
type sessionToolCall struct {
	Session string
	Name    string
	At      time.Time
}

// sessionToolCallRecord is one entry of an assistant message's tool_calls
// array. Transcripts written by this host keep the tool name at the top level
// ({"name":"bash","arguments":{…}}), while OpenAI-shaped payloads nest it under
// function.name ({"function":{"name":"bash","arguments":"…"}}). Both shapes are
// read: task 150 found the scanner looking for a top-level name on the wrong
// role, so live transcripts scanned to zero tool calls.
type sessionToolCallRecord struct {
	Name         string `json:"name"`
	CapabilityID string `json:"capability_id"`
	Function     struct {
		Name string `json:"name"`
	} `json:"function"`
}

// toolName returns the tool the model invoked, accepting either wire shape. A
// use_capability dispatch is reported under the capability it resolved to
// (mcp-tool:exa/web_search_exa, memory:remember, skill:review) rather than under
// the proxy, because "bash → use_capability" says nothing about the workflow while
// "bash → mcp-tool:exa/web_search_exa" names the step (#task 150).
func (r sessionToolCallRecord) toolName() string {
	name := strings.TrimSpace(r.Name)
	if name == "" {
		return strings.TrimSpace(r.Function.Name)
	}
	if id := strings.TrimSpace(r.CapabilityID); id != "" {
		return id
	}
	return name
}

// scanSessionJSONL extracts user texts and tool names from one session file.
// Malformed lines are skipped: an extraction pass must never fail because one
// record is unreadable.
func scanSessionJSONL(path string, cutoff time.Time) (users []sessionUserLine, tools []sessionToolCall) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil
	}
	defer f.Close()
	base := filepath.Base(path)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var dispatched, results []sessionToolCall
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var rec struct {
			Role      string                  `json:"role"`
			Content   json.RawMessage         `json:"content"`
			Kind      string                  `json:"kind"`
			Name      string                  `json:"name"`
			ToolCalls []sessionToolCallRecord `json:"tool_calls"`
			TS        string                  `json:"ts"`
			Time      string                  `json:"time"`
		}
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		at := parseRecordTime(rec.TS, rec.Time)
		if !at.IsZero() && at.Before(cutoff) {
			continue
		}
		switch rec.Role {
		case "user":
			if text := rawContentText(rec.Content); text != "" {
				users = append(users, sessionUserLine{Session: base, Text: text, At: at})
			}
		case "tool":
			// One row per tool result, named at the top level. This is the
			// fallback channel resolved after the scan.
			if name := strings.TrimSpace(rec.Name); name != "" {
				results = append(results, sessionToolCall{Session: base, Name: name, At: at})
			}
		case "assistant", "":
			if len(rec.ToolCalls) > 0 {
				for _, call := range rec.ToolCalls {
					if name := call.toolName(); name != "" {
						dispatched = append(dispatched, sessionToolCall{Session: base, Name: name, At: at})
					}
				}
				continue
			}
			// Older transcripts name the tool on the record itself.
			if rec.Kind == "tool" || rec.Name != "" {
				if name := strings.TrimSpace(rec.Name); name != "" {
					dispatched = append(dispatched, sessionToolCall{Session: base, Name: name, At: at})
				}
			}
		}
	}
	// Both channels describe the same invocations — a transcript persists the
	// assistant dispatch and the tool result for every call — so exactly one is
	// returned. The dispatch array wins because it keeps the model's call order
	// inside a batch; the result rows are the fallback for transcripts that
	// never persisted it. Returning both would double every call and destroy
	// the sequence statistics any tool-sequence analysis is built on.
	tools = dispatched
	if len(dispatched) == 0 {
		tools = results
	}
	return users, tools
}

func parseRecordTime(values ...string) time.Time {
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
			return t
		}
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			return t
		}
	}
	return time.Time{}
}

func rawContentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strings.TrimSpace(s)
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err == nil {
		var b strings.Builder
		for _, p := range parts {
			if p.Type == "text" || p.Type == "" {
				b.WriteString(p.Text)
				b.WriteByte('\n')
			}
		}
		return strings.TrimSpace(b.String())
	}
	return ""
}

func truncateTitle(s string, n int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
