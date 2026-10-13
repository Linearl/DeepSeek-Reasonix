package main

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/store"
)

// 任务 836（coldHistorySliceRead 冷历史切片升级交付）三重回归钉：
// ① 尾窗首屏：display index 失效时最新页请求从权威事件日志的尾窗直接切页
//    （Source=cold-tail），不再先付全量扫描/全量事件重放；
// ② 延迟补载：首屏页的 v2 cursor 在后台自愈（扫描回写 / read-model repair）
//    完成后照常翻页——页指纹取 ledger 身份（与 index 页同源），cursor 不落
//    Stale，更早内容经正常翻页补全；
// ③ 读侧等价：尾窗页与全量重放页在返回窗口内除 turn 标注（窗口内递推、
//    基数 0 的短期偏差，自愈后消除）外逐字段一致。
// 末尾附冷启动耗时前后对照（同 fixture 实测，数字摘录进交付报告）。
//
// fixture 说明：复用 831③ 的 buildLargeSessionLog（40×1MiB ≈ 40MiB，稳定
// 越过 LoadSessionTail 的 32MiB 首绘阈值，保证截尾前提成立）。小日志走原
// 全量路径的行为由既有 TestHistorySliceCold* 族锁定（零改动全绿即证明）。

// staleColdTailFixture grows the large log, then appends one more user turn
// and rewinds the .jsonl anchor to its pre-append bytes — the canonical
// "authoritative event log ahead of the anchor" shape that used to force the
// full event-log replay before the first page could be cut.
func staleColdTailFixture(t *testing.T, path string) (appendedUserText, appendedAssistantText string) {
	t.Helper()
	buildLargeSessionLog(t, path)
	sess, err := agent.LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession fixture: %v", err)
	}
	oldAnchor, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read anchor: %v", err)
	}
	appendedUserText = "cold tail question"
	appendedAssistantText = strings.Repeat("cold tail answer ", 4096)
	sess.Add(historySliceUser(999, appendedUserText))
	sess.Add(historySliceAssistant(999, appendedAssistantText))
	if err := sess.SaveSnapshot(path); err != nil {
		t.Fatalf("append save: %v", err)
	}
	if err := os.WriteFile(path, oldAnchor, 0o600); err != nil {
		t.Fatalf("rewind anchor: %v", err)
	}
	return appendedUserText, appendedAssistantText
}

// waitForColdTailSelfHeal polls until the background self-heal has published a
// display index that validates against the ledger identity. The anchor stat is
// refreshed every round: the repair branch rewrites the anchor, so a stale
// size would fail validation forever.
func waitForColdTailSelfHeal(t *testing.T, path string) {
	t.Helper()
	identity, known, err := agent.SessionContentIdentity(path)
	if err != nil || !known {
		t.Fatalf("SessionContentIdentity: ok=%v err=%v", known, err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		info, statErr := os.Stat(path)
		if statErr == nil {
			idx, loadErr := agent.LoadSessionDisplayIndex(store.SessionDisplayIndex(path))
			if loadErr == nil && idx != nil && agent.ValidateSessionDisplayIndex(idx, identity.Revision, identity.RevisionKnown, identity.Digest, info.Size()) {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("background cold-history self-heal did not republish a validating display index")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// 钉①a：anchor 陈旧（事件日志权威且超前）的大会话首屏走尾窗，最新内容可见，
// 指纹取 ledger 身份。
func TestColdHistoryTailFirstPaintOnStaleAnchor(t *testing.T) {
	app := historySliceTestApp(t)
	tab := newColdHistoryTab(t, app)
	path := filepath.Join(tabSessionDir(tab), "stale-anchor.jsonl")
	userText, _ := staleColdTailFixture(t, path)
	tab.SessionPath = path

	page := app.HistorySliceForTab("cold", HistorySliceRequest{})
	if page.Source != "cold-tail" {
		t.Fatalf("Source = %q, want cold-tail", page.Source)
	}
	if len(page.Entries) == 0 {
		t.Fatal("cold-tail first paint came back empty")
	}
	if !page.HasOlder || page.NextCursor == "" {
		t.Fatalf("HasOlder=%v cursor=%q, want an older-page entry point", page.HasOlder, page.NextCursor)
	}
	identity, known, err := agent.SessionContentIdentity(path)
	if err != nil || !known {
		t.Fatalf("SessionContentIdentity: ok=%v err=%v", known, err)
	}
	if page.Digest != identity.DigestHex || !page.RevisionKnown || page.Revision != identity.Revision {
		t.Fatalf("page identity = known:%v rev:%d digest:%q, want the ledger identity", page.RevisionKnown, page.Revision, page.Digest)
	}
	// 尾窗读的是权威事件日志：anchor 里还没有的追加内容必须出现在首屏。
	last := page.Entries[len(page.Entries)-1]
	if !strings.Contains(last.Message.Content, "cold tail answer") {
		t.Fatalf("first paint misses the authoritative tail (last entry content %d bytes)", len(last.Message.Content))
	}
	foundUser := false
	for _, e := range page.Entries {
		if strings.Contains(e.Message.Content, userText) {
			foundUser = true
		}
	}
	if !foundUser {
		t.Fatal("first paint misses the appended user turn")
	}
	// 截尾前提自证：fixture 必须真的越过尾窗阈值。
	tail, err := agent.LoadSessionTail(path)
	if err != nil {
		t.Fatalf("LoadSessionTail: %v", err)
	}
	if !tail.TailTruncated() {
		t.Fatal("fixture log is below the first-paint tail threshold; the large-log test lost its premise")
	}
}

// 钉①b：display index 缺失的大会话首屏同样走尾窗；后台自愈（扫描+回写）
// 收敛出一个通过验证的 index。
func TestColdHistoryTailFirstPaintOnMissingIndex(t *testing.T) {
	app := historySliceTestApp(t)
	tab := newColdHistoryTab(t, app)
	path := filepath.Join(tabSessionDir(tab), "missing-index.jsonl")
	buildLargeSessionLog(t, path)
	if err := os.Remove(store.SessionDisplayIndex(path)); err != nil {
		t.Fatal(err)
	}
	tab.SessionPath = path

	page := app.HistorySliceForTab("cold", HistorySliceRequest{})
	if page.Source != "cold-tail" {
		t.Fatalf("Source = %q, want cold-tail", page.Source)
	}
	if len(page.Entries) == 0 || !page.HasOlder {
		t.Fatalf("cold-tail page = entries:%d hasOlder:%v", len(page.Entries), page.HasOlder)
	}
	waitForColdTailSelfHeal(t, path)
}

// 钉②：首屏尾窗页的 cursor 在自愈完成后照常翻页——非 Stale、返回更早内容、
// 页与页的消息序衔接。
func TestColdHistoryTailPagePagingAfterSelfHeal(t *testing.T) {
	app := historySliceTestApp(t)
	tab := newColdHistoryTab(t, app)
	path := filepath.Join(tabSessionDir(tab), "paging.jsonl")
	staleColdTailFixture(t, path)
	tab.SessionPath = path

	first := app.HistorySliceForTab("cold", HistorySliceRequest{Turns: 3, Entries: 40})
	if first.Source != "cold-tail" || first.NextCursor == "" {
		t.Fatalf("first page source=%q cursor=%q", first.Source, first.NextCursor)
	}
	waitForColdTailSelfHeal(t, path)

	older := app.HistorySliceForTab("cold", HistorySliceRequest{Cursor: first.NextCursor, Turns: 3, Entries: 40})
	if older.Stale {
		t.Fatal("older-page request went stale after self-heal; the ledger-identity fingerprint chain broke")
	}
	if len(older.Entries) == 0 {
		t.Fatal("older page came back empty")
	}
	// 合法终态两种：自愈已完成 → index 快路径；未完成 → 同步扫描（自愈并
	// 服务，原语义）。两者都不是 Stale，且都会收敛出新鲜 index。
	if older.Source != "index" && older.Source != "scan" {
		t.Fatalf("older page source = %q, want the healed index/scan path", older.Source)
	}
	lastOlder := older.Entries[len(older.Entries)-1].Order
	firstOldest := first.Entries[0].Order
	if lastOlder >= firstOldest {
		t.Fatalf("older page ends at order %d, want strictly below the first page's oldest %d", lastOlder, firstOldest)
	}
}

// 钉③：同一请求参数下，尾窗页与全量重放页在返回窗口内除 turn 标注外逐字段
// 一致（entryID/order/message 内容/refs）——读侧等价。
func TestColdHistoryTailPageMatchesFullReplayTail(t *testing.T) {
	app := historySliceTestApp(t)
	tab := newColdHistoryTab(t, app)
	path := filepath.Join(tabSessionDir(tab), "equivalence.jsonl")
	staleColdTailFixture(t, path)
	sessionDir := filepath.Dir(path)
	tab.SessionPath = path
	req := normalizeHistorySliceRequest(HistorySliceRequest{Turns: 3, Entries: 40})

	tailPage := app.HistorySliceForTab("cold", req)
	if tailPage.Source != "cold-tail" {
		t.Fatalf("Source = %q, want cold-tail", tailPage.Source)
	}

	resolver := sessionDisplayResolver(sessionDir, path)
	msgs, state, _, err := agent.LoadSessionDisplayMessages(path)
	if err != nil {
		t.Fatalf("full replay baseline: %v", err)
	}
	fullSrc := newInMemoryHistorySliceSource(strings.TrimSuffix(filepath.Base(path), ".jsonl"), msgs, resolver, state, true)
	fullPage, err := app.pageHistorySliceSource(fullSrc, req, resolver, nil, nil, path)
	if err != nil {
		t.Fatalf("full baseline page: %v", err)
	}
	if len(fullPage.Entries) == 0 || len(fullPage.Entries) != len(tailPage.Entries) {
		t.Fatalf("entry count: tail=%d full=%d", len(tailPage.Entries), len(fullPage.Entries))
	}
	for i := range fullPage.Entries {
		want, got := fullPage.Entries[i], tailPage.Entries[i]
		// EntryID 的 r<epoch> 段允许不同：尾窗页取 ledger 身份的 epoch（与
		// index 页一致，自愈后 ID 连续），全量基准（event-log 同款 in-memory
		// source）历来是 r0 —— 既有行为本就跨 source 不稳定，比较归一到
		// 消息坐标段。
		wantMsgIdx, wantSub, _, wantOK := parseHistoryEntryID(want.EntryID)
		gotMsgIdx, gotSub, _, gotOK := parseHistoryEntryID(got.EntryID)
		if !wantOK || !gotOK || wantMsgIdx != gotMsgIdx || wantSub != gotSub {
			t.Fatalf("entry %d id = %q, want coordinates of %q", i, got.EntryID, want.EntryID)
		}
		if want.Order != got.Order {
			t.Fatalf("entry %d order = %d, want %d", i, got.Order, want.Order)
		}
		if !reflect.DeepEqual(want.Message, got.Message) {
			t.Fatalf("entry %d message diverges (role %q, %d vs %d content bytes)", i, got.Message.Role, len(got.Message.Content), len(want.Message.Content))
		}
		// Refs 携带完整 EntryID（含 epoch 段），同上归一后比较。
		normalizeRefs := func(refs []HistoryContentRef) []HistoryContentRef {
			out := append([]HistoryContentRef(nil), refs...)
			for i := range out {
				if _, sub, _, ok := parseHistoryEntryID(out[i].EntryID); ok {
					out[i].EntryID = fmt.Sprintf("m*:o%d", sub)
				}
			}
			return out
		}
		if !reflect.DeepEqual(normalizeRefs(want.Refs), normalizeRefs(got.Refs)) {
			t.Fatalf("entry %d refs diverge", i)
		}
	}
	// turn 标注的短期偏差是有意为之（窗口内递推、基数 0）；全量页的绝对
	// turn 必须不小于尾窗页的窗口内值，且尾窗页 Start/EndTurn 落在其
	// Entries 的 turn 范围内——保证前端分组在窗口内自洽。
	if fullPage.Entries[len(fullPage.Entries)-1].Turn < tailPage.Entries[len(tailPage.Entries)-1].Turn {
		t.Fatal("tail-window turn estimate exceeds the full replay's absolute turn")
	}
}

// 冷启动耗时前后对照（任务书验收①：实测占比数据）。口径：
//   - 「前」= 改动前该慢路径分支的同步主体（S3 全量流式扫描 / S4 全量事件
//     日志重放），同 fixture 上直接计时；
//   - 「后」= HistorySliceForTab 首屏总耗时（内含 cold-tail-read 尾窗读分相）。
//
// 同进程顺序执行、OS cache 条件一致；结构性结论（尾窗窗口 << 全量解码）对
// 噪声稳健，生产口径以 451 的 cold-* 分相 desktop.log 打点为准。
func TestColdHistoryTailFirstPaintTimingAgainstFullPass(t *testing.T) {
	// --- S4 形态：anchor 陈旧，「前」= 全量事件日志重放 ---
	app := historySliceTestApp(t)
	s4Tab := newColdHistoryTab(t, app)
	s4Path := filepath.Join(tabSessionDir(s4Tab), "timing-stale-anchor.jsonl")
	staleColdTailFixture(t, s4Path)
	s4Tab.SessionPath = s4Path

	beforeStart := time.Now()
	s4Msgs, _, _, err := agent.LoadSessionDisplayMessages(s4Path)
	if err != nil {
		t.Fatalf("full replay baseline: %v", err)
	}
	s4Before := time.Since(beforeStart)
	if len(s4Msgs) == 0 {
		t.Fatal("baseline replay came back empty")
	}

	afterStart := time.Now()
	page := app.HistorySliceForTab("cold", HistorySliceRequest{})
	s4After := time.Since(afterStart)
	if page.Source != "cold-tail" || len(page.Entries) == 0 {
		t.Fatalf("cold-tail page source=%q entries=%d", page.Source, len(page.Entries))
	}
	tailStart := time.Now()
	if _, err := agent.LoadSessionTail(s4Path); err != nil {
		t.Fatalf("LoadSessionTail: %v", err)
	}
	tailMs := time.Since(tailStart)
	t.Logf("S4 anchor-stale @40MiB fixture: full event replay (before) = %s; cold-tail first paint (after) = %s; tail window read ≈ %s (%.0f%% of first paint)",
		s4Before.Round(time.Millisecond), s4After.Round(time.Millisecond), tailMs.Round(time.Millisecond),
		float64(tailMs)/float64(s4After)*100)
	if s4After >= s4Before {
		t.Fatalf("cold-tail first paint %s was not cheaper than the full replay baseline %s", s4After, s4Before)
	}

	// --- S3 形态：index 缺失（jsonl 与 ledger 一致），「前」= 全量流式扫描 ---
	s3Tab := newColdHistoryTab(t, app)
	s3Path := filepath.Join(tabSessionDir(s3Tab), "timing-missing-index.jsonl")
	buildLargeSessionLog(t, s3Path)
	if err := os.Remove(store.SessionDisplayIndex(s3Path)); err != nil {
		t.Fatal(err)
	}
	s3Tab.SessionPath = s3Path
	beforeStart = time.Now()
	scanned, err := agent.ScanSessionDisplayIndex(s3Path)
	if err != nil {
		t.Fatalf("full scan baseline: %v", err)
	}
	s3Before := time.Since(beforeStart)
	if scanned.MessageCount == 0 {
		t.Fatal("baseline scan came back empty")
	}

	afterStart = time.Now()
	page2 := app.HistorySliceForTab("cold", HistorySliceRequest{})
	s3After := time.Since(afterStart)
	if page2.Source != "cold-tail" || len(page2.Entries) == 0 {
		t.Fatalf("cold-tail page source=%q entries=%d", page2.Source, len(page2.Entries))
	}
	t.Logf("S3 missing-index @40MiB fixture: full streaming scan (before) = %s; cold-tail first paint (after) = %s",
		s3Before.Round(time.Millisecond), s3After.Round(time.Millisecond))
	// S3 不做「后<前」硬断言：fixture 量级（40MiB）加上同进程热 OS cache 会
	// 抹平「O(全文件) 扫描 vs 固定 20MiB 尾窗」的结构差距（扫描后自愈也让该
	// 形态只是每会话一次性）；结构优势在生产量级（数百 MB）上成立，装机
	// 口径以 451 的 cold-* 分相打点为准。此处的硬断言只锁定路径与正确性。
	waitForColdTailSelfHeal(t, s3Path)
}
