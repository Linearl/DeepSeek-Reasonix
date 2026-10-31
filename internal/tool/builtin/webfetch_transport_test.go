package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"reasonix/internal/netclient"
)

// resetTransportCacheForTest drains the global transport cache (closing idle
// connections so tests don't leak sockets into each other).
func resetTransportCacheForTest() {
	transportCacheMu.Lock()
	defer transportCacheMu.Unlock()
	for _, tr := range transportCache {
		tr.CloseIdleConnections()
	}
	transportCache = map[string]*http.Transport{}
	transportCacheOrder = nil
}

// countingListener tracks how many TCP connections are currently open, so
// tests can observe keep-alive reuse and idle-connection recycling from the
// server side.
type countingListener struct {
	net.Listener
	mu    sync.Mutex
	open  int
	total int
}

func (l *countingListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	l.mu.Lock()
	l.open++
	l.total++
	l.mu.Unlock()
	return &countingConn{Conn: c, l: l}, nil
}

func (l *countingListener) stats() (open, total int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.open, l.total
}

type countingConn struct {
	net.Conn
	l    *countingListener
	once sync.Once
}

func (c *countingConn) Close() error {
	var err error
	c.once.Do(func() {
		err = c.Conn.Close()
		c.l.mu.Lock()
		c.l.open--
		c.l.mu.Unlock()
	})
	return err
}

func startCountingServer(t *testing.T, body string) (string, *countingListener) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cl := &countingListener{Listener: lis}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, body)
	})}
	t.Cleanup(func() { srv.Close() })
	go func() { _ = srv.Serve(cl) }()
	return "http://" + lis.Addr().String() + "/", cl
}

// TestTransportCacheHitsPerProxyKey proves transports are cached by proxyURL:
// the same key yields the same instance, a different key yields a new one.
func TestTransportCacheHitsPerProxyKey(t *testing.T) {
	resetTransportCacheForTest()
	t.Cleanup(resetTransportCacheForTest)

	first := cachedTransport("proxy-a")
	second := cachedTransport("proxy-a")
	if first != second {
		t.Fatal("same proxyURL must return the cached transport instance")
	}
	other := cachedTransport("proxy-b")
	if other == first {
		t.Fatal("different proxyURL must not share a transport instance")
	}
	direct := cachedTransport("")
	if direct == first || direct == other {
		t.Fatal("direct transport must have its own cache entry")
	}
}

// TestTransportCacheReusesKeepAliveConn is the end-to-end leak reading: two
// sequential web_fetch calls must ride ONE kept-alive TCP connection. Before
// the fix each call built a fresh Transport and opened a second connection.
func TestTransportCacheReusesKeepAliveConn(t *testing.T) {
	resetTransportCacheForTest()
	t.Cleanup(resetTransportCacheForTest)

	targetURL, listener := startCountingServer(t, "keep-alive reuse")
	wf := webFetch{proxySpec: netclient.ProxySpec{Mode: netclient.ModeOff}}
	args, _ := json.Marshal(map[string]string{"url": targetURL})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for i := 0; i < 2; i++ {
		if _, err := wf.Execute(ctx, args); err != nil {
			t.Fatalf("execute #%d: %v", i+1, err)
		}
	}

	open, total := listener.stats()
	if total != 1 || open != 1 {
		t.Fatalf("connection reuse broken: total conns = %d, open = %d; want 1/1", total, open)
	}
}

// TestTransportCacheEvictionClosesIdleConns is the idle-recycling reading:
// once a transport is FIFO-evicted from the cache its idle keep-alive
// connection must be closed (CloseIdleConnections fallback), not left pinned.
func TestTransportCacheEvictionClosesIdleConns(t *testing.T) {
	resetTransportCacheForTest()
	t.Cleanup(resetTransportCacheForTest)

	targetURL, listener := startCountingServer(t, "idle recycle")
	wf := webFetch{proxySpec: netclient.ProxySpec{Mode: netclient.ModeOff}}
	args, _ := json.Marshal(map[string]string{"url": targetURL})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := wf.Execute(ctx, args); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if open, total := listener.stats(); open != 1 || total != 1 {
		t.Fatalf("precondition: open = %d, total = %d; want 1/1", open, total)
	}

	// Push the cache past its FIFO bound so the "" entry (holding the idle
	// conn above) is evicted.
	for i := 0; i < webfetchMaxCachedTransports; i++ {
		cachedTransport(fmt.Sprintf("filler-%d", i))
	}

	// The evicted transport's idle conn must drop; poll briefly for the
	// server-side close to land.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if open, _ := listener.stats(); open == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if open, _ := listener.stats(); open != 0 {
		t.Fatalf("evicted transport kept %d idle connection(s) open; want 0", open)
	}
}

// TestTransportCacheIdleConnReusedNotReDialed checks the same cached transport
// serves a second request over the existing idle conn (no re-dial), i.e. the
// cache delivers actual reuse rather than just bounded leakage.
func TestTransportCacheIdleConnReusedNotReDialed(t *testing.T) {
	resetTransportCacheForTest()
	t.Cleanup(resetTransportCacheForTest)

	targetURL, listener := startCountingServer(t, "idle then reuse")
	wf := webFetch{proxySpec: netclient.ProxySpec{Mode: netclient.ModeOff}}
	args, _ := json.Marshal(map[string]string{"url": targetURL})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for i := 0; i < 3; i++ {
		if _, err := wf.Execute(ctx, args); err != nil {
			t.Fatalf("execute #%d: %v", i+1, err)
		}
		time.Sleep(20 * time.Millisecond) // let the conn settle back to idle
	}

	open, total := listener.stats()
	if total != 1 || open != 1 {
		t.Fatalf("expected one long-lived conn across 3 fetches: total = %d, open = %d", total, open)
	}
}
