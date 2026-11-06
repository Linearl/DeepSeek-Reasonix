package main

// tl1429c 回归钉:归档同流程自撞(2026-11-06 17:46 装机现场,包 80 v1.38.3)。
//
// 现场三线配对(desktop.log.1 11853-11861):
//  17:46:30.173 guard acquired op=archive_ownership waited_ms=1
//  17:46:30.288 lease still held after contention retries attempts=3
//                holder="... (lock held inside this process: kind=removal_guard
//                since=2026-11-06T09:46:30Z)"
//  17:46:30.310 guard refused op=archive_ownership holder_class=self_lock
//                holder_pid=0 holder_lock_kind=removal_guard
//                holder_lock_since=09:46:30Z(与 acquire 同秒)
//  17:46:30.314 orphan sweep left an in-flight removal guard in place
//
// 机制:同一个物理会话以两套拼写进入目标集(磁盘目录列表一套、tab/名册存的
// 旧时代大小写拼写一套)——目标去重按原始字符串,锁层按 canonical(Windows
// 大小写折叠)。归档循环先给拼写 A 拿 guard,再给拼写 B 拿 → 撞上自己的 guard
// → heal 按 tl1429b-2 纪律正确留下 in-flight guard → 归档失败,用户被逼强制。
//
// 修复:目标身份统一走 canonical(扫描层 + runtime 合并层;ownership 批内
// 幂等与日志打点钉见后续 commit)。本文件先钉:两套拼写合并为一个目标;
// 端到端 TrashTopic 一次成功且过程零 self_lock 拒绝。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/store"
)

// caseVariantPath returns the same absolute path with the base name's case
// flipped — on Windows the same physical file under a second spelling (the
// legacy stored-path form this incident archived twice).
func caseVariantPath(path string) string {
	base := filepath.Base(path)
	upper := strings.ToUpper(base[:1]) + base[1:]
	if upper == base {
		upper = strings.ToLower(base[:1]) + base[1:]
	}
	return filepath.Join(filepath.Dir(path), upper)
}

// 扫描层(runtime 合并):同一文件的两套拼写合并为一个目标,磁盘拼写优先。
// 修复前这里是两个目标——第二个目标就是 17:46 自撞的第二次 acquire。
func TestAppendTopicRuntimeTrashTargetsMergesCaseVariantSpelling(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("canonical session identity case-folds on Windows only; the duplicate-spelling form cannot be modeled portably")
	}
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "tl1429c-dup.jsonl")
	if err := os.WriteFile(sessionPath, []byte(`{"role":"user","content":"tl1429c"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	variant := caseVariantPath(sessionPath)

	// 磁盘扫描产出的目标(盘上真实大小写)。
	targets := []topicTrashTarget{{dir: dir, sessionPath: sessionPath, key: filepath.Base(sessionPath)}}

	app := NewApp()
	app.mu.Lock()
	app.tabs["tl1429c_tab"] = &WorkspaceTab{
		ID:          "tl1429c_tab",
		TopicID:     "topic_tl1429c_merge",
		SessionPath: variant, // 旧时代大小写拼写的 tab 绑定
	}
	app.mu.Unlock()

	merged, err := app.appendTopicRuntimeTrashTargets("topic_tl1429c_merge", targets)
	if err != nil {
		t.Fatalf("appendTopicRuntimeTrashTargets: %v", err)
	}
	if len(merged) != 1 {
		t.Fatalf("targets = %d, want 1 — 两套拼写各成一个目标=17:46 自撞回潮(%v / %v)", len(merged), merged[0].sessionPath, variant)
	}
	if merged[0].sessionPath != sessionPath {
		t.Fatalf("merged target path = %v, want the on-disk spelling %v", merged[0].sessionPath, sessionPath)
	}
	if sessionRuntimeKey(merged[0].sessionPath) != sessionRuntimeKey(variant) {
		t.Fatal("merged target and variant disagree on the canonical identity")
	}
}

// 端到端(现场流程):tab 以旧时代拼写绑定同一会话 → TrashTopic 一次成功,
// 过程日志零 self_lock 拒绝(17:46 的签名日志不复现),归档产物零残留。
func TestTrashTopicSurvivesDuplicateTargetSpelling(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("canonical session identity case-folds on Windows only; the duplicate-spelling form cannot be modeled portably")
	}
	isolateDesktopUserDirs(t)
	logs := captureSlog(t)
	topicID := "topic_tl1429c_dup_spelling"
	sessionPath, projectRoot := stageTerminalArchiveTopic(t, "tl1429c-dup.jsonl", topicID, "TL1429C dup spelling")
	variant := caseVariantPath(sessionPath)

	app := NewApp()
	app.mu.Lock()
	app.tabs["tl1429c_tab"] = &WorkspaceTab{
		ID:          "tl1429c_tab",
		TopicID:     topicID,
		SessionPath: variant,
	}
	app.mu.Unlock()

	if err := app.TrashTopic(topicID); err != nil {
		t.Fatalf("TrashTopic with a duplicate-spelling binding = %v, want one-shot success", err)
	}
	if _, err := os.Stat(sessionPath); !os.IsNotExist(err) {
		t.Fatalf("session transcript survived the archive: %v", err)
	}
	for _, sidecar := range []string{store.SessionLeaseInfo(sessionPath), store.SessionLeaseLock(sessionPath), store.SessionLockFile(sessionPath)} {
		if _, err := os.Stat(sidecar); !os.IsNotExist(err) {
			t.Fatalf("sidecar %s survived the archive: %v", sidecar, err)
		}
	}
	if got := loadTopicTitle(projectRoot, topicID); got != "" {
		t.Fatalf("archived topic title = %q, want cleared", got)
	}
	// 17:46 的签名日志(同秒 acquire→refuse self_lock)不得复现。
	for _, line := range strings.Split(logs.String(), "\n") {
		if strings.Contains(line, "session removal guard refused by lease holder") {
			var entry map[string]any
			if jsonErr := json.Unmarshal([]byte(line), &entry); jsonErr != nil {
				continue
			}
			if entry["holder_class"] == "self_lock" && entry["op"] == "archive_ownership" {
				t.Fatalf("archive refused itself again (self-collision back): %s", line)
			}
		}
	}
	// 归档后锁面干净:同路径立即可重新 acquire。
	reattach, err := agent.TryAcquireSessionLease(sessionPath)
	if err != nil {
		t.Fatalf("reacquire after archive = %v, want a clean lease state", err)
	}
	reattach.Release()
}

// ownership 层(纵深防御):重复拼写的目标只拿一把 guard,第二个拼写复用批内
// 所有权。修复前第二个 acquire 撞上第一个(同秒自撞)→ busy 拒绝 → 整批失败;
// 将来任何调用面再出现重复目标,这一层保证不再自撞。
func TestAcquireTopicArchiveOwnershipDuplicateSpellingTakesOneGuard(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("canonical session identity case-folds on Windows only; the duplicate-spelling form cannot be modeled portably")
	}
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "tl1429c-own.jsonl")
	if err := os.WriteFile(sessionPath, []byte("own"), 0o644); err != nil {
		t.Fatal(err)
	}
	variant := caseVariantPath(sessionPath)
	targets := []topicTrashTarget{
		{dir: dir, sessionPath: sessionPath, key: filepath.Base(sessionPath)},
		{dir: dir, sessionPath: variant, key: filepath.Base(variant)},
	}

	batch, err := acquireTopicArchiveOwnership(nil, targets, nil, false)
	if err != nil {
		t.Fatalf("ownership for duplicate spellings = %v, want one guard serving both (17:46 自撞)", err)
	}
	t.Cleanup(batch.release)
	if len(batch.entries) != 1 {
		t.Fatalf("batch entries = %d, want 1 per canonical session", len(batch.entries))
	}
	guard := batch.take(sessionPath)
	if guard == nil {
		t.Fatal("batch did not keep a guard for the target")
	}
	if !agent.SessionLeaseLockHeldInProcess(sessionPath) {
		t.Fatal("the single guard does not hold the session's lease lock")
	}
	guard.Release()
	if agent.SessionLeaseLockHeldInProcess(sessionPath) {
		t.Fatal("guard release left the registry entry behind")
	}
}

// 拒绝日志打点(tl1429c):进程内持有者不再打 holder_pid=0——holder_scope=
// in_process + kind/since 才是一眼可读的身份;真未知形态(无登记证据)保持
// holder_pid=0 的诚实零身份。
func TestHolderLogAttrsInProcessHolderNamesScopeNotPidZero(t *testing.T) {
	evidence := []agent.SessionLockHolderEvidence{
		{Kind: "removal_guard", AcquiredAt: time.Now().UTC()},
	}
	attrs := sessionLeaseHolderLogAttrsWithEvidence(nil, evidence)
	values := map[string]any{}
	for i := 0; i+1 < len(attrs); i += 2 {
		values[attrs[i].(string)] = attrs[i+1]
	}
	if values["holder_scope"] != "in_process" {
		t.Fatalf("holder_scope = %v, want in_process", values["holder_scope"])
	}
	if values["holder_lock_kind"] != "removal_guard" {
		t.Fatalf("holder_lock_kind = %v, want removal_guard", values["holder_lock_kind"])
	}
	if _, ok := values["holder_lock_since"]; !ok {
		t.Fatal("holder_lock_since missing — 登记时刻未 dump")
	}
	if _, ok := values["holder_pid"]; ok {
		t.Fatalf("in-process holder still logs holder_pid=%v — pid=0 被读成「身份未知」的误读回潮", values["holder_pid"])
	}

	// 对照:无登记证据的真未知形态保持 holder_pid=0(诚实零身份)。
	unknown := sessionLeaseHolderLogAttrsWithEvidence(nil, nil)
	unknownValues := map[string]any{}
	for i := 0; i+1 < len(unknown); i += 2 {
		unknownValues[unknown[i].(string)] = unknown[i+1]
	}
	if unknownValues["holder_pid"] != 0 {
		t.Fatalf("unidentified holder pid = %v, want the honest 0", unknownValues["holder_pid"])
	}
	if _, ok := unknownValues["holder_scope"]; ok {
		t.Fatal("unidentified holder must not claim holder_scope=in_process")
	}
}
