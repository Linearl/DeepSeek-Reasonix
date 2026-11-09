package agent

// tl1485d 回归钉：活租约（打开中的会话）behind 冻结戳的自愈缺口。
//
// tl1485c 给 behind 存量补了双向探针 + replay settle，但 replay 的入口在活
// 租约行上一律 Busy 让位（tl1459 反事故语义，2026-11-06 早停事故的护栏）。
// 这个组合有一个永远到不了 settle 的人群：**打开着的会话**。冻结戳的成因
// （collab/detached/跨会话追加不走保存漏斗、UpdatedAt 不动）同时意味着该会
// 话不再有自己的「下一次保存」——tl1459 的让位理由（“its own saves heal it”）
// 对它不成立，行就在租约被持有的整个生命周期里停在「1 个月」。四轮打回现场
// （2026-11-09）指名的正是这个形态：活跃会话左栏时间永久冻结。
//
// 修 = 探针门控的戳抬升（见 session_listing_repair.go 的 tl1485d 注释）：
// behind 且无 ahead 证据时按保存漏斗同款 raise-only 语义把 UpdatedAt 抬到尾
// 部消息级，零 ledger 触碰（写权威/revision/digest 不动，tl1459 事故面不变）；
// counts fresh 时按 AlreadyCurrent 同一套认证标准返回 Applied（catalog 照常
// complete→RequestReconcile 收敛行投影）；counts 不 fresh 时只抬戳仍 Busy
// （计数归 replay 管，不借戳修复认证计数）；ahead / 无证据维持 Busy。
//
// 五枚钉：
//  1. 活租约 + behind 冻结戳 + counts fresh → Applied：戳抬到消息级、ledger
//     逐字节不动、释放后重修 AlreadyCurrent（收敛）。修前红：Busy 且戳冻结。
//  2. 活租约 + 戳已咬合 → Busy（tl1459 廉价 park 语义不变，恒绿）。
//  3. 活租约 + ahead 抬高戳 → Busy 且戳不动（活会话不做无解码降戳，恒绿）。
//  4. 活租约 + behind + counts 不 fresh（投影失效形态）→ 戳仍抬升但返回
//     Busy。修前红：戳冻结。
//  5. 无证据（转录无消息级时间戳）→ Busy 且一字不动（缺证据不乱写，恒绿）。
//
// 修前红口径：钉 1/4 在基线（d98b9ef0a，tl1485c 已含）上直接跑 = 行为红
// （返回 Busy、UpdatedAt 冻结），不依赖新符号编译错。

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// liveLeaseBehindStampTL1485d 组装现场形态：counts/ledger/投影全 fresh（一次
// 真实 save），UpdatedAt 被存量冻结在消息级之下 30 天，租约被活运行时持有。
func liveLeaseBehindStampTL1485d(t *testing.T, path string, msgAt time.Time) (func(), func() time.Time) {
	t.Helper()
	seedFreshProjectionTL1485c(t, path, msgAt)
	frozen := msgAt.Add(-30 * 24 * time.Hour)
	freezeStampTL1485c(t, path, frozen)
	lease, err := TryAcquireSessionLease(path)
	if err != nil {
		t.Fatalf("TryAcquireSessionLease: %v", err)
	}
	stamp := func() time.Time {
		meta, ok, err := LoadBranchMeta(path)
		if err != nil || !ok {
			t.Fatalf("load meta: ok=%v err=%v", ok, err)
		}
		return meta.UpdatedAt
	}
	return lease.Release, stamp
}

// TestListingRepairSettlesBehindStampForLiveWriterTL1485d 是四轮打回的机制钉：
// 打开中的会话（活租约）带 behind 冻结戳时，repair 必须把戳抬到消息级并返回
// applied——不允许 Busy 让位把「1 个月」钉死在打开的会话上。ledger 必须逐字
// 节不动（tl1459 事故面），且释放后重修收敛为 already_current。
func TestListingRepairSettlesBehindStampForLiveWriterTL1485d(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "live-behind.jsonl")
	msgAt := time.Now().Add(-48 * time.Hour)
	release, stamp := liveLeaseBehindStampTL1485d(t, path, msgAt)
	defer release()

	before := ledgerOf(t, path)
	result, err := RepairSessionListingProjection(context.Background(), path)
	if err != nil {
		t.Fatalf("repair on a live writer's behind-stamp session = %v, want a stamp settle (applied)", err)
	}
	if result.Status != SessionListingRepairApplied {
		t.Fatalf("repair status = %v, want applied — an open session must not sit on a frozen stamp for its whole lease", result)
	}
	want := msgAt.Add(time.Minute)
	if got := stamp(); got.UnixMilli() < want.Add(-2*time.Hour).UnixMilli() || got.UnixMilli() > want.Add(2*time.Hour).UnixMilli() {
		t.Fatalf("UpdatedAt = %v, want the transcript's last-message time ~%v; the frozen stamp survived the repair", got, want)
	}
	if after := ledgerOf(t, path); after != before {
		t.Fatalf("stamp settle moved the live writer's ledger: before %+v after %+v", before, after)
	}

	// 收敛：结算之后探针无失配，释放租约后重修 = already_current（不再解码）。
	release()
	result, err = RepairSessionListingProjection(context.Background(), path)
	if err != nil {
		t.Fatalf("repair after release: %v", err)
	}
	if result.Status != SessionListingRepairAlreadyCurrent {
		t.Fatalf("post-settle repair status = %v, want already_current (settled rows must not re-decode)", result)
	}
}

// TestListingRepairLiveWriterCleanStampStaysBusyTL1485d 守 tl1459 的廉价 park：
// 戳与转录咬合的活会话没有可修的东西，必须维持 Busy（不解码、不写边车）。
func TestListingRepairLiveWriterCleanStampStaysBusyTL1485d(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "live-clean.jsonl")
	msgAt := time.Now().Add(-48 * time.Hour)
	seedFreshProjectionTL1485c(t, path, msgAt)
	lease, err := TryAcquireSessionLease(path)
	if err != nil {
		t.Fatalf("TryAcquireSessionLease: %v", err)
	}
	defer lease.Release()

	result, err := RepairSessionListingProjection(context.Background(), path)
	if !errors.Is(err, ErrSessionListingRepairBusy) {
		t.Fatalf("repair on a clean live session = %+v err=%v, want ErrSessionListingRepairBusy", result, err)
	}
	if got := stampOfTL1485d(t, path); got.UnixMilli() < msgAt.Add(time.Minute).Add(-time.Minute).UnixMilli() {
		t.Fatalf("UpdatedAt = %v moved off the aligned stamp", got)
	}
}

// TestListingRepairLiveWriterAheadStampStaysBusyTL1485d：活会话不做无解码降戳
// （ahead 形态交给释放后的 replay 全解码定案），戳保持不动。
func TestListingRepairLiveWriterAheadStampStaysBusyTL1485d(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "live-ahead.jsonl")
	msgAt := time.Now().Add(-48 * time.Hour)
	seedFreshProjectionTL1485c(t, path, msgAt)
	inflated := time.Now()
	freezeStampTL1485c(t, path, inflated)
	lease, err := TryAcquireSessionLease(path)
	if err != nil {
		t.Fatalf("TryAcquireSessionLease: %v", err)
	}
	defer lease.Release()

	result, err := RepairSessionListingProjection(context.Background(), path)
	if !errors.Is(err, ErrSessionListingRepairBusy) {
		t.Fatalf("repair on a live ahead-stamp session = %+v err=%v, want ErrSessionListingRepairBusy", result, err)
	}
	if got := stampOfTL1485d(t, path); got.UnixMilli() < inflated.Add(-time.Minute).UnixMilli() {
		t.Fatalf("UpdatedAt = %v, want the inflated stamp untouched for a live session", got)
	}
}

// TestListingRepairLiveWriterBehindWithStaleCountsTL1485d：投影失效（counts 不
// fresh）的活会话只抬戳、不认证——计数仍归 replay 全解码管，返回 Busy。
func TestListingRepairLiveWriterBehindWithStaleCountsTL1485d(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "live-behind-stale-counts.jsonl")
	msgAt := time.Now().Add(-48 * time.Hour)
	release, _ := liveLeaseBehindStampTL1485d(t, path, msgAt)
	defer release()
	// 使投影失效（invalidateSessionListingProjection 同款形态：revision 前进 +
	// schema 版本清零），counts 不再 fresh。
	if err := UpdateBranchMeta(path, false, func(meta *BranchMeta) error {
		meta.Revision++
		meta.SchemaVersion = 0
		return nil
	}); err != nil {
		t.Fatalf("invalidate projection: %v", err)
	}
	frozen := msgAt.Add(-30 * 24 * time.Hour)

	result, err := RepairSessionListingProjection(context.Background(), path)
	if !errors.Is(err, ErrSessionListingRepairBusy) {
		t.Fatalf("repair with stale counts = %+v err=%v, want ErrSessionListingRepairBusy (counts belong to the replay)", result, err)
	}
	want := msgAt.Add(time.Minute)
	if got := stampOfTL1485d(t, path); got.UnixMilli() < want.Add(-2*time.Hour).UnixMilli() {
		t.Fatalf("UpdatedAt = %v stayed frozen at %v; the stamp settle must run even when counts stay with the replay", got, frozen)
	}
}

// TestListingRepairLiveWriterMissingEvidenceUntouchedTL1485d：转录没有消息级
// 时间戳（legacy 全零）时缺证据不乱写——戳原样、Busy。
func TestListingRepairLiveWriterMissingEvidenceUntouchedTL1485d(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "live-legacy.jsonl")
	seedFreshProjectionTL1485c(t, path, time.Now().Add(-48*time.Hour))
	legacy := stampOfTL1485d(t, path)
	lease, err := TryAcquireSessionLease(path)
	if err != nil {
		t.Fatalf("TryAcquireSessionLease: %v", err)
	}
	defer lease.Release()
	// 把转录换成无 createdAt 的 legacy 行（边车投影字段不动，模拟全零存量）。
	if err := os.WriteFile(path, []byte("{\"role\":\"user\",\"content\":\"legacy\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := RepairSessionListingProjection(context.Background(), path)
	if !errors.Is(err, ErrSessionListingRepairBusy) {
		t.Fatalf("repair without timestamp evidence = %+v err=%v, want ErrSessionListingRepairBusy", result, err)
	}
	if got := stampOfTL1485d(t, path); got.UnixMilli() != legacy.UnixMilli() {
		t.Fatalf("UpdatedAt = %v, want the pre-repair value %v untouched on missing evidence", got, legacy)
	}
}

func stampOfTL1485d(t *testing.T, path string) time.Time {
	t.Helper()
	meta, ok, err := LoadBranchMeta(path)
	if err != nil || !ok {
		t.Fatalf("load meta: ok=%v err=%v", ok, err)
	}
	return meta.UpdatedAt
}
