package collabinbox

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/sessioncollab"
)

const dayMs = 24 * 60 * 60 * 1000

// fixtureStore builds a store over a temp mail dir plus its transport store.
func fixtureStore(t *testing.T) (*Store, *sessioncollab.MailStore) {
	t.Helper()
	dir := t.TempDir()
	s := New(dir, nil)
	return s, sessioncollab.NewMailStore(dir)
}

// deliver is the fixture shorthand: explicit At keeps the clock deterministic.
func deliver(t *testing.T, mail *sessioncollab.MailStore, msg sessioncollab.MailMessage) sessioncollab.MailMessage {
	t.Helper()
	out, err := mail.Deliver(context.Background(), msg)
	if err != nil {
		t.Fatalf("deliver: %v", err)
	}
	return out
}

// 验收 1：10 封信件（不同收/发/日期）→ 发信方/收信方筛选 + 日期排序。
func TestFilterBySenderRecipientAndDateOrder(t *testing.T) {
	store, mail := fixtureStore(t)
	now := store.now()
	alice, bob, carol := "sc_alice", "sc_bob", "sc_carol"
	for i := 0; i < 10; i++ {
		from, to := alice, bob
		switch i % 3 {
		case 1:
			from, to = bob, carol
		case 2:
			from, to = carol, alice
		}
		deliver(t, mail, sessioncollab.MailMessage{
			From: from, To: to, Body: "msg " + string(rune('a'+i)),
			At: now - int64(10-i)*dayMs,
		})
	}

	snap, err := store.List(context.Background(), Query{From: alice}, false)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Total != 4 { // i = 0,3,6,9
		t.Fatalf("from=alice total = %d, want 4 (%s)", snap.Total, snap.Revision)
	}
	snap, err = store.List(context.Background(), Query{To: bob}, false)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Total != 4 { // i = 0,1,...: to=bob on i%3==0 and i%3==1? case0: to=bob (i%3==0), case1: from=bob — recount below
		// i%3==0 → to=bob (4 of 10), i%3==1 → to=carol, i%3==2 → to=alice.
		t.Fatalf("to=bob total = %d, want 4", snap.Total)
	}
	// 排序：默认 desc（新在前），asc 反之。
	desc, err := store.List(context.Background(), Query{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(desc.Entries) != 10 || desc.Entries[0].At < desc.Entries[len(desc.Entries)-1].At {
		t.Fatalf("default order must be date desc: %+v", desc.Entries)
	}
	asc, err := store.List(context.Background(), Query{Order: "asc"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if asc.Entries[0].At > asc.Entries[len(asc.Entries)-1].At {
		t.Fatalf("order=asc must be date asc: %+v", asc.Entries[0])
	}
	if desc.Entries[0].ID == asc.Entries[0].ID {
		t.Fatal("desc and asc must start from opposite ends")
	}
}

// 验收 4：五桶各 ≥1 条且分类正确；审批桶子态 + 裁决者（human 与对话 id 各一）。
func TestFiveBucketsClassifyPendingAndDecisions(t *testing.T) {
	dir := t.TempDir()
	// resolver：sc_heartbeat 发的信按发送方注册身份归入自动化桶（task 348 联动）。
	s := New(dir, func(contact string) string {
		if contact == "sc_heartbeat" {
			return "heartbeat"
		}
		return ""
	})
	mail := sessioncollab.NewMailStore(dir)
	now := s.now()

	// 审批：显式 approver。
	approval := deliver(t, mail, sessioncollab.MailMessage{
		From: "sc_child", To: "sc_main", Body: "请求批准部署", Approver: "sc_main",
		At: now, Kind: "approval",
	})
	// 提及：普通点对点派活。
	mention := deliver(t, mail, sessioncollab.MailMessage{
		From: "sc_alice", To: "sc_main", Body: "帮我调研 X", At: now - 1000,
	})
	// 自动化：心跳会话发信（resolver 归类）。
	automation := deliver(t, mail, sessioncollab.MailMessage{
		From: "sc_heartbeat", To: "sc_main", Body: "定时巡检报告", At: now - 2000,
	})
	// 系统：平台回执（创建时打标）。
	system := deliver(t, mail, sessioncollab.MailMessage{
		From: "sc_alice", To: "sc_main", Body: "已读回执：...", At: now - 3000, Kind: "system",
	})

	snap, err := s.List(context.Background(), Query{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Total != 4 {
		t.Fatalf("want 4 entries, got %d", snap.Total)
	}
	byID := map[string]Entry{}
	for _, e := range snap.Entries {
		byID[e.ID] = e
	}
	if byID[approval.ID].Bucket != BucketApproval {
		t.Fatalf("approval bucket: %+v", byID[approval.ID])
	}
	if byID[mention.ID].Bucket != BucketMention {
		t.Fatalf("mention bucket: %+v", byID[mention.ID])
	}
	if byID[automation.ID].Bucket != BucketAutomation {
		t.Fatalf("automation bucket (resolver): %+v", byID[automation.ID])
	}
	if byID[system.ID].Bucket != BucketSystem {
		t.Fatalf("system bucket: %+v", byID[system.ID])
	}

	// 各桶过滤恰好命中。
	for bucket, want := range map[string]string{
		BucketApproval: approval.ID, BucketMention: mention.ID,
		BucketAutomation: automation.ID, BucketSystem: system.ID,
	} {
		got, err := s.List(context.Background(), Query{Bucket: bucket}, false)
		if err != nil {
			t.Fatal(err)
		}
		if got.Total != 1 || got.Entries[0].ID != want {
			t.Fatalf("bucket %s: total=%d want-id=%s got=%+v", bucket, got.Total, want, got.Entries)
		}
	}

	// 待我审：viewer=sc_main 时审批条目 PendingMe。
	pending, err := s.List(context.Background(), Query{Bucket: BucketApproval, State: StatePendingMe, Viewer: "sc_main"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if pending.Total != 1 || !pending.Entries[0].PendingMe {
		t.Fatalf("pendingMe for sc_main: %+v", pending.Entries)
	}

	// 裁决①：人工（面板点击）→ decidedBy=human。
	decided, err := s.Decide(context.Background(), approval.ID, "human")
	if err != nil {
		t.Fatal(err)
	}
	_ = decided
	after, err := s.List(context.Background(), Query{Bucket: BucketApproval}, false)
	if err != nil {
		t.Fatal(err)
	}
	if after.Entries[0].DecidedBy != "human" {
		t.Fatalf("human decision must be recorded: %+v", after.Entries[0])
	}
	// 已裁决子态过滤命中。
	decidedView, err := s.List(context.Background(), Query{Bucket: BucketApproval, State: StateDecided}, false)
	if err != nil {
		t.Fatal(err)
	}
	if decidedView.Total != 1 {
		t.Fatalf("decided filter: %+v", decidedView)
	}

	// 裁决②：对话批准——approver（sc_boss）在该审批自己的 thread 上回信。
	approval2 := deliver(t, mail, sessioncollab.MailMessage{
		From: "sc_child", To: "sc_boss", Body: "请求批准 2", Approver: "sc_boss",
		At: now - 5000, Kind: "approval",
	})
	deliver(t, mail, sessioncollab.MailMessage{
		From: "sc_boss", To: "sc_child", Body: "批准", ThreadID: approval2.ID, At: now - 4000,
	})
	conv, err := s.List(context.Background(), Query{Bucket: BucketApproval}, false)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, e := range conv.Entries {
		if e.ID == approval2.ID {
			found = true
			if e.DecidedBy != "sc_boss" {
				t.Fatalf("conversation decision must derive sc_boss, got %+v", e)
			}
		}
	}
	if !found {
		t.Fatalf("approval2 missing: %+v", conv.Entries)
	}
	// 未被裁决的那条（decidedBy=human 的 approval 之外）——sc_main 的审批
	// 现在已人工裁决；确认没有把「随便谁回信」误判：mention 的 thread 无回信。
	if byID[mention.ID].DecidedBy != "" {
		t.Fatalf("non-approval must never show a decision: %+v", byID[mention.ID])
	}
}

// 验收 3：保留期 7→30：30 天内保留、超 30 天清理；永久不清理。
func TestRetentionSwitchPrunesAndForeverKeeps(t *testing.T) {
	store, mail := fixtureStore(t)
	now := store.now()
	inside := deliver(t, mail, sessioncollab.MailMessage{From: "sc_a", To: "sc_b", Body: "10d old", At: now - 10*dayMs})
	outside := deliver(t, mail, sessioncollab.MailMessage{From: "sc_a", To: "sc_b", Body: "40d old", At: now - 40*dayMs})

	// 切到 30d：立即应用——超期条目物理移除，期内保留。
	if _, err := store.SetRetention(context.Background(), Retention30d); err != nil {
		t.Fatal(err)
	}
	snap, err := store.List(context.Background(), Query{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Settings.Retention != Retention30d {
		t.Fatalf("settings not persisted: %+v", snap.Settings)
	}
	ids := map[string]bool{}
	for _, e := range snap.Entries {
		ids[e.ID] = true
	}
	if !ids[inside.ID] {
		t.Fatal("entry inside the 30d window must be kept")
	}
	if ids[outside.ID] {
		t.Fatal("entry older than 30d must be pruned")
	}
	// 物理移除：传输层文件里也不再有这封信。
	raw, err := mail.Inbox("sc_b")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range raw {
		if m.ID == outside.ID {
			t.Fatal("prune must rewrite the inbox file, not just hide the row")
		}
	}

	// 永久：不清理。
	deliver(t, mail, sessioncollab.MailMessage{From: "sc_a", To: "sc_b", Body: "ancient", At: now - 400*dayMs})
	if _, err := store.SetRetention(context.Background(), RetentionForever); err != nil {
		t.Fatal(err)
	}
	snap, err = store.List(context.Background(), Query{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Settings.Retention != RetentionForever || snap.Total != 2 {
		t.Fatalf("forever keeps everything: %+v", snap)
	}
}

// 验收 5：revision 快照契约 + 排队中未落库不产生条目、落库后仅一次。
func TestRevisionContractAndQueuedInvisible(t *testing.T) {
	dir := t.TempDir()
	storeA := New(dir, nil)
	mail := sessioncollab.NewMailStore(dir)

	// 排队中：只写发送方 sent log，未投递收件箱 → 不产生条目。
	queued := sessioncollab.MailMessage{ID: "msg_queued", From: "sc_a", To: "sc_b", Body: "queued", At: storeA.now()}
	mail.RecordSent(context.Background(), queued, "B")

	snapA, err := storeA.List(context.Background(), Query{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if snapA.Total != 0 {
		t.Fatalf("a queued (sent-only) message must not be an entry: %+v", snapA.Entries)
	}

	// 真实落库 → 出现且仅一次。
	deliver(t, mail, queued)
	snapA, err = storeA.List(context.Background(), Query{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if snapA.Total != 1 {
		t.Fatalf("delivered message must appear exactly once: %+v", snapA.Entries)
	}

	// A 端 dismiss → 返回新 revision；B 端（独立 Store 实例=另一窗口）list
	// 返回的 revision 变化且条目消失。
	revBefore := snapA.Revision
	dismissed, err := storeA.Dismiss(context.Background(), []string{queued.ID})
	if err != nil {
		t.Fatal(err)
	}
	if dismissed.Revision == revBefore {
		t.Fatalf("dismiss must bump the revision: before=%s after=%s", revBefore, dismissed.Revision)
	}
	if dismissed.Total != 0 {
		t.Fatalf("dismissed entry must vanish from the default view: %+v", dismissed.Entries)
	}
	storeB := New(dir, nil)
	snapB, err := storeB.List(context.Background(), Query{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if snapB.Revision == revBefore || snapB.Total != 0 {
		t.Fatalf("window B must converge on the new snapshot: rev=%s total=%d", snapB.Revision, snapB.Total)
	}
	// 已消除仍可显形（IncludeDismissed），供「已消除」视图。
	withGone, err := storeB.List(context.Background(), Query{IncludeDismissed: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	if withGone.Total != 1 || !withGone.Entries[0].Dismissed {
		t.Fatalf("dismissed entries stay addressable: %+v", withGone.Entries)
	}
}

// 验收 6：重启保留——新 Store 实例（模拟重启）后条目仍在、已消除不重现、设置还在。
func TestRestartKeepsEntriesDismissalsAndSettings(t *testing.T) {
	dir := t.TempDir()
	first := New(dir, nil)
	mail := sessioncollab.NewMailStore(dir)
	deliver(t, mail, sessioncollab.MailMessage{From: "sc_a", To: "sc_b", Body: "kept", At: first.now()})
	gone := deliver(t, mail, sessioncollab.MailMessage{From: "sc_a", To: "sc_b", Body: "eliminated", At: first.now() - 1000})
	if _, err := first.SetRetention(context.Background(), Retention90d); err != nil {
		t.Fatal(err)
	}
	if _, err := first.Dismiss(context.Background(), []string{gone.ID}); err != nil {
		t.Fatal(err)
	}

	reborn := New(dir, nil) // restart: nothing in memory, everything on disk
	snap, err := reborn.List(context.Background(), Query{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Settings.Retention != Retention90d {
		t.Fatalf("retention must survive restart: %+v", snap.Settings)
	}
	for _, e := range snap.Entries {
		if e.ID == gone.ID {
			t.Fatal("a dismissed entry must not reappear after restart")
		}
	}
	if snap.Total != 1 {
		t.Fatalf("surviving entries: %+v", snap.Entries)
	}
}

// 验收 g：对话链视图——同 thread 多轮 = 一条链，展开即全链。
func TestChainViewGroupsThread(t *testing.T) {
	store, mail := fixtureStore(t)
	now := store.now()
	orig := deliver(t, mail, sessioncollab.MailMessage{From: "sc_a", To: "sc_b", Body: "第 1 轮", At: now - 3000})
	for i := 0; i < 3; i++ {
		deliver(t, mail, sessioncollab.MailMessage{
			From: []string{"sc_b", "sc_a"}[i%2], To: []string{"sc_a", "sc_b"}[i%2],
			Body: "轮次", ThreadID: orig.ID, At: now - int64(2000-i),
		})
	}
	// 另一封独立消息，自成一链。
	deliver(t, mail, sessioncollab.MailMessage{From: "sc_x", To: "sc_y", Body: "另一链", At: now})

	snap, err := store.Chains(context.Background(), Query{})
	if err != nil {
		t.Fatal(err)
	}
	if snap.Total != 2 {
		t.Fatalf("want 2 chains, got %d: %+v", snap.Total, snap.Chains)
	}
	var main *Chain
	for i := range snap.Chains {
		if snap.Chains[i].ThreadID == orig.ID {
			main = &snap.Chains[i]
		}
	}
	if main == nil {
		t.Fatalf("thread chain missing: %+v", snap.Chains)
	}
	if main.Count != 4 || len(main.Entries) != 4 {
		t.Fatalf("chain must hold the whole thread: %+v", main)
	}
	for i := 1; i < len(main.Entries); i++ {
		if main.Entries[i].At < main.Entries[i-1].At {
			t.Fatal("chain entries must be chronological")
		}
	}
	if len(main.Participants) < 2 {
		t.Fatalf("participants: %+v", main.Participants)
	}
}

// 验收 2 的库侧边界：limit 默认 50、硬上限 500、截断标记。
func TestLimitDefaultAndHardCap(t *testing.T) {
	store, mail := fixtureStore(t)
	now := store.now()
	// 任务461 P8 ①：投递层同内容幂等会折叠同 from/to/body 的种子，分页测试
	// 需要 60 条独立消息 → 正文带序号。
	for i := 0; i < 60; i++ {
		deliver(t, mail, sessioncollab.MailMessage{
			From: "sc_a", To: "sc_b", Body: fmt.Sprintf("m-%d", i), At: now - int64(i),
		})
	}
	snap, err := store.List(context.Background(), Query{}, false) // default
	if err != nil {
		t.Fatal(err)
	}
	if snap.Returned != 50 || snap.Total != 60 || !snap.Truncated {
		t.Fatalf("default limit=50 + truncated: returned=%d total=%d truncated=%v", snap.Returned, snap.Total, snap.Truncated)
	}
	big, err := store.List(context.Background(), Query{Limit: 5000}, false)
	if err != nil {
		t.Fatal(err)
	}
	if big.Returned != 60 || big.Truncated {
		t.Fatalf("60 < hard cap must return all: %+v", big)
	}
	if max := 500; big.Returned > max {
		t.Fatalf("hard cap violated: %d", big.Returned)
	}
}

// Read 状态来自收件方 seen cursor（task 320 b）。
func TestReadStateFollowsSeenCursor(t *testing.T) {
	store, mail := fixtureStore(t)
	m := deliver(t, mail, sessioncollab.MailMessage{From: "sc_a", To: "sc_b", Body: "x", At: store.now()})
	snap, _ := store.List(context.Background(), Query{}, false)
	if snap.Entries[0].Read || !snap.Entries[0].Delivered {
		t.Fatalf("before ack: %+v", snap.Entries[0])
	}
	if err := mail.Ack(context.Background(), "sc_b", m.ID); err != nil {
		t.Fatal(err)
	}
	snap, _ = store.List(context.Background(), Query{}, false)
	if !snap.Entries[0].Read {
		t.Fatalf("after ack the entry must read as read: %+v", snap.Entries[0])
	}
	unread, _ := store.List(context.Background(), Query{Unread: true}, false)
	if unread.Total != 0 {
		t.Fatalf("unread filter: %+v", unread)
	}
}

// state 文件永不越出邮件目录（所有状态与消息同址，重启保留的载体）。
func TestStateFileStaysBesideMail(t *testing.T) {
	dir := t.TempDir()
	s := New(dir, nil)
	if _, err := s.Dismiss(context.Background(), []string{"msg_x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := filepath.Abs(filepath.Join(dir, stateName)); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, stateName))
	if err != nil || !strings.Contains(string(b), "msg_x") {
		t.Fatalf("state file must live in the mail dir: %v %s", err, b)
	}
}

// 349 挂账 note①：带群来源戳的信 → 条目透出 Channel 群标识；未打戳的
// 点对点信保持为空。桶分类不受该戳影响（分类是 Kind/身份的职责）。
func TestEntryCarriesChannelGroupStamp(t *testing.T) {
	s, mail := fixtureStore(t)
	now := s.now()
	stamped := deliver(t, mail, sessioncollab.MailMessage{
		From: "sc_s", To: "sc_main", Body: "来自群聊的行", At: now, Channel: "dev",
	})
	plain := deliver(t, mail, sessioncollab.MailMessage{
		From: "sc_alice", To: "sc_main", Body: "点对点派活", At: now - 1000,
	})
	snap, err := s.List(context.Background(), Query{}, false)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]Entry{}
	for _, e := range snap.Entries {
		byID[e.ID] = e
	}
	if got := byID[stamped.ID].Channel; got != "dev" {
		t.Fatalf("stamped entry must carry the group identifier: %+v", byID[stamped.ID])
	}
	if byID[stamped.ID].Bucket != BucketMention {
		t.Fatalf("channel stamp must not change bucket classification: %+v", byID[stamped.ID])
	}
	if got := byID[plain.ID].Channel; got != "" {
		t.Fatalf("point-to-point entry must have no channel identifier: %+v", byID[plain.ID])
	}
}
