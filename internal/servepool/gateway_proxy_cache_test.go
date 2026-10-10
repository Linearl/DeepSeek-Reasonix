package servepool

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// Task 746: the gateway's per-project proxy cache never expired and was
// accessed without a lock. These tests pin both halves: an entry must rebuild
// when the manager's port changes (idle reclaim / crash respawn bind a fresh
// 127.0.0.1:0 port), and concurrent proxyFor traffic must not reach the map
// unsynchronized (a fatal concurrent-map-write crash no recover can catch).

func gatewayGet(t *testing.T, url string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer s")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func TestGatewayProxyCacheRebuildsOnPortChange(t *testing.T) {
	m, root := takeoverGateTestManager(t)
	id := WorkspaceSlug(root)

	// Both backends stay live for the whole test: the stale-cache failure
	// mode this pins is "the OLD backend keeps answering" (permanent wrong
	// target), not a dial failure a 502 could mask.
	b1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("one"))
	}))
	defer b1.Close()
	b2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("two"))
	}))
	defer b2.Close()

	g := NewGateway(m, "s")
	ts := httptest.NewServer(g)
	defer ts.Close()

	takeoverGateForceRunning(t, m, id, backendPort(t, b1))
	status, body := gatewayGet(t, ts.URL+"/p/"+id+"/sessions")
	if status != http.StatusOK || body != "one" {
		t.Fatalf("first proxy = %d %q, want 200 \"one\"", status, body)
	}

	// The serve died / was idle-reclaimed and respawned on a fresh port: the
	// manager record moving is the whole respawn as far as the gateway sees.
	takeoverGateForceRunning(t, m, id, backendPort(t, b2))
	status, body = gatewayGet(t, ts.URL+"/p/"+id+"/sessions")
	if status != http.StatusOK || body != "two" {
		t.Fatalf("proxy after port change = %d %q, want 200 \"two\" — the cached entry must rebuild against the new port (task 746)", status, body)
	}
}

func TestGatewayProxyForConcurrentAccess(t *testing.T) {
	m, root := takeoverGateTestManager(t)
	id := WorkspaceSlug(root)
	g := NewGateway(m, "s")

	var wg sync.WaitGroup
	for n := 0; n < 32; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 300; j++ {
				// Alternate the port so goroutines also rebuild entries, not
				// only read the cached one — the unsynchronized map write this
				// reaches is a fatal runtime error no recover can catch.
				p := g.proxyFor(id, 30000+(n+j)%2)
				if p == nil {
					t.Error("proxyFor returned nil")
					return
				}
			}
		}(n)
	}
	wg.Wait()
}
