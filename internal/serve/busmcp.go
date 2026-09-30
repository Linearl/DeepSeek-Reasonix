package serve

import (
	"net/http"
)

// Bus route wiring. The bus authenticates every request itself with per-role
// bearer tokens (busmcp.Handler / busmcp.HandleEvent), so these paths are
// exempted from the serve-wide browser auth gate — see authGate.middleware.
// The exemption is conditional on a successfully constructed bus: when the
// bus is disabled or its config is broken, nothing is mounted, nothing is
// exempted, and /mcp answers 404 like any unknown route.

// busPublicPaths lists the exact paths that skip the serve-wide auth gate
// when the bus is mounted. The MCP streamable endpoint is exactly "/mcp"
// (Go 1.22 ServeMux matches it without a trailing slash), so exact matching
// keeps the exemption as narrow as the mount.
func busPublicPaths() []string {
	return []string{"/mcp", "/bus/events"}
}

func isBusPublicPath(path string) bool {
	for _, p := range busPublicPaths() {
		if path == p {
			return true
		}
	}
	return false
}

// registerBusRoutes mounts the bus when constructed. Called from handler();
// a nil bus registers nothing.
func (s *Server) registerBusRoutes(mux *http.ServeMux) {
	if s.bus == nil {
		return
	}
	// Streamable HTTP lives on one endpoint for all methods: POST for
	// JSON-RPC, GET for the optional SSE stream, DELETE for session end.
	mux.Handle("/mcp", s.bus.Handler())
	mux.HandleFunc("POST /bus/events", s.bus.HandleEvent)
}
