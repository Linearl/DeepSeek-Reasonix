package forkupdate

import (
	"net/http"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/netclient"
)

// channelClientOptions tunes the transport for fork channel lookups. The
// desktop's upstream-manifest client (updater.go) defaults to a 10s TLS
// handshake timeout because Cloudflare (R2) occasionally stalls the first
// handshake on CN IPv6 routes; api.github.com is on the same class of
// anycast front end, and the tl1690 field log was literally a TLS handshake
// timeout, so the fork channel client inherits the same bound. Zero values
// keep net/http defaults.
var channelClientOptions = netclient.TransportOptions{
	// fetchTimeout (forkupdate.go) is 15s end-to-end; a handshake must not
	// eat more than 10s of it.
	TLSHandshakeTimeout: 10 * time.Second,
}

// channelProxySpec resolves the user-facing [network] proxy settings for fork
// channel lookups: the same four proxy_mode semantics as every other Reasonix
// HTTP face (config.NetworkProxySpec), minus the provider no_proxy
// DirectHosts table (the channel is GitHub, which has no per-provider
// direct-host list).
//
// tl1690: a Windows desktop app has no process env vars, so without the OS
// leg (netclient auto = env, then sysproxy) the channel check dials
// api.github.com direct and dies on TLS handshake timeout —
// "update: fork release channel unreachable; check skipped". Detection
// failure stays fail-open to direct (sysproxy returns nil on any error).
func channelProxySpec() netclient.ProxySpec {
	// config.Load is authoritative for the user's proxy_mode; a load failure
	// (unreadable config, first run) keeps the auto default — a broken config
	// must not turn the channel check into a hard error (tl1680's silent
	// degradation contract covers transport, and the same spirit applies).
	spec := netclient.ProxySpec{Mode: netclient.ModeAuto}
	if cfg, err := loadChannelConfig(); err == nil && cfg != nil {
		spec = cfg.NetworkProxySpec()
		spec.DirectHosts = nil
	}
	return spec
}

// loadChannelConfig is the config seam so tests can pin proxy_mode without
// touching the real user config file.
var loadChannelConfig = config.Load

// newChannelClient builds the HTTP client for fork channel lookups with the
// resolved proxy settings applied. A var so tests can substitute a client
// without a network (mirrors the desktop forkCheckClient seam).
var newChannelClient = func() (*http.Client, error) {
	return netclient.NewHTTPClient(channelProxySpec(), channelClientOptions)
}

// NewChannelClient is the exported face for callers outside this package
// (the desktop updater falls back to it when its own config load fails):
// a fork channel HTTP client honoring [network] proxy_mode, with the OS
// system-proxy leg active under auto.
func NewChannelClient() (*http.Client, error) {
	return newChannelClient()
}
