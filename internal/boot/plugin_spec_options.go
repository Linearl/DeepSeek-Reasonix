package boot

import (
	"net/http"
	"time"

	"reasonix/internal/mcplaunch"
	"reasonix/internal/netclient"
)

// PluginSpecOptions carries host runtime policy into plugin specifications.
type PluginSpecOptions struct {
	DefaultStartupTimeout time.Duration
	DefaultCallTimeout    time.Duration
	LaunchManager         *mcplaunch.Manager
	ConfigSource          string
	StateHome             string
	WriterRoots           []string
	ForbidReadRoots       []string
	Network               bool
	PackageOwners         map[string]string
	OAuthHTTPClient       *http.Client
	// NetworkProxy is the session's resolved user-facing proxy ([network]
	// config + env + OS system proxy). It rides on every MCP spec so http/sse
	// servers route through the same proxy as web_fetch and model providers;
	// the zero value keeps netclient's auto mode (env, then OS proxy).
	NetworkProxy netclient.ProxySpec
}
