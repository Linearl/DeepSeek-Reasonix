package agent

// tl1305 ①③：delete_session 批量面（targets 数组 + filter 谓词 + 保护阶梯 +
// dry-run 批量影响报告 + 失败项不阻断）。单删契约（target 单参）不在本文件
// 回归——session_collab_delete_test.go / _unread_gate_test.go / _shell_probe_test.go
// 就是它的逐字节回归集。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/tool"
)

// batchHost records every host call so a test can assert exactly which
// sessions the batch actually trashed.
type batchHost struct {
	dryRuns  []string
	trashes  []string
	impacts  map[string]DeleteSessionImpact
	failOn   map[string]error
	noImpact DeleteSessionImpact
}

func (h *batchHost) funcFor(t *testing.T) DeleteSessionFunc {
	return func(contactID, sessionPath string, dryRun bool) (DeleteSessionImpact, DeleteSessionResult, error) {
		impact := h.noImpact
		if got, ok := h.impacts[sessionPath]; ok {
			impact = got
		}
		impact.ContactID = contactID
		impact.SessionPath = sessionPath
		if dryRun {
			h.dryRuns = append(h.dryRuns, sessionPath)
			if err, ok := h.failOn[sessionPath]; ok && err != nil {
				return DeleteSessionImpact{}, DeleteSessionResult{}, err
			}
			return impact, DeleteSessionResult{}, nil
		}
		h.trashes = append(h.trashes, sessionPath)
		if err, ok := h.failOn[sessionPath]; ok && err != nil {
			return impact, DeleteSessionResult{}, err
		}
		return impact, DeleteSessionResult{ContactID: contactID, SessionPath: sessionPath, Trashed: true, RestoreUntil: "manual"}, nil
	}
}

// batchResult is the decoded shape of a batch answer, kept loose: the test
// asserts on the fields that carry the contract, not the whole wire.
type batchResult struct {
	Status string `json:"status"`
	Items  []struct {
		Target      string               `json:"target"`
		Title       string               `json:"title"`
		SessionPath string               `json:"sessionPath"`
		Status      string               `json:"status"`
		Reason      string               `json:"reason"`
		Protected   bool                 `json:"protected"`
		Result      *DeleteSessionResult `json:"result"`
	} `json:"items"`
	Summary struct {
		Matched    int `json:"matched"`
		Trashed    int `json:"trashed"`
		Skipped    int `json:"skipped"`
		Failed     int `json:"failed"`
		WouldTrash int `json:"wouldTrash"`
	} `json:"summary"`
	Note string `json:"note"`
}

// runBatchFor executes one batch call and decodes the answer; a malformed
// answer is a test failure, not a decode error for every caller to re-check.
func runBatchFor(t *testing.T, tl tool.Tool, raw string) (batchResult, error) {
	t.Helper()
	out, err := tl.Execute(nil, []byte(raw))
	if err != nil {
		return batchResult{}, err
	}
	var res batchResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("batch answer is not the batch shape: %v\n%s", err, out)
	}
	return res, nil
}

// ① 批量 N 个会话一次调用完成。
func TestDeleteSessionBatchTrashesAllTargets(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "hb-1.jsonl")
	b := filepath.Join(dir, "hb-2.jsonl")
	c := filepath.Join(dir, "hb-3.jsonl")
	for _, p := range []string{a, b, c} {
		writeEmpty(t, p)
	}
	host := &batchHost{}
	tool := NewDeleteSessionTool(SessionCollabConfig{Enabled: true, SessionDir: dir, WorkspaceRoot: dir}, host.funcFor(t))
	out, err := tool.Execute(nil, []byte(`{"targets":["hb-1","hb-2","hb-3"],"confirm":true}`))
	if err != nil {
		t.Fatal(err)
	}
	var res batchResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	if res.Status != "batch_done" || res.Summary.Trashed != 3 || res.Summary.Failed != 0 {
		t.Fatalf("want batch_done trashed=3, got %+v", res.Summary)
	}
	if len(host.trashes) != 3 {
		t.Fatalf("host must see exactly 3 trashes, got %v", host.trashes)
	}
	for i := range res.Items {
		if res.Items[i].Status != "trashed" || res.Items[i].Result == nil || !res.Items[i].Result.Trashed {
			t.Fatalf("item %d not trashed: %+v", i, res.Items[i])
		}
	}
}

// ② 失败项跳过并报告，不阻断其余目标。
func TestDeleteSessionBatchIsolatesFailures(t *testing.T) {
	dir := t.TempDir()
	good1 := filepath.Join(dir, "keep-going.jsonl")
	good2 := filepath.Join(dir, "still-fine.jsonl")
	writeEmpty(t, good1)
	writeEmpty(t, good2)
	host := &batchHost{failOn: map[string]error{good1: os.ErrPermission}}
	tool := NewDeleteSessionTool(SessionCollabConfig{Enabled: true, SessionDir: dir, WorkspaceRoot: dir}, host.funcFor(t))
	out, err := tool.Execute(nil, []byte(`{"targets":["keep-going","no-such-session","still-fine"],"confirm":true}`))
	if err != nil {
		t.Fatalf("a per-item failure must not fail the call: %v", err)
	}
	var res batchResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	if res.Summary.Trashed != 1 || res.Summary.Failed != 2 {
		t.Fatalf("want trashed=1 failed=2 (bad ref + host error), got %+v", res.Summary)
	}
	if len(host.trashes) != 1 || host.trashes[0] != good2 {
		t.Fatalf("only still-fine must reach the host, got %v", host.trashes)
	}
	byStatus := map[string]int{}
	for _, item := range res.Items {
		byStatus[item.Status]++
		if item.Status == "failed" && item.Reason == "" {
			t.Fatalf("failed item must carry its reason: %+v", item)
		}
	}
	if byStatus["trashed"] != 1 || byStatus["failed"] != 2 {
		t.Fatalf("per-item statuses wrong: %v", byStatus)
	}
}

// ③ dry-run 批量影响报告：零写入，逐项 impact + 汇总。
func TestDeleteSessionBatchDryRunWritesNothing(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "dry-a.jsonl")
	b := filepath.Join(dir, "dry-b.jsonl")
	writeEmpty(t, a)
	writeEmpty(t, b)
	host := &batchHost{impacts: map[string]DeleteSessionImpact{
		a: {HasTurn: true},
	}}
	tool := NewDeleteSessionTool(SessionCollabConfig{Enabled: true, SessionDir: dir, WorkspaceRoot: dir}, host.funcFor(t))
	res, err := runBatchFor(t, tool, `{"targets":["dry-a","dry-b"]}`)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "dry_run" || res.Summary.WouldTrash != 2 {
		t.Fatalf("want dry_run wouldTrash=2, got status=%s %+v", res.Status, res.Summary)
	}
	if len(host.trashes) != 0 {
		t.Fatalf("dry run trashed: %v", host.trashes)
	}
	if len(host.dryRuns) != 2 {
		t.Fatalf("dry run must inspect both targets, saw %v", host.dryRuns)
	}
	for _, item := range res.Items {
		if item.Status != "" {
			t.Fatalf("dry-run items carry no execution status: %+v", item)
		}
		if item.Title == "" {
			t.Fatalf("dry-run item must name its conversation: %+v", item)
		}
	}
}

// 验收④保护阶梯：turnInFlight 恒保护、openTab 默认保护且可被
// include_open_tabs 覆盖、调用者自身恒保护。
func TestDeleteSessionBatchProtectsActive(t *testing.T) {
	dir := t.TempDir()
	running := filepath.Join(dir, "running-now.jsonl")
	openTab := filepath.Join(dir, "open-tab.jsonl")
	self := filepath.Join(dir, "am-the-caller.jsonl")
	free := filepath.Join(dir, "free-to-go.jsonl")
	for _, p := range []string{running, openTab, self, free} {
		writeEmpty(t, p)
	}
	host := &batchHost{impacts: map[string]DeleteSessionImpact{
		running: {OpenTab: true, HasTurn: true, TurnInFlight: true},
		openTab: {OpenTab: true, HasTurn: true},
	}}
	cfg := SessionCollabConfig{Enabled: true, SessionDir: dir, WorkspaceRoot: dir, CurrentSessionPath: self}

	// 第一轮（默认保护）：running/openTab/self 全保护，只有 free 可删。
	tool := NewDeleteSessionTool(cfg, host.funcFor(t))
	res, err := runBatchFor(t, tool, `{"targets":["running-now","open-tab","am-the-caller","free-to-go"],"confirm":true}`)
	if err != nil {
		t.Fatal(err)
	}
	if res.Summary.Trashed != 1 || res.Summary.Skipped != 3 {
		t.Fatalf("default ladder must spare 3, got %+v", res.Summary)
	}
	if len(host.trashes) != 1 || !strings.HasSuffix(host.trashes[0], "free-to-go.jsonl") {
		t.Fatalf("only free-to-go may trash, got %v", host.trashes)
	}
	for _, item := range res.Items {
		if !item.Protected && item.Status == "skipped" || item.Protected && item.Reason == "" {
			t.Fatalf("protected rows must carry their reason: %+v", item)
		}
	}

	// include_open_tabs 覆盖 open-tab 保护，但 turnInFlight 与自身仍保护。
	host2 := &batchHost{impacts: host.impacts}
	tool2 := NewDeleteSessionTool(cfg, host2.funcFor(t))
	res2, err := runBatchFor(t, tool2, `{"targets":["running-now","open-tab","am-the-caller","free-to-go"],"include_open_tabs":true,"confirm":true}`)
	if err != nil {
		t.Fatal(err)
	}
	if res2.Summary.Trashed != 2 || res2.Summary.Skipped != 2 {
		t.Fatalf("include_open_tabs must free only the open tab, got %+v", res2.Summary)
	}
	for _, item := range res2.Items {
		if item.SessionPath == running && item.Status != "skipped" {
			t.Fatalf("turnInFlight stays protected: %+v", item)
		}
		if item.SessionPath == self && item.Status != "skipped" {
			t.Fatalf("the calling session stays protected: %+v", item)
		}
	}
}

// protect_hours：时间保护窗内的会话跳过（mtime 判据）。
func TestDeleteSessionBatchProtectHours(t *testing.T) {
	dir := t.TempDir()
	fresh := filepath.Join(dir, "fresh-today.jsonl")
	stale := filepath.Join(dir, "stale-long-ago.jsonl")
	writeEmpty(t, fresh)
	writeEmpty(t, stale)
	staleTime := time.Now().Add(-72 * time.Hour)
	if err := os.Chtimes(stale, staleTime, staleTime); err != nil {
		t.Fatal(err)
	}
	host := &batchHost{}
	tool := NewDeleteSessionTool(SessionCollabConfig{Enabled: true, SessionDir: dir, WorkspaceRoot: dir}, host.funcFor(t))
	res, err := runBatchFor(t, tool, `{"targets":["fresh-today","stale-long-ago"],"protect_hours":24,"confirm":true}`)
	if err != nil {
		t.Fatal(err)
	}
	if res.Summary.Trashed != 1 || res.Summary.Skipped != 1 {
		t.Fatalf("protect_hours=24 must spare fresh-today, got %+v", res.Summary)
	}
	if len(host.trashes) != 1 || !strings.HasSuffix(host.trashes[0], "stale-long-ago.jsonl") {
		t.Fatalf("only the stale session may trash, got %v", host.trashes)
	}
}

// ③ 过滤条件：标题前缀 + 不活跃天数（AND），命中即批量。
func TestDeleteSessionBatchFilterTitlePrefixAndInactiveDays(t *testing.T) {
	dir := t.TempDir()
	hb1 := filepath.Join(dir, "hb-old-1.jsonl")
	hb2 := filepath.Join(dir, "hb-old-2.jsonl")
	other := filepath.Join(dir, "unrelated.jsonl")
	recent := filepath.Join(dir, "hb-recent.jsonl")
	for _, p := range []string{hb1, hb2, other, recent} {
		writeEmpty(t, p)
	}
	old := time.Now().Add(-48 * time.Hour)
	for _, p := range []string{hb1, hb2} {
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}
	host := &batchHost{}
	tool := NewDeleteSessionTool(SessionCollabConfig{Enabled: true, SessionDir: dir, WorkspaceRoot: dir}, host.funcFor(t))
	// dry-run 先看影响面
	res, err := runBatchFor(t, tool, `{"filter":{"title_prefix":"hb-old","inactive_days":1}}`)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "dry_run" || res.Summary.Matched != 2 || res.Summary.WouldTrash != 2 {
		t.Fatalf("prefix+inactive must match exactly the two stale hb sessions, got %+v", res.Summary)
	}
	if len(host.trashes) != 0 {
		t.Fatalf("dry run trashed: %v", host.trashes)
	}
	// confirm 执行
	res2, err := runBatchFor(t, tool, `{"filter":{"title_prefix":"hb-old","inactive_days":1},"confirm":true}`)
	if err != nil {
		t.Fatal(err)
	}
	if res2.Summary.Trashed != 2 || len(host.trashes) != 2 {
		t.Fatalf("confirm must trash exactly the two stale sessions, got %+v %v", res2.Summary, host.trashes)
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatalf("unrelated session must survive: %v", err)
	}
	if _, err := os.Stat(recent); err != nil {
		t.Fatalf("the active hb session must survive the inactive_days gate: %v", err)
	}
}

// 分组过滤：探针在 → 按组成员匹配；探针不在 → fail closed（整批拒绝）。
func TestDeleteSessionBatchFilterGroup(t *testing.T) {
	dir := t.TempDir()
	inGroup := filepath.Join(dir, "grouped-one.jsonl")
	writeEmpty(t, inGroup)
	if err := os.WriteFile(inGroup+".meta", []byte(`{"topic_id":"topic-in-group","contact_id":"sc_in"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	probe := func(topicID, group string) bool { return topicID == "topic-in-group" && group == "心跳历史" }
	host := &batchHost{}
	tool := NewDeleteSessionTool(SessionCollabConfig{
		Enabled: true, SessionDir: dir, WorkspaceRoot: dir,
		SessionGroupMatch: probe,
	}, host.funcFor(t))
	res, err := runBatchFor(t, tool, `{"filter":{"group":"心跳历史"},"confirm":true}`)
	if err != nil {
		t.Fatal(err)
	}
	if res.Summary.Trashed != 1 || len(host.trashes) != 1 {
		t.Fatalf("the group member must be the only match, got %+v %v", res.Summary, host.trashes)
	}
	// 探针缺席：分组谓词 fail closed（宁可空手而归不可全目录误删）。
	toolNoProbe := NewDeleteSessionTool(SessionCollabConfig{Enabled: true, SessionDir: dir, WorkspaceRoot: dir}, host.funcFor(t))
	if _, err := runBatchFor(t, toolNoProbe, `{"filter":{"group":"心跳历史"},"confirm":true}`); err == nil {
		t.Fatalf("a group filter without the host probe must fail closed")
	}
}

// 空 filter 拒绝：没有谓词的批量 = 无界全目录删除。
func TestDeleteSessionBatchRejectsEmptyFilter(t *testing.T) {
	dir := t.TempDir()
	host := &batchHost{}
	tool := NewDeleteSessionTool(SessionCollabConfig{Enabled: true, SessionDir: dir, WorkspaceRoot: dir}, host.funcFor(t))
	if _, err := runBatchFor(t, tool, `{"filter":{},"confirm":true}`); err == nil || !strings.Contains(err.Error(), "at least one") {
		t.Fatalf("empty filter must be refused, got %v", err)
	}
	if _, err := runBatchFor(t, tool, `{"targets":[],"confirm":true}`); err == nil {
		t.Fatalf("an empty targets array is not a batch either, got no error")
	}
}

// target 与 targets/filter 混传拒绝：两条契约的输出形状不同，不能猜。
func TestDeleteSessionBatchRejectsMixedModes(t *testing.T) {
	dir := t.TempDir()
	host := &batchHost{}
	tool := NewDeleteSessionTool(SessionCollabConfig{Enabled: true, SessionDir: dir, WorkspaceRoot: dir}, host.funcFor(t))
	_, err := runBatchFor(t, tool, `{"target":"x","targets":["y"]}`)
	if err == nil || !strings.Contains(err.Error(), "not both") {
		t.Fatalf("target+targets must be refused, got %v", err)
	}
	_, err = runBatchFor(t, tool, `{"target":"x","filter":{"title_prefix":"y"}}`)
	if err == nil || !strings.Contains(err.Error(), "not both") {
		t.Fatalf("target+filter must be refused, got %v", err)
	}
}

// 批量上限：失控的调用在门口被打回，不是删到第 200 个才停。
func TestDeleteSessionBatchRejectsOverLimit(t *testing.T) {
	dir := t.TempDir()
	host := &batchHost{}
	tool := NewDeleteSessionTool(SessionCollabConfig{Enabled: true, SessionDir: dir, WorkspaceRoot: dir}, host.funcFor(t))
	refs := make([]string, 0, deleteSessionBatchLimit+1)
	for i := 0; i <= deleteSessionBatchLimit; i++ {
		refs = append(refs, "ref-"+strings.Repeat("x", i%40))
	}
	raw, err := json.Marshal(map[string]any{"targets": refs, "confirm": true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Execute(nil, raw); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("over-limit batch must be refused at the door, got %v", err)
	}
	if len(host.trashes) != 0 {
		t.Fatalf("refused batch must not trash anything: %v", host.trashes)
	}
}
