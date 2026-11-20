package serve

import (
	"encoding/json"
	"io"
	"log/slog"
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
	if err := s.ctl().CompactForTrigger(r.Context(), strings.TrimSpace(body.Instructions), agent.CompactionTriggerServe); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Persist the compacted session to disk — ctrl.Compact() only mutates in-memory.
	if err := s.ctl().Snapshot(); err != nil {
		slog.Warn("serve: snapshot after compact", "err", err)
	}
	w.WriteHeader(http.StatusNoContent)
}
