package main

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/sessioninbox"
)

// tl1296 验收钉：引导消息（durable inbox）发送面撞 write authority stale 闸门
// 时——lease 可重取（活 lease 重发一代 / 空闲锁接管）才自动 re-mint 并重驱
// 队头；不可重取（活持有者在别处）保持原行为（条目留队 + 拒绝 notice，手动
// reopen 语义零回退）；并发场景不产生第二个 lease 持有者、终态单活 authority。
// 复用 tl1342 的同一批 harness（真 Controller，executor session 必须在场，
// 否则 authority bind 是静默 no-op）。

// inboxWaitLeftRetryable 轮询等待条目离开可重试态（queued/uncertain/blocked）
// ——被重驱的队头先转 running，轮收尾后被消费或落定。
func inboxWaitLeftRetryable(t *testing.T, ctrl control.SessionAPI, id string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		for _, it := range ctrl.InboxSnapshot().Items {
			if it.ID == id {
				state := sessioninbox.InboxState(it.State)
				if state != sessioninbox.StateQueued && state != sessioninbox.StateUncertain && state != sessioninbox.StateBlocked {
					return string(state)
				}
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("inbox item %s never left the retryable states within 5s; snapshot states=%v", id, inboxStateList(ctrl))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func inboxStateList(ctrl control.SessionAPI) []string {
	states := []string{}
	for _, it := range ctrl.InboxSnapshot().Items {
		states = append(states, it.ID+":"+string(it.State))
	}
	return states
}

// enqueueUncertain 给测试播一条「崩溃孤儿」形态的引导行（InitialParkReason
// → born uncertain，不入派发视野），返回条目 id。这正是崩溃恢复循环里等待
// 用户点重试的那一行。
func enqueueUncertain(t *testing.T, ctrl *control.Controller, label string) string {
	t.Helper()
	rec, err := ctrl.TryEnqueueFollowup(control.InboxRequest{
		Intent:            sessioninbox.IntentFollowup,
		Display:           label,
		Raw:               label,
		Submit:            label,
		InitialParkReason: "tl1296_test_park",
	})
	if err != nil {
		t.Fatalf("enqueue uncertain inbox item: %v", err)
	}
	return rec.ItemID
}

// 正钉①a（重试面）：uncertain 引导行 + stale authority → 用户点重试 →
// heal 重发一代 → 队头被重驱（条目离开可重试态）→ 流上恰好一条拒绝 notice
// + 一条自动恢复 notice，世代单调前进。
func TestRetryInboxItemAutoHealsStaleWriteAuthorityAndRedrives(t *testing.T) {
	app, tab, ctrl, rec, path := newStaleRetryFixture(t, "inbox_retry_main")
	lease, err := agent.TryAcquireSessionLease(path)
	if err != nil {
		t.Fatalf("acquire lease: %v", err)
	}
	t.Cleanup(lease.Release)
	tab.adoptSessionLease(lease)
	if err := control.IssueAndBindWriteAuthority(ctrl, lease); err != nil {
		t.Fatalf("bind first generation: %v", err)
	}
	staleSideController(t, config.SessionDir(), path, lease, "inbox_retry_main_side")
	staleGen := ctrl.WriteAuthorityGeneration()
	itemID := enqueueUncertain(t, ctrl, "crash orphan guidance")
	if !submissionRefusedByStaleWriteAuthority(ctrl) {
		t.Fatal("precondition failed: controller not stale before retry")
	}

	if err := app.RetryInboxItem(tab.ID, itemID); err != nil {
		t.Fatalf("retry with stale authority must auto-heal, got error: %v", err)
	}
	if got := ctrl.WriteAuthorityGeneration(); got <= staleGen {
		t.Fatalf("authority generation after heal = %d, want a fresh value above %d", got, staleGen)
	}
	if state := inboxWaitLeftRetryable(t, ctrl, itemID); state == "" {
		t.Fatalf("retried item %s reported an empty state", itemID)
	}
	if n := rec.countEventually(t, 1, noticeMatching("no longer writable")); n != 1 {
		t.Fatalf("gate refusal notices = %d, want exactly 1", n)
	}
	if n := rec.countEventually(t, 1, noticeMatching("restored automatically")); n != 1 {
		t.Fatalf("auto-restored notices = %d, want exactly 1 (no retry storm)", n)
	}
	// tl1296：纠正行携带稳定 code（write_authority_auto_recovered），前端按
	// 它取三语「应用崩溃后正在恢复」文案。
	for _, ev := range rec.snapshot() {
		if ev.kind == "notice" && strings.Contains(ev.text, "restored automatically") && ev.code != noticeCodeWriteAuthorityRecovered {
			t.Fatalf("auto-restored notice code = %q, want %q", ev.code, noticeCodeWriteAuthorityRecovered)
		}
	}
}

// 正钉①b（恢复面）：暂停队列里排着一条 queued 引导 + stale authority →
// SetInboxPaused(false) 恢复派发 → 闸门拒绝 → heal 重发一代 → 队头重驱成功。
// 恢复面不发流上纠正行（无法锚定单条目交付；纠错行归重试面）。
func TestResumeInboxAutoHealsStaleWriteAuthorityAndRedrives(t *testing.T) {
	app, tab, ctrl, rec, path := newStaleRetryFixture(t, "inbox_resume_main")
	lease, err := agent.TryAcquireSessionLease(path)
	if err != nil {
		t.Fatalf("acquire lease: %v", err)
	}
	t.Cleanup(lease.Release)
	// 先暂停再入队：入队派发被暂停挡住（NextQueued 不见暂停队列），条目
	// 稳定停在 queued。
	if err := ctrl.SetInboxPausedPassive(true); err != nil {
		t.Fatalf("pause inbox: %v", err)
	}
	tab.adoptSessionLease(lease)
	if err := control.IssueAndBindWriteAuthority(ctrl, lease); err != nil {
		t.Fatalf("bind first generation: %v", err)
	}
	staleSideController(t, config.SessionDir(), path, lease, "inbox_resume_main_side")
	staleGen := ctrl.WriteAuthorityGeneration()
	recEnqueue, err := ctrl.TryEnqueueFollowup(control.InboxRequest{
		Intent: sessioninbox.IntentFollowup, Display: "queued guidance", Raw: "queued guidance", Submit: "queued guidance",
	})
	if err != nil {
		t.Fatalf("enqueue queued inbox item: %v", err)
	}
	if state, ok := inboxItemState(ctrl, recEnqueue.ItemID); !ok || state != sessioninbox.StateQueued {
		t.Fatalf("precondition failed: item state = %q ok=%v, want queued", state, ok)
	}

	if err := app.SetInboxPaused(tab.ID, false); err != nil {
		t.Fatalf("resume with stale authority must auto-heal, got error: %v", err)
	}
	if got := ctrl.WriteAuthorityGeneration(); got <= staleGen {
		t.Fatalf("authority generation after heal = %d, want a fresh value above %d", got, staleGen)
	}
	inboxWaitLeftRetryable(t, ctrl, recEnqueue.ItemID)
	if n := rec.countEventually(t, 1, noticeMatching("no longer writable")); n != 1 {
		t.Fatalf("gate refusal notices = %d, want exactly 1", n)
	}
	if n := rec.countEventually(t, 0, noticeMatching("restored automatically")); n != 0 {
		t.Fatalf("resume surface must not emit the per-item correction notice, got %d", n)
	}
}

// 负钉（tl1296 验收①）：lease 不可重取（活持有者在本进程另一个 runtime 手
// 里——双进程互斥的进程内替身）→ heal 必须拒绝：世代原样（零 mint）、持有
// 者 lease 对象原样、条目留队、拒绝 notice 恰一条、无自动恢复行——手动
// reopen 老路零回退。
func TestRetryInboxItemKeepsManualReopenPathWhenHealDeclined(t *testing.T) {
	app, tab, ctrl, rec, path := newStaleRetryFixture(t, "inbox_retry_declined")
	// 持有者 lease 保持存活但永不装到 tab 上：fast path 无 held lease 可发代，
	// takeover 撞活锁被拒。
	holder, err := agent.TryAcquireSessionLease(path)
	if err != nil {
		t.Fatalf("acquire holder lease: %v", err)
	}
	t.Cleanup(holder.Release)
	if err := control.IssueAndBindWriteAuthority(ctrl, holder); err != nil {
		t.Fatalf("bind first generation: %v", err)
	}
	staleSideController(t, config.SessionDir(), path, holder, "inbox_retry_declined_side")
	staleGen := ctrl.WriteAuthorityGeneration()
	itemID := enqueueUncertain(t, ctrl, "stranded guidance")

	if err := app.RetryInboxItem(tab.ID, itemID); err != nil {
		t.Fatalf("declined heal keeps the notice-only contract, got error: %v", err)
	}
	if got := ctrl.WriteAuthorityGeneration(); got != staleGen {
		t.Fatalf("authority generation = %d, want unchanged %d (no mint on a refused heal)", got, staleGen)
	}
	if state, ok := inboxItemState(ctrl, itemID); !ok || state != sessioninbox.StateQueued {
		t.Fatalf("item state after declined heal = %q ok=%v, want queued (stranded, unchanged)", state, ok)
	}
	if !agent.SessionLeaseHeldByCurrentRuntime(sessionRuntimeKey(path)) {
		t.Fatal("holder lease lost during a refused inbox heal")
	}
	if n := rec.countEventually(t, 1, noticeMatching("no longer writable")); n != 1 {
		t.Fatalf("gate refusal notices = %d, want exactly 1", n)
	}
	if n := rec.countEventually(t, 0, noticeMatching("restored automatically")); n != 0 {
		t.Fatalf("auto-restored notices = %d, want 0 on a declined heal", n)
	}
}

// 并发钉（tl1296 验收②·双进程半边）：活持有者在场时并发 N 路 heal 全部
// 拒绝——零 mint（世代原样）、单一持有者保持、无竞态偷锁。
func TestInboxHealConcurrentWithForeignHolderMintsNothing(t *testing.T) {
	app, tab, ctrl, _, path := newStaleRetryFixture(t, "inbox_heal_race_holder")
	holder, err := agent.TryAcquireSessionLease(path)
	if err != nil {
		t.Fatalf("acquire holder lease: %v", err)
	}
	t.Cleanup(holder.Release)
	if err := control.IssueAndBindWriteAuthority(ctrl, holder); err != nil {
		t.Fatalf("bind first generation: %v", err)
	}
	staleSideController(t, config.SessionDir(), path, holder, "inbox_heal_race_holder_side")
	staleGen := ctrl.WriteAuthorityGeneration()

	var wg sync.WaitGroup
	declined := atomic.Int32{}
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !app.healInboxDispatchAfterStaleAuthority(tab.ID, ctrl) {
				declined.Add(1)
			}
		}()
	}
	wg.Wait()
	if got := declined.Load(); got != 8 {
		t.Fatalf("declined heals = %d, want 8 (a live holder must refuse every racer)", got)
	}
	if got := ctrl.WriteAuthorityGeneration(); got != staleGen {
		t.Fatalf("authority generation = %d, want unchanged %d (zero mint under a foreign holder)", got, staleGen)
	}
	if !agent.SessionLeaseHeldByCurrentRuntime(sessionRuntimeKey(path)) {
		t.Fatal("holder lease lost during concurrent refused heals")
	}
}

// 并发钉（tl1296 验收②·空闲锁半边）：崩溃残留（tab 手上是已释放的 lease
// 对象、锁已空闲）+ N 路并发 heal → 恰好一路接管，registry 单一持有者，
// 终态单活 authority 覆盖会话路径——不重复 mint 出第二个持有者。
func TestInboxHealConcurrentTakeoverConvergesToSingleLiveAuthority(t *testing.T) {
	app, tab, ctrl, _, path := newStaleRetryFixture(t, "inbox_heal_race_takeover")
	lease, err := agent.TryAcquireSessionLease(path)
	if err != nil {
		t.Fatalf("acquire lease: %v", err)
	}
	tab.adoptSessionLease(lease)
	if err := control.IssueAndBindWriteAuthority(ctrl, lease); err != nil {
		t.Fatalf("bind first generation: %v", err)
	}
	staleSideController(t, config.SessionDir(), path, lease, "inbox_heal_race_takeover_side")
	staleGen := ctrl.WriteAuthorityGeneration()
	// 崩溃残留形态：锁已释放（zombie），tab 手上只剩死对象。
	lease.Release()

	var wg sync.WaitGroup
	healed := atomic.Int32{}
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if app.healInboxDispatchAfterStaleAuthority(tab.ID, ctrl) {
				healed.Add(1)
			}
		}()
	}
	wg.Wait()
	if got := healed.Load(); got == 0 {
		t.Fatal("every concurrent heal declined on a free lock — takeover never landed")
	}
	key := sessionRuntimeKey(path)
	if !agent.SessionLeaseHeldByCurrentRuntime(key) {
		t.Fatal("no single live lease owner after concurrent takeover")
	}
	if extra, err := agent.TryAcquireSessionLease(path); err == nil {
		extra.Release()
		t.Fatal("a second lease acquire succeeded — concurrent heals minted a duplicate holder")
	}
	tab.sessionLeaseMu.Lock()
	adopted := tab.sessionLease
	tab.sessionLeaseMu.Unlock()
	if adopted == nil || adopted.Released() {
		t.Fatal("tab did not converge on a live lease after concurrent heals")
	}
	if got := ctrl.WriteAuthorityGeneration(); got <= staleGen {
		t.Fatalf("authority generation = %d, want a fresh value above %d", got, staleGen)
	}
	// 终态单活 authority：入场闸门同一判据（WriteAuthorityReady）必须给绿。
	if err := ctrl.WriteAuthorityReady(); err != nil {
		t.Fatalf("final authority verdict after concurrent heals = %v, want ready", err)
	}
	_ = path
}

// 健康路径零变化：authority 正常时重试/恢复不触发 heal——世代原样、零附加
// notice、照常派发。
func TestInboxHealthySurfacesNeverHeal(t *testing.T) {
	app, tab, ctrl, rec, path := newStaleRetryFixture(t, "inbox_healthy")
	lease, err := agent.TryAcquireSessionLease(path)
	if err != nil {
		t.Fatalf("acquire lease: %v", err)
	}
	t.Cleanup(lease.Release)
	tab.adoptSessionLease(lease)
	if err := control.IssueAndBindWriteAuthority(ctrl, lease); err != nil {
		t.Fatalf("bind authority: %v", err)
	}
	freshGen := ctrl.WriteAuthorityGeneration()
	itemID := enqueueUncertain(t, ctrl, "healthy guidance")

	if err := app.RetryInboxItem(tab.ID, itemID); err != nil {
		t.Fatalf("healthy retry: %v", err)
	}
	if got := ctrl.WriteAuthorityGeneration(); got != freshGen {
		t.Fatalf("authority generation = %d, want unchanged %d (no heal on the healthy path)", got, freshGen)
	}
	inboxWaitLeftRetryable(t, ctrl, itemID)
	if n := rec.countEventually(t, 0, noticeMatching("no longer writable")); n != 0 {
		t.Fatalf("refusal notices on healthy path = %d, want 0", n)
	}
	if n := rec.countEventually(t, 0, noticeMatching("restored automatically")); n != 0 {
		t.Fatalf("auto-restored notices on healthy path = %d, want 0", n)
	}
}

// 沉默钉（纠正行的诚实半边）：heal 成功但条目仍留队（暂停的用户队列挡住
// 重驱）→ 不得谎报「正在投递」——纠正行保持沉默，拒绝行是最后一句话。
func TestHealedNoticeStaysSilentWhenItemStaysQueued(t *testing.T) {
	app, tab, ctrl, rec, path := newStaleRetryFixture(t, "inbox_notice_silent")
	lease, err := agent.TryAcquireSessionLease(path)
	if err != nil {
		t.Fatalf("acquire lease: %v", err)
	}
	t.Cleanup(lease.Release)
	// 暂停队列 + 无 tab lease + 活持有者... 组合拆解：本钉只钉纠正行的
	// verify 半边，直接走发射函数——条目留队时必须沉默。
	if err := ctrl.SetInboxPausedPassive(true); err != nil {
		t.Fatalf("pause inbox: %v", err)
	}
	tab.adoptSessionLease(lease)
	if err := control.IssueAndBindWriteAuthority(ctrl, lease); err != nil {
		t.Fatalf("bind authority: %v", err)
	}
	itemID := enqueueUncertain(t, ctrl, "still parked guidance")

	app.emitInboxAuthorityHealedNotice(tab.ID, itemID, ctrl)
	if n := rec.countEventually(t, 0, noticeMatching("restored automatically")); n != 0 {
		t.Fatalf("correction notice emitted while the item is still queued, got %d", n)
	}
}
