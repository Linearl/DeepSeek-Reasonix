package servepool

// tl 1182: the gateway mounts the task-bus MCP surface (SetBus) so 18789 is
// the single external MCP entry. These tests pin the three-layer contract:
//
//	auth    — bus paths authenticate with per-role bearer tokens inside the
//	          bus handlers (gateway token is meaningless there); every other
//	          path keeps the strict gateway bearer check; an UNMOUNTED
//	          gateway never opens an unauthenticated /mcp.
//	session — MCP client sessions complete initialize + tool calls through
//	          the gateway (the streamable session lives in the one in-process
//	          bus instance, so sticky routing is structural).
//	routing — bus traffic never touches the project-serve route: no /p/<id>
//	          spawn is triggered, projects stay unspawned, and bus role
//	          tokens cannot reach /p/* paths.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"reasonix/internal/busmcp"
)

const (
	busTokenDev    = "tok-dev-0000000000000000000000000000aa"
	busTokenHB     = "tok-hb-000000000000000000000000000000bb"
	busGatewayTok  = "gateway-secret-tl1182"
	busEventTarget = "zcode-heartbeat"
)

// newBusForTest builds a bus with two roles on a throwaway mailbox dir.
func newBusForTest(t *testing.T) (*busmcp.Server, string) {
	t.Helper()
	dir := t.TempDir()
	bus, err := busmcp.New(busmcp.Config{
		Enabled: true,
		Roles: map[string]string{
			"dev":       busTokenDev,
			"heartbeat": busTokenHB,
		},
		MailDir: dir,
	})
	if err != nil {
		t.Fatalf("busmcp.New: %v", err)
	}
	return bus, dir
}

// newBusGateway is a mounted gateway over a manager whose spawn binary never
// exists: if any bus request were routed into a project serve, the tests
// would see spawn attempts / non-zero ports instead of clean bus answers.
func newBusGateway(t *testing.T) (*Gateway, *httptest.Server, string) {
	t.Helper()
	m := newTestManager(t)
	g := NewGateway(m, busGatewayTok)
	bus, mailDir := newBusForTest(t)
	g.SetBus(bus.Handler(), http.HandlerFunc(bus.HandleEvent))
	ts := httptest.NewServer(g)
	t.Cleanup(ts.Close)
	return g, ts, mailDir
}

type busBearerTransport struct{ token string }

func (t *busBearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+t.token)
	return http.DefaultTransport.RoundTrip(r)
}

// connectGatewayBus runs a full MCP initialize against the gateway's /mcp
// route with a per-role bearer token, exactly as an external zcode runtime
// would after the port convergence. Call from the test goroutine (it
// registers cleanup).
func connectGatewayBus(t *testing.T, serverURL, token string) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "gateway-bus-test", Version: "0"}, nil)
	transport := &mcp.StreamableClientTransport{
		Endpoint:   serverURL + "/mcp",
		HTTPClient: &http.Client{Transport: &busBearerTransport{token: token}},
	}
	cs, err := client.Connect(context.Background(), transport, nil)
	if err != nil {
		t.Fatalf("MCP connect through gateway: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// callBusTool invokes a bus tool and fails the test on transport or tool
// errors.
func callBusTool(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	if result.IsError {
		b, _ := json.Marshal(result.Content)
		t.Fatalf("call %s returned tool error: %s", name, string(b))
	}
	return result
}

// busRequest runs one raw HTTP request with a bearer token and returns the
// response (body unclosed — the caller closes it via mustStatus or defer).
func busRequest(t *testing.T, method, url, token, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	return resp
}

func mustStatus(t *testing.T, resp *http.Response, want int, what string) {
	t.Helper()
	defer resp.Body.Close()
	if resp.StatusCode != want {
		t.Fatalf("%s: want %d, got %d", what, want, resp.StatusCode)
	}
}

// TestGatewayBusMCPMounted pins the auth split on a mounted gateway: role
// tokens drive the bus through /mcp; the gateway token neither opens the
// bus paths nor loses its grip on the regular ones.
func TestGatewayBusMCPMounted(t *testing.T) {
	_, ts, _ := newBusGateway(t)

	// (a) Full MCP session through 18789: initialize + a tool call.
	cs := connectGatewayBus(t, ts.URL, busTokenDev)
	callBusTool(t, cs, "collab_inbox_read", map[string]any{})

	// (b) No token: 401 from the bus handler (fail-closed).
	mustStatus(t, busRequest(t, http.MethodPost, ts.URL+"/mcp", "", "{}"),
		http.StatusUnauthorized, "no token /mcp")
	// (c) The gateway token is NOT a bus credential: 401, same as the
	// standalone serve where the role table is the only key set.
	mustStatus(t, busRequest(t, http.MethodPost, ts.URL+"/mcp", busGatewayTok, "{}"),
		http.StatusUnauthorized, "gateway token /mcp")
	// (d) Regular paths keep the gateway bearer check — semantics intact.
	mustStatus(t, busRequest(t, http.MethodGet, ts.URL+"/status", busGatewayTok, ""),
		http.StatusOK, "gateway token /status")
	// (e) A bus role token cannot reach regular gateway paths either.
	mustStatus(t, busRequest(t, http.MethodGet, ts.URL+"/status", busTokenDev, ""),
		http.StatusUnauthorized, "role token /status")
	// (f) The unauthenticated default route stays closed for anonymous
	// traffic (401 before the 404 default, exactly as before tl 1182).
	mustStatus(t, busRequest(t, http.MethodGet, ts.URL+"/manifest", "", ""),
		http.StatusUnauthorized, "no token /manifest")
}

// TestGatewayBusUnmountedFailClosed: without SetBus the bus paths must not
// become an unauthenticated hole — no gateway token means 401 (the normal
// bearer gate), a valid gateway token reaches the 404 default route.
func TestGatewayBusUnmountedFailClosed(t *testing.T) {
	m := newTestManager(t)
	g := NewGateway(m, busGatewayTok)
	ts := httptest.NewServer(g)
	defer ts.Close()

	for _, path := range []string{"/mcp", "/bus/events"} {
		mustStatus(t, busRequest(t, http.MethodPost, ts.URL+path, "", "{}"),
			http.StatusUnauthorized, "unmounted "+path+" no token")
		mustStatus(t, busRequest(t, http.MethodPost, ts.URL+path, busGatewayTok, "{}"),
			http.StatusNotFound, "unmounted "+path+" with gateway token")
	}
}

// TestGatewayBusEventsDelivers: the hook receiver mounted at /bus/events
// authenticates with role tokens (and only role tokens) and lands the
// payload in the event target's mailbox on the shared dir.
func TestGatewayBusEventsDelivers(t *testing.T) {
	_, ts, mailDir := newBusGateway(t)

	mustStatus(t, busRequest(t, http.MethodPost, ts.URL+"/bus/events", busTokenDev,
		`{"event":"turn_done","session_id":"s1","summary":"tl1182"}`),
		http.StatusAccepted, "event with role token")

	raw, err := os.ReadFile(filepath.Join(mailDir, busEventTarget+".inbox.jsonl"))
	if err != nil {
		t.Fatalf("event target inbox: %v", err)
	}
	// The body arrives JSON-escaped inside the mail envelope, so match
	// without the surrounding quotes.
	if !strings.Contains(string(raw), "turn_done") || !strings.Contains(string(raw), "zcode-dev") {
		t.Fatalf("event not delivered with sender role: %s", string(raw))
	}
	mustStatus(t, busRequest(t, http.MethodPost, ts.URL+"/bus/events", "", `{"event":"x"}`),
		http.StatusUnauthorized, "event without token")
	mustStatus(t, busRequest(t, http.MethodPost, ts.URL+"/bus/events", busGatewayTok, `{"event":"x"}`),
		http.StatusUnauthorized, "event with gateway token")
}

type busInboxOut struct {
	Messages []struct {
		From string `json:"from"`
		Body string `json:"body"`
	} `json:"messages"`
	Unread int `json:"unread"`
}

func decodeInbox(t *testing.T, result *mcp.CallToolResult) busInboxOut {
	t.Helper()
	b, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("encode structured content: %v", err)
	}
	var out busInboxOut
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("decode inbox: %v (%s)", err, string(b))
	}
	return out
}

// TestGatewayBusMultiProjectRouting is the ≥2-serve acceptance: two MCP
// client sessions (two roles) run concurrently through one gateway while
// TWO projects are registered, and
//
//  1. neither session's traffic spawns or touches a project serve (ports
//     stay 0 — bus routing never enters the /p/<id> path),
//  2. mail sent by the dev session lands in the heartbeat session's inbox
//     and nowhere else (no cross-talk),
//  3. bus role tokens cannot open project routes.
func TestGatewayBusMultiProjectRouting(t *testing.T) {
	dirA, dirB := t.TempDir(), t.TempDir()
	m := newTestManager(t, dirA, dirB)
	g := NewGateway(m, busGatewayTok)
	bus, _ := newBusForTest(t)
	g.SetBus(bus.Handler(), http.HandlerFunc(bus.HandleEvent))
	ts := httptest.NewServer(g)
	// Cleanup ordering (busmcp_test.go lesson): client sessions register
	// cs.Close via t.Cleanup DURING the test body, so the server close must
	// be registered BEFORE any connect — LIFO then closes sessions first,
	// and a deferred ts.Close (which runs before t.Cleanup) would block on
	// the live SSE connections instead.
	t.Cleanup(ts.Close)

	csDev := connectGatewayBus(t, ts.URL, busTokenDev)
	csHB := connectGatewayBus(t, ts.URL, busTokenHB)

	// Concurrent tool traffic from both sessions — the gateway multiplexes
	// them onto the one bus instance (structural session stickiness).
	errs := make(chan error, 2)
	go func() {
		_, err := csDev.CallTool(context.Background(), &mcp.CallToolParams{
			Name: "collab_send", Arguments: map[string]any{
				"to":   busEventTarget,
				"body": "hello from dev",
			},
		})
		errs <- err
	}()
	go func() {
		_, err := csHB.CallTool(context.Background(), &mcp.CallToolParams{
			Name: "collab_inbox_read", Arguments: map[string]any{"mark_read": false},
		})
		errs <- err
	}()
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent bus call %d: %v", i, err)
		}
	}

	// dev's mail landed in the heartbeat box and nowhere else.
	inbox := decodeInbox(t, callBusTool(t, csHB, "collab_inbox_read", map[string]any{}))
	if len(inbox.Messages) != 1 || inbox.Messages[0].From != "zcode-dev" || inbox.Messages[0].Body != "hello from dev" {
		t.Fatalf("heartbeat inbox: want exactly 1 mail from zcode-dev, got %+v", inbox)
	}
	devBox := decodeInbox(t, callBusTool(t, csDev, "collab_inbox_read", map[string]any{}))
	if len(devBox.Messages) != 0 {
		t.Fatalf("dev inbox must be empty, got %+v", devBox)
	}

	// Routing untangled: no project serve was spawned or touched by any of
	// the bus traffic above.
	ids := g.managerProjectIDs()
	if len(ids) < 2 {
		t.Fatalf("want both projects registered, got %v", ids)
	}
	for _, id := range ids {
		if port := m.Port(id); port != 0 {
			t.Fatalf("bus traffic touched project %q (port %d)", id, port)
		}
	}

	// A role token cannot open the project route; the gateway token still
	// sees the manifest.
	mustStatus(t, busRequest(t, http.MethodGet, ts.URL+"/p/"+ids[0]+"/sessions", busTokenDev, ""),
		http.StatusUnauthorized, "role token on /p/")
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/manifest", nil)
	req.Header.Set("Authorization", "Bearer "+busGatewayTok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("gateway token /manifest: want 200, got %d", resp.StatusCode)
	}
}

// managerProjectIDs lists the registered project ids (manifest snapshot).
func (g *Gateway) managerProjectIDs() []string {
	projects := g.mgr.Projects()
	ids := make([]string, 0, len(projects))
	for _, p := range projects {
		ids = append(ids, p.ID)
	}
	return ids
}

// TestBusHandlersFromConfig pins the host-side construction contract:
// disabled/absent config mounts nothing without an error, a broken role
// table reports why, and a valid table yields two live handlers that a
// gateway can mount for a working MCP round trip.
func TestBusHandlersFromConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	mailDir := t.TempDir()

	// (a) No config file at all: nothing mounted, no error.
	if mcpH, evH, err := BusHandlersFromConfig(); err != nil || mcpH != nil || evH != nil {
		t.Fatalf("absent config: want nil,nil,nil, got %v,%v,%v", mcpH, evH, err)
	}

	// (b) Broken table (empty token): error explains the absent bus.
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(`
[serve.bus_mcp]
enabled = true
roles = { dev = "" }
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := BusHandlersFromConfig(); err == nil {
		t.Fatal("broken role table: want error, got nil")
	}

	// (c) Valid table: two live handlers that complete an MCP round trip
	// through a mounted gateway.
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(`
[serve.bus_mcp]
enabled = true
roles = { dev = "`+busTokenDev+`" }
mail_dir = "`+filepath.ToSlash(mailDir)+`"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	mcpH, evH, err := BusHandlersFromConfig()
	if err != nil || mcpH == nil || evH == nil {
		t.Fatalf("valid config: want two handlers, got %v,%v (%v)", mcpH, evH, err)
	}
	m := newTestManager(t)
	g := NewGateway(m, busGatewayTok)
	g.SetBus(mcpH, evH)
	ts := httptest.NewServer(g)
	t.Cleanup(ts.Close) // before connectGatewayBus: LIFO closes the session first
	cs := connectGatewayBus(t, ts.URL, busTokenDev)
	callBusTool(t, cs, "collab_inbox_read", map[string]any{})
}
