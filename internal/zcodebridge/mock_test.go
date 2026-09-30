package zcodebridge

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"
)

// The mock app-server runs inside the test binary (re-exec via
// TestZcodebridgeHelperProcess) and speaks the same wire the real CLI
// verified live: bare {id, method, params} frames over stdio NDJSON. Modes
// select scenario behavior; each mode is a negative or positive path of the
// frozen face. A request containing a "jsonrpc" key makes the mock exit(9) —
// that envelope is the one wire mistake the spec explicitly forbids.

// Mock scenario selectors (env ZCODEBRIDGE_MOCK_MODE).
const (
	mockGood    = "good"    // full frozen face, always correct answers
	mockBadFlow = "badflow" // connection/flow result violates the {} contract
	mockBadList = "badlist" // session/list result violates the shape
	mockHang    = "hang"    // reads stdin, never answers (handshake timeout path)
	mockDie     = "die"     // handshake passes, then the process exits on next request
	mockGarbage = "garbage" // good + unparseable lines and notifications interleaved
)

// mockSessions is what every healthy mode enumerates. sessions[0] is active
// so the handshake's events probe succeeds; sessions[2] mimics a cold one.
var mockSessions = []map[string]any{
	{
		"sessionId":   "sess_active_1",
		"title":       "主开发",
		"status":      "idle",
		"mode":        "build",
		"traceId":     "trace-extra-unknown-field", // tolerant reader: unknown keys ignored
		"sessionKind": "interactive",
		"createdAt":   1790000000000,
		"updatedAt":   1790000001000,
		"workspace":   map[string]any{"workspacePath": `C:\ws`, "workspaceKey": `C:\ws`},
	},
	{
		"sessionId": "sess_active_2",
		"title":     "调研对话",
		"status":    "running",
		"mode":      "yolo",
		"createdAt": 1790000002000,
		"updatedAt": 1790000003000,
		"workspace": map[string]any{"workspacePath": `C:\ws`, "workspaceKey": `C:\ws`},
	},
	{
		"sessionId": "sess_cold",
		"title":     "冷会话",
		"status":    "idle",
		"mode":      "build",
		"createdAt": 1790000004000,
		"updatedAt": 1790000004000,
		"workspace": map[string]any{"workspacePath": `C:\ws`, "workspaceKey": `C:\ws`},
	},
}

func TestZcodebridgeHelperProcess(t *testing.T) {
	mode := helperModeFromArgs()
	if mode == "" {
		return // ordinary test run of this binary
	}
	runMockAppServer(mode, os.Stdin, os.Stdout)
	// Parent closed stdin or the scenario finished: exit so the test-side
	// Wait reaps a real exit event.
	os.Exit(0)
}

// helperModeFromArgs extracts the scenario marker passed after "--" by
// helperConfig (standard test-binary re-exec trick; no environment needed so
// parent t.Setenv cannot leak between tests).
func helperModeFromArgs() string {
	for i, arg := range os.Args {
		if arg != "--" {
			continue
		}
		if i+1 < len(os.Args) {
			if m, _, ok := strings.Cut(os.Args[i+1], ":"); ok {
				return m
			}
		}
	}
	return ""
}

// runMockAppServer is the mock's main loop. Errors go to stderr as
// MOCK-FAIL lines so a parent test failure names the exact wire offense.
func runMockAppServer(mode string, in io.Reader, out io.Writer) {
	w := bufio.NewWriter(out)
	defer w.Flush()
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 64<<10), maxFrameBytes)

	garbage := mode == mockGarbage
	hang := mode == mockHang
	reply := func(id, method string, params map[string]json.RawMessage) {
		if hang {
			return // silent peer: handshake must hit its fail-closed timeout
		}
		switch method {
		case methodConnectionFlow:
			if mode == mockBadFlow {
				writeFrame(w, id, map[string]any{"unexpectedKey": 1})
				return
			}
			writeFrame(w, id, map[string]any{})
		case methodSessionList:
			if mode == mockBadList {
				writeFrame(w, id, map[string]any{"sessions": "not-an-array"})
				return
			}
			writeFrame(w, id, map[string]any{"sessions": mockSessions})
		case methodSessionEvents:
			handleMockEvents(w, id, params)
		case methodCommand:
			handleMockCommand(mode, w, id, params)
		default:
			writeError(w, id, -32601, "Method not found: "+method)
		}
	}

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			fmt.Fprintln(os.Stderr, "MOCK-FAIL unparseable request:", line)
			os.Exit(7)
		}
		if _, ok := raw["jsonrpc"]; ok {
			// The one wire mistake the spec explicitly freezes out: the CLI's
			// strict schema rejects the JSON-RPC 2.0 envelope outright.
			fmt.Fprintln(os.Stderr, "MOCK-FAIL request carries forbidden jsonrpc key:", line)
			os.Exit(9)
		}
		idRaw, hasID := raw["id"]
		methodRaw, hasMethod := raw["method"]
		if !hasMethod {
			fmt.Fprintln(os.Stderr, "MOCK-FAIL request without method:", line)
			os.Exit(7)
		}
		var method string
		_ = json.Unmarshal(methodRaw, &method)
		var params map[string]json.RawMessage
		if p, ok := raw["params"]; ok {
			if err := json.Unmarshal(p, &params); err != nil {
				fmt.Fprintln(os.Stderr, "MOCK-FAIL params not an object:", line)
				os.Exit(7)
			}
		}
		if garbage {
			// Noise first: a JSON object that is a notification (tolerated),
			// then a truly unparseable line (tolerated), then the answer.
			writeNotification(w, "process/mcpTelemetry", map[string]any{"n": 1})
			w.WriteString("<<<not json at all>>>\n")
			w.Flush()
		}
		if !hasID {
			continue // notification: mock has no use for them
		}
		var id string
		_ = json.Unmarshal(idRaw, &id)
		reply(id, method, params)
		w.Flush()
	}
}

func handleMockEvents(w *bufio.Writer, id string, params map[string]json.RawMessage) {
	var p struct {
		SessionID string `json:"sessionId"`
		AfterSeq  *int64 `json:"afterSeq"`
		Limit     *int   `json:"limit"`
	}
	if err := json.Unmarshal(mustObj(params), &p); err != nil || p.SessionID == "" {
		writeError(w, id, -32602, "Invalid params")
		return
	}
	if p.SessionID == "sess_cold" {
		// Live-verified CLI behavior: cold sessions answer sessionUnavailable.
		writeError(w, id, errCodeSessionUnavailable, "Session is not active: "+p.SessionID)
		return
	}
	after := int64(0)
	if p.AfterSeq != nil {
		after = *p.AfterSeq
	}
	// Two pages of the journal; echo afterSeq inside an unknown field so the
	// client's param construction is observable end to end.
	evs := []map[string]any{
		{
			"eventId":   fmt.Sprintf("ev_%d_1", after+1),
			"sessionId": p.SessionID,
			"seq":       after + 1,
			"timestamp": 1790000010000,
			"type":      "turn.started",
			"payload":   map[string]any{"turnId": "turn_1"},
		},
		{
			"eventId":   fmt.Sprintf("ev_%d_2", after+2),
			"sessionId": p.SessionID,
			"seq":       after + 2,
			"timestamp": 1790000010001,
			"type":      "message.upserted",
			"payload":   map[string]any{"echoAfterSeq": after, "echoLimit": limitOr(p.Limit)},
		},
	}
	writeFrame(w, id, map[string]any{"events": evs})
}

func limitOr(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

func handleMockCommand(mode string, w *bufio.Writer, id string, params map[string]json.RawMessage) {
	if mode == mockDie {
		// Simulate the app-server dying right as a command arrives: exit
		// without replying. The pending call must fail with ErrClosed and the
		// bridge must report Dead.
		fmt.Fprintln(os.Stderr, "mock: die mode exiting on v4/command")
		os.Exit(3)
	}
	var env struct {
		CommandID string         `json:"commandId"`
		ClientID  string         `json:"clientId"`
		SessionID string         `json:"sessionId"`
		Type      string         `json:"type"`
		Payload   map[string]any `json:"payload"`
		IssuedAt  *int64         `json:"issuedAt"`
	}
	if err := json.Unmarshal(mustObj(params), &env); err != nil {
		writeError(w, id, -32602, "Invalid params: "+err.Error())
		return
	}
	if env.Type != "sendText" {
		writeError(w, id, -32602, "mock only implements sendText")
		return
	}
	text, _ := env.Payload["text"].(string)
	delivery, _ := env.Payload["requestedDelivery"].(string)
	if env.CommandID == "" || env.ClientID == "" || env.SessionID == "" || env.IssuedAt == nil {
		writeError(w, id, -32602, "mock envelope missing required member")
		return
	}
	if text == "mock-reject" {
		writeFrame(w, id, map[string]any{
			"commandId":          env.CommandID,
			"status":             "rejected",
			"reasonCode":         "fault.command.session_busy",
			"message":            "session has an active turn",
			"revisionAtDecision": 7,
		})
		return
	}
	writeFrame(w, id, map[string]any{
		"commandId":          env.CommandID,
		"status":             "accepted",
		"revisionAtDecision": 41,
		"result": map[string]any{
			"type":         "inputAccepted",
			"delivery":     delivery,
			"inputId":      "in_1",
			"echoText":     text,
			"echoClientId": env.ClientID,
			"echoIssuedAt": *env.IssuedAt,
		},
	})
}

func mustObj(params map[string]json.RawMessage) []byte {
	if len(params) == 0 {
		return []byte("{}")
	}
	// Reassemble: the mock is loose about key order, params arrive as a map.
	b, _ := json.Marshal(params)
	return b
}

func writeFrame(w *bufio.Writer, id string, result any) {
	b, err := json.Marshal(map[string]any{"id": id, "result": result})
	if err != nil {
		fmt.Fprintln(os.Stderr, "MOCK-FAIL marshal:", err)
		os.Exit(7)
	}
	w.Write(b)
	w.WriteByte('\n')
	w.Flush()
}

func writeError(w *bufio.Writer, id string, code int, message string) {
	b, err := json.Marshal(map[string]any{
		"id":    id,
		"error": map[string]any{"code": code, "message": message},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "MOCK-FAIL marshal:", err)
		os.Exit(7)
	}
	w.Write(b)
	w.WriteByte('\n')
	w.Flush()
}

func writeNotification(w *bufio.Writer, method string, params any) {
	b, err := json.Marshal(map[string]any{"method": method, "params": params})
	if err != nil {
		return
	}
	w.Write(b)
	w.WriteByte('\n')
	w.Flush()
}

// helperConfig spawns this test binary as the mock child in the given mode.
func helperConfig(t *testing.T, mode string) Config {
	t.Helper()
	exe := os.Args[0]
	// One argument per element; no shell in between (proc.Command keeps argv
	// separate, same as production spawning zcode).
	return Config{
		Command: exe,
		Args: []string{
			"-test.run=^TestZcodebridgeHelperProcess$",
			"--",
			mode + ":" + strconv.Itoa(os.Getpid()),
		},
	}
}
