package forkupdate

import (
	"net/http"
	"net/url"
	"testing"

	"reasonix/internal/config"
	"reasonix/internal/netclient"
)

// stubChannelConfig pins the [network] proxy settings the channel client
// resolves, without touching the real user config file.
func stubChannelConfig(t *testing.T, cfg *config.Config) {
	t.Helper()
	orig := loadChannelConfig
	loadChannelConfig = func() (*config.Config, error) {
		if cfg == nil {
			return nil, errStubConfig
		}
		return cfg, nil
	}
	t.Cleanup(func() { loadChannelConfig = orig })
}

// errStubConfig stands in for an unreadable config in tests.
type stubConfigError struct{}

func (stubConfigError) Error() string { return "stub config unavailable" }

var errStubConfig = stubConfigError{}

// stubOSProxy swaps netclient's OS-proxy seam for the duration of one test.
// Production wires sysproxy.ForURL (Windows IE/PAC/WPAD); the fake keeps the
// three states below hermetic — no registry read, no PAC lookup.
func stubOSProxy(t *testing.T, fn func(*url.URL) (*url.URL, error)) {
	t.Helper()
	orig := netclient.OSProxy
	netclient.OSProxy = fn
	t.Cleanup(func() { netclient.OSProxy = orig })
}

// clearProxyEnv removes every env-proxy spelling so a developer machine's
// HTTP_PROXY/HTTPS_PROXY cannot leak into the resolution under test.
func clearProxyEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "NO_PROXY", "no_proxy"} {
		t.Setenv(k, "")
	}
}

// TestChannelProxySpecThreeStates pins the tl1690 contract: auto resolves the
// environment proxy first and the OS system proxy when the env vars are empty
// (the Windows desktop case that used to time out against api.github.com);
// env/off stay byte-for-byte their pre-tl1690 semantics and never consult the
// OS leg; an unreadable config fails open to auto.
func TestChannelProxySpecThreeStates(t *testing.T) {
	clearProxyEnv(t)

	osProxyCalls := 0
	stubOSProxy(t, func(*url.URL) (*url.URL, error) {
		osProxyCalls++
		return mustProxyURL("http://os-proxy.example:8080"), nil
	})

	autoCfg := config.Default()
	autoCfg.Network.ProxyMode = netclient.ModeAuto
	envCfg := config.Default()
	envCfg.Network.ProxyMode = netclient.ModeEnv
	offCfg := config.Default()
	offCfg.Network.ProxyMode = netclient.ModeOff

	tests := []struct {
		name        string
		cfg         *config.Config // nil = config.Load fails
		envSet      bool
		wantMode    string
		wantOSCalls int
	}{
		{name: "auto with env proxy uses env", cfg: autoCfg, envSet: true, wantMode: netclient.ModeAuto},
		{name: "auto without env consults the OS leg", cfg: autoCfg, wantMode: netclient.ModeAuto, wantOSCalls: 1},
		{name: "env mode never consults the OS leg", cfg: envCfg, wantMode: netclient.ModeEnv},
		{name: "off mode stays direct", cfg: offCfg, wantMode: netclient.ModeOff},
		{name: "unreadable config fails open to auto", cfg: nil, wantMode: netclient.ModeAuto, wantOSCalls: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Windows env vars are case-insensitive: clear both spellings, then
			// set the intended value last so the clear cannot wipe it.
			clearProxyEnv(t)
			if tt.envSet {
				t.Setenv("HTTP_PROXY", "http://env-proxy.example:8080")
			}
			stubChannelConfig(t, tt.cfg)
			osProxyCalls = 0

			spec := channelProxySpec()
			if spec.Mode != tt.wantMode {
				t.Fatalf("mode = %q, want %q", spec.Mode, tt.wantMode)
			}

			// Resolve through the same proxyFunc netclient applies to the
			// transport, so the assertion covers the wired behaviour, not just
			// the struct fields.
			pf, err := netclient.ProxyFunc(spec)
			if err != nil {
				t.Fatalf("ProxyFunc: %v", err)
			}
			if pf == nil {
				if tt.wantMode != netclient.ModeOff {
					t.Fatalf("nil proxy func for mode %q", tt.wantMode)
				}
				if osProxyCalls != 0 {
					t.Fatalf("off mode must not consult the OS leg, got %d calls", osProxyCalls)
				}
				return
			}
			target := "https://api.github.com/repos/Linearl/DeepSeek-Reasonix/releases"
			if tt.envSet {
				// httpproxy sends http:// through HTTP_PROXY only; use an http
				// target for the env leg so HTTPS_PROXY cannot mask it.
				target = "http://api.github.com/repos/Linearl/DeepSeek-Reasonix/releases"
			}
			got, err := pf(&http.Request{URL: mustProxyURL(target)})
			if err != nil {
				t.Fatalf("proxy lookup: %v", err)
			}
			gotStr := ""
			if got != nil {
				gotStr = got.String()
			}
			want := ""
			switch {
			case tt.envSet:
				want = "http://env-proxy.example:8080"
			case tt.wantOSCalls > 0:
				want = "http://os-proxy.example:8080"
			}
			if gotStr != want {
				t.Fatalf("proxy = %q, want %q", gotStr, want)
			}
			if osProxyCalls != tt.wantOSCalls {
				t.Fatalf("OS proxy calls = %d, want %d", osProxyCalls, tt.wantOSCalls)
			}
		})
	}
}

// TestNewChannelClientAppliesTransportTimeouts: the channel client must carry
// the TLS handshake bound (the tl1690 field failure was a TLS handshake
// timeout) and the resolved proxy on its transport.
func TestNewChannelClientAppliesTransportTimeouts(t *testing.T) {
	clearProxyEnv(t)
	stubChannelConfig(t, nil) // fail open to auto
	stubOSProxy(t, func(*url.URL) (*url.URL, error) { return nil, nil })

	c, err := NewChannelClient()
	if err != nil {
		t.Fatalf("NewChannelClient: %v", err)
	}
	tr, ok := c.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport = %T, want *http.Transport", c.Transport)
	}
	if tr.TLSHandshakeTimeout != channelClientOptions.TLSHandshakeTimeout {
		t.Fatalf("TLSHandshakeTimeout = %v, want %v", tr.TLSHandshakeTimeout, channelClientOptions.TLSHandshakeTimeout)
	}
	if tr.Proxy == nil {
		t.Fatal("channel transport must carry the resolved proxy func")
	}
	// Off mode must produce a nil-proxy (direct) transport.
	offCfg := config.Default()
	offCfg.Network.ProxyMode = netclient.ModeOff
	stubChannelConfig(t, offCfg)
	c2, err := NewChannelClient()
	if err != nil {
		t.Fatalf("NewChannelClient(off): %v", err)
	}
	tr2, ok := c2.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport = %T, want *http.Transport", c2.Transport)
	}
	if tr2.Proxy != nil {
		u, err := tr2.Proxy(&http.Request{URL: mustProxyURL("https://api.github.com/x")})
		if err != nil || u != nil {
			t.Fatalf("off mode proxy = %v, err = %v; want nil/direct", u, err)
		}
	}
}

func mustProxyURL(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		panic(err)
	}
	return u
}
