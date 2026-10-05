package sysproxy

import "testing"

func TestParseProxyList(t *testing.T) {
	for _, tc := range []struct {
		name, list, scheme, want string
	}{
		{"single all-protocol", "proxy.example.com:8080", "https", "http://proxy.example.com:8080"},
		{"per-protocol https", "http=p1:80;https=p2:443", "https", "http://p2:443"},
		{"per-protocol http", "http=p1:80;https=p2:443", "http", "http://p1:80"},
		{"scheme miss falls back to bare", "p0:3128;ftp=p1:21", "https", "http://p0:3128"},
		{"strips scheme prefix", "http://p:8080", "https", "http://p:8080"},
		{"empty", "", "https", ""},
		{"only other protocol, no bare", "ftp=p:21", "https", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := parseProxyList(tc.list, tc.scheme)
			if tc.want == "" {
				if got != nil {
					t.Fatalf("got %v, want nil", got)
				}
				return
			}
			if got == nil || got.String() != tc.want {
				t.Fatalf("got %v, want %s", got, tc.want)
			}
		})
	}
}

func TestBypassed(t *testing.T) {
	for _, tc := range []struct {
		host, bypass string
		want         bool
	}{
		{"api.deepseek.com", "<local>", false},
		{"intranet", "<local>", true},
		{"host.corp.local", "*.corp.local", true},
		{"api.deepseek.com", "*.corp.local;<local>", false},
		{"api.deepseek.com", "api.deepseek.com", true},
		{"API.DeepSeek.com", "api.deepseek.com", true},
		{"api.deepseek.com", "", false},
		// Task 491: WinINET bypass lists use trailing-* prefix wildcards; the
		// machine that reproduced the hang carries "127.*" and expects it to
		// exempt loopback IPs. Note "<local>" alone must NOT match "127.0.0.1"
		// (it has a dot) — that's why loopbackHost is a hard exemption.
		{"127.0.0.1", "127.*", true},
		{"127.0.0.1", "<local>;localhost;127.*;10.*", true},
		{"127.0.0.1", "<local>", false},
		{"www.127.example.com", "127.*", false},
		{"10.20.30.40", "10.*", true},
	} {
		if got := bypassed(tc.host, tc.bypass); got != tc.want {
			t.Errorf("bypassed(%q, %q) = %v, want %v", tc.host, tc.bypass, got, tc.want)
		}
	}
}

func TestLoopbackHost(t *testing.T) {
	for _, tc := range []struct {
		host string
		want bool
	}{
		{"localhost", true},
		{"LOCALHOST", true},
		{"127.0.0.1", true},
		{"127.8.8.8", true},
		{"::1", true},
		{"api.deepseek.com", false},
		{"10.0.0.1", false},
		{"::2", false},
		{"", false},
	} {
		if got := loopbackHost(tc.host); got != tc.want {
			t.Errorf("loopbackHost(%q) = %v, want %v", tc.host, got, tc.want)
		}
	}
}
