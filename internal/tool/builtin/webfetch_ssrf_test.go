package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
