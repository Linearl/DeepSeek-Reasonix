package agent

// tl1768: mailbox(action=batch_dispose) tests.
//
// 验收锚点：
// ① 两段式：dry_run 缺省 = true，只列清单零改动；dry_run=false 才执行——
//    忘写 flag 的调用永远不可能误删（验收③：不可逆清理必须先出清单）；
// ② mode=archive 归档可恢复（dismiss 墓碑 + 面板可恢复）；mode=delete 物理
//    移除留档（tl1722 双语义先例；对话已注入内容不受影响的 store 契约）；
// ③ 过滤条件与 query 同口径（to/bucket/since/until/unread_only/state/viewer）；
// ④ 路由：mailbox 委托同一实现；未知 mode/bucket/state/since≥until 拒绝；
//    peek/drain/query 零回退由 mailbox_tool_test.go 既有等价钉全量回归。

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"reasonix/internal/sessioncollab"
)

// deliverDisposeFixture seeds sc_from → sc_to with one old handled mail and
// one fresh mail, plus an unrelated pair's mail that no filter may touch.
func deliverDisposeFixture(t *testing.T, env statusTestEnv) {
	t.Helper()
	store := sessioncollab.NewMailStore(env.mailDir)
	base := jsonTime()
	for _, m := range []sessioncollab.MailMessage{
		{ID: "m_old", From: env.fromID, To: env.toID, Body: "old handled mail", At: base - 3*dayMsAgent},
		{ID: "m_new", From: env.fromID, To: env.toID, Body: "fresh pending mail", At: base},
		{ID: "m_other", From: "sc_third", To: "sc_fourth", Body: "unrelated", At: base},
	} {
		if _, err := store.Deliver(context.Background(), m); err != nil {
			t.Fatal(err)
		}
	}
}

// jsonTime/test-clock anchor: a fixed recent ms epoch keeps the since/until
// fixtures deterministic without dragging in a clock seam.
func jsonTime() int64 { return 1_775_000_000_000 } // 2026-04-01 (fixed)

const dayMsAgent = 24 * 60 * 60 * 1000

func disposeOut(t *testing.T, out string) map[string]any {
	t.Helper()
	var p map[string]any
	if err := json.Unmarshal([]byte(out), &p); err != nil {
		t.Fatalf("batch_dispose must return parseable JSON: %v\n%s", err, out)
	}
	return p
}

// 验收①：dry_run 缺省（=true）只列清单零改动——这是两段式的安全底座。
func TestBatchDisposeDryRunDefaultsToListOnly(t *testing.T) {
	env := newStatusTestEnv(t)
	deliverDisposeFixture(t, env)
	cfg := mailboxTestCfg(env)

	// No dry_run flag at all.
	out := runTool(t, NewMailboxTool(cfg, true), `{"action":"batch_dispose","mode":"delete","to":"`+env.toID+`"}`)
	p := disposeOut(t, out)
	if p["dryRun"] != true {
		t.Fatalf("missing dry_run must behave as phase 1: %s", out)
	}
	if n, _ := p["matched"].(float64); int(n) != 2 {
		t.Fatalf("the plan must list the to-filtered matches: %s", out)
	}
	entries, _ := p["entries"].([]any)
	if len(entries) != 2 {
		t.Fatalf("phase 1 must carry the entry list: %s", out)
	}
	// Nothing was disposed: a fresh query still sees all three rows.
	q := runTool(t, NewMailboxTool(cfg, true), `{"action":"query"}`)
	qp := disposeOut(t, q)
	if n, _ := qp["total"].(float64); int(n) != 3 {
		t.Fatalf("dry-run must not mutate: query total = %v, want 3", qp["total"])
	}
}

// 验收①③：delete 的完整两段式——phase 1 清单 → phase 2 执行，物理移除且
// 对话已注入内容不受影响（store 契约：archive-only 删除）。
func TestBatchDisposeDeleteTwoPhase(t *testing.T) {
	env := newStatusTestEnv(t)
	deliverDisposeFixture(t, env)
	cfg := mailboxTestCfg(env)
	base := jsonTime()

	// Phase 1: the until-capped plan lists exactly the old handled mail.
	plan := runTool(t, NewMailboxTool(cfg, true), `{"action":"batch_dispose","mode":"delete","to":"`+env.toID+`","until":`+msEpoch(base-2*dayMsAgent)+`}`)
	pp := disposeOut(t, plan)
	if n, _ := pp["matched"].(float64); int(n) != 1 {
		t.Fatalf("phase 1 must match only the pre-until row: %s", plan)
	}

	// Phase 2: same filters, dry_run=false.
	out := runTool(t, NewMailboxTool(cfg, true), `{"action":"batch_dispose","mode":"delete","to":"`+env.toID+`","until":`+msEpoch(base-2*dayMsAgent)+`,"dry_run":false}`)
	p := disposeOut(t, out)
	if p["dryRun"] != false || p["recoverable"] != false {
		t.Fatalf("phase 2 delete must be non-recoverable: %s", out)
	}
	if n, _ := p["disposed"].(float64); int(n) != 1 {
		t.Fatalf("phase 2 must dispose the planned row: %s", out)
	}
	// The archive copy is physically gone; the untouched rows survive.
	store := sessioncollab.NewMailStore(env.mailDir)
	if _, found := store.ParentThread(env.toID, "m_old"); found {
		t.Fatal("the disposed mail still sits in the recipient inbox")
	}
	if _, found := store.ParentThread(env.toID, "m_new"); !found {
		t.Fatal("the dispose touched mail outside the filter")
	}
}

// 验收③：archive 是可恢复路径——dismiss 墓碑隐藏，transport 层不动。
func TestBatchDisposeArchiveIsRecoverable(t *testing.T) {
	env := newStatusTestEnv(t)
	deliverDisposeFixture(t, env)
	cfg := mailboxTestCfg(env)

	out := runTool(t, NewMailboxTool(cfg, true), `{"action":"batch_dispose","mode":"archive","to":"`+env.toID+`","dry_run":false}`)
	p := disposeOut(t, out)
	if p["recoverable"] != true {
		t.Fatalf("archive must report recoverable=true: %s", out)
	}
	if n, _ := p["disposed"].(float64); int(n) != 2 {
		t.Fatalf("archive must dispose both to-matches: %s", out)
	}
	// Hidden from the default view, transport layer untouched.
	q := runTool(t, NewMailboxTool(cfg, true), `{"action":"query"}`)
	qp := disposeOut(t, q)
	if n, _ := qp["total"].(float64); int(n) != 1 {
		t.Fatalf("archived rows must leave the default view: %s", q)
	}
	store := sessioncollab.NewMailStore(env.mailDir)
	if _, found := store.ParentThread(env.toID, "m_old"); !found {
		t.Fatal("archive must not touch the transport layer")
	}
}

// 验收④+过滤口径：unread_only / viewer 子态与 query 同源；不影响 drain 游标
// 语义（non-goal：消费语义不动）。
func TestBatchDisposeUnreadOnlyFilterMatchesQuery(t *testing.T) {
	env := newStatusTestEnv(t)
	deliverDisposeFixture(t, env)
	cfg := mailboxTestCfg(env)

	// unread_only over sc_fourth's fresh mail: the plan matches what query
	// matches with the same filter (same filterEntries core).
	base := jsonTime()
	plan := runTool(t, NewMailboxTool(cfg, true), `{"action":"batch_dispose","mode":"archive","to":"sc_fourth","since":`+msEpoch(base-3*dayMsAgent)+`,"dry_run":true}`)
	pp := disposeOut(t, plan)
	want := runTool(t, NewMailboxTool(cfg, true), `{"action":"query","to":"sc_fourth","since":`+msEpoch(base-3*dayMsAgent)+`}`)
	wp := disposeOut(t, want)
	gotN, _ := pp["matched"].(float64)
	wantN, _ := wp["total"].(float64)
	if gotN != wantN || int(gotN) != 1 {
		t.Fatalf("dispose filter must agree with query: plan %v vs query %v", pp["matched"], wp["total"])
	}
}

// 参数校验：未知 mode/bucket/state、since≥until 全拒绝，文案给下一步。
func TestBatchDisposeArgumentValidation(t *testing.T) {
	env := newStatusTestEnv(t)
	deliverDisposeFixture(t, env)
	cfg := mailboxTestCfg(env)
	mb := NewMailboxTool(cfg, true)

	cases := []struct {
		args string
		want string
	}{
		{`{"action":"batch_dispose","mode":"shred"}`, "archive|delete"},
		{`{"action":"batch_dispose"}`, "archive|delete"},
		{`{"action":"batch_dispose","mode":"archive","bucket":"spam"}`, "all|approval|mention|automation|system"},
		{`{"action":"batch_dispose","mode":"archive","state":"expired"}`, "all|pendingMe|mine|decided"},
		{`{"action":"batch_dispose","mode":"archive","since":200,"until":100}`, "since must precede until"},
	}
	for _, c := range cases {
		_, err := mb.Execute(context.Background(), json.RawMessage(c.args))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("args %s must be refused naming %q: %v", c.args, c.want, err)
		}
	}
	// Validation errors never mutate.
	q := runTool(t, mb, `{"action":"query"}`)
	if n, _ := disposeOut(t, q)["total"].(float64); int(n) != 3 {
		t.Fatal("refused calls must not mutate")
	}
}

// 路由同源：直调构造器与 mailbox 委托逐字节一致（689 范式）。
func TestBatchDisposeDelegationMatchesDirectCall(t *testing.T) {
	env := newStatusTestEnv(t)
	deliverDisposeFixture(t, env)
	cfg := mailboxTestCfg(env)

	args := `{"mode":"archive","to":"` + env.toID + `"}`
	got := runTool(t, NewMailboxTool(cfg, true), withAction(t, args, "batch_dispose"))
	want := runTool(t, NewBatchDisposeTool(cfg), args)
	if got != want {
		t.Fatalf("mailbox(action=batch_dispose) diverged from the direct call:\n got: %s\nwant: %s", got, want)
	}
}

func msEpoch(n int64) string {
	return strconv.FormatInt(n, 10)
}
