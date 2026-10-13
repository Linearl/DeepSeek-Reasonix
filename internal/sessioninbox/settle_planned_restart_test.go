package sessioninbox

import (
	"path/filepath"
	"testing"
)

// 任务840：计划内切换重启的已读确认结算。
//
// 现场复现（2026-10-13 用户实测，24.1 无人值守更新重启）：更新前已消费的
// 引导消息在重启后被重新标记「待处理」并带重试按钮。根因 = 切换重启在消费
// 链中途杀进程，Recover 把孤儿行改写为 Uncertain（RestartOrphan /
// SteerUnapplied），而既有两条结算路都够不到它们：
//   - P15（SettleAppliedResidue）只认 transcript 的 steer 回执；
//   - Recover 的 settled drop 对两类 review park 有意豁免（585/641/589b）。
//
// SettlePlannedRestartResidue 补上的第三条路：调用点（名册消费点）提供
// 「计划内重启」证据，delivered 回调提供 collab 已读确认（投递即写的
// delivery receipt / recipient cursor）。本文件钉结算矩阵。
func seedMeta(t *testing.T, s *Store, text, idem, source string, state InboxState, reason string) InboxItemMeta {
	t.Helper()
	rec, err := s.Enqueue(EnqueueRequest{
		Envelope:    PromptEnvelope{SubmitText: text},
		Idempotency: idem,
		Source:      source,
	})
	if err != nil {
		t.Fatal(err)
	}
	if state != StateQueued || reason != "" {
		if err := s.SetState(rec.ItemID, state, reason); err != nil {
			t.Fatal(err)
		}
	}
	for _, it := range s.Snapshot().Items {
		if it.ID == rec.ItemID {
			return it
		}
	}
	t.Fatalf("seeded item %s missing from snapshot", rec.ItemID)
	return InboxItemMeta{}
}

func deliveredBySource(sources map[string]bool) func(InboxItemMeta) bool {
	return func(meta InboxItemMeta) bool { return sources[meta.Source] }
}

func TestSettlePlannedRestartResidueDropsAdmittedParksWithDeliveryReceipt(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "s.jsonl"), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// 两类 admission-proven park + collab 已读确认 → 结算。
	orphan := seedMeta(t, s, "guardian dispatch notice", "k-orphan", "collab:sc_g", StateUncertain, BlockReasonRestartOrphan)
	unapplied := seedMeta(t, s, "downgrade receipt", "k-unapplied", "collab:sc_g", StateUncertain, BlockReasonSteerUnapplied)

	delivered := deliveredBySource(map[string]bool{"collab:sc_g": true})
	n, err := s.SettlePlannedRestartResidue(delivered)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("settled = %d, want 2 (orphan + unapplied)", n)
	}
	for _, it := range s.Snapshot().Items {
		if it.ID == orphan.ID || it.ID == unapplied.ID {
			t.Fatalf("row %s (%s) must settle off the shelf", it.ID, it.BlockReason)
		}
	}
}

func TestSettlePlannedRestartResidueKeepsRowsWithoutEvidence(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "s.jsonl"), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// 无已读确认（receipt 未 settled）→ 保留（宁可人审，不误删）。
	noReceipt := seedMeta(t, s, "guidance without delivery evidence", "k-norcpt", "collab:sc_none", StateUncertain, BlockReasonRestartOrphan)
	// desktop 本地引导（无 collab 邮箱证据链）→ 保留，仍归 P15/人审。
	desktopRow := seedMeta(t, s, "local desktop guidance", "k-local", "desktop", StateUncertain, BlockReasonRestartOrphan)
	// transcript 未落盘 park → 保留（585 keep half：应用回执本身丢了）。
	notDurable := seedMeta(t, s, "guidance lost with the transcript", "k-notdurable", "collab:sc_x", StateUncertain, BlockReasonTranscriptNotDurable)
	// 真待处理工作 → 保留。
	queued := seedMeta(t, s, "genuinely pending work", "k-queued", "collab:sc_x", StateQueued, "")
	blocked := seedMeta(t, s, "user-decided blocked work", "k-blocked", "collab:sc_x", StateBlocked, "refs broken")

	n, err := s.SettlePlannedRestartResidue(deliveredBySource(map[string]bool{"collab:sc_x": true}))
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("settled = %d, want 0 (no row has all three evidence legs)", n)
	}
	states := map[string]InboxState{}
	for _, it := range s.Snapshot().Items {
		states[it.ID] = it.State
	}
	for _, row := range []InboxItemMeta{noReceipt, desktopRow, notDurable, queued, blocked} {
		if _, ok := states[row.ID]; !ok {
			t.Fatalf("row %s (%s) must survive the planned-restart settle", row.ID, row.BlockReason)
		}
	}
}

func TestSettlePlannedRestartResidueIsMixed(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "s.jsonl"), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// 同 park 面一半有 receipt 一半没有：只清有证据的一半。
	with := seedMeta(t, s, "consumed guidance A", "k-with", "collab:sc_a", StateUncertain, BlockReasonRestartOrphan)
	without := seedMeta(t, s, "consumed guidance B", "k-without", "collab:sc_b", StateUncertain, BlockReasonRestartOrphan)

	n, err := s.SettlePlannedRestartResidue(deliveredBySource(map[string]bool{"collab:sc_a": true}))
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("settled = %d, want 1", n)
	}
	for _, it := range s.Snapshot().Items {
		if it.ID == with.ID {
			t.Fatal("row with the delivery receipt must settle")
		}
		if it.ID == without.ID {
			// 保留面完整：正文与状态都在。
			if it.State != StateUncertain || it.BlockReason != BlockReasonRestartOrphan {
				t.Fatalf("kept row mutated: %+v", it)
			}
		}
	}
}

func TestSettlePlannedRestartResidueCleansIdempotencyAndReceipts(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "s.jsonl"), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	seedMeta(t, s, "consumed guidance", "k-settled", "collab:sc_g", StateUncertain, BlockReasonSteerUnapplied)
	if n, err := s.SettlePlannedRestartResidue(deliveredBySource(map[string]bool{"collab:sc_g": true})); err != nil || n != 1 {
		t.Fatalf("settled = %d, err = %v; want 1, nil", n, err)
	}
	// 幂等键随 drop 清理：同键重投得到新行（at-least-once 兜底不失效）。
	again, err := s.Enqueue(EnqueueRequest{
		Envelope:    PromptEnvelope{SubmitText: "consumed guidance"},
		Idempotency: "k-settled",
		Source:      "collab:sc_g",
	})
	if err != nil {
		t.Fatal(err)
	}
	if again.Idempotent {
		t.Fatal("re-delivery must not dedupe onto the settled row")
	}
}

func TestSettlePlannedRestartResidueGuards(t *testing.T) {
	var nilStore *Store
	if n, err := nilStore.SettlePlannedRestartResidue(func(InboxItemMeta) bool { return true }); n != 0 || err != ErrClosed {
		t.Fatalf("nil store = (%d, %v), want (0, ErrClosed)", n, err)
	}
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "s.jsonl"), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// nil 回调 = 无证据源 = 不动任何行。
	seedMeta(t, s, "guidance", "k-guard", "collab:sc_g", StateUncertain, BlockReasonRestartOrphan)
	if n, err := s.SettlePlannedRestartResidue(nil); n != 0 || err != nil {
		t.Fatalf("nil callback = (%d, %v), want (0, nil)", n, err)
	}
	if got := len(s.Snapshot().Items); got != 1 {
		t.Fatalf("items = %d, want 1 (nil callback keeps every row)", got)
	}
}
