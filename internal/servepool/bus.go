package servepool

import (
	"net/http"

	"reasonix/internal/busmcp"
	"reasonix/internal/config"
)

// BusHandlersFromConfig resolves the [serve.bus_mcp] table into the two
// HTTP handlers a gateway host mounts via Gateway.SetBus (tl 1182). Both
// gateway hosts (the desktop embed and `serve --pool`) call this so the
// bus construction rules stay in one place, exactly like serve builds its
// own bus from the same table in serve.New.
//
// Return contract (fail-closed, mirroring serve):
//   - bus disabled in config, or the config cannot be loaded, or the table
//     is missing: nil handlers, nil error — the caller mounts nothing and
//     the gateway keeps answering /mcp through the normal bearer check
//     (401 without a gateway token, 404 with one). A config-load failure
//     must not disable the gateway (same posture as servePoolTimeouts).
//   - table present but invalid: nil handlers and the error, so the host
//     can log WHY the bus is absent (serve logs the equivalent case).
//
// Injector stays nil here on purpose: the realtime mail→inject nudge lives
// in the serve processes (they own the zcodebridge); the durable mailbox
// delivery — the guaranteed channel — is unaffected, and gateway-mounted
// mail still lands in the same shared MailDir the serves poll.
func BusHandlersFromConfig() (mcpHandler, eventsHandler http.Handler, err error) {
	cfg, cfgErr := config.Load()
	if cfgErr != nil || cfg == nil || !cfg.Serve.BusMCP.Enabled {
		return nil, nil, nil
	}
	bus, err := busmcp.New(busmcp.Config{
		Enabled:         cfg.Serve.BusMCP.Enabled,
		Roles:           cfg.Serve.BusMCP.Roles,
		MailDir:         cfg.Serve.BusMCP.MailDir,
		HopLimit:        cfg.Serve.BusMCP.HopLimit,
		EventTarget:     cfg.Serve.BusMCP.EventTarget,
		SpawnRoles:      cfg.Serve.BusMCP.SpawnRoles,
		SpawnDailyQuota: cfg.Serve.BusMCP.SpawnDailyQuota,
	})
	if err != nil {
		return nil, nil, err
	}
	return bus.Handler(), http.HandlerFunc(bus.HandleEvent), nil
}
