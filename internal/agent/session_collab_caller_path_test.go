package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/sessioncollab"
	"reasonix/internal/tool"
)

// Task 158.B: a self-directed call must resolve the caller's transcript path at
// CALL TIME. Boot builds the collaboration tools before the control layer binds
// the path (desktop/session_prompt.go), so the boot-time snapshot is empty for
// a desktop session — which is why `set_session_purpose` without `target` used
// to fail with "no session path" and why the only workaround was an explicit
// target.
func TestSetSessionPurposeResolvesCallerPathAtCallTime(t *testing.T) {
	dir := t.TempDir()
	self := filepath.Join(dir, "self.jsonl")
	writeEmpty(t, self)

	// boot-time order: nothing bound yet.
	live := ""
	cfg := SessionCollabConfig{
		Enabled:            true,
		SessionDir:         dir,
		WorkspaceRoot:      dir,
		ResolveSessionPath: func() string { return live },
	}
	purposeTool := NewSetSessionPurposeTool(cfg)
	if _, err := purposeTool.Execute(context.Background(), json.RawMessage(`{"purpose":"too early"}`)); err == nil {
		t.Fatal("an unbound caller path must still be reported, not silently written somewhere")
	}

	// The control layer binds the path after boot; the same tool instance must
	// now resolve it without the caller passing a target.
	live = self
	out, err := purposeTool.Execute(context.Background(), json.RawMessage(`{"purpose":"governance"}`))
	if err != nil {
		t.Fatalf("self-directed set_session_purpose failed after the path was bound: %v", err)
	}
	if !strings.Contains(out, `"appliedTo":"self"`) {
		t.Fatalf("want appliedTo=self, got %s", out)
	}
	meta, found, err := LoadBranchMeta(self)
	if err != nil || !found {
		t.Fatalf("branch meta: found=%v err=%v", found, err)
	}
	if meta.Purpose != "governance" {
		t.Fatalf("purpose not written to the live path: %+v", meta)
	}
	if meta.ContactID == "" {
		t.Fatal("set_session_purpose must leave the session addressable")
	}
}

// The same call through the use_capability proxy: the proxy resolves the
// registry instance, so the fix must hold on the proxy path too — the reported
// symptom was exactly this route.
func TestSetSessionPurposeThroughUseCapabilityProxy(t *testing.T) {
	dir := t.TempDir()
	self := filepath.Join(dir, "self.jsonl")
	writeEmpty(t, self)

	reg := tool.NewRegistry()
	reg.Add(NewSetSessionPurposeTool(SessionCollabConfig{
		Enabled:            true,
		SessionDir:         dir,
		WorkspaceRoot:      dir,
		ResolveSessionPath: func() string { return self },
	}))
	proxy := NewUseCapabilityTool(context.Background(), nil, nil, reg, nil, nil, nil)
	out, err := proxy.Execute(context.Background(), json.RawMessage(
		`{"action":"call","capability_id":"tool:set_session_purpose","arguments":{"purpose":"proxy claim"}}`))
	if err != nil {
		t.Fatalf("proxied set_session_purpose failed: %v", err)
	}
	if strings.Contains(out, "no session path") {
		t.Fatalf("proxy route lost the caller path: %s", out)
	}
	meta, found, err := LoadBranchMeta(self)
	if err != nil || !found || meta.Purpose != "proxy claim" {
		t.Fatalf("purpose not applied through the proxy: %+v found=%v err=%v", meta, found, err)
	}
}

// A sender that only ever sends must still be addressable: the reply address is
// resolved from the live path, not from a boot snapshot (task 156.A meeting
// task 158.B).
func TestTalkToSessionMintsSenderFromLivePath(t *testing.T) {
	dir := t.TempDir()
	mailDir := filepath.Join(dir, "mail")
	self := filepath.Join(dir, "self.jsonl")
	other := filepath.Join(dir, "other.jsonl")
	writeEmpty(t, self)
	writeEmpty(t, other)
	otherID, err := EnsureContactID(other)
	if err != nil {
		t.Fatal(err)
	}

	sendTool := NewTalkToSessionTool(SessionCollabConfig{
		Enabled:            true,
		SessionDir:         dir,
		WorkspaceRoot:      dir,
		MailDir:            mailDir,
		ResolveSessionPath: func() string { return self },
	})
	out, err := sendTool.Execute(context.Background(), json.RawMessage(`{"to":"`+otherID+`","message":"status?"}`))
	if err != nil {
		t.Fatal(err)
	}
	var sent struct {
		From string `json:"from"`
	}
	if err := json.Unmarshal([]byte(out), &sent); err != nil {
		t.Fatalf("send result: %v (%s)", err, out)
	}
	if sent.From == "" {
		t.Fatalf("sender identity must be minted from the live path: %s", out)
	}
	if want := SessionContactID(self); sent.From != want {
		t.Fatalf("sender id %q is not this session's id %q", sent.From, want)
	}
	box, err := sessioncollab.NewMailStore(mailDir).Inbox(otherID)
	if err != nil || len(box) != 1 {
		t.Fatalf("inbox: %v %v", box, err)
	}
	if box[0].From != sent.From {
		t.Fatalf("delivered message must carry the sender id: %+v", box[0])
	}
}

// Backward compatibility: a host that only sets the snapshot keeps working.
func TestSessionCollabFallsBackToBootSnapshot(t *testing.T) {
	dir := t.TempDir()
	self := filepath.Join(dir, "self.jsonl")
	writeEmpty(t, self)
	selfID, err := EnsureContactID(self)
	if err != nil {
		t.Fatal(err)
	}
	purposeTool := NewSetSessionPurposeTool(SessionCollabConfig{
		Enabled:            true,
		SessionDir:         dir,
		WorkspaceRoot:      dir,
		CurrentSessionPath: self,
		CurrentContactID:   selfID,
	})
	if _, err := purposeTool.Execute(context.Background(), json.RawMessage(`{"purpose":"snapshot"}`)); err != nil {
		t.Fatalf("snapshot-only config must keep working: %v", err)
	}
	meta, found, err := LoadBranchMeta(self)
	if err != nil || !found || meta.Purpose != "snapshot" {
		t.Fatalf("snapshot path not honoured: %+v found=%v err=%v", meta, found, err)
	}
}

// A task card filed before the path was bound must still name its origin — the
// same boot-order problem as the collab tools.
func TestTaskCardCarriesLiveSessionIdentity(t *testing.T) {
	dir := t.TempDir()
	self := filepath.Join(dir, "self.jsonl")
	writeEmpty(t, self)
	cards := NewTaskCardTools(TaskCardConfig{
		Enabled:            true,
		WorkspaceRoot:      dir,
		ResolveSessionPath: func() string { return self },
	})
	var create tool.Tool
	for _, c := range cards {
		if c.Name() == "create_task_card" {
			create = c
		}
	}
	if create == nil {
		t.Fatal("create_task_card must be registered")
	}
	out, err := create.Execute(context.Background(), json.RawMessage(`{"title":"hand off the audit"}`))
	if err != nil {
		t.Fatal(err)
	}
	var card sessioncollab.Card
	if err := json.Unmarshal([]byte(out), &card); err != nil {
		t.Fatalf("card json: %v (%s)", err, out)
	}
	if card.SessionFrom != self {
		t.Fatalf("card must carry the live session path, got %q", card.SessionFrom)
	}
	if card.Initiator == "" {
		t.Fatalf("card must carry the initiator's contact id: %+v", card)
	}
}
