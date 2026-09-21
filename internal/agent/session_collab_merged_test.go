package agent

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// Task 174: the merged task_card routes all four actions, and every old
// capability stays reachable through the one schema.
func TestMergedTaskCardRoutesAllActions(t *testing.T) {
	cfg := TaskCardConfig{Enabled: true, WorkspaceRoot: t.TempDir(), CurrentContactID: "sc_a"}
	tool := NewTaskCardTool(cfg)

	created, err := tool.Execute(nil, []byte(`{"action":"create","title":"Wire the board","body":"do it"}`))
	if err != nil {
		t.Fatalf("create action: %v", err)
	}
	var card struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	if err := json.Unmarshal([]byte(created), &card); err != nil || card.ID == "" {
		t.Fatalf("create must return the card: %v %s", err, created)
	}

	if _, err := tool.Execute(nil, []byte(`{"action":"update","id":"`+card.ID+`","status":"done","result":"shipped"}`)); err != nil {
		t.Fatalf("update action: %v", err)
	}
	got, err := tool.Execute(nil, []byte(`{"action":"get","id":"`+card.ID+`"}`))
	if err != nil || !strings.Contains(got, "shipped") {
		t.Fatalf("get action must see the update: %v %s", err, got)
	}
	listed, err := tool.Execute(nil, []byte(`{"action":"list"}`))
	if err != nil || !strings.Contains(listed, "Wire the board") {
		t.Fatalf("list action must include the card: %v %s", err, listed)
	}
	if _, err := tool.Execute(nil, []byte(`{"action":"rename"}`)); err == nil {
		t.Fatal("an unknown action must be refused")
	}
}

// Task 174: search is list's query now — an empty query is the unfiltered page
// (the old default), a query filters over title/purpose/ids.
func TestMergedListAddressableSessionsKeepsQuerySemantics(t *testing.T) {
	dir := t.TempDir()
	hit := filepath.Join(dir, "hit.jsonl")
	miss := filepath.Join(dir, "miss.jsonl")
	writeEmpty(t, hit)
	writeEmpty(t, miss)
	if err := UpdateBranchMeta(hit, true, func(m *BranchMeta) error {
		m.CustomTitle = "Release Notes Draft"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := UpdateBranchMeta(miss, true, func(m *BranchMeta) error {
		m.CustomTitle = "Something else"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	cfg := SessionCollabConfig{Enabled: true, SessionDir: dir, WorkspaceRoot: dir}
	tool := NewListAddressableSessionsTool(cfg)

	filtered, err := tool.Execute(nil, []byte(`{"query":"release"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(filtered, "Release Notes Draft") || strings.Contains(filtered, "Something else") {
		t.Fatalf("query must filter: %s", filtered)
	}
	unfiltered, err := tool.Execute(nil, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(unfiltered, "Something else") {
		t.Fatalf("no query must keep the unfiltered page: %s", unfiltered)
	}
}

// Task 175/174: the sent log is reachable as the directory tool's sent=true
// action — the model-side check for a misdirected batch dispatch. Newest
// first, one summary line per message, recipient title included.
func TestMergedListAddressableSessionsExposesSentLog(t *testing.T) {
	cfg, _ := gateFixture(t)
	talk := NewTalkToSessionTool(cfg)
	if _, err := talk.Execute(nil, []byte(`{"to":"gate target","message":"one"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := talk.Execute(nil, []byte(`{"to":"gate target","message":"two\nsecond line"}`)); err != nil {
		t.Fatal(err)
	}
	out, err := NewListAddressableSessionsTool(cfg).Execute(nil, []byte(`{"sent":true}`))
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		From  string `json:"from"`
		Count int    `json:"count"`
		Sent  []struct {
			To      string `json:"to"`
			ToTitle string `json:"toTitle"`
			Summary string `json:"summary"`
		} `json:"sent"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("sent page must be JSON: %v\n%s", err, out)
	}
	if payload.Count != 2 {
		t.Fatalf("both sends must be listed: %s", out)
	}
	if payload.Sent[0].Summary != "two" || payload.Sent[0].ToTitle != "gate target" {
		t.Fatalf("newest first with recipient title and a first-line summary: %s", out)
	}
	if !strings.Contains(out, "gate target") {
		t.Fatalf("the recipient must be visible for the misdirected-send check: %s", out)
	}
}
