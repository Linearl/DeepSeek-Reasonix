package builtin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"reasonix/internal/netclient"
)

func TestBlockedFetchIP(t *testing.T) {
	blocked := []string{
		"169.254.169.254",      // cloud metadata (link-local)
		"10.1.2.3",             // RFC1918
		"172.16.5.6",           // RFC1918
		"192.168.1.1",          // RFC1918
		"0.0.0.0",              // unspecified
		"fe80::1",              // IPv6 link-local
		"fc00::1",              // IPv6 unique-local
		"::ffff:10.0.0.1",      // IPv4-mapped private
		"100.100.100.200",      // Alibaba Cloud metadata (CGNAT)
		"100.64.0.1",           // RFC 6598 shared space
		"::ffff:100.100.100.1", // IPv4-mapped CGNAT
	}
	for _, s := range blocked {
		if !blockedFetchIP(net.ParseIP(s)) {
			t.Errorf("%s should be blocked", s)
		}
	}
	allowed := []string{"8.8.8.8", "1.1.1.1", "127.0.0.1", "::1", "93.184.216.34"}
	for _, s := range allowed {
		if blockedFetchIP(net.ParseIP(s)) {
			t.Errorf("%s should be allowed", s)
		}
	}
}

// TestWebFetchAllowsLoopback proves the guard doesn't break normal fetches: a
// loopback dev server (httptest binds 127.0.0.1) stays reachable.
func TestWebFetchAllowsLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hello from localhost"))
	}))
	defer srv.Close()

	args, _ := json.Marshal(map[string]any{"url": srv.URL})
	out, err := webFetch{}.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("loopback fetch should succeed, got %v", err)
	}
	if !strings.Contains(out, "hello from localhost") {
		t.Fatalf("body missing: %q", out)
	}
}

// TestWebFetchRefusesLinkLocal proves a fetch aimed at the cloud-metadata
// endpoint is refused at dial time (no packet leaves the host).
func TestWebFetchRefusesLinkLocal(t *testing.T) {
	args, _ := json.Marshal(map[string]any{"url": "http://169.254.169.254/latest/meta-data/"})
	_, err := webFetch{}.Execute(context.Background(), args)
	if err == nil {
		t.Fatal("fetch to 169.254.169.254 should be refused")
	}
	if !strings.Contains(err.Error(), "169.254.169.254") {
		t.Fatalf("error should name the refused address, got %v", err)
	}
}

// --- proxy-branch hostname SSRF pre-check (audit-medium#9, plan A) ---
//
// Plan A: local resolution is best-effort. Success → validate resolved IPs;
// failure → allow through the proxy + slog.Warn (searchable, not silent).
// Local resolution must NEVER be mandatory: GFW users need the proxy to
// resolve DNS remotely, and a hard local lookup would fail every fetch.

func swapLookupForTest(t *testing.T, lookup func(ctx context.Context, host string) ([]net.IPAddr, error)) {
	t.Helper()
	prevLookup := webfetchLookupIPAddr
	prevTimeout := webfetchResolveTimeout
	webfetchLookupIPAddr = lookup
	t.Cleanup(func() {
		webfetchLookupIPAddr = prevLookup
		webfetchResolveTimeout = prevTimeout
	})
}

func captureSlogForTest(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// State 1a (解析成功→校验): a hostname that locally resolves into an internal
// IP is refused even when a proxy is configured.
func TestSSRFProxyHostnameResolvingInternalIsBlocked(t *testing.T) {
	swapLookupForTest(t, func(ctx context.Context, host string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("10.0.0.5")}}, nil
	})
	proxyURL, hits := startInterceptingCONNECTProxy(t, "should not get here")
	wf := webFetch{proxySpec: netclient.ProxySpec{Mode: netclient.ModeCustom, URL: proxyURL}}
	args, _ := json.Marshal(map[string]string{"url": "http://metadata.internal.test/latest/meta-data"})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := wf.Execute(ctx, args)
	if err == nil || !strings.Contains(err.Error(), "refusing to fetch internal address") {
		t.Fatalf("hostname resolving to private IP must be blocked through proxy, got %v", err)
	}
	if got := atomic.LoadInt32(hits); got != 0 {
		t.Fatalf("blocked target must not reach the proxy, hits = %d", got)
	}
}

// State 1b (解析成功→校验): a hostname resolving to public IPs passes the
// pre-check and the fetch completes through the proxy.
func TestSSRFProxyHostnameResolvingPublicIsAllowed(t *testing.T) {
	swapLookupForTest(t, func(ctx context.Context, host string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}, {IP: net.ParseIP("2606:2800:220:1:248:1893:25c8:1946")}}, nil
	})
	proxyURL, hits := startInterceptingCONNECTProxy(t, "public hostname via proxy")
	wf := webFetch{proxySpec: netclient.ProxySpec{Mode: netclient.ModeCustom, URL: proxyURL}}
	args, _ := json.Marshal(map[string]string{"url": "http://public.example.test/page"})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := wf.Execute(ctx, args)
	if err != nil {
		t.Fatalf("public-resolving hostname must pass the pre-check: %v", err)
	}
	if !strings.Contains(out, "public hostname via proxy") {
		t.Fatalf("expected proxied body, got %q", out)
	}
	if got := atomic.LoadInt32(hits); got != 1 {
		t.Fatalf("proxy hits = %d, want 1", got)
	}
}

// State 2 (解析失败→放行+Warn) AND state 3 (强制解析未引入): when local
// resolution fails the fetch still goes through the proxy — resolution is
// best-effort, never mandatory — and the unchecked path leaves a searchable
// slog.Warn naming the host.
func TestSSRFProxyResolveFailureAllowsFetchAndWarns(t *testing.T) {
	swapLookupForTest(t, func(ctx context.Context, host string) ([]net.IPAddr, error) {
		return nil, fmt.Errorf("simulated local DNS outage")
	})
	logBuf := captureSlogForTest(t)
	proxyURL, hits := startInterceptingCONNECTProxy(t, "fetched despite dns outage")
	wf := webFetch{proxySpec: netclient.ProxySpec{Mode: netclient.ModeCustom, URL: proxyURL}}
	args, _ := json.Marshal(map[string]string{"url": "http://gfw-blocked.example.test/doc"})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := wf.Execute(ctx, args)
	if err != nil {
		t.Fatalf("resolution failure must not fail the fetch (no forced local resolution): %v", err)
	}
	if !strings.Contains(out, "fetched despite dns outage") {
		t.Fatalf("expected proxied body, got %q", out)
	}
	if got := atomic.LoadInt32(hits); got != 1 {
		t.Fatalf("fetch must actually go through the proxy, hits = %d", got)
	}
	logs := logBuf.String()
	if !strings.Contains(logs, "ssrf") || !strings.Contains(logs, "gfw-blocked.example.test") {
		t.Fatalf("expected searchable ssrf warn naming the host, log = %q", logs)
	}
}

// State 3 complement (尽力而为有界): a slow resolver is cut off at
// webfetchResolveTimeout and the fetch still proceeds — resolution can delay
// the request only briefly, never fail it.
func TestSSRFProxyResolveTimeoutAllowsFetch(t *testing.T) {
	swapLookupForTest(t, func(ctx context.Context, host string) ([]net.IPAddr, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(5 * time.Second):
			return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
		}
	})
	webfetchResolveTimeout = 50 * time.Millisecond
	logBuf := captureSlogForTest(t)
	proxyURL, hits := startInterceptingCONNECTProxy(t, "fetched despite slow dns")
	wf := webFetch{proxySpec: netclient.ProxySpec{Mode: netclient.ModeCustom, URL: proxyURL}}
	args, _ := json.Marshal(map[string]string{"url": "http://slow-dns.example.test/doc"})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := wf.Execute(ctx, args)
	if err != nil {
		t.Fatalf("slow resolution must be bounded and not fail the fetch: %v", err)
	}
	if !strings.Contains(out, "fetched despite slow dns") {
		t.Fatalf("expected proxied body, got %q", out)
	}
	if got := atomic.LoadInt32(hits); got != 1 {
		t.Fatalf("proxy hits = %d, want 1", got)
	}
	if !strings.Contains(logBuf.String(), "ssrf") {
		t.Fatalf("expected ssrf warn for timed-out resolution, log = %q", logBuf.String())
	}
}

// Same mechanism, SOCKS5 branch: a hostname locally resolving into an
// internal IP is blocked there too (both proxy branches share the check).
func TestSSRFSOCKS5HostnameResolvingInternalIsBlocked(t *testing.T) {
	swapLookupForTest(t, func(ctx context.Context, host string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("192.168.5.5")}}, nil
	})
	wf := webFetch{proxySpec: netclient.ProxySpec{Mode: netclient.ModeCustom, URL: "socks5://127.0.0.1:1080"}}
	args, _ := json.Marshal(map[string]string{"url": "http://metadata.internal.test/latest/meta-data"})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := wf.Execute(ctx, args)
	if err == nil || !strings.Contains(err.Error(), "refusing to fetch internal address") {
		t.Fatalf("SOCKS5 hostname resolving to private IP must be blocked, got %v", err)
	}
}

// Same mechanism, SOCKS5 branch, state 2/3: resolution failure allows the
// target through (the proxy dial then fails for other reasons) with the
// searchable warn — no forced local resolution here either.
func TestSSRFSOCKS5ResolveFailureAllowsTargetAndWarns(t *testing.T) {
	swapLookupForTest(t, func(ctx context.Context, host string) ([]net.IPAddr, error) {
		return nil, fmt.Errorf("simulated local DNS outage")
	})
	logBuf := captureSlogForTest(t)
	wf := webFetch{proxySpec: netclient.ProxySpec{Mode: netclient.ModeCustom, URL: "socks5://127.0.0.1:1"}}
	args, _ := json.Marshal(map[string]string{"url": "https://gfw-blocked.example.test/"})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := wf.Execute(ctx, args)
	if err == nil {
		t.Log("unexpected success (no SOCKS5 server running)")
	} else if strings.Contains(err.Error(), "refusing to fetch internal address") {
		t.Fatalf("resolution failure must not be treated as SSRF block: %v", err)
	}
	if !strings.Contains(logBuf.String(), "ssrf") {
		t.Fatalf("expected searchable ssrf warn, log = %q", logBuf.String())
	}
}

// fakeResolver pins resolver edge cases for the vetting gate without touching
// the network.
type fakeResolver struct {
	ips []net.IPAddr
	err error
}

func (f fakeResolver) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	return f.ips, f.err
}

// tl1323 回归钉：LookupIPAddr 返回空列表且 err==nil 时（解析器对某些记录的
// 合法应答），修前直接取 ips[0] 在 http.Client 拨号 goroutine 内越界 panic
// （Execute 无 recover，会击穿 turn 执行器）。现在必须返回普通"无地址"错误。
func TestResolveVettedIPsEmptyAnswerIsErrorNotPanic(t *testing.T) {
	_, err := resolveVettedIPs(context.Background(), fakeResolver{}, "empty-answer.example.invalid")
	if err == nil {
		t.Fatal("empty resolver answer must be an error, not a panic")
	}
	var dnsErr *net.DNSError
	if !errors.As(err, &dnsErr) || !dnsErr.IsNotFound {
		t.Fatalf("empty answer should surface as not-found DNS error, got %v", err)
	}
}

// 对照：同一道闸上的既有语义不回归——黑名单 IP 仍拒、公网 IP 仍放行。
func TestResolveVettedIPsStillVetsAndPasses(t *testing.T) {
	blocked, err := resolveVettedIPs(context.Background(), fakeResolver{ips: []net.IPAddr{{IP: net.ParseIP("169.254.169.254")}}}, "host.example.invalid")
	if err == nil || !strings.Contains(err.Error(), "refusing to fetch internal address") {
		t.Fatalf("blocked IP must still be refused, got %v (%v)", blocked, err)
	}

	public := []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}
	got, err := resolveVettedIPs(context.Background(), fakeResolver{ips: public}, "host.example.invalid")
	if err != nil || len(got) != 1 || !got[0].IP.Equal(public[0].IP) {
		t.Fatalf("public IP must pass the gate untouched, got %v err=%v", got, err)
	}
}
