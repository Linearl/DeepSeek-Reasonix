package agent

// tl1843 回归钉：CAS 基线装配 fail-closed（「账本页空白的人不许动笔」）。
// d4 调研（tl1831d4 报告 A1 节 + desktop.log.4:5852 base_revision=0 现场）实锤：
// 持有写路径的会话对象若从未装配 persist 基线（persistState ok=false），其保存
// 全程绕过 revision CAS（save.go CAS 武装条件 baseState.ok && revisionKnown），
// 装配失败被静默降级成「无锁裸奔」。修复契约：
//  1. 对已存在账本文件的首次保存前必须完成基线装配（heal 到 observed disk
//     state），之后才放行写路径——写路径不再存在 ok=false 形态；
//  2. 装配事件打结构化归因 WARN（d4 遗留：基线为何从未装配入口不明，先补观测）；
//  3. healed 基线不构成所有权证明（磁盘内容不是本对象 load 派生的，祖先关系
//     未验证）——不得喂给 ownsPersistedState / 冲突 identity 的 BaseDigest；
//  4. 可用性：heal 本身不可失败（磁盘+账本刚读成功才走到这），装配失败的既有
//     报错路径（sidecar 不可读）不变；append 形态首写照常落地，diverged 形态
//     走既有冲突阶梯（takeover 恢复路径完整保留）。
// 测试构造完全按 d4 机制链：同进程 peer 写者推进账本 + 无基线 live 对象保存。

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/provider"
)

func captureTl1843Logs(t *testing.T) *strings.Builder {
	t.Helper()
	var buf strings.Builder
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// seedPeerSession 用一个独立对象把磁盘会话写成「peer 已推进」形态（d4 链：
// peer 经 snapshotActivityIfChanged 落账，live 对象没有它的基线）。
func seedPeerSession(t *testing.T, path string, contents ...string) *Session {
	t.Helper()
	peer := NewSession("sys")
	for _, m := range peerMsgs(contents...) {
		peer.Add(m)
	}
	if err := peer.SaveSnapshot(path); err != nil {
		t.Fatalf("peer seed save: %v", err)
	}
	return peer
}

func roleForPeerContent(c string) provider.Role {
	if strings.HasPrefix(c, "u:") {
		return provider.RoleUser
	}
	return provider.RoleAssistant
}

func peerMsgs(contents ...string) []provider.Message {
	out := make([]provider.Message, 0, len(contents))
	for _, c := range contents {
		out = append(out, provider.Message{Role: roleForPeerContent(c), Content: strings.TrimPrefix(c, "u:")})
	}
	return out
}

// baselinelessLive 构造「从未 load/save 过该路径」的 live 对象（ok=false 现场）。
func baselinelessLive(t *testing.T, path string, contents ...string) *Session {
	t.Helper()
	live := NewSession("sys")
	for _, c := range contents {
		live.Add(provider.Message{Role: roleForPeerContent(c), Content: strings.TrimPrefix(c, "u:")})
	}
	bindSessionWriter(t, live, path)
	if live.persistState(path).ok {
		t.Fatal("precondition: live object must hold no assembled baseline for the path")
	}
	return live
}

// TestTl1843BaselinelessAppendSaveHealsBeforeWrite 钉 append 形态（d4 链里静默
// 落地的 disarm 写家族）：首写照常落地（可用性），但写前基线必须已装配（结构上
// 不再有 ok=false 写入），且装配事件带归因锚；落账后 CAS 武装（后续 conflict
// 带 non-zero BaseRevision）。
func TestTl1843BaselinelessAppendSaveHealsBeforeWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "append.jsonl")
	seedPeerSession(t, path, "u:hello", "hi")
	diskRev, _, err := sessionContentRevision(path)
	if err != nil || diskRev != 1 {
		t.Fatalf("precondition disk revision = %d err=%v, want 1", diskRev, err)
	}
	logs := captureTl1843Logs(t)
	live := baselinelessLive(t, path, "u:hello", "hi", "u:tail1", "tail2")

	if err := live.SaveSnapshot(path); err != nil {
		t.Fatalf("append-shaped first save must land (availability): %v", err)
	}
	if got := strings.Count(logs.String(), "no assembled persist baseline"); got != 1 {
		t.Fatalf("heal WARN count = %d, want 1; log=%s", got, logs.String())
	}
	for _, anchor := range []string{
		"disk_revision=1",
		"disk_messages=3", // sys + hello + hi
		"live_messages=5", // sys + hello + hi + tail1 + tail2
		"ledger_writer_origin=self_process",
	} {
		if !strings.Contains(logs.String(), anchor) {
			t.Fatalf("heal WARN missing attribution anchor %q; log=%s", anchor, logs.String())
		}
	}
	st := live.persistState(path)
	if !st.ok || !st.revisionKnown || !st.saveVerified || st.revision != 2 {
		t.Fatalf("baseline after landed save = ok=%v known=%v verified=%v rev=%d, want assembled save-verified rev 2", st.ok, st.revisionKnown, st.saveVerified, st.revision)
	}
	if st.healedFromDisk {
		t.Fatal("a landed save must replace the healed baseline with a verified one")
	}
	loaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(loaded.Snapshot()) != 5 || loaded.Snapshot()[4].Content != "tail2" {
		t.Fatalf("disk transcript = %d messages, want the appended live content", len(loaded.Snapshot()))
	}

	// CAS 武装证明：peer 在带外再推进一笔，live 从旧基线分叉 → conflict 的
	// BaseRevision 必须是真实基线 revision 2（基线缺失形态下恒为 0）。
	peer := func() *Session {
		loaded, err := LoadSession(path)
		if err != nil {
			t.Fatalf("peer reload: %v", err)
		}
		return loaded
	}()
	peer.Add(provider.Message{Role: provider.RoleUser, Content: "peer-advance"})
	if err := peer.SaveSnapshot(path); err != nil {
		t.Fatalf("peer advance save: %v", err)
	}
	live.Add(provider.Message{Role: provider.RoleUser, Content: "live-diverge"})
	err = live.SaveSnapshot(path)
	var conflict *SessionSnapshotConflictError
	if !errors.As(err, &conflict) || conflict == nil || conflict.Kind != SessionSnapshotConflictDiverged {
		t.Fatalf("save after peer advance err = %v, want diverged conflict", err)
	}
	if conflict.BaseRevision != 2 {
		t.Fatalf("conflict BaseRevision = %d, want 2 (CAS armed by the healed baseline)", conflict.BaseRevision)
	}
	if got := strings.Count(logs.String(), "no assembled persist baseline"); got != 1 {
		t.Fatalf("heal WARN must not repeat once the baseline exists, count = %d", got)
	}
}

// TestTl1843BaselinelessDivergedSaveConflictsWithRecoveryPath 重放 d4 机制链
// （rev_lag 形态）：无基线 live 对象对 peer 已推进的盘分叉保存 → 必须冲突拒绝
// （不许盲写），但基线装配已在拒绝前完成（fail-closed 不等于失忆）；恢复路径
// SaveAdoptOverStaleDisk 六前提完整保留，接管后基线转正。
func TestTl1843BaselinelessDivergedSaveConflictsWithRecoveryPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "revlag.jsonl")
	seedPeerSession(t, path, "u:a1", "a2", "a3")
	diskRev, _, err := sessionContentRevision(path)
	if err != nil || diskRev != 1 {
		t.Fatalf("precondition disk revision = %d err=%v, want 1", diskRev, err)
	}
	seedDisk, err := LoadSession(path)
	if err != nil {
		t.Fatalf("load seed disk: %v", err)
	}
	diskDigest, err := digestSessionMessages(seedDisk.Snapshot())
	if err != nil {
		t.Fatalf("digest disk: %v", err)
	}
	logs := captureTl1843Logs(t)
	live := baselinelessLive(t, path, "u:b1", "b2", "b3", "b4")

	err = live.SaveSnapshot(path)
	var conflict *SessionSnapshotConflictError
	if !errors.As(err, &conflict) || conflict == nil || conflict.Kind != SessionSnapshotConflictDiverged {
		t.Fatalf("diverged first save err = %v, want diverged conflict (no blind rewrite)", err)
	}
	if conflict.BaseRevision != 0 || conflict.DiskRevision != diskRev {
		t.Fatalf("conflict revisions = (base %d, disk %d), want (0 unknown heal, %d)", conflict.BaseRevision, conflict.DiskRevision, diskRev)
	}
	if conflict.Identity.BaseDigest != "" {
		t.Fatalf("healed baseline must not pose as a verified conflict base, BaseDigest=%q", conflict.Identity.BaseDigest)
	}
	if conflict.Identity.DiskDigest != digestString(diskDigest) {
		t.Fatalf("conflict DiskDigest = %q, want the on-disk transcript digest", conflict.Identity.DiskDigest)
	}
	// fail-closed 不失忆：被拒的那一刻基线必须已在（heal 先于分类完成）。
	st := live.persistState(path)
	if !st.ok || st.revisionKnown || !st.healedFromDisk {
		t.Fatalf("baseline after refused save = ok=%v known=%v healed=%v, want healed revisionUnknown", st.ok, st.revisionKnown, st.healedFromDisk)
	}
	// 导出视图（control/desktop 消费侧）必须同样如实标注 healed 形态。
	if exported, known := live.PersistedState(path); !known || !exported.BaselineHealedFromDisk || exported.RevisionKnown {
		t.Fatalf("exported PersistedState = known=%v healed=%v revKnown=%v, want healed + revisionUnknown", known, exported.BaselineHealedFromDisk, exported.RevisionKnown)
	}
	if got := strings.Count(logs.String(), "no assembled persist baseline"); got != 1 {
		t.Fatalf("heal WARN count = %d, want exactly 1 (one episode, no storm); log=%s", got, logs.String())
	}
	// 盘未被盲写：peer 的 3 条仍在。
	onDisk, err := LoadSession(path)
	if err != nil {
		t.Fatalf("reload after refusal: %v", err)
	}
	if len(onDisk.Snapshot()) != 4 || onDisk.Snapshot()[3].Content != "a3" {
		t.Fatalf("disk transcript changed on a refused save: %d messages", len(onDisk.Snapshot()))
	}

	// 恢复路径（d4 链的真实出口）：takeover 六前提 + 指纹复验照常放行。
	if err := live.SaveAdoptOverStaleDisk(path, conflict); err != nil {
		t.Fatalf("SaveAdoptOverStaleDisk (recovery path) must stay available: %v", err)
	}
	finalRev, ledgerDigest, err := sessionContentRevision(path)
	if err != nil || finalRev != diskRev+1 {
		t.Fatalf("ledger after takeover = %d err=%v, want %d", finalRev, err, diskRev+1)
	}
	liveDigest, err := digestSessionMessages(live.Snapshot())
	if err != nil {
		t.Fatalf("digest live: %v", err)
	}
	if ledgerDigest != digestString(liveDigest) {
		t.Fatal("ledger digest after takeover does not describe the live transcript")
	}
	st = live.persistState(path)
	if !st.ok || !st.revisionKnown || !st.saveVerified || st.healedFromDisk {
		t.Fatalf("baseline after takeover = ok=%v known=%v verified=%v healed=%v, want fully verified", st.ok, st.revisionKnown, st.saveVerified, st.healedFromDisk)
	}
	if got := strings.Count(logs.String(), "no assembled persist baseline"); got != 1 {
		t.Fatalf("takeover must not re-heal, WARN count = %d", got)
	}
}

// TestTl1843HealedBaselineGrantsNoOwnership 钉所有权闸（heal 形态的核心安全属性）：
// healed 基线的 digest 恰好等于磁盘内容，若无卫语句，ownsPersistedState 的
// unknown-revision 分支会凭 digest 相等把「从未 load 过的磁盘内容」当成本对象
// 的持久化历史 → allowOwnedRewrite 变成盲重写。卫语句必须在。
func TestTl1843HealedBaselineGrantsNoOwnership(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "own.jsonl")
	seedPeerSession(t, path, "u:a1", "a2")
	live := baselinelessLive(t, path, "u:b1", "b2", "b3")
	if err := live.SaveSnapshot(path); err == nil {
		t.Fatal("precondition: diverged save must be refused")
	}
	st := live.persistState(path)
	if !st.ok || !st.healedFromDisk {
		t.Fatalf("precondition healed state: ok=%v healed=%v", st.ok, st.healedFromDisk)
	}
	ledger, err := sessionContentLedger(path)
	if err != nil {
		t.Fatalf("sessionContentLedger: %v", err)
	}
	disk, err := LoadSession(path)
	if err != nil {
		t.Fatalf("reload disk: %v", err)
	}
	existingDigest, err := digestSessionMessages(disk.Snapshot())
	if err != nil {
		t.Fatalf("digest disk: %v", err)
	}
	if live.ownsPersistedState(path, existingDigest, ledger.revision, ledger.digest, live.version) {
		t.Fatal("healed baseline must not act as ownership proof: the live transcript never descended from the disk content")
	}
	decision, err := live.checkSnapshotWrite(path, live.Snapshot(), snapshotDigest(t, live), live.version, true)
	if err == nil {
		t.Fatalf("allowOwnedRewrite on a healed baseline must not produce an owned rewrite, got decision %+v", decision)
	}
	var conflict *SessionSnapshotConflictError
	if !errors.As(err, &conflict) || conflict.Kind != SessionSnapshotConflictDiverged {
		t.Fatalf("allowOwnedRewrite on healed baseline err = %v, want diverged conflict", err)
	}
}

// TestTl1843FreshCreateSaveNotTaxed 钉可用性边界：全新会话对不存在的路径首存
// 是合法的「建账」写，门不得征收（无 heal WARN），基线由落账本身装配。
func TestTl1843FreshCreateSaveNotTaxed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fresh.jsonl")
	logs := captureTl1843Logs(t)
	live := NewSession("sys")
	live.Add(provider.Message{Role: provider.RoleUser, Content: "first"})
	bindSessionWriter(t, live, path)
	if err := live.SaveSnapshot(path); err != nil {
		t.Fatalf("fresh create save: %v", err)
	}
	if got := strings.Count(logs.String(), "no assembled persist baseline"); got != 0 {
		t.Fatalf("fresh create must not trip the baseline gate, WARN count = %d", got)
	}
	st := live.persistState(path)
	if !st.ok || !st.revisionKnown || !st.saveVerified {
		t.Fatalf("baseline after fresh create = ok=%v known=%v verified=%v, want assembled by the save itself", st.ok, st.revisionKnown, st.saveVerified)
	}
}

// TestTl1843LoadWithUnreadableSidecarWarnsAttribution 钉 d4 遗留观测点的另一半：
// load 时 sidecar 不可读 → markPersistedRevisionUnknown 降级装配。这是「基线为何
// 不完整」的已知入口之一，修复前静默，修复后带归因 WARN（tl1843 要求②）。
func TestTl1843LoadWithUnreadableSidecarWarnsAttribution(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tornmeta.jsonl")
	seedPeerSession(t, path, "u:hello", "hi")
	metaPath := BranchMetaPath(path)
	if err := os.WriteFile(metaPath, []byte("{\"corrupt"), 0o644); err != nil {
		t.Fatalf("corrupt branch meta: %v", err)
	}
	logs := captureTl1843Logs(t)
	loaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("load with torn sidecar must still open: %v", err)
	}
	st := loaded.persistState(path)
	if !st.ok || st.revisionKnown {
		t.Fatalf("torn-sidecar baseline = ok=%v known=%v, want revisionUnknown degradation", st.ok, st.revisionKnown)
	}
	if got := strings.Count(logs.String(), "without a ledger revision"); got != 1 {
		t.Fatalf("torn-sidecar attribution WARN count = %d, want 1; log=%s", got, logs.String())
	}
}
