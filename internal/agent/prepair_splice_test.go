package agent

// tl1787 续作（tl1674 阶段二定案）：pre-repair 分叉元信息拼接的单测。
//
// 验收钉四组：
//  ① 拼接判据三形态（分叉长链/covered 无事/折叠续接需锚点）；
//  ② 登记写档案袋：校验值三条本地重算（段哈希+前后边界指纹）、重复登记
//     幂等拒绝；
//  ③ **投影零改动**（定案核心）：登记前后主线转录与事件日志逐字节不变、
//     零模型调用面（本层无任何模型触点，结构上成立）；
//  ④ 档案袋随会话侧车清单走（删除/还原/身份换位全链单一来源）。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/store"
)

// seedSplicePair 建分叉形态（轮次粒度——判据按整轮对齐）：宿主=改写后新链
// （sys + [C,C2] + [F,G]），备份=被换掉的旧链（sys + [C,C2] + [D,E]）——
// 共享整轮 [C,C2]，宿主续轮 [F,G]，备份丢失轮 [D,E]。
// 返回宿主路径与备份转录路径。
func seedSplicePair(t *testing.T, dir, stem string) (string, string) {
	t.Helper()
	ownerPath := filepath.Join(dir, stem+".jsonl")
	owner := NewSession("sys")
	owner.Add(userTurn("C shared"))
	owner.Add(assistantTurn("C2 shared reply"))
	owner.Add(userTurn("F new question"))
	owner.Add(assistantTurn("G new answer"))
	if err := owner.Save(ownerPath); err != nil {
		t.Fatalf("save owner: %v", err)
	}
	backupDir := filepath.Join(dir, stem+".pre-repair-20261110T010000")
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		t.Fatal(err)
	}
	backupPath := filepath.Join(backupDir, stem+".jsonl")
	backup := NewSession("sys")
	backup.Add(userTurn("C shared"))
	backup.Add(assistantTurn("C2 shared reply"))
	backup.Add(userTurn("D folded away"))
	backup.Add(assistantTurn("E folded away"))
	if err := backup.Save(backupPath); err != nil {
		t.Fatalf("save backup: %v", err)
	}
	return ownerPath, backupPath
}

func TestCheckPreRepairSpliceDivergedLongChain(t *testing.T) {
	dir := t.TempDir()
	ownerPath, backupPath := seedSplicePair(t, dir, "topic")

	plan, err := CheckPreRepairSplice(ownerPath, backupPath, 0)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if !PreRepairSplicePlanSplicable(plan) {
		t.Fatalf("plan = %+v, want splicable (分叉长链)", plan)
	}
	if plan.Kind != PreRepairSpliceDiverged {
		t.Fatalf("kind = %q, want %q", plan.Kind, PreRepairSpliceDiverged)
	}
	// trim 口径：公共整轮 [C,C2]=2；备份丢失轮 [D,E]=[2,4)；拼接点=主线第 2 条。
	if plan.SharedPrefix != 2 || plan.SegmentStart != 2 || plan.SegmentEnd != 4 || plan.SplicePoint != 2 || plan.SegmentMessages != 2 {
		t.Fatalf("plan = %+v, want prefix 2 / segment [2,4) / splice point 2", plan)
	}
}

// covered：备份已含于主线（主线是长链）——无事可登记。
func TestCheckPreRepairSpliceCoveredIsNoop(t *testing.T) {
	dir := t.TempDir()
	ownerPath := filepath.Join(dir, "long.jsonl")
	owner := NewSession("sys")
	owner.Add(userTurn("C shared"))
	owner.Add(userTurn("D folded away"))
	owner.Add(assistantTurn("E folded away"))
	owner.Add(userTurn("F new"))
	if err := owner.Save(ownerPath); err != nil {
		t.Fatal(err)
	}
	backupPath := filepath.Join(dir, "short.pre-repair-20261110T010000", "short.jsonl")
	if err := os.MkdirAll(filepath.Dir(backupPath), 0o755); err != nil {
		t.Fatal(err)
	}
	backup := NewSession("sys")
	backup.Add(userTurn("C shared"))
	backup.Add(userTurn("D folded away"))
	backup.Add(assistantTurn("E folded away"))
	if err := backup.Save(backupPath); err != nil {
		t.Fatal(err)
	}

	plan, err := CheckPreRepairSplice(ownerPath, backupPath, 0)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if PreRepairSplicePlanSplicable(plan) {
		t.Fatalf("plan = %+v, want non-splicable (covered)", plan)
	}
	if !strings.Contains(plan.Reason, "covered") {
		t.Fatalf("reason = %q, want the covered explanation", plan.Reason)
	}
}

// 折叠续接：无前缀关系（折叠摘要不指纹匹配旧文）——无锚点拒绝，有锚点
// （repairlog 命中，调用方已验时间线）登记整链拼接（拼接点=0）。
func TestCheckPreRepairSpliceFoldContinuationNeedsAnchor(t *testing.T) {
	dir := t.TempDir()
	ownerPath := filepath.Join(dir, "folded.jsonl")
	owner := NewSession("sys")
	owner.Add(userTurn("E folded summary"))
	owner.Add(userTurn("F new"))
	if err := owner.Save(ownerPath); err != nil {
		t.Fatal(err)
	}
	backupPath := filepath.Join(dir, "folded.pre-repair-20261110T010000", "folded.jsonl")
	if err := os.MkdirAll(filepath.Dir(backupPath), 0o755); err != nil {
		t.Fatal(err)
	}
	backup := NewSession("sys")
	backup.Add(userTurn("A original"))
	backup.Add(assistantTurn("B original"))
	if err := backup.Save(backupPath); err != nil {
		t.Fatal(err)
	}

	noAnchor, err := CheckPreRepairSplice(ownerPath, backupPath, 0)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if PreRepairSplicePlanSplicable(noAnchor) {
		t.Fatalf("plan = %+v, want non-splicable without the repair anchor", noAnchor)
	}
	plan, err := CheckPreRepairSplice(ownerPath, backupPath, time.Now().Unix())
	if err != nil {
		t.Fatalf("check with anchor: %v", err)
	}
	if plan.Kind != PreRepairSpliceFoldContinuation || plan.SplicePoint != 0 || plan.SegmentMessages != 2 {
		t.Fatalf("plan = %+v, want fold-continuation splicing the whole old chain at point 0", plan)
	}
}

// 登记写档案袋：校验值三条本地重算；重复登记幂等拒绝；**投影零改动**——
// 登记前后主线转录与事件日志逐字节不变（定案核心的字节级钉）。
func TestRegisterPreRepairSpliceWritesChecksumsAndNeverTouchesProjection(t *testing.T) {
	dir := t.TempDir()
	ownerPath, backupPath := seedSplicePair(t, dir, "topic")
	plan, err := CheckPreRepairSplice(ownerPath, backupPath, 0)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	transcriptBefore, err := os.ReadFile(ownerPath)
	if err != nil {
		t.Fatal(err)
	}
	eventsBefore, err := os.ReadFile(ownerPath[:len(ownerPath)-len(".jsonl")] + ".events.jsonl")
	if err != nil {
		t.Fatal(err)
	}

	entry, err := RegisterPreRepairSplice(ownerPath, PreRepairSpliceRequest{
		BackupDir:        filepath.Dir(backupPath),
		BackupTranscript: backupPath,
		Stamp:            "20261110T010000",
		Plan:             plan,
		Source:           PreRepairSpliceSourceManual,
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if entry.SegmentHash == "" || entry.BoundaryPrevHash == "" || entry.BoundaryNextHash == "" {
		t.Fatalf("entry = %+v, want the three locally recomputed checksums", entry)
	}
	if entry.Source != PreRepairSpliceSourceManual {
		t.Fatalf("source = %q, want %q (登记必须携带来源标记)", entry.Source, PreRepairSpliceSourceManual)
	}
	if entry.OwnerMessages != 4 {
		t.Fatalf("ownerMessages = %d, want 4 (trim 口径：[C,C2,F,G])", entry.OwnerMessages)
	}
	if !HasPreRepairSplice(ownerPath, filepath.Dir(backupPath)) {
		t.Fatal("the registered backup must read back from the ledger")
	}
	// 投影零改动（字节级）：转录与事件日志与登记前逐字节一致。
	transcriptAfter, err := os.ReadFile(ownerPath)
	if err != nil {
		t.Fatal(err)
	}
	eventsAfter, err := os.ReadFile(ownerPath[:len(ownerPath)-len(".jsonl")] + ".events.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if string(transcriptBefore) != string(transcriptAfter) {
		t.Fatal("the transcript changed during a metadata-only splice (投影内容一律不变 violated)")
	}
	if string(eventsBefore) != string(eventsAfter) {
		t.Fatal("the event log changed during a metadata-only splice (投影内容一律不变 violated)")
	}

	// 幂等：同一备份重复登记拒绝。
	if _, err := RegisterPreRepairSplice(ownerPath, PreRepairSpliceRequest{
		BackupDir:        filepath.Dir(backupPath),
		BackupTranscript: backupPath,
		Stamp:            "20261110T010000",
		Plan:             plan,
		Source:           PreRepairSpliceSourceManual,
	}); err == nil {
		t.Fatal("a duplicate registration must be refused (idempotent)")
	}
}

// 来源闸（tl1674 阶段二）：auto/manual 二选一，空值与陌生值拒绝——自动与
// 手动两条触发路径共用登记面，来源是唯一可观测区分键，不留「来历不明」件。
func TestRegisterPreRepairSpliceRequiresSource(t *testing.T) {
	dir := t.TempDir()
	ownerPath, backupPath := seedSplicePair(t, dir, "topic")
	plan, err := CheckPreRepairSplice(ownerPath, backupPath, 0)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	for _, source := range []string{"", "unknown-origin"} {
		_, err := RegisterPreRepairSplice(ownerPath, PreRepairSpliceRequest{
			BackupDir:        filepath.Dir(backupPath),
			BackupTranscript: backupPath,
			Stamp:            "20261110T010000",
			Plan:             plan,
			Source:           source,
		})
		if err == nil {
			t.Fatalf("source=%q must be refused (no unattributed registrations)", source)
		}
	}
	if HasPreRepairSplice(ownerPath, filepath.Dir(backupPath)) {
		t.Fatal("a refused registration must not write the ledger")
	}
	// 合法来源照常落袋，且 auto/manual 都被接受（共用判据的两条路径）。
	for _, source := range []string{PreRepairSpliceSourceAuto, PreRepairSpliceSourceManual} {
		// 每轮换一个备份目录，避开幂等闸（幂等闸自身已被上游钉覆盖）。
		backupDir := filepath.Join(dir, "topic.pre-repair-20261110T0100"+map[string]string{PreRepairSpliceSourceAuto: "0", PreRepairSpliceSourceManual: "1"}[source])
		altPath := filepath.Join(backupDir, "topic.jsonl")
		if err := os.MkdirAll(backupDir, 0o755); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(backupPath)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(altPath, raw, 0o644); err != nil {
			t.Fatal(err)
		}
		entry, err := RegisterPreRepairSplice(ownerPath, PreRepairSpliceRequest{
			BackupDir:        backupDir,
			BackupTranscript: altPath,
			Stamp:            "20261110T01000" + map[string]string{PreRepairSpliceSourceAuto: "0", PreRepairSpliceSourceManual: "1"}[source],
			Plan:             plan,
			Source:           source,
		})
		if err != nil {
			t.Fatalf("register source=%s: %v", source, err)
		}
		if entry.Source != source {
			t.Fatalf("entry source = %q, want %q", entry.Source, source)
		}
	}
}

// 档案袋随会话侧车清单走（单一来源）：store.SessionSidecarFiles 必含拼接
// 档案袋，删除/还原/身份换位全链才会一并搬运。
func TestSpliceLedgerRidesSessionSidecarInventory(t *testing.T) {
	sessionPath := filepath.Join(t.TempDir(), "topic.jsonl")
	found := false
	for _, sidecar := range store.SessionSidecarFiles(sessionPath) {
		if sidecar == store.SessionSpliceLedger(sessionPath) {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("store.SessionSidecarFiles must carry the splice ledger (清单单一来源纪律)")
	}
}

// ---- tl1674c 折叠态降级 ----

// seedDanglingToolCallBackup 手写一份与 20261125 用户现场（调研员3）同形态
// 的备份转录：末行 assistant 带 tool_calls 但结果未落（diet 改写瞬间轮次在
// 飞）——LoadSession 读它必然补占位结果（normalizedDirty=true），严格快照
// 按 tl1315 拒。
func seedDanglingToolCallBackup(t *testing.T, dir, stem string) string {
	t.Helper()
	backupDir := filepath.Join(dir, stem+".pre-repair-20261123T091022")
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		t.Fatal(err)
	}
	backupPath := filepath.Join(backupDir, stem+".jsonl")
	lines := []string{
		`{"role":"user","content":"A original"}`,
		`{"role":"assistant","content":"B original"}`,
		`{"role":"assistant","content":"","tool_calls":[{"id":"call-diet-1","type":"function","function":{"name":"wait","arguments":"{}"}}]}`,
	}
	if err := os.WriteFile(backupPath, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 前置自证：该形态严格快照必拒、load 时修复必脏（测试红=构造失败）。
	if sess, err := LoadSession(backupPath); err != nil || sess == nil {
		t.Fatalf("seed: load backup: %v", err)
	} else if !sess.normalizedDirty {
		t.Fatal("seed: the dangling tool call must trip load-time normalization (构造失败=fixture 失真)")
	}
	if _, ok := LoadSessionContentSnapshot(backupPath); ok {
		t.Fatal("seed: the strict snapshot must refuse a dirty backup (tl1315 fail-closed)")
	}
	return backupPath
}

// 现场钉（tl1674c 验收1）：折叠改写留下的脏备份（dangling tool call）不再
// 堵死拼接——判据降级跑在修复后视图上，fold-continuation 形态照常可拼，Plan
// 带 Degraded=backup 标记；登记同口径落袋（entry.Degraded 如实），主线转录
// 与事件日志逐字节不动。
func TestCheckAndRegisterPreRepairSpliceDegradedDirtyBackup(t *testing.T) {
	dir := t.TempDir()
	ownerPath := filepath.Join(dir, "folded.jsonl")
	owner := NewSession("sys")
	owner.Add(userTurn("[本地折叠] 已省略 3 条早期消息"))
	owner.Add(userTurn("F new"))
	if err := owner.Save(ownerPath); err != nil {
		t.Fatal(err)
	}
	backupPath := seedDanglingToolCallBackup(t, dir, "folded")

	plan, err := CheckPreRepairSplice(ownerPath, backupPath, time.Now().Unix())
	if err != nil {
		t.Fatalf("check with degraded backup: %v", err)
	}
	if !PreRepairSplicePlanSplicable(plan) {
		t.Fatalf("plan = %+v, want splicable via the degraded read", plan)
	}
	if plan.Kind != PreRepairSpliceFoldContinuation || plan.SplicePoint != 0 {
		t.Fatalf("plan = %+v, want fold-continuation at point 0", plan)
	}
	if plan.Degraded != "backup" {
		t.Fatalf("plan.Degraded = %q, want %q (放行必须带降级标记)", plan.Degraded, "backup")
	}
	// 降级视图口径：修复补的占位结果在备份侧消息数里（3 条原文 + 1 条占位）。
	if plan.BackupMessages != 4 {
		t.Fatalf("plan.BackupMessages = %d, want 4 (修复后视图=原文+占位结果)", plan.BackupMessages)
	}

	transcriptBefore, err := os.ReadFile(ownerPath)
	if err != nil {
		t.Fatal(err)
	}
	eventsBefore, err := os.ReadFile(store.SessionEventLog(ownerPath))
	if err != nil {
		t.Fatal(err)
	}
	entry, err := RegisterPreRepairSplice(ownerPath, PreRepairSpliceRequest{
		BackupDir:        filepath.Dir(backupPath),
		BackupTranscript: backupPath,
		Stamp:            "20261123T091022",
		Plan:             plan,
		Source:           PreRepairSpliceSourceManual,
	})
	if err != nil {
		t.Fatalf("register with degraded backup: %v", err)
	}
	if entry.Degraded != "backup" {
		t.Fatalf("entry.Degraded = %q, want %q (档案袋如实带降级标记)", entry.Degraded, "backup")
	}
	if entry.SegmentHash == "" || entry.BackupDigest == "" {
		t.Fatalf("entry = %+v, want the locally recomputed checksums on the repaired view", entry)
	}
	// 校验值与判据同口径（修复后视图）：段哈希 == 修复后备份段的消息摘要。
	backupSess, err := LoadSession(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	segment := trimLeadingSystemMessages(backupSess.Messages)[plan.SegmentStart:plan.SegmentEnd]
	segDigest, err := digestSessionMessages(segment)
	if err != nil {
		t.Fatal(err)
	}
	if entry.SegmentHash != digestString(segDigest) {
		t.Fatal("segment hash must describe the repaired view the verdict ran on (判定与校验值同口径)")
	}
	if !HasPreRepairSplice(ownerPath, filepath.Dir(backupPath)) {
		t.Fatal("the degraded verdict must land in the ledger")
	}
	// 投影零改动不变式在降级路径同样成立（字节级）。
	transcriptAfter, err := os.ReadFile(ownerPath)
	if err != nil {
		t.Fatal(err)
	}
	eventsAfter, err := os.ReadFile(store.SessionEventLog(ownerPath))
	if err != nil {
		t.Fatal(err)
	}
	if string(transcriptBefore) != string(transcriptAfter) || string(eventsBefore) != string(eventsAfter) {
		t.Fatal("the main line changed during a metadata-only degraded splice")
	}
}

// fail-closed 最后防线（tl1674c 验收2）：降级只收窄「load 时修复」这一形态
// ——转录读不了、事件日志损坏仍然拒绝，且人话点名哪一侧缺什么。干净配对
// （健康会话）判据零降级标记（非折叠行为不变的语义钉）。
func TestCheckPreRepairSpliceStillFailsClosedAndHealthyStaysStrict(t *testing.T) {
	dir := t.TempDir()
	ownerPath, backupPath := seedSplicePair(t, dir, "topic")
	plan, err := CheckPreRepairSplice(ownerPath, backupPath, 0)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if plan.Degraded != "" {
		t.Fatalf("plan.Degraded = %q, want empty (健康配对必须严格读，零降级)", plan.Degraded)
	}

	// 备份转录读不了（字节损坏）：拒，点名 backup。
	broken := filepath.Join(dir, "broken.pre-repair-20261110T010000", "topic.jsonl")
	if err := os.MkdirAll(filepath.Dir(broken), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(broken, []byte("not json at all\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	refused, rerr := CheckPreRepairSplice(ownerPath, broken, 0)
	if rerr == nil {
		t.Fatalf("plan = %+v, want refusal for an unreadable backup", refused)
	}
	if !strings.Contains(refused.Reason, "backup") || !strings.Contains(refused.Reason, "fail-closed") {
		t.Fatalf("reason = %q, want fail-closed naming the backup side", refused.Reason)
	}

	// 备份事件日志损坏（残缺记录）：拒，点名 damage——降级不吞损坏形态。
	damaged := filepath.Join(dir, "damaged.pre-repair-20261110T010000", "topic.jsonl")
	if err := os.MkdirAll(filepath.Dir(damaged), 0o755); err != nil {
		t.Fatal(err)
	}
	seed := NewSession("sys")
	seed.Add(userTurn("A original"))
	if err := seed.Save(damaged); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(store.SessionEventLog(damaged), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("{\"broken\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	damagedPlan, derr := CheckPreRepairSplice(ownerPath, damaged, time.Now().Unix())
	if derr == nil {
		t.Fatalf("plan = %+v, want refusal for a damaged event log", damagedPlan)
	}
	if !strings.Contains(damagedPlan.Reason, "event log damaged") {
		t.Fatalf("reason = %q, want the damaged-log wording (fail-closed 语义要守住)", damagedPlan.Reason)
	}
}
