package plugin

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"reasonix/internal/netclient"
)

// Task 168: the MCP connection layer must bound every connect attempt by the
// spec's startup timeout (configurable, 30s default), and the timeout error
// itself must name the server — a bare "context deadline exceeded" gives the
// model and the user nothing to act on.
func TestHTTPTransportConnectTimeoutNamesServer(t *testing.T) {
	// A raw listener black hole: accepts TCP, drains writes, never answers —
	// the shape of an endpoint whose connect succeeds but whose HTTP layer is
	// unreachable. Only the startup timeout can end the wait.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var connsMu sync.Mutex
	var conns []net.Conn
	var readers sync.WaitGroup
	acceptDone := make(chan struct{})
	go func() {
		defer close(acceptDone)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			connsMu.Lock()
			conns = append(conns, conn)
			connsMu.Unlock()
			readers.Add(1)
			go func(c net.Conn) {
				defer readers.Done()
				defer c.Close()
				buf := make([]byte, 4096)
				for {
					if _, err := c.Read(buf); err != nil {
						return
					}
				}
			}(conn)
		}
	}()
	// Unblock the draining readers deterministically: an expired read deadline
	// ends each goroutine without waiting for the client side to close first
	// (the package runs goleak on every test).
	t.Cleanup(func() {
		listener.Close()
		connsMu.Lock()
		for _, conn := range conns {
			_ = conn.SetReadDeadline(time.Now())
		}
		conns = nil
		connsMu.Unlock()
		done := make(chan struct{})
		go func() {
			readers.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
		<-acceptDone
	})

	transport, err := newHTTPTransport(Spec{
		Name: "slowexa", Type: "http", URL: "http://" + listener.Addr().String() + "/mcp",
		StartupTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer transport.close()

	started := time.Now()
	_, err = transport.call(context.Background(), "tools/list", map[string]any{})
	elapsed := time.Since(started)
	if err == nil {
		t.Fatal("connect unexpectedly succeeded against a silent endpoint")
	}
	if !strings.Contains(err.Error(), "slowexa") {
		t.Fatalf("timeout error does not name the server %q: %v", "slowexa", err)
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("timeout error does not say it timed out: %v", err)
	}
	if elapsed > 10*time.Second {
		t.Fatalf("connect returned after %s, want around the configured 2s startup timeout", elapsed)
	}
}

// Task 168 acceptance: the connect timeout is at most 30s out of the box.
func TestDefaultStartupTimeoutIsThirtySeconds(t *testing.T) {
	if got := DefaultStartupTimeout(); got != 30*time.Second {
		t.Fatalf("DefaultStartupTimeout() = %s, want 30s", got)
	}
	if got := DefaultStartupWaitBudget(); got != 5*time.Second {
		t.Fatalf("DefaultStartupWaitBudget() = %s, want 5s", got)
	}
}

// proxyRecordingServer returns an httptest server standing in for an HTTP
// proxy. It records the Host header of every request it receives — for an
// http:// target Go sends the absolute-form request whose Host is the target
// origin, so seeing the target host proves the request was proxied.
func proxyRecordingServer(recording *atomic.Value) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recording.Store(r.Host)
		w.WriteHeader(http.StatusBadGateway)
	}))
}

// Task 168: an [network] proxy_mode=custom proxy must reach the MCP http
// transport — overseas http MCP servers are unreachable without it.
func TestHTTPTransportUsesConfiguredProxy(t *testing.T) {
	var recording atomic.Value
	proxySrv := proxyRecordingServer(&recording)
	defer proxySrv.Close()

	spec := Spec{
		Name: "exa", Type: "http", URL: "http://mcp.example.invalid/mcp",
		Proxy: netclient.ProxySpec{Mode: netclient.ModeCustom, URL: proxySrv.URL},
	}
	client, err := newMCPHTTPClient(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, spec.URL, strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request through the configured proxy: %v", err)
	}
	resp.Body.Close()
	if got, _ := recording.Load().(string); got != "mcp.example.invalid" {
		t.Fatalf("proxy saw Host %q, want mcp.example.invalid (the configured proxy was not applied)", got)
	}
}

// Task 168: proxy_mode auto must keep honoring https_proxy/http_proxy style
// environment variables for MCP http transports.
func TestHTTPTransportProxyAutoModeReadsEnvironment(t *testing.T) {
	var recording atomic.Value
	proxySrv := proxyRecordingServer(&recording)
	defer proxySrv.Close()

	t.Setenv("HTTP_PROXY", proxySrv.URL)
	t.Setenv("HTTPS_PROXY", proxySrv.URL)
	t.Setenv("NO_PROXY", "")

	// Proxy is the zero value: auto mode (env vars, then OS system proxy).
	spec := Spec{Name: "exa", Type: "http", URL: "http://mcp.example.invalid/mcp"}
	client, err := newMCPHTTPClient(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, spec.URL, strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request through the environment proxy: %v", err)
	}
	resp.Body.Close()
	if got, _ := recording.Load().(string); got != "mcp.example.invalid" {
		t.Fatalf("proxy saw Host %q, want mcp.example.invalid (env proxy was not applied in auto mode)", got)
	}
}

// proxy_mode=off must dial direct: the proxy must see nothing.
func TestHTTPTransportProxyOffDialsDirect(t *testing.T) {
	var recording atomic.Value
	proxySrv := proxyRecordingServer(&recording)
	defer proxySrv.Close()

	spec := Spec{
		Name: "exa", Type: "http", URL: "http://mcp.example.invalid/mcp",
		Proxy: netclient.ProxySpec{Mode: netclient.ModeOff},
	}
	client, err := newMCPHTTPClient(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, spec.URL, strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Do(req); err == nil {
		t.Fatal("direct dial to an unresolvable host unexpectedly succeeded")
	}
	if _, seen := recording.Load().(string); seen {
		t.Fatal("proxy_mode=off still contacted the proxy")
	}
}

// Task 168 regression: loopback MCP endpoints (local IDE/browser proxies on
// 127.0.0.1) must never be routed through any proxy — the OS system proxy can
// never reach the user's own loopback listeners, which hung local-server MCP
// transports when a Windows system proxy was configured. The target below is a
// loopback httptest server; the recording proxy must see nothing.
func TestHTTPTransportLoopbackStaysDirectDespiteProxy(t *testing.T) {
	var recording atomic.Value
	proxySrv := proxyRecordingServer(&recording)
	defer proxySrv.Close()

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
	}))
	defer target.Close()

	spec := Spec{
		Name: "localjetbrains", Type: "http", URL: target.URL,
		Proxy: netclient.ProxySpec{Mode: netclient.ModeCustom, URL: proxySrv.URL},
	}
	client, err := newMCPHTTPClient(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, spec.URL, strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("loopback request forced through the proxy: %v", err)
	}
	resp.Body.Close()
	if _, seen := recording.Load().(string); seen {
		t.Fatal("loopback endpoint was proxied; local MCP servers must stay direct")
	}
}
