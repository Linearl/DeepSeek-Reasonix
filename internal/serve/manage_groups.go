package serve

import (
	"encoding/json"
	"net/http"

	"reasonix/internal/organization"
)

// 任务911: the /manage-groups surface (experimental_gc_manage_sessions) — the
// GrandCouncil remote client manages the SAME group rosters the desktop app
// does. Every operation goes through internal/organization, which owns the
// desktop-projects.json + sidecar store: one roster, both writers. The
// desktop frontend converges by re-reading on its next refresh (the Wails
// write event is desktop-local by design); the operation log rides along so
// the desktop's rollback tooling stays authoritative.
//
// The routes are only mounted while the lab switch is on (fail-closed
// otherwise), and GET /capabilities advertises "manage-groups" alongside.

type manageGroupsRequest struct {
	Kind          string `json:"kind"`          // "session" | "project" (group ops)
	Scope         string `json:"scope"`         // "global" | "project" (session ops)
	WorkspaceRoot string `json:"workspaceRoot"` // required for project scope
	GroupID       string `json:"groupId"`
	Title         string `json:"title"`    // create: new title; rename: new title
	ParentID      string `json:"parentId"` // create only
	Cascade       bool   `json:"cascade"`  // delete only
	Confirm       bool   `json:"confirm"`  // delete only: plan → execute
	TopicID       string `json:"topicId"`  // reorder only
	Index         *int   `json:"index"`    // reorder only (pointer: 0 is meaningful)
}

func (s *Server) manageGroupsList(w http.ResponseWriter, r *http.Request) {
	result, err := organization.List()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, result)
}

// manageGroupsMutate wraps one POST operation with the shared body handling.
func (s *Server) manageGroupsMutate(op func(manageGroupsRequest) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body manageGroupsRequest
		if r.Body != nil && r.Body != http.NoBody {
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, foregroundMutationMaxBody)).Decode(&body); err != nil {
				http.Error(w, "invalid body", http.StatusBadRequest)
				return
			}
		}
		result, err := op(body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		writeJSON(w, result)
	}
}

func (s *Server) manageGroupsCreate(body manageGroupsRequest) (any, error) {
	if body.Kind == organization.KindProject {
		return organization.CreateProjectGroup(body.GroupID, body.Title, body.ParentID)
	}
	return organization.CreateSessionGroup(body.Scope, body.WorkspaceRoot, body.GroupID, body.Title, body.ParentID)
}

func (s *Server) manageGroupsRename(body manageGroupsRequest) (any, error) {
	if body.Kind == organization.KindProject {
		return organization.RenameProjectGroup(body.GroupID, body.Title)
	}
	return organization.RenameSessionGroup(body.Scope, body.WorkspaceRoot, body.GroupID, body.Title)
}

func (s *Server) manageGroupsDelete(body manageGroupsRequest) (any, error) {
	if body.Kind == organization.KindProject {
		return organization.DeleteProjectGroup(body.GroupID, body.Cascade, body.Confirm)
	}
	return organization.DeleteSessionGroup(body.Scope, body.WorkspaceRoot, body.GroupID, body.Cascade, body.Confirm)
}

func (s *Server) manageGroupsReorder(body manageGroupsRequest) (any, error) {
	index := 0
	if body.Index != nil {
		index = *body.Index
	}
	return organization.ReorderTopicInGroup(body.Scope, body.WorkspaceRoot, body.TopicID, body.GroupID, index)
}

func (s *Server) manageGroupsFileTopic(body manageGroupsRequest) (any, error) {
	return organization.FileTopic(body.Scope, body.WorkspaceRoot, body.TopicID, body.GroupID, body.Title)
}
