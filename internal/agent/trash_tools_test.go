package agent

// tl1710 工具层回归：trash_list / trash_restore 的宿主转发契约，以及
// delete_session 对 hasTurn=true 目标的三语强警示（dry_run / confirm /
// batch 三面）。宿主侧的真实回收站夹具回归在 desktop 包
//（desktop/session_trash_agent_test.go）——本文件只钉工具面自己的行为。

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/tool"
)

// --- trash_list -------------------------------------------------------

func TestTrashListToolForwardsHostEntries(t *testing.T) {
	host := []TrashEntry{
		{Title: "决策模式", SessionPath: "C:\\trash\\bucket\\决策模式.jsonl", HasTurn: true, DeletedAt: 1763600000000, RestoreUntil: TrashRestoreUntilManual, Scope: "global"},
		{Title: "empty shell", SessionPath: "C:\\trash\\bucket2\\empty.jsonl", HasTurn: false, DeletedAt: 1763600001000, RestoreUntil: TrashRestoreUntilManual},
	}
	tl := NewTrashListTool(SessionCollabConfig{Enabled: true}, func() ([]TrashEntry, error) {
		return host, nil
	})
	out, err := tl.Execute(nil, nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var payload struct {
		Count        int          `json:"count"`
		HasTurnCount int          `json:"hasTurnCount"`
		Items        []TrashEntry `json:"items"`
		RestoreUntil string       `json:"restoreUntil"`
		Note         string       `json:"note"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("answer not JSON: %v\n%s", err, out)
	}
	if payload.Count != 2 || len(payload.Items) != 2 {
		t.Fatalf("count=%d items=%d, want 2/2", payload.Count, len(payload.Items))
	}
	if payload.HasTurnCount != 1 {
		t.Fatalf("hasTurnCount=%d, want 1 — the incident's discriminator", payload.HasTurnCount)
	}
	if payload.Items[0].Title != "决策模式" || !payload.Items[0].HasTurn {
		t.Fatalf("first entry lost its fields: %+v", payload.Items[0])
	}
	if payload.RestoreUntil != "manual" {
		t.Fatalf("restoreUntil=%q, want manual", payload.RestoreUntil)
	}
	// 三语 note：en + 简体 + 繁體 都要在（各段指向 trash_restore）。
	for _, seg := range []string{"trash_restore", "trash_restore", "sessionPath"} {
		if !strings.Contains(payload.Note, seg) {
			t.Fatalf("note missing %q: %s", seg, payload.Note)
		}
	}
	if !strings.Contains(payload.Note, "还原") || !strings.Contains(payload.Note, "還原") {
		t.Fatalf("note must carry both zh and zh-TW segments: %s", payload.Note)
	}
}

func TestTrashListToolEmptyTrashIsNotAnError(t *testing.T) {
	tl := NewTrashListTool(SessionCollabConfig{}, func() ([]TrashEntry, error) { return nil, nil })
	out, err := tl.Execute(nil, nil)
	if err != nil {
		t.Fatalf("empty trash must answer, got %v", err)
	}
	if !strings.Contains(out, `"count":0`) {
		t.Fatalf("empty answer should carry count=0: %s", out)
	}
}

func TestTrashListToolNilHostRefuses(t *testing.T) {
	tl := NewTrashListTool(SessionCollabConfig{}, nil)
	if _, err := tl.Execute(nil, nil); err == nil || !strings.Contains(err.Error(), "cannot list the trash") {
		t.Fatalf("nil host must refuse with an actionable error, got %v", err)
	}
}

// --- trash_restore ----------------------------------------------------

func TestTrashRestoreToolForwardsPath(t *testing.T) {
	got := ""
	tr := NewTrashRestoreTool(SessionCollabConfig{Enabled: true}, func(path string) error {
		got = path
		return nil
	})
	out, err := tr.Execute(nil, []byte(`{"path":"C:\\trash\\b\\决策模式.jsonl"}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got != "C:\\trash\\b\\决策模式.jsonl" {
		t.Fatalf("host got %q, want the reported sessionPath verbatim", got)
	}
	if !strings.Contains(out, `"status":"restored"`) {
		t.Fatalf("answer missing restored status: %s", out)
	}
	if !strings.Contains(out, "核验") || !strings.Contains(out, "核驗") || !strings.Contains(out, "read_session_tail") {
		t.Fatalf("note must carry the trilingual verify hint: %s", out)
	}
}

func TestTrashRestoreToolRequiresPath(t *testing.T) {
	tr := NewTrashRestoreTool(SessionCollabConfig{}, func(path string) error {
		t.Fatal("host must not run without a path")
		return nil
	})
	if _, err := tr.Execute(nil, []byte(`{}`)); err == nil || !strings.Contains(err.Error(), "path is required") {
		t.Fatalf("missing path must be refused with the source of a valid one, got %v", err)
	}
}

func TestTrashRestoreToolNilHostRefuses(t *testing.T) {
	tr := NewTrashRestoreTool(SessionCollabConfig{}, nil)
	if _, err := tr.Execute(nil, []byte(`{"path":"x.jsonl"}`)); err == nil || !strings.Contains(err.Error(), "cannot restore") {
		t.Fatalf("nil host must refuse, got %v", err)
	}
}

func TestTrashRestoreToolPassesHostErrorThrough(t *testing.T) {
	sentinel := errors.New("session already exists: x.jsonl")
	tr := NewTrashRestoreTool(SessionCollabConfig{}, func(path string) error { return sentinel })
	if _, err := tr.Execute(nil, []byte(`{"path":"x.jsonl"}`)); !errors.Is(err, sentinel) {
		t.Fatalf("host conflict error must reach the caller verbatim, got %v", err)
	}
}

// --- delete_session hasTurn 警示（件3） --------------------------------

// deleteSessionWarningTool wires a one-session directory whose host impact is
// given by the test.
func deleteSessionWarningTool(t *testing.T, dir string, impact DeleteSessionImpact) tool.Tool {
	t.Helper()
	return NewDeleteSessionTool(SessionCollabConfig{
		Enabled:       true,
		SessionDir:    dir,
		WorkspaceRoot: dir,
	}, func(contactID, sessionPath string, dryRun bool) (DeleteSessionImpact, DeleteSessionResult, error) {
		impact.ContactID = contactID
		impact.SessionPath = sessionPath
		if dryRun {
			return impact, DeleteSessionResult{}, nil
		}
		return impact, DeleteSessionResult{ContactID: contactID, SessionPath: sessionPath, Trashed: true, RestoreUntil: "manual"}, nil
	})
}

// A1 件3: dry_run of a hasTurn=true target carries the prominent trilingual
// warning BEFORE any confirm — the 20261119 incident's fix point.
func TestDeleteSessionDryRunWarnsOnHasTurn(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "decision-mode.jsonl")
	writeEmpty(t, target)
	id, err := EnsureContactID(target)
	if err != nil {
		t.Fatal(err)
	}
	tl := deleteSessionWarningTool(t, dir, DeleteSessionImpact{Title: "决策模式", HasTurn: true})
	out, err := tl.Execute(nil, []byte(`{"target":"`+id+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Status  string `json:"status"`
		Warning string `json:"warning"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("dry run not JSON: %v\n%s", err, out)
	}
	if payload.Status != "dry_run" {
		t.Fatalf("status=%s", payload.Status)
	}
	if payload.Warning == "" {
		t.Fatalf("hasTurn=true dry run must carry a warning: %s", out)
	}
	// 显著性三要件：点名目标、hasTurn 事实、真实存在的出口（trash_restore）。
	if !strings.Contains(payload.Warning, "决策模式") {
		t.Fatalf("warning must name the target: %s", payload.Warning)
	}
	if !strings.Contains(payload.Warning, "hasTurn=true") {
		t.Fatalf("warning must state hasTurn=true: %s", payload.Warning)
	}
	if !strings.Contains(payload.Warning, "trash_restore") {
		t.Fatalf("warning must name the recovery exit: %s", payload.Warning)
	}
	// 三语：简体（请停手）与繁體（請停手）两段都要在；en 段由 hasTurn/trash_
	// restore 断言覆盖。
	if !strings.Contains(payload.Warning, "请停手") || !strings.Contains(payload.Warning, "請停手") {
		t.Fatalf("warning must carry both zh and zh-TW segments: %s", payload.Warning)
	}
}

// turnInFlight gets its own clause — "busy" is a stronger fact than "history".
func TestDeleteSessionDryRunWarnsTurnInFlight(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "inflight.jsonl")
	writeEmpty(t, target)
	id, err := EnsureContactID(target)
	if err != nil {
		t.Fatal(err)
	}
	tl := deleteSessionWarningTool(t, dir, DeleteSessionImpact{Title: "inflight", HasTurn: true, TurnInFlight: true})
	out, err := tl.Execute(nil, []byte(`{"target":"`+id+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Warning string `json:"warning"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if !strings.Contains(payload.Warning, "in flight") || !strings.Contains(payload.Warning, "正在进行") || !strings.Contains(payload.Warning, "正在進行") {
		t.Fatalf("in-flight clause missing (trilingual): %s", payload.Warning)
	}
}

// An empty-shell dry run stays warning-free — no noise for the safe case.
func TestDeleteSessionDryRunNoWarningForEmptyShell(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "shell.jsonl")
	writeEmpty(t, target)
	id, err := EnsureContactID(target)
	if err != nil {
		t.Fatal(err)
	}
	tl := deleteSessionWarningTool(t, dir, DeleteSessionImpact{Title: "shell", HasTurn: false})
	out, err := tl.Execute(nil, []byte(`{"target":"`+id+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "warning") {
		t.Fatalf("empty shell must not warn: %s", out)
	}
}

// The confirmed result carries the recovery hint so the filed receipt names
// its own undo exit (留痕).
func TestDeleteSessionConfirmCarriesRecoveryHint(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "confirmed.jsonl")
	writeEmpty(t, target)
	id, err := EnsureContactID(target)
	if err != nil {
		t.Fatal(err)
	}
	tl := deleteSessionWarningTool(t, dir, DeleteSessionImpact{Title: "confirmed", HasTurn: true})
	out, err := tl.Execute(nil, []byte(`{"target":"`+id+`","confirm":true}`))
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Status  string `json:"status"`
		Warning string `json:"warning"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if payload.Status != "trashed" {
		t.Fatalf("status=%s", payload.Status)
	}
	if !strings.Contains(payload.Warning, "trash_restore") {
		t.Fatalf("confirmed result must carry the recovery hint: %s", out)
	}
}

// Batch face: the summary counts the hasTurn rows that would leave, and the
// warning names each affected target; protected rows are not counted.
func TestDeleteSessionBatchWarnsOnHasTurnTargets(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "batch-real.jsonl")
	shell := filepath.Join(dir, "batch-shell.jsonl")
	writeEmpty(t, real)
	writeEmpty(t, shell)
	realID, err := EnsureContactID(real)
	if err != nil {
		t.Fatal(err)
	}
	shellID, err := EnsureContactID(shell)
	if err != nil {
		t.Fatal(err)
	}
	tl := NewDeleteSessionTool(SessionCollabConfig{
		Enabled:       true,
		SessionDir:    dir,
		WorkspaceRoot: dir,
	}, func(contactID, sessionPath string, dryRun bool) (DeleteSessionImpact, DeleteSessionResult, error) {
		impact := DeleteSessionImpact{HasTurn: sessionPath == real}
		if dryRun {
			return impact, DeleteSessionResult{}, nil
		}
		return impact, DeleteSessionResult{ContactID: contactID, SessionPath: sessionPath, Trashed: true, RestoreUntil: "manual"}, nil
	})
	out, err := tl.Execute(nil, []byte(`{"targets":["`+realID+`","`+shellID+`"]}`))
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Status  string `json:"status"`
		Warning string `json:"warning"`
		Summary struct {
			Matched    int `json:"matched"`
			WouldTrash int `json:"wouldTrash"`
			HasTurn    int `json:"hasTurn"`
		} `json:"summary"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if payload.Summary.WouldTrash != 2 {
		t.Fatalf("wouldTrash=%d, want 2", payload.Summary.WouldTrash)
	}
	if payload.Summary.HasTurn != 1 {
		t.Fatalf("summary.hasTurn=%d, want 1 (only the real conversation)", payload.Summary.HasTurn)
	}
	if !strings.Contains(payload.Warning, realID) || strings.Contains(payload.Warning, shellID) {
		t.Fatalf("warning must name exactly the hasTurn targets: %s", payload.Warning)
	}
	if !strings.Contains(payload.Warning, "trash_restore") {
		t.Fatalf("batch warning must name the exit: %s", payload.Warning)
	}
}
