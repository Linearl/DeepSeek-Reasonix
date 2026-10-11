package session

// Task 773: workspace-state-v1.json (Desktop v5 registry) parsing. The v3
// shape (upstream 1.38.11+) is the authoritative sample; older registries are
// tolerated by ignoring fields they did not carry. An unreadable registry
// yields no owners — sessions then fall back to the caller-chosen default
// target, mirroring the upstream studio importer's tolerance
// (internal/state/migration/desktop_registry.go).

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// V5WorkspaceState is the registry subset the importer needs. Sorting,
// pinning and provenance surfaces are read only for the loss report; the
// legacy store has no equivalents (766 ⑦ reverse-degradation table).
type V5WorkspaceState struct {
	Version    int `json:"version"`
	Generation int `json:"generation"`
	Workspaces map[string]struct {
		ID         string   `json:"id"`
		Root       string   `json:"root"`
		Title      string   `json:"title"`
		SessionIDs []string `json:"sessionIds"`
	} `json:"workspaces"`
	ArchivedSessionIDs []string `json:"archivedSessionIds"`
	PendingCreates     map[string]struct {
		WorkspaceID string `json:"workspaceId"`
		SessionID   string `json:"sessionId"`
	} `json:"pendingCreates"`
	SessionStates map[string]struct {
		Lifecycle string `json:"lifecycle"`
	} `json:"sessionStates"`
	Presentation json.RawMessage `json:"presentation"`
}

// ReadV5WorkspaceState parses desktop/workspace-state-v1.json from the
// directory that CONTAINS desktop-sessions-v5 (the tree root, one level above
// by-id). Missing file means the store predates the registry or runs from a
// bare by-id copy: the importer still works, every session lands in the
// default target with an empty workspace root recorded.
func ReadV5WorkspaceState(treeRoot string) (*V5WorkspaceState, error) {
	raw, err := os.ReadFile(filepath.Join(treeRoot, "desktop", "workspace-state-v1.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return &V5WorkspaceState{}, nil
		}
		return nil, err
	}
	if len(raw) > v5ImportMaxManifestBytes {
		return nil, fmt.Errorf("workspace-state-v1.json exceeds %d bytes", v5ImportMaxManifestBytes)
	}
	var state V5WorkspaceState
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, fmt.Errorf("decode workspace-state-v1.json: %w", err)
	}
	return &state, nil
}

// Owner returns the workspace root a session id was filed under and whether
// the registry knows the session at all. Archived and reserved (pending
// create) sessions report their status so the plan can classify them; a
// lifecycle entry wins over plain membership because it is the registry's
// own state record.
func (s *V5WorkspaceState) Owner(sessionID string) (root string, known bool, status string) {
	if s == nil {
		return "", false, ""
	}
	for _, ws := range s.Workspaces {
		for _, id := range ws.SessionIDs {
			if id == sessionID {
				if strings.TrimSpace(ws.Root) != "" {
					root = ws.Root
				}
				known = true
			}
		}
	}
	for _, id := range s.ArchivedSessionIDs {
		if id == sessionID {
			return root, known, "archived"
		}
	}
	if pending, ok := s.PendingCreates[sessionID]; ok && pending.SessionID == sessionID {
		return root, true, "reserved"
	}
	if state, ok := s.SessionStates[sessionID]; ok {
		switch state.Lifecycle {
		case "archived":
			return root, true, "archived"
		case "deleted", "purged":
			return root, true, state.Lifecycle
		}
	}
	return root, known, ""
}

// RegistryPath returns the tree-root-relative location of the registry for
// reports (always forward slashes for stable display across platforms).
func RegistryPath() string { return "desktop/workspace-state-v1.json" }
