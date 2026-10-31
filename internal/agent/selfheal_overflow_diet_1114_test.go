package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fmt"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// 任务 1114 回归钉：投影失效 × 物理超窗时的自动应急减肥。
//
// tl1083 §5.2-P0① 前置门槛改绑：原三钉随 tl1227 DAG 读退役被整组 t.Skip
// （DAG fixture 的取样读面已删）——这正是「Diet 对 schema-1 会话结构性
// 失效」的测试面呈现。本文件以 schema-1 物化转录 fixture 重新武装三钉，
// 并补三条护栏钉：可校验判据 fail-safe（校验失败拒绝重写）、备份白名单
// 先行顺序（原件字节先落备份再动笔）、重写产物逐字校验（verify=ok 进
// repair 记录）。§6.1 验收读数直接断在 ① 里。

// imageDominantSchemaOneSession builds the 新9 incident shape on the
// post-tl1227 storage reality: a schema-1 materialized checkpoint whose
// projection is dominated by embedded images (~95% of estimated tokens) with
// a text tail long enough that fold must fire after the strip.
func imageDominantSchemaOneSession(t *testing.T, dir, name string) string {
	t.Helper()
	path := dir + string(os.PathSeparator) + name
	sess := NewSession("sys")
	// 3 张 1568×1080 大图（3.5MB base64 每张）。请求尺（estimatedVisibleRequestTokens）
	// 按像素计价（~2K tokens/张），数据 URL 字节只进 projection 尺——所以 3×超窗
	// 的量级由文本尾部撑起：35 条 × 40000 字符 ≈ 350K tokens（100K 窗 ⇒ 3.5×）。
	for i := 0; i < 3; i++ {
		m := provider.Message{Role: provider.RoleUser, Content: fmt.Sprintf("screenshot-%d", i)}
		m.Images = []string{pngHeaderBase64(t, 1568, 1080, 3_500_000)}
		sess.Add(m)
	}
	// 文本尾部：剥图后仍远超 60% 折叠预算（60K），使 fold 必须触发（新9 形态：
	// 剥图后 projection 仍贴顶，折叠是第二道收敛）。
	for i := 0; i < 35; i++ {
		sess.Add(provider.Message{Role: provider.RoleAssistant, Content: strings.Repeat("t", 40000)})
	}
	if err := sess.Save(path); err != nil {
		t.Fatalf("persist schema-1 fixture: %v", err)
	}
	return path
}

func agentOnDisk1114(t *testing.T, path string) *Agent {
	t.Helper()
	s, err := LoadSession(path)
	if err != nil {
		t.Fatal(err)
	}
	a := New(&sharedWindowRecorder813{}, tool.NewRegistry(), s, Options{
		ContextWindow: 100_000,
		CompactRatio:  0.80,
		ArchiveDir:    t.TempDir(),
	}, event.Discard)
	a.BindSessionPath(path, false)
	return a
}

func waitForDietSettled1114(t *testing.T, a *Agent) bool {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if !a.sess.dietPending.Load() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// ① §6.1 验收主腿：非 DAG（schema-1 物化转录）+ 失效 + 3×超窗 ⇒ Diet 通路
// 触发、会话回到可发送态、repair 记录带 strip+fold 与 verify=ok、备份目录
// 先于重写持有原件字节（备份里的 data: 载荷还在，活文件已剥净）。
func Test1114OverflowDietFiresOnFallbackEpisodeSchemaOne(t *testing.T) {
	const window = 100_000
	path := imageDominantSchemaOneSession(t, t.TempDir(), "overflow.jsonl")
	a := agentOnDisk1114(t, path)

	canonical := a.sess.conversation.Snapshot()
	est := a.estimatedVisibleRequestTokens(canonical)
	if est < 3*window {
		t.Fatalf("fixture setup: estimate %d must exceed 3x the window %d (the §6.1 3x-over shape)", est, 3*window)
	}
	if est < a.hardInputCeiling() {
		t.Fatalf("fixture setup: estimate %d must exceed the hard ceiling %d", est, a.hardInputCeiling())
	}

	a.observeProjectionFallback(est)
	if !waitForDietSettled1114(t, a) {
		t.Fatal("overflow diet never settled")
	}

	dieted := mustSnapshot(t, path)
	for i, m := range dieted {
		for _, img := range m.Images {
			if strings.HasPrefix(img, "data:") {
				t.Fatalf("message %d still carries an embedded payload after the auto diet", i)
			}
		}
	}
	// 回到可发送态：剥图+折叠后的估算必须落在物理硬顶之下。
	if after := estimateProjectionTokens(dieted); after >= a.hardInputCeiling() {
		t.Fatalf("post-diet estimate %d must sit below the hard ceiling %d to be sendable again", after, a.hardInputCeiling())
	}

	records, err := ListSessionRepairRecordsFor(path)
	if err != nil || len(records) != 1 {
		t.Fatalf("exactly one repair record expected: %d records err=%v", len(records), err)
	}
	status := map[string]int{}
	for _, act := range records[0].Actions {
		if act.Status == "done" {
			status[act.Action]++
		}
	}
	if status[HealActionStripImages] != 1 || status[HealActionFold] != 1 {
		t.Fatalf("repair record must carry exactly one strip_images and one fold done: %+v", records[0].Actions)
	}
	if !strings.Contains(records[0].Before, "no_projection") {
		t.Fatalf("repair record must name the fallback reason: %q", records[0].Before)
	}
	if !strings.Contains(records[0].Before, "verify=ok") {
		t.Fatalf("repair record must carry the verbatim verification verdict: %q", records[0].Before)
	}

	// 备份白名单先行顺序：备份目录先于重写持有原件——里面的检查点仍带
	// data: 载荷，与已剥净的活文件形成字节级对照。
	backupDirs, err := filepath.Glob(strings.TrimSuffix(path, ".jsonl") + ".pre-repair-*")
	if err != nil || len(backupDirs) != 1 {
		t.Fatalf("exactly one backup directory expected: %v err=%v", backupDirs, err)
	}
	backupCheckpoint := filepath.Join(backupDirs[0], filepath.Base(path))
	originalBytes, err := os.ReadFile(backupCheckpoint)
	if err != nil || !strings.Contains(string(originalBytes), "data:image/png") {
		t.Fatalf("backup must hold the pre-rewrite checkpoint with the embedded payloads: err=%v has_data=%v", err, err == nil && strings.Contains(string(originalBytes), "data:image/png"))
	}
	liveBytes, err := os.ReadFile(path)
	if err != nil || strings.Contains(string(liveBytes), "data:image/png") {
		t.Fatalf("live checkpoint must be stripped after the diet: err=%v has_data=%v", err, err == nil && strings.Contains(string(liveBytes), "data:image/png"))
	}
}

// ② 门槛钉：canonical 未过硬顶时 fallback 观测不得动手——重建（813）才是
// 又快又省的正道，本地重写只留给「发不出去」的量级。
func Test1114OverflowDietSkippedBelowCeiling(t *testing.T) {
	const window = 100_000
	path := imageDominantSchemaOneSession(t, t.TempDir(), "small.jsonl")
	a := agentOnDisk1114(t, path)

	// 人为把窗口放大，使估算落在硬顶之下（同一份转录、不同的物理窗口）。
	a.contextWindow = 100_000_000
	canonical := a.sess.conversation.Snapshot()
	est := a.estimatedVisibleRequestTokens(canonical)
	if est >= a.hardInputCeiling() {
		t.Fatalf("fixture setup: estimate %d must sit below the hard ceiling %d", est, a.hardInputCeiling())
	}

	a.observeProjectionFallback(est)
	if a.sess.dietPending.Load() {
		t.Fatal("below-ceiling episode must not arm the overflow diet")
	}
	dieted := mustSnapshot(t, path)
	embedded := 0
	for _, m := range dieted {
		for _, img := range m.Images {
			if strings.HasPrefix(img, "data:") {
				embedded++
			}
		}
	}
	if embedded == 0 {
		t.Fatal("fixture lost its embedded images — the diet must not have run")
	}
	if records, _ := ListSessionRepairRecordsFor(path); len(records) != 0 {
		t.Fatalf("below-ceiling episode must not write a repair record: %+v", records)
	}
}

// ③ 单飞钉：同一 episode 至多一次 Diet——in-flight 标志位挡住重复 kick，
// 不产生第二个备份目录或第二条修复记录。
func Test1114OverflowDietDeduplicatesWhileInFlight(t *testing.T) {
	const window = 100_000
	path := imageDominantSchemaOneSession(t, t.TempDir(), "dup.jsonl")
	a := agentOnDisk1114(t, path)

	a.sess.dietPending.Store(true)
	a.kickOverflowDiet("test_dedup", a.hardInputCeiling()+1)
	if records, _ := ListSessionRepairRecordsFor(path); len(records) != 0 {
		t.Fatalf("in-flight flag must suppress the kick: %+v", records)
	}
	backupDirs, err := filepath.Glob(strings.TrimSuffix(path, ".jsonl") + ".pre-repair-*")
	if err != nil || len(backupDirs) != 0 {
		t.Fatalf("in-flight flag must not create a backup dir: %v err=%v", backupDirs, err)
	}
	embedded := 0
	for _, m := range mustSnapshot(t, path) {
		for _, img := range m.Images {
			if strings.HasPrefix(img, "data:") {
				embedded++
			}
		}
	}
	if embedded == 0 {
		t.Fatal("transcript was rewritten while the kick should have been suppressed")
	}
}

// ④ 护栏钉（tl1083 三护栏之一）：可校验判据 fail-safe——CAS 账本不可读时
// 必须拒绝重写（ERROR 留痕），不得带病动手：转录零改写、零备份、零 repair
// 记录。
func Test1114OverflowDietRefusedWhenTranscriptUnverifiable(t *testing.T) {
	path := imageDominantSchemaOneSession(t, t.TempDir(), "torn.jsonl")
	if err := os.WriteFile(BranchMetaPath(path), []byte("{corrupt ledger"), 0o644); err != nil {
		t.Fatalf("corrupt the CAS ledger: %v", err)
	}
	if _, _, err := sessionContentRevision(path); err == nil {
		t.Fatal("fixture setup: the corrupted ledger must fail sessionContentRevision")
	}
	a := agentOnDisk1114(t, path)

	a.observeProjectionFallback(a.hardInputCeiling() + 1)
	if !waitForDietSettled1114(t, a) {
		t.Fatal("refused diet never settled")
	}

	embedded := 0
	for _, m := range mustSnapshot(t, path) {
		for _, img := range m.Images {
			if strings.HasPrefix(img, "data:") {
				embedded++
			}
		}
	}
	if embedded == 0 {
		t.Fatal("transcript was rewritten while verification should have refused the diet")
	}
	if backupDirs, err := filepath.Glob(strings.TrimSuffix(path, ".jsonl") + ".pre-repair-*"); err != nil || len(backupDirs) != 0 {
		t.Fatalf("a refused diet must not create a backup dir: %v err=%v", backupDirs, err)
	}
	if records, _ := ListSessionRepairRecordsFor(path); len(records) != 0 {
		t.Fatalf("a refused diet must not write a repair record: %+v", records)
	}
}

// ⑤ 逐字校验纯函数钉：盘上重放与折叠意图一致 ⇒ nil；任何字节漂移 ⇒ 指名
// 消息序号的错误。工具调用与图片占位都在指纹覆盖内。
func TestVerifyRewrittenTranscriptVerbatim(t *testing.T) {
	dir := t.TempDir()
	path := dir + string(os.PathSeparator) + "verify.jsonl"
	sess := NewSession("sys")
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "plan"})
	ta := provider.Message{Role: provider.RoleAssistant}
	ta.ToolCalls = []provider.ToolCall{{ID: "call-1", Name: "read_file", Arguments: `{"path":"a.go"}`}}
	sess.Add(ta)
	sess.Add(provider.Message{Role: provider.RoleTool, ToolCallID: "call-1", Name: "read_file", Content: "bytes"})
	img := provider.Message{Role: provider.RoleUser, Content: "shot"}
	img.Images = []string{"data:image/png;base64,AAAA"}
	sess.Add(img)
	if err := sess.Save(path); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadSession(path)
	if err != nil {
		t.Fatal(err)
	}
	folded := loaded.Snapshot()
	if err := verifyRewrittenTranscript(path, folded); err != nil {
		t.Fatalf("identical transcript must verify clean: %v", err)
	}

	drifted := append([]provider.Message(nil), folded...)
	drifted[2].Content += "x"
	if err := verifyRewrittenTranscript(path, drifted); err == nil {
		t.Fatal("a one-byte content drift must fail the verbatim verification")
	}
	drifted2 := append([]provider.Message(nil), folded...)
	drifted2 = drifted2[:len(drifted2)-1]
	if err := verifyRewrittenTranscript(path, drifted2); err == nil {
		t.Fatal("a dropped message must fail the verbatim verification")
	}
}
