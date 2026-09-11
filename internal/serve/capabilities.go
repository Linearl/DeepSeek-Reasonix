package serve

import "net/http"

// serveProtocolVersion is the version of the route contract this host speaks. It
// bumps only when an existing route changes incompatibly, so a client can refuse
// a host it cannot talk to before it opens a session instead of discovering the
// gap one failing call at a time.
const serveProtocolVersion = 1

// serveCapabilities is the single declaration of the feature routes a serve host
// offers, and it is the list /capabilities reports. Keeping one declaration is the
// point: the client asks what it can use, and the answer cannot drift from what the
// host actually registered. A newer client meeting an older host used to discover
// that gap mid-session, one 404 at a time.
//
// Keep it in sync with handler(); TestCapabilitiesMatchRoutes fails when a listed
// capability has no route, and when a feature route is unlisted.
var serveCapabilities = []string{
	"events",
	"runtime-states",
	"history",
	"context",
	"submit",
	"provider-setup",
	"inbox",
	"checkpoints",
}

// Capabilities returns a copy so callers cannot mutate the declaration.
func Capabilities() []string {
	out := make([]string, len(serveCapabilities))
	copy(out, serveCapabilities)
	return out
}

// capabilitiesResponse is the wire shape. Protocol and MinProtocol let a client
// decide compatibility before connecting: a host whose MinProtocol exceeds the
// client's protocol offers nothing the client can safely use.
type capabilitiesResponse struct {
	Protocol      int      `json:"protocol"`
	MinProtocol   int      `json:"minProtocol"`
	Capabilities  []string `json:"capabilities"`
}

func (s *Server) capabilities(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, capabilitiesResponse{
		Protocol:     serveProtocolVersion,
		MinProtocol:  serveProtocolVersion,
		Capabilities: Capabilities(),
	})
}
