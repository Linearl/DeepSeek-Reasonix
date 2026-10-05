package collabinbox

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/sessioncollab"
)

// 任务 464 夹具：六封信覆盖删除三分支的每一种组合。
//
//	msgA  sc_delS → sc_delR   双方都已删除
//	msgB  sc_live → sc_delR   仅收方已删除
//	msgC  sc_delS → sc_live   仅发方已删除
//	msgD  sc_live → sc_live   双方都在
//	msgE  ""       → sc_delR   系统邮件（空发方），收方已删除
//	msgF  sc_arch → sc_live   发方已归档（仍在地址簿 = 未删除）
func fixture464(t *testing.T) (*Store, *sessioncollab.MailStore, map[string]bool) {
	t.Helper()
	store, mail := fixtureStore(t)
	deliver(t, mail, sessioncollab.MailMessage{From: "sc_delS", To: "sc_delR", Body: "msgA", At: store.now() - 1000})
	deliver(t, mail, sessioncollab.MailMessage{From: "sc_live", To: "sc_delR", Body: "msgB", At: store.now() - 2000})
	deliver(t, mail, sessioncollab.MailMessage{From: "sc_delS", To: "sc_live", Body: "msgC", At: store.now() - 3000})
	deliver(t, mail, sessioncollab.MailMessage{From: "sc_live", To: "sc_live", Body: "msgD", At: store.now() - 4000})
	deliver(t, mail, sessioncollab.MailMessage{From: "", To: "sc_delR", Body: "msgE-system", At: store.now() - 5000})
	deliver(t, mail, sessioncollab.MailMessage{From: "sc_arch", To: "sc_live", Body: "msgF", At: store.now() - 6000})
	// 归档会话仍在可寻址名单（archive ≠ delete），所以算「活着」。
	live := map[string]bool{"sc_live": true, "sc_arch": true}
	store.SetLiveContacts(func() map[string]bool { return live })
	return store, mail, live
}

// 验收①现状有据：默认规则（never，等价于 464 之前的行为）下，发方删 / 收方删 /
// 双方都删，三支消息全部保留——用户实测「删除会话后消息去向」的钉死答案。
func Test464_DefaultRuleKeepsAllThreeDeletionBranches(t *testing.T) {
	store, _, _ := fixture464(t)
	// 不写任何状态文件：默认规则必须等于 never。
	snap, err := store.List(context.Background(), Query{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Total != 6 {
		t.Fatalf("default rule must keep all 6 messages across all three deletion branches, got %d", snap.Total)
	}
	if snap.Settings.CleanupRule != CleanupNever {
		t.Fatalf("default cleanup rule = %q, want never", snap.Settings.CleanupRule)
	}
}

// 旧状态文件（464 之前写入、没有 cleanupRule 键）必须静默归位为 never，
// 不得误触发清理。
func Test464_OldStateFileWithoutRuleNormalizesToNever(t *testing.T) {
	store, _, _ := fixture464(t)
	old := `{"revision": 7, "retention": "forever"}`
	if err := os.WriteFile(store.statePath(), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApplyRetention(context.Background()); err != nil {
		t.Fatal(err)
	}
	snap, err := store.List(context.Background(), Query{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Total != 6 {
		t.Fatalf("state file without cleanupRule must keep all 6, got %d", snap.Total)
	}
}

// 验收②规则②：收方删除 → 只有发到已删收件的信被清；发方删除但收方在的信保留。
func Test464_ReceiverRuleCleansOnlyReceiverDeletedMail(t *testing.T) {
	store, _, _ := fixture464(t)
	if _, err := store.SetCleanupRule(context.Background(), CleanupReceiver); err != nil {
		t.Fatal(err)
	}
	snap, err := store.List(context.Background(), Query{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Total != 3 { // msgC(收方在), msgD, msgF
		t.Fatalf("receiver rule: want 3 survivors (C,D,F), got %d: %+v", snap.Total, snap.Entries)
	}
	for _, e := range snap.Entries {
		if e.To == "sc_delR" {
			t.Fatalf("receiver rule: mail to deleted receiver survived: %+v", e)
		}
	}
}

// 规则①：发方删除 → 只清已删发方发的信；空发方（系统邮件）绝不判删。
func Test464_SenderRuleCleansOnlySenderDeletedMail(t *testing.T) {
	store, _, _ := fixture464(t)
	if _, err := store.SetCleanupRule(context.Background(), CleanupSender); err != nil {
		t.Fatal(err)
	}
	snap, err := store.List(context.Background(), Query{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Total != 4 { // msgB(发方在), msgD, msgE(空发方), msgF(归档=在)
		t.Fatalf("sender rule: want 4 survivors (B,D,E,F), got %d: %+v", snap.Total, snap.Entries)
	}
	for _, e := range snap.Entries {
		if e.From == "sc_delS" {
			t.Fatalf("sender rule: mail from deleted sender survived: %+v", e)
		}
	}
}

// 规则③：双方都删除才清。仅一端删除的都要留；空发方的系统邮件不算「已删发方」。
func Test464_BothRuleNeedsBothSidesDeleted(t *testing.T) {
	store, _, _ := fixture464(t)
	if _, err := store.SetCleanupRule(context.Background(), CleanupBoth); err != nil {
		t.Fatal(err)
	}
	snap, err := store.List(context.Background(), Query{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Total != 5 { // 只有 msgA 被清
		t.Fatalf("both rule: want 5 survivors (all but msgA), got %d: %+v", snap.Total, snap.Entries)
	}
}

// 规则④正交性第一半：保留期 forever（按龄清理关闭）+ 规则 both →
// 按会话存在性的清理照常生效——两个维度互不依赖。
func Test464_CleanupWorksWithRetentionForever(t *testing.T) {
	store, _, _ := fixture464(t)
	if _, err := store.SetRetention(context.Background(), RetentionForever); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetCleanupRule(context.Background(), CleanupBoth); err != nil {
		t.Fatal(err)
	}
	snap, err := store.List(context.Background(), Query{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Total != 5 {
		t.Fatalf("forever retention + both rule: want 5, got %d", snap.Total)
	}
}

// 规则④正交性第二半：保留期 7d（按龄清理开）+ 规则 never →
// 会话全删也不清，只有超龄才被清。
func Test464_RetentionWorksWithCleanupNever(t *testing.T) {
	store, mail, _ := fixture464(t)
	ctx := context.Background()
	// 先设规则（此刻信都还新鲜，任何 sweep 都不动它们），再把六封信全改到
	// 10 天前：7 天保留期会清掉它们（与删除无关）。
	if _, err := store.SetCleanupRule(ctx, CleanupNever); err != nil {
		t.Fatal(err)
	}
	aged := store.now() - 10*dayMs
	for _, id := range []string{"msgA", "msgB", "msgC", "msgD", "msgE-system", "msgF"} {
		if err := redateInboxRows(t, mail, id, aged); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := store.ApplyRetention(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 6 {
		t.Fatalf("7d retention must prune all 6 aged messages, removed %d", removed)
	}
	if snap, err := store.List(ctx, Query{}, false); err != nil || snap.Total != 0 {
		t.Fatalf("aged mail must be gone via retention alone, total=%d err=%v", snap.Total, err)
	}
}

// 无 liveness 名单（oracle 未安装）时，即便配置了规则也绝不清理——
// 没有权威名单时宁可不删（查询工具侧的 Store 就没有名单）。
func Test464_NoLiveContactsOracleMeansNoCleanup(t *testing.T) {
	store, _ := fixtureStore(t)
	deliver(t, store.mail, sessioncollab.MailMessage{From: "sc_gone", To: "sc_gone2", Body: "orphan", At: store.now()})
	if _, err := store.SetCleanupRule(context.Background(), CleanupBoth); err != nil {
		t.Fatal(err)
	}
	snap, err := store.List(context.Background(), Query{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Total != 1 {
		t.Fatalf("cleanup without a liveness oracle must be a no-op, got total=%d", snap.Total)
	}
}

// 归档 ≠ 删除：归档会话仍在名单里，它参与的信在任何规则下都保留。
func Test464_ArchivedSessionCountsAsLive(t *testing.T) {
	store, _, _ := fixture464(t)
	for _, rule := range []string{CleanupSender, CleanupReceiver, CleanupBoth} {
		if _, err := store.SetCleanupRule(context.Background(), rule); err != nil {
			t.Fatal(err)
		}
		snap, err := store.List(context.Background(), Query{From: "sc_arch"}, false)
		if err != nil {
			t.Fatal(err)
		}
		if snap.Total != 1 {
			t.Fatalf("rule=%s: archived sender's mail must survive, got %d", rule, snap.Total)
		}
	}
}

// SetCleanupRule：持久化到状态文件 + 立即生效 + 快照回带新规则；非法规则报错。
func Test464_SetCleanupRulePersistsAndAppliesImmediately(t *testing.T) {
	store, _, _ := fixture464(t)
	snap, err := store.SetCleanupRule(context.Background(), CleanupReceiver)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Settings.CleanupRule != CleanupReceiver {
		t.Fatalf("snapshot settings cleanupRule = %q, want receiver", snap.Settings.CleanupRule)
	}
	if _, err := store.SetCleanupRule(context.Background(), "bogus"); err == nil {
		t.Fatal("invalid cleanup rule must be rejected")
	}
	// 重启等价：新 Store 读同一目录，规则仍在且立即生效。
	reopened := New(store.mailDir, nil)
	reopened.SetLiveContacts(func() map[string]bool { return map[string]bool{"sc_live": true, "sc_arch": true} })
	if got := reopened.Settings(context.Background()).CleanupRule; got != CleanupReceiver {
		t.Fatalf("reopened store cleanupRule = %q, want receiver (persisted)", got)
	}
	snap2, err := reopened.List(context.Background(), Query{}, true)
	if err != nil {
		t.Fatal(err)
	}
	if snap2.Total != 3 {
		t.Fatalf("reopened store must apply the persisted rule on sweep, want 3, got %d", snap2.Total)
	}
}

// 清理后：被清条目的 dismissed/decided 状态一并回收（随条目同生死），活着的不动。
func Test464_StateKeysDieWithCleanedEntries(t *testing.T) {
	store, _, _ := fixture464(t)
	ctx := context.Background()
	// 拿到 msgA（将被清）与 msgD（将存活）的 id。
	snap, err := store.List(ctx, Query{}, false)
	if err != nil {
		t.Fatal(err)
	}
	var idA, idD string
	for _, e := range snap.Entries {
		switch e.Preview {
		case "msgA":
			idA = e.ID
		case "msgD":
			idD = e.ID
		}
	}
	if idA == "" || idD == "" {
		t.Fatalf("fixture ids missing: A=%q D=%q", idA, idD)
	}
	if _, err := store.Dismiss(ctx, []string{idA, idD}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Decide(ctx, idA, "human"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetCleanupRule(ctx, CleanupBoth); err != nil {
		t.Fatal(err)
	}
	st := store.loadState()
	if _, ok := st.Dismissed[idA]; ok {
		t.Fatal("dismissed key of a cleaned entry must be garbage-collected")
	}
	if _, ok := st.Dismissed[idD]; !ok {
		t.Fatal("dismissed key of a surviving entry must stay")
	}
	if _, ok := st.Decided[idA]; ok {
		t.Fatal("decided key of a cleaned entry must be garbage-collected")
	}
}

// 320 运行时复盘钉死：写侧维护（保留期/清理）失败不得把整个读拖死——
// 面板必须照常拿到数据，且快照带 Degraded 让桌面端留痕。
func Test464_SweepFailureDoesNotEmptyPanel(t *testing.T) {
	store, _, _ := fixture464(t)
	// retention 永久 + 规则 never：sweep 本身无事可做，注入失败模拟写锁楔死。
	if _, err := store.SetRetention(context.Background(), RetentionForever); err != nil {
		t.Fatal(err)
	}
	store.sweep = func(context.Context) (int, error) { return 0, errors.New("collab inbox lock busy (wedged holder)") }
	snap, err := store.List(context.Background(), Query{}, true)
	if err != nil {
		t.Fatalf("a failed sweep must not fail the read: %v", err)
	}
	if snap.Total != 6 {
		t.Fatalf("panel must still see all 6 entries after a failed sweep, got %d", snap.Total)
	}
	if !snap.Degraded {
		t.Fatal("snapshot must be marked Degraded when the sweep was skipped, so desktop logs the holder")
	}
}

// redateInboxRows rewrites the message carrying the given body to the new
// timestamp (test helper — the transport has no update verb by design).
func redateInboxRows(t *testing.T, mail *sessioncollab.MailStore, body string, at int64) error {
	t.Helper()
	dir := mail.Dir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".inbox.jsonl") {
			continue
		}
		path := filepath.Join(dir, name)
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var keep []string
		changed := false
		for _, line := range strings.Split(string(b), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var m sessioncollab.MailMessage
			if err := json.Unmarshal([]byte(line), &m); err != nil {
				keep = append(keep, line)
				continue
			}
			if m.Body == body {
				m.At = at
				changed = true
				rewritten, _ := json.Marshal(m)
				keep = append(keep, string(rewritten))
				continue
			}
			keep = append(keep, line)
		}
		if !changed {
			continue
		}
		if err := os.WriteFile(path, []byte(strings.Join(keep, "\n")+"\n"), 0o600); err != nil {
			return err
		}
	}
	return nil
}
