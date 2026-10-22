package servepool

// Task 1049: the spawn timeout default was 8s while a cold serve start
// measures ~9s on the dev machine (skill/memory canonical probe scan), so
// every lazy spawn 503'd at the gateway while the serve itself was healthy.
// These tests pin the three fixes: the 30s default, the config override
// path ([serve.pool] via PoolTimeoutsFromSeconds), and the 503 body carrying
// a serve output summary instead of a bare timeout line.

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	// Helper-process mode: the spawn tests point Config.ReasonixBin at this
	// test binary; the pool invokes it with "serve" as the first argument,
	// which normal `go test` runs never see as os.Args[1].
	if len(os.Args) > 1 && os.Args[1] == "serve" {
		runFakeServeHelper()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// runFakeServeHelper mimics a serve that starts, logs, and never becomes
// ready: it prints the marker on both streams and then sits silent — no port
// file — so the spawn always rides the timeout path with captured output.
func runFakeServeHelper() {
	const marker = "SERVEPOOL1049 fake serve output"
	_, _ = os.Stdout.WriteString(marker + " (stdout)\n")
	_, _ = os.Stderr.WriteString(marker + " (stderr)\n")
	time.Sleep(2 * time.Minute)
}

func TestSpawnTimeoutDefaultIs30s(t *testing.T) {
	// Not newTestManager: it pins short timeouts explicitly; this test owns a
	// zero-value Config so the NewManager fallbacks are what's under test.
	m, err := NewManager(Config{
		ReasonixBin:  "definitely-not-a-real-binary",
		ProjectRoots: []string{t.TempDir()},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	if m.cfg.SpawnTimeout != 30*time.Second {
		t.Fatalf("default SpawnTimeout = %s, want 30s (task 1049: 8s < ~9s cold start)", m.cfg.SpawnTimeout)
	}
	if m.cfg.IdleTimeout != 15*time.Minute {
		t.Fatalf("default IdleTimeout = %s, want 15m", m.cfg.IdleTimeout)
	}
}

func TestPoolTimeoutsFromSeconds(t *testing.T) {
	cases := []struct {
		name        string
		spawn, idle int
		wantSpawn   time.Duration
		wantIdle    time.Duration
	}{
		{"absent keys keep defaults", 0, 0, 30 * time.Second, 15 * time.Minute},
		{"positive values override", 45, 120, 45 * time.Second, 2 * time.Minute},
		{"negative spawn keeps default", -1, 0, 30 * time.Second, 15 * time.Minute},
		{"negative idle keeps default", 0, -5, 30 * time.Second, 15 * time.Minute},
	}
	for _, tc := range cases {
		spawn, idle := PoolTimeoutsFromSeconds(tc.spawn, tc.idle)
		if spawn != tc.wantSpawn || idle != tc.wantIdle {
			t.Fatalf("%s: PoolTimeoutsFromSeconds(%d, %d) = (%s, %s), want (%s, %s)",
				tc.name, tc.spawn, tc.idle, spawn, idle, tc.wantSpawn, tc.wantIdle)
		}
	}
}

// TestSpawnTimeoutOverrideDrivesDeadline proves the override is not just
// stored: the spawn wait actually ends on the configured (short) deadline.
func TestSpawnTimeoutOverrideDrivesDeadline(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	m, err := NewManager(Config{
		ReasonixBin:  self, // helper mode: prints, never writes the port file
		ProjectRoots: []string{root},
		SpawnTimeout: 700 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	start := time.Now()
	err = m.Open(WorkspaceSlug(root))
	if err == nil {
		t.Fatal("Open = nil, want timeout error")
	}
	if !strings.Contains(err.Error(), "within 700ms") {
		t.Fatalf("error = %q, want it to name the configured 700ms deadline", err)
	}
	if elapsed := time.Since(start); elapsed > 20*time.Second {
		t.Fatalf("Open blocked %s, want it bounded by the 700ms override", elapsed.Round(time.Millisecond))
	}
}

// TestSpawnTimeoutErrorCarriesServeOutput is the 503-summary fix at the
// manager level: the error message must carry a one-line serve output
// summary, not just the timeout sentence.
func TestSpawnTimeoutErrorCarriesServeOutput(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	m, err := NewManager(Config{
		ReasonixBin:  self,
		ProjectRoots: []string{root},
		SpawnTimeout: 700 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	err = m.Open(WorkspaceSlug(root))
	if err == nil {
		t.Fatal("Open = nil, want timeout error")
	}
	if !strings.Contains(err.Error(), "SERVEPOOL1049 fake serve output") {
		t.Fatalf("error = %q, want it to carry the captured serve output summary", err)
	}
}

// TestGateway503BodyCarriesServeOutput is the task's acceptance probe: a
// lazy-spawn timeout through the gateway answers 503 with a JSON body that
// contains the serve output summary.
func TestGateway503BodyCarriesServeOutput(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	m, err := NewManager(Config{
		ReasonixBin:  self,
		ProjectRoots: []string{root},
		SpawnTimeout: 700 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	g := NewGateway(m, "secret")
	ts := httptest.NewServer(g)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/p/" + WorkspaceSlug(root) + "/sessions?token=secret")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	body := string(bodyBytes)
	if !strings.Contains(body, "did not become ready within 700ms") {
		t.Fatalf("503 body = %q, want the timeout sentence", body)
	}
	if !strings.Contains(body, "SERVEPOOL1049 fake serve output") {
		t.Fatalf("503 body = %q, want the serve output summary", body)
	}
}

func TestServeOutputSummary(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty stays empty", "", ""},
		{"whitespace only stays empty", " \n\r\t ", ""},
		{"newlines collapse to one line", "line one\nline two\r\nline three", "line one line two line three"},
		{"control characters become spaces", "bind: \x1b[31mrefused\x1b[0m", "bind: [31mrefused [0m"},
	}
	for _, tc := range cases {
		if got := serveOutputSummary(tc.in, 400); got != tc.want {
			t.Fatalf("%s: serveOutputSummary(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
	long := strings.Repeat("a", 500)
	got := serveOutputSummary(long+"\n"+strings.Repeat("b", 100), 100)
	if len([]rune(got)) != 101 || !strings.HasPrefix(got, "…") || !strings.HasSuffix(got, strings.Repeat("b", 100)) {
		t.Fatalf("tail truncation broken: len=%d prefix=%q", len([]rune(got)), got[:1])
	}
}
