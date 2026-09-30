package busmcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"reasonix/internal/sessioncollab"
)

const (
	testTokenDev = "tok-dev-0000000000000000000000000000aa"
	testTokenHB  = "tok-hb-000000000000000000000000000000bb"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	s, err := New(Config{
		Enabled: true,
		Roles: map[string]string{
			"dev":       testTokenDev,
			"heartbeat": testTokenHB,
		},
		MailDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func TestNewFailClosed(t *testing.T) {
	dir := t.TempDir()
	if _, err := New(Config{Enabled: false, Roles: map[string]string{"dev": "t"}, MailDir: dir}); !errors.Is(err, ErrDisabled) {
		t.Fatalf("disabled config: want ErrDisabled, got %v", err)
	}
	if _, err := New(Config{Enabled: true, Roles: nil, MailDir: dir}); !errors.Is(err, ErrDisabled) {
		t.Fatalf("empty roles: want ErrDisabled, got %v", err)
	}
	if _, err := New(Config{Enabled: true, Roles: map[string]string{"dev": "  "}, MailDir: dir}); err == nil {
		t.Fatal("empty token: want error, got nil")
	}
	if _, err := New(Config{Enabled: true, Roles: map[string]string{"Bad_Role": "t"}, MailDir: dir}); err == nil {
		t.Fatal("invalid role name: want error, got nil")
	}
	if _, err := New(Config{Enabled: true, Roles: map[string]string{"dev": "t", "hb": "t"}, MailDir: dir}); err == nil {
		t.Fatal("duplicate token across roles: want error, got nil")
	}
}

func TestAuthorizeBearer(t *testing.T) {
	s := newTestServer(t)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	post := func(token string) int {
		req, _ := http.NewRequest("POST", srv.URL+"/mcp", strings.NewReader("{}"))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("POST /mcp: %v", err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}
	if got := post(""); got != http.StatusUnauthorized {
		t.Fatalf("no token: want 401, got %d", got)
	}
	if got := post("wrong"); got != http.StatusUnauthorized {
		t.Fatalf("wrong token: want 401, got %d", got)
	}
	if got := post(testTokenDev); got == http.StatusUnauthorized {
		t.Fatalf("valid token: got 401 (status %d)", got)
	}
}

func TestEndToEndInboxAndSend(t *testing.T) {
	s := newTestServer(t)
	srv := httptest.NewServer(s.Handler())
	// Cleanup LIFO: connectAs registers cs.Close after this, so the client
	// session (and its standalone SSE connection) closes before the server;
	// the reverse order blocks httptest.Close on the live SSE connection.
	t.Cleanup(func() { srv.Close() })

	cs := connectAs(t, srv.URL, testTokenDev)

	// The heartbeat side drops a message into the dev contact's mailbox
	// directly on the shared store — the sending half of a real peer.
	if _, err := s.mail.Deliver(sessioncollab.MailMessage{
		From: "zcode-heartbeat",
		To:   "zcode-dev",
		Body: "please take task 1",
	}); err != nil {
		t.Fatalf("seed deliver: %v", err)
	}

	// First read drains the batch (mark_read defaults to true).
	var first inboxReadOut
	if err := callTool(t, cs, "collab_inbox_read", map[string]any{}, &first); err != nil {
		t.Fatalf("collab_inbox_read: %v", err)
	}
	if len(first.Messages) != 1 || first.Messages[0].Body != "please take task 1" {
		t.Fatalf("first read: want 1 seeded message, got %+v", first)
	}
	if first.Unread != 0 {
		t.Fatalf("first read: want unread 0 after drain, got %d", first.Unread)
	}

	// Second read sees an empty box; peek keeps messages in place.
	var second inboxReadOut
	if err := callTool(t, cs, "collab_inbox_read", map[string]any{}, &second); err != nil {
		t.Fatalf("second read: %v", err)
	}
	if len(second.Messages) != 0 {
		t.Fatalf("second read: want empty, got %+v", second.Messages)
	}
	if _, err := s.mail.Deliver(sessioncollab.MailMessage{From: "zcode-heartbeat", To: "zcode-dev", Body: "again"}); err != nil {
		t.Fatal(err)
	}
	var peek inboxReadOut
	if err := callTool(t, cs, "collab_inbox_read", map[string]any{"mark_read": false}, &peek); err != nil {
		t.Fatalf("peek: %v", err)
	}
	if len(peek.Messages) != 1 || peek.Unread != 1 {
		t.Fatalf("peek: want message kept and unread 1, got %+v", peek)
	}

	// Sending from the dev role lands in the target contact's inbox.
	var sent sendOut
	if err := callTool(t, cs, "collab_send", map[string]any{"to": "zcode-heartbeat", "body": "task 1 done"}, &sent); err != nil {
		t.Fatalf("collab_send: %v", err)
	}
	if sent.ID == "" || sent.To != "zcode-heartbeat" {
		t.Fatalf("send result: %+v", sent)
	}
	pending, err := s.mail.Peek("zcode-heartbeat")
	if err != nil || len(pending) != 1 || pending[0].From != "zcode-dev" {
		t.Fatalf("heartbeat inbox: want 1 mail from zcode-dev, got %v (%v)", pending, err)
	}
}

func TestTaskCardLifecycle(t *testing.T) {
	s := newTestServer(t)
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(func() { srv.Close() }) // after cs.Close, see TestEndToEndInboxAndSend
	cs := connectAs(t, srv.URL, testTokenDev)

	var created taskCreateOut
	if err := callTool(t, cs, "collab_task_create", map[string]any{"title": "port CommandInbox", "body": "acceptance: serial admission tests green"}, &created); err != nil {
		t.Fatalf("collab_task_create: %v", err)
	}
	if created.CardID == "" || created.Status != "pending" {
		t.Fatalf("create result: %+v", created)
	}

	var upd taskUpdateOut
	if err := callTool(t, cs, "collab_task_update", map[string]any{"card_id": created.CardID, "status": "running", "note": "started"}, &upd); err != nil {
		t.Fatalf("running: %v", err)
	}
	if upd.Status != "running" {
		t.Fatalf("running result: %+v", upd)
	}
	if err := callTool(t, cs, "collab_task_update", map[string]any{"card_id": created.CardID, "status": "done", "result": "merged"}, &upd); err != nil {
		t.Fatalf("done: %v", err)
	}

	// Terminal states are terminal: done → running must surface as a tool
	// error result, not a Go error.
	result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "collab_task_update",
		Arguments: map[string]any{"card_id": created.CardID, "status": "running"},
	})
	if err != nil {
		t.Fatalf("done→running call: %v", err)
	}
	if !result.IsError {
		t.Fatalf("done→running: want IsError, got %+v", result)
	}
	card, err := s.cards.Get(created.CardID)
	if err != nil || card.Status != sessioncollab.StatusDone {
		t.Fatalf("card after rejected transition: %+v (%v)", card, err)
	}
	if len(card.Nodes) == 0 || card.Nodes[len(card.Nodes)-1].ContactID != "zcode-dev" {
		t.Fatalf("card chain missing zcode node: %+v", card.Nodes)
	}
}

func TestHandleEvent(t *testing.T) {
	s := newTestServer(t)
	srv := httptest.NewServer(http.HandlerFunc(s.HandleEvent))
	defer srv.Close()

	post := func(token, body string) *http.Response {
		req, _ := http.NewRequest("POST", srv.URL+"/bus/events", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("POST /bus/events: %v", err)
		}
		return resp
	}

	resp := post("wrong", `{"event":"Stop"}`)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong token: want 401, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	resp = post(testTokenHB, `{"session_id":"s1"}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing event: want 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	resp = post(testTokenHB, `{"event":"Stop","session_id":"s1","summary":"batch 7 finished"}`)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("valid event: want 202, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	pending, err := s.mail.Peek(s.eventTarget)
	if err != nil || len(pending) != 1 {
		t.Fatalf("event target inbox: want 1 message, got %v (%v)", pending, err)
	}
	if pending[0].From != "zcode-heartbeat" || !strings.Contains(pending[0].Body, `"event":"Stop"`) {
		t.Fatalf("event message: %+v", pending[0])
	}

	// Audit trail exists and records both an auth failure and a delivery.
	b, err := os.ReadFile(filepath.Join(s.mail.Dir(), "bus-mcp-audit.jsonl"))
	if err != nil || len(strings.Split(strings.TrimSpace(string(b)), "\n")) < 2 {
		t.Fatalf("audit log: want >=2 lines, file err %v", err)
	}
}

// TestAuditWrittenOnDeniedMCP pins the fail-closed visibility guarantee: a
// denied /mcp attempt leaves a trace even though no tool ran.
func TestAuditWrittenOnDeniedMCP(t *testing.T) {
	s := newTestServer(t)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	req, _ := http.NewRequest("POST", srv.URL+"/mcp", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	b, err := os.ReadFile(filepath.Join(s.mail.Dir(), "bus-mcp-audit.jsonl"))
	if err != nil || !strings.Contains(string(b), `"ok":false`) {
		t.Fatalf("audit log after denial: %q (%v)", b, err)
	}
}

// ── helpers ──────────────────────────────────────────────────────────────────

type bearerTransport struct {
	token string
	next  http.RoundTripper
}

func (t *bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+t.token)
	return t.next.RoundTrip(r)
}

func connectAs(t *testing.T, serverURL, token string) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "busmcp-test", Version: "0"}, nil)
	transport := &mcp.StreamableClientTransport{
		Endpoint:   serverURL + "/mcp",
		HTTPClient: &http.Client{Transport: &bearerTransport{token: token, next: http.DefaultTransport}},
	}
	cs, err := client.Connect(context.Background(), transport, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// callTool invokes a tool and decodes the SDK's structured content into out.
// The SDK marshals the typed handler output into StructuredContent; the
// re-marshal normalizes whatever dynamic shape it carries into our structs.
func callTool(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any, out any) error {
	t.Helper()
	result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return err
	}
	if result.IsError {
		b, _ := json.Marshal(result.Content)
		return errors.New("tool error: " + string(b))
	}
	b, err := json.Marshal(result.StructuredContent)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

// TestSpawnGateAndQuota pins the collab_spawn fail-closed contract: the tool
// exists only for spawn-authorized roles, and the daily quota caps even the
// authorized ones. Every denial lands in the audit log.
func TestSpawnGateAndQuota(t *testing.T) {
	s, err := New(Config{
		Enabled: true,
		Roles: map[string]string{
			"dev":       testTokenDev,
			"heartbeat": testTokenHB,
		},
		MailDir:         t.TempDir(),
		SpawnRoles:      []string{"heartbeat"},
		SpawnDailyQuota: 1,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(func() { srv.Close() })

	// Unauthorized role: the tool was never registered for it.
	csDev := connectAs(t, srv.URL, testTokenDev)
	result, err := csDev.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "collab_spawn",
		Arguments: map[string]any{"to": "zcode-worker", "title": "t", "body": "b"},
	})
	if err == nil && !result.IsError {
		t.Fatal("dev spawn: want tool-not-found or error, got success")
	}

	// Authorized role: first call succeeds and creates card + assignment mail.
	csHB := connectAs(t, srv.URL, testTokenHB)
	var first spawnOut
	if err := callTool(t, csHB, "collab_spawn", map[string]any{
		"to": "zcode-worker", "title": "run batch", "body": "acceptance: batch green",
	}, &first); err != nil {
		t.Fatalf("heartbeat spawn: %v", err)
	}
	if first.CardID == "" || first.MailID == "" || first.To != "zcode-worker" {
		t.Fatalf("spawn result: %+v", first)
	}
	pending, err := s.mail.Peek("zcode-worker")
	if err != nil || len(pending) != 1 {
		t.Fatalf("assignment mail: want 1, got %v (%v)", pending, err)
	}
	card, err := s.cards.Get(first.CardID)
	if err != nil || card.Initiator != "zcode-heartbeat" || card.Assignee != "zcode-worker" {
		t.Fatalf("card: %+v (%v)", card, err)
	}

	// Quota (1/day): the second call must fail and the audit trail must
	// show both a success and the denial.
	var second spawnOut
	err = callTool(t, csHB, "collab_spawn", map[string]any{
		"to": "zcode-worker", "title": "second", "body": "b",
	}, &second)
	if err == nil || !strings.Contains(err.Error(), "quota") {
		t.Fatalf("second spawn: want quota error, got %v", err)
	}
	audit := readAudit(t, s)
	if !strings.Contains(audit, "daily quota exhausted") || !strings.Contains(audit, "card=") {
		t.Fatalf("audit missing spawn traces: %s", audit)
	}
}

func readAudit(t *testing.T, s *Server) string {
	t.Helper()
	b, err := os.ReadFile(s.auditPath)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	return string(b)
}
