package acp

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"reasonix/internal/permission"
	"reasonix/internal/provider"
)

// Task 790: the resident-listener acceptance paths. These mirror the 787
// stdio acceptance semantics (TestE2EStandardClientOneTask on wt-787) over the
// TCP transport: same scripted provider, same gated-tool turn, same restart +
// session/load recovery — only the wire changes (net.Conn instead of pipes).

// dialClient connects to the listener and wraps the net.Conn in the shared
// test JSON-RPC client. net.Conn is an io.Writer/io.Reader pair, which is
// exactly what newRPCClient takes — the transport swap ends here.
func dialClient(t *testing.T, l *Listener) *rpcClient {
	t.Helper()
	conn, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatalf("dial %s: %v", l.Addr().String(), err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return newRPCClient(conn, conn)
}

// startListener arms a resident listener on the given address and stops it
// (waiting for every connection to unwind) when the test ends.
func startListener(t *testing.T, addr string, factory Factory) *Listener {
	t.Helper()
	l, err := Listen(addr, factory, AgentInfo{Name: "reasonix-test", Version: "0"})
	if err != nil {
		t.Fatalf("listen %s: %v", addr, err)
	}
	done := make(chan struct{})
	go func() { _ = l.Serve(context.Background()); close(done) }()
	t.Cleanup(func() { _ = l.Close(); <-done })
	return l
}

// TestListenTCPStandardClientOneTask drives the full 790 acceptance path over
// TCP: initialize → session/new → gated write-tool prompt answered through
// session/request_permission → end_turn, then a simulated restart (a fresh
// listener over the same session dir) reloads the session via session/load
// with its replayed transcript, runs a second turn, and cancels it
// mid-execution.
func TestListenTCPStandardClientOneTask(t *testing.T) {
	dir := t.TempDir()
	addr := "127.0.0.1:0"

	// Run 1: one full task through the gate.
	factory1 := &e2eFactory{
		prov: &scriptedProvider{name: "fake", responses: [][]provider.Chunk{
			{
				{Type: provider.ChunkText, Text: "Writing."},
				toolCallChunk("w1", "writeit", `{"path":"README.md"}`),
				{Type: provider.ChunkDone},
			},
			{{Type: provider.ChunkText, Text: "Task complete."}, {Type: provider.ChunkDone}},
		}},
		tool:       fakeTool{name: "writeit", ro: false, out: "written ok"},
		policy:     permission.New("ask", nil, nil, nil),
		sessionDir: dir,
	}
	listener1 := startListener(t, addr, factory1)
	client1 := dialClient(t, listener1)

	initResp := client1.call(t, "initialize", InitializeParams{ProtocolVersion: 1})
	var ir InitializeResult
	if err := json.Unmarshal(initResp.Result, &ir); err != nil {
		t.Fatalf("initialize result: %v", err)
	}
	if ir.ProtocolVersion != ProtocolVersion {
		t.Errorf("protocolVersion = %d, want %d", ir.ProtocolVersion, ProtocolVersion)
	}

	sid := openSession(t, client1)
	promptCh := client1.callAsync("session/prompt", SessionPromptParams{
		SessionID: sid,
		Prompt:    []ContentBlock{{Type: "text", Text: "write README.md"}},
	})

	var req frame
	select {
	case req = <-client1.reqs:
	case <-time.After(5 * time.Second):
		t.Fatal("no permission request was raised")
	}
	var perm PermissionRequestParams
	if err := json.Unmarshal(req.Params, &perm); err != nil {
		t.Fatalf("permission params: %v", err)
	}
	if perm.SessionID != sid {
		t.Errorf("permission sessionId = %q, want %q", perm.SessionID, sid)
	}
	client1.reply(req.ID, PermissionRequestResult{
		Outcome: PermissionOutcome{Outcome: "selected", OptionID: string(OptAllowOnce)},
	})

	notifs, resp := drainPrompt(t, client1, promptCh)
	if resp.Error != nil {
		t.Fatalf("session/prompt errored: %+v", resp.Error)
	}
	kinds := map[string]bool{}
	for _, n := range notifs {
		kinds[updateKind(t, n)] = true
	}
	for _, want := range []string{"agent_message_chunk", "tool_call", "tool_call_update"} {
		if !kinds[want] {
			t.Errorf("missing %s update; saw %v", want, kinds)
		}
	}
	var pr SessionPromptResult
	if err := json.Unmarshal(resp.Result, &pr); err != nil {
		t.Fatalf("prompt result: %v", err)
	}
	if pr.StopReason != StopEndTurn {
		t.Errorf("stopReason = %q, want %q", pr.StopReason, StopEndTurn)
	}

	// Simulated restart: stop the listener (which closes the live connection
	// and unwinds its session controllers) — the transcript persists on disk.
	if err := listener1.Close(); err != nil {
		t.Fatalf("close listener 1: %v", err)
	}

	// Run 2: a brand-new listener on the same session dir reloads the session,
	// replays the transcript, runs a second turn, and the client cancels it
	// while the tool is executing.
	releaseTool := make(chan struct{})
	started := make(chan struct{})
	factory2 := &e2eFactory{
		prov: &scriptedProvider{name: "fake", responses: [][]provider.Chunk{
			{
				{Type: provider.ChunkText, Text: "Round two."},
				toolCallChunk("c1", "slow", `{}`),
				{Type: provider.ChunkDone},
			},
			{{Type: provider.ChunkText, Text: "unreachable"}, {Type: provider.ChunkDone}},
		}},
		tool:       blockingTool{started: started, release: releaseTool},
		policy:     permission.New("ask", nil, nil, nil),
		sessionDir: dir,
	}
	listener2 := startListener(t, addr, factory2)
	client2 := dialClient(t, listener2)
	defer func() { _ = listener2.Close() }()
	defer close(releaseTool) // let the tool goroutine unwind

	loadCh := client2.callAsync("session/load", SessionLoadParams{SessionID: sid})
	loadNotifs, loadResp := drainPrompt(t, client2, loadCh)
	if loadResp.Error != nil {
		t.Fatalf("session/load errored: %+v", loadResp.Error)
	}
	sawUser := false
	for _, n := range loadNotifs {
		// Loose parse: replay updates carry two incompatible content shapes
		// (single block vs array); only the single-block text matters here.
		var p struct {
			Update struct {
				SessionUpdate string `json:"sessionUpdate"`
				Content       struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"update"`
		}
		if json.Unmarshal(n.Params, &p) != nil {
			continue
		}
		if p.Update.SessionUpdate == "user_message_chunk" && strings.Contains(p.Update.Content.Text, "write README.md") {
			sawUser = true
		}
	}
	if !sawUser {
		t.Error("session/load replay missing the original user prompt")
	}

	prompt2 := client2.callAsync("session/prompt", SessionPromptParams{
		SessionID: sid,
		Prompt:    []ContentBlock{{Type: "text", Text: "run the slow thing"}},
	})
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("tool never started")
	}
	client2.notify("session/cancel", SessionCancelParams{SessionID: sid})

	select {
	case resp := <-prompt2:
		var pr SessionPromptResult
		json.Unmarshal(resp.Result, &pr)
		if pr.StopReason != StopCancelled {
			t.Errorf("stopReason = %q, want cancelled", pr.StopReason)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not end the turn")
	}
}

// TestListenerIndependentConnections proves the per-connection client-surface
// guarantee: two simultaneous connections each initialize, open their own
// session, and drive a turn to end_turn without seeing each other's updates.
func TestListenerIndependentConnections(t *testing.T) {
	mkFactory := func() *e2eFactory {
		return &e2eFactory{
			prov: &scriptedProvider{name: "fake", responses: [][]provider.Chunk{
				{{Type: provider.ChunkText, Text: "own answer."}, {Type: provider.ChunkDone}},
			}},
			tool:       fakeTool{name: "peek", ro: true, out: "unused"},
			policy:     permission.New("ask", nil, nil, nil),
			sessionDir: t.TempDir(),
		}
	}
	listener := startListener(t, "127.0.0.1:0", mkFactory())

	clientA := dialClient(t, listener)
	clientB := dialClient(t, listener)

	sidA := openSession(t, clientA)
	sidB := openSession(t, clientB)
	if sidA == sidB {
		t.Fatalf("both connections resolved the same session id %q", sidA)
	}

	promptA := clientA.callAsync("session/prompt", SessionPromptParams{
		SessionID: sidA, Prompt: []ContentBlock{{Type: "text", Text: "for A"}},
	})
	promptB := clientB.callAsync("session/prompt", SessionPromptParams{
		SessionID: sidB, Prompt: []ContentBlock{{Type: "text", Text: "for B"}},
	})

	for _, tc := range []struct {
		name       string
		client     *rpcClient
		promptCh   chan frame
		wantPrompt string
		otherText  string
	}{
		{name: "A", client: clientA, promptCh: promptA, wantPrompt: "for A", otherText: "for B"},
		{name: "B", client: clientB, promptCh: promptB, wantPrompt: "for B", otherText: "for A"},
	} {
		_, resp := drainPrompt(t, tc.client, tc.promptCh)
		if resp.Error != nil {
			t.Fatalf("connection %s prompt errored: %+v", tc.name, resp.Error)
		}
		var pr SessionPromptResult
		if err := json.Unmarshal(resp.Result, &pr); err != nil {
			t.Fatalf("connection %s prompt result: %v", tc.name, err)
		}
		if pr.StopReason != StopEndTurn {
			t.Errorf("connection %s stopReason = %q, want end_turn", tc.name, pr.StopReason)
		}
		if pr.TranscriptPath == nil {
			t.Fatalf("connection %s prompt returned no transcript path", tc.name)
		}
		// The turn landed in its own transcript only: the other connection's
		// prompt text appears nowhere on this surface's disk state.
		data, err := os.ReadFile(*pr.TranscriptPath)
		if err != nil {
			t.Fatalf("connection %s transcript: %v", tc.name, err)
		}
		body := string(data)
		if !strings.Contains(body, tc.wantPrompt) {
			t.Errorf("connection %s transcript missing its own prompt %q", tc.name, tc.wantPrompt)
		}
		if strings.Contains(body, tc.otherText) {
			t.Errorf("connection %s transcript leaked the other connection's prompt %q", tc.name, tc.otherText)
		}
	}
}
