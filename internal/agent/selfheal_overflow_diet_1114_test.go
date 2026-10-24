package agent

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/tool"
)

// 任务 1114 回归钉：投影失效 × 物理超窗时的自动应急减肥。
//
// 现场（20261024 新9 二次复发）：投影因 covered 前缀漂移被判失效
// （content_mismatch，UI「投影失效 · 全量回退中」）后，视图服务全量
// canonical（3.02M/1M），而 Diet 通路（1）被备份锁挡死（1116 已修）、
// （2）只在打开/切 tab 触发——失效现场没有任何自动收敛，压缩循环对
// 3M token 摘要请求反复 400/超时直到人工干预。本组测试钉住：fallback
// episode 触发自动 Diet（备份→剥图+折叠→CAS 重写）、未过硬顶不触发、
// 单飞去重。

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

// ① 接线钉：fallback 观测（observeProjectionFallback 的 no_projection 分支，
// 与新9 的 content_mismatch 共用 warnProjectionFallback 出口）在 canonical
// 过硬顶时必须自动跑完一次 Diet：剥净 data: 载荷、估算落回溢出线下、
// repairlog 落一条带 strip+fold 的记录、备份目录存在。
func Test1114OverflowDietFiresOnFallbackEpisode(t *testing.T) {
	const window = 100_000
	path := imageDominantSession(t, t.TempDir(), "overflow.jsonl", window)
	a := agentOnDisk1114(t, path)

	canonical := a.sess.conversation.Snapshot()
	est := a.estimatedVisibleRequestTokens(canonical)
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
	if after := estimateProjectionTokens(dieted); after >= int(float64(window)*projectionOverflowRatio) {
		t.Fatalf("post-diet estimate %d must sit below the overflow ratio of window %d", after, window)
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
	backupDirs, err := filepath.Glob(strings.TrimSuffix(path, ".jsonl") + ".pre-repair-*")
	if err != nil || len(backupDirs) != 1 {
		t.Fatalf("exactly one backup directory expected: %v err=%v", backupDirs, err)
	}
}

// ② 门槛钉：canonical 未过硬顶时 fallback 观测不得动手——重建（813）才是
// 又快又省的正道，本地重写只留给「发不出去」的量级。
func Test1114OverflowDietSkippedBelowCeiling(t *testing.T) {
	const window = 100_000
	path := imageDominantSession(t, t.TempDir(), "small.jsonl", window)
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
	path := imageDominantSession(t, t.TempDir(), "dup.jsonl", window)
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
