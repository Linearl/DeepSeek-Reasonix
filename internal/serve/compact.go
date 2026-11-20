package serve

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"reasonix/internal/agent"
)

func (s *Server) compact(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Instructions string `json:"instructions"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	// tl1719: the serve surface names itself so the compaction receipt/events
	// attribute this pass to the serve entry (same manual-class core as the
	// desktop button and the slash command).
	// tl1723: persistence is folded into CompactForTrigger itself
	// (SnapshotRewrite semantics) — this endpoint no longer carries its own
	// post-compact Snapshot tail, so the serve save lands through the same
	// closure as the slash command and the desktop button.
	if err := s.ctl().CompactForTrigger(r.Context(), strings.TrimSpace(body.Instructions), agent.CompactionTriggerServe); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
