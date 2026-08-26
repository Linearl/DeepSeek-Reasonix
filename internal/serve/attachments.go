package serve

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"reasonix/internal/control"
)

const maxUploadBytes = 64 << 20 // 64 MiB, matching maxImageAttachmentBytes

// POST /attachments — upload an image and get back a reference path.
//
// Request body (JSON):
//
//	{"data": "<base64-encoded image>", "mime": "image/png"}
//
// Returns:
//
//	{"ref": ".reasonix/attachments/clipboard-<ts>-<seq>.png"}
//
// The caller then sends `POST /submit {"input": "@<ref> ..."}` to include
// the image in a message.  The agent's existing @ref parsing and multimodal
// pipeline handles the rest — no protocol changes needed.
func (s *Server) uploadAttachment(w http.ResponseWriter, r *http.Request) {
	if s.ctl() == nil {
		http.Error(w, "no active session", http.StatusBadRequest)
		return
	}
	root := s.ctl().WorkspaceRoot()
	if root == "" {
		http.Error(w, "workspace root unknown", http.StatusBadRequest)
		return
	}

	var req struct {
		Data string `json:"data"` // base64-encoded image bytes
		Mime string `json:"mime"` // optional, e.g. "image/png"
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("bad request: %v", err), http.StatusBadRequest)
		return
	}
	if req.Data == "" {
		http.Error(w, "data field required (base64-encoded image)", http.StatusBadRequest)
		return
	}

	raw, err := base64.StdEncoding.DecodeString(req.Data)
	if err != nil {
		// Try URL-safe base64 as fallback.
		raw, err = base64.URLEncoding.DecodeString(req.Data)
		if err != nil {
			http.Error(w, "invalid base64 data", http.StatusBadRequest)
			return
		}
	}
	if len(raw) > maxUploadBytes {
		http.Error(w, "file too large (max 64 MiB)", http.StatusRequestEntityTooLarge)
		return
	}

	// Normalize MIME: strip parameters like "; charset=..."
	mime := strings.TrimSpace(req.Mime)
	if i := strings.IndexByte(mime, ';'); i >= 0 {
		mime = strings.TrimSpace(mime[:i])
	}

	path, err := control.SaveImageBytesInRoot(root, mime, raw)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	writeJSON(w, map[string]string{"ref": path})
}
