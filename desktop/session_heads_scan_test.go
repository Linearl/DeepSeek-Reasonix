package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/store"
)

// 任务 1019 验收：面板级 heads 读数的缓存（重复展开不再整链重放）、
// 超预算大会话的降级（不阻塞设置页）、列表层零重放，以及真实样本
// （TL1019_SAMPLE_DIR，ai-passport 20261019 备份六件）的端到端读数。

// resetPanelHeadsScanForTest 清空包级缓存并还原预算，测试互不串味。
func resetPanelHeadsScanForTest(t *testing.T) {
	t.Helper()
	panelHeadsCache.Lock()
	panelHeadsCache.entries = map[string]panelHeadsEntry{}
	panelHeadsCache.order = nil
	panelHeadsCache.hits = 0
	panelHeadsCache.Unlock()
	t.Cleanup(func() {
		panelHeadsCache.Lock()
		panelHeadsCache.entries = map[string]panelHeadsEntry{}
		panelHeadsCache.order = nil
		panelHeadsCache.hits = 0
		panelHeadsCache.Unlock()
	})
}

// shrinkPanelHeadsBudgetForTest 收窄重放预算并在测试结束后还原。
func shrinkPanelHeadsBudgetForTest(t *testing.T, bytes int64) {
	t.Helper()
	saved := panelHeadsReplayMaxBytes
	panelHeadsReplayMaxBytes = bytes
	t.Cleanup(func() { panelHeadsReplayMaxBytes = saved })
}

func panelHeadsHits() int64 {
	panelHeadsCache.Lock()
	defer panelHeadsCache.Unlock()
	return panelHeadsCache.hits
}

// Test1019PanelHeadsCacheAvoidsReplay：同一文件身份的第二次读数必须走缓存
// ——此前每次展开都全额重放（事故样本实测 2s+/次），这是「懒加载不重付」
// 的核心断言。
func Test1019PanelHeadsCacheAvoidsReplay(t *testing.T) {
	t.Skip("tl1236 DAG 读退役善后(1227 后续): 面板 heads 读数走单日志多头重放缓存——tl-1173 P1 起新会话扁平无多头、tl1227 ListSessionHeads 垫片恒空后面板被动收敛零改动，重放缓存场景不可达，显式退役非静默")
	isolateDesktopUserDirs(t)
	resetPanelHeadsScanForTest(t)
	dir := desktopSessionDir(globalWorkspaceRoot())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := desktopHealFixture(t, dir, "cache1019.jsonl", 2)

	first, degraded, err := panelSessionHeads(path)
	if err != nil || degraded {
		t.Fatalf("first read must replay: degraded=%v err=%v", degraded, err)
	}
	if len(first) < 2 {
		t.Fatalf("fixture heads missing: %d", len(first))
	}
	if hits := panelHeadsHits(); hits != 0 {
		t.Fatalf("first read must be a miss, hits=%d", hits)
	}

	second, degraded, err := panelSessionHeads(path)
	if err != nil || degraded {
		t.Fatalf("second read must serve from cache: degraded=%v err=%v", degraded, err)
	}
	if len(second) != len(first) || second[0].ID != first[0].ID {
		t.Fatal("cached read must return the same heads")
	}
	if hits := panelHeadsHits(); hits != 1 {
		t.Fatalf("second read must be a cache hit, hits=%d", hits)
	}

	// 文件一变（保存/轮换/修复写回）缓存即失效：键带 size+mtime。键量的是
	// events 日志（重放真正读的文件），所以写它。
	if err := os.WriteFile(store.SessionEventLog(path), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := panelSessionHeads(path); err != nil {
		t.Fatal(err)
	}
	if hits := panelHeadsHits(); hits != 1 {
		t.Fatalf("changed file must invalidate the cache, hits=%d", hits)
	}
}

// Test1019PanelHeadsBudgetDegradesOversizeLog（验收②限流断言）：events 日志
// 超出面板预算时不做全量重放，降级返回且快速结束——大会话不再把设置页挂在
// 重放上；ListRecoveryChains 保留该会话行并带 Degraded 标记。
func Test1019PanelHeadsBudgetDegradesOversizeLog(t *testing.T) {
	t.Setenv("REASONIX_SESSION_LOG", "v2") // pin the DAG route: the flat route is the default since tl-1173 P1
	isolateDesktopUserDirs(t)
	resetPanelHeadsScanForTest(t)
	shrinkPanelHeadsBudgetForTest(t, 1) // 任何 events 日志都超预算
	dir := desktopSessionDir(globalWorkspaceRoot())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := desktopHealFixture(t, dir, "oversize1019.jsonl", 2)

	started := time.Now()
	heads, degraded, err := panelSessionHeads(path)
	elapsed := time.Since(started)
	if err != nil {
		t.Fatal(err)
	}
	if !degraded || heads != nil {
		t.Fatalf("oversize log must degrade: degraded=%v heads=%d", degraded, len(heads))
	}
	// 降级 = 零重放。全量重放连三消息夹具也要毫秒级跑 DAG；这里给 200ms
	// 宽限是为了防「误走重放」，不是性能声明。
	if elapsed > 200*time.Millisecond {
		t.Fatalf("degrade path must not replay: %v", elapsed)
	}

	set, err := NewApp().ListRecoveryChains(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Chains) != 1 || !set.Chains[0].Degraded {
		t.Fatalf("oversize candidate must surface one degraded row: %+v", set.Chains)
	}
	if set.Chains[0].MessageCount != 0 || set.Chains[0].Turns != 0 {
		t.Fatalf("degraded row must carry zero counts: %+v", set.Chains[0])
	}
	if set.LongestPath == set.Chains[0].Path {
		t.Fatal("a degraded row must never be recommended as the longest chain")
	}
}

// Test1019VersionGroupsListStaysZeroReplay（验收②懒加载断言）：设置页加载
// 路径（ListSessionVersionGroups）在目录里有大 gap 会话时仍然零重放——只读
// meta 副车，绝不给面板缓存添重放条目。
func Test1019VersionGroupsListStaysZeroReplay(t *testing.T) {
	t.Setenv("REASONIX_SESSION_LOG", "v2") // pin the DAG route: the flat route is the default since tl-1173 P1
	isolateDesktopUserDirs(t)
	resetPanelHeadsScanForTest(t)
	dir := desktopSessionDir(globalWorkspaceRoot())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := desktopHealFixture(t, dir, "lazy1019.jsonl", 2)

	groups, err := NewApp().ListSessionVersionGroups()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, g := range groups {
		if samePath(g.MainPath, path) {
			found = true
			if g.HeadCount < 2 {
				t.Fatalf("multi-head session must list via meta only: %d", g.HeadCount)
			}
		}
	}
	if !found {
		t.Fatal("multi-head session missing from the zero-replay list")
	}
	panelHeadsCache.Lock()
	grew := len(panelHeadsCache.entries)
	panelHeadsCache.Unlock()
	if grew != 0 {
		t.Fatalf("the list path must stay zero-replay: %d cache entries appeared", grew)
	}
}

// Test1019DesktopRecoveryChainsSamplePanel（验收①）：真实 20261019 备份六件
// 下，面板列表 + 链枚举 + 缓存复用全部成立——8 heads 可见、选中 01M51VB、
// 建议链 01M59JNK（7087/120）、第二次展开零重放。
func Test1019DesktopRecoveryChainsSamplePanel(t *testing.T) {
	sample := os.Getenv("TL1019_SAMPLE_DIR")
	if sample == "" {
		t.Skip("TL1019_SAMPLE_DIR not set; sample acceptance runs only against the 20261019 backup")
	}
	isolateDesktopUserDirs(t)
	resetPanelHeadsScanForTest(t)
	root := globalWorkspaceRoot()
	dir := desktopSessionDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(sample)
	if err != nil {
		t.Fatal(err)
	}
	var path string
	for _, e := range entries {
		blob, err := os.ReadFile(filepath.Join(sample, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, e.Name()), blob, 0o600); err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(e.Name(), ".jsonl") && !strings.HasSuffix(e.Name(), ".events.jsonl") && !strings.HasSuffix(e.Name(), ".turns.jsonl") {
			path = filepath.Join(dir, e.Name())
		}
	}
	if path == "" {
		t.Fatal("sample must contain the materialized transcript")
	}
	app := NewApp()
	activateWorkspaceRootForTest(t, app, root)

	groups, err := app.ListSessionVersionGroups()
	if err != nil {
		t.Fatal(err)
	}
	var group *SessionVersionGroupView
	for i := range groups {
		if samePath(groups[i].MainPath, path) {
			group = &groups[i]
		}
	}
	if group == nil {
		t.Fatalf("incident session must appear in the panel list: %d groups", len(groups))
	}
	if group.HeadCount != 8 {
		t.Errorf("panel head count = %d, want 8", group.HeadCount)
	}
	if group.Selected != "01M51VBWNBR0MXNBPJ25SNRYHC" {
		t.Errorf("panel selected head = %q, want 01M51VB chain", group.Selected)
	}

	// 第一次展开：全量重放（真实样本约 2s），8 条链齐、建议 = 更长且更新的
	// 01M59JNK、降级标记不出现。
	set, err := app.ListRecoveryChains(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Chains) != 8 {
		t.Errorf("chains = %d, want 8 (样本任务书读数)", len(set.Chains))
	}
	if set.LongestHead != "01M59JNKEEW3CEQC16XTX035TW" {
		t.Errorf("recommended chain = %q, want the 01M59JNK chain", set.LongestHead)
	}
	for _, c := range set.Chains {
		if c.Degraded {
			t.Errorf("sample fits the budget; chain %s must not degrade", c.HeadID)
		}
	}
	var winner *RecoveryChainView
	for i := range set.Chains {
		if set.Chains[i].HeadID == set.LongestHead {
			winner = &set.Chains[i]
		}
	}
	if winner == nil {
		t.Fatal("the recommended 01M59JNK chain must be present in the set")
	}
	if winner.Turns != 120 {
		t.Errorf("winner turns = %d, want 120 (样本读数)", winner.Turns)
	}

	// 第二次展开：缓存命中——「展开→收起→再展开」「切换选中头」不再重付
	// 全额重放，这是 1019 的核心收益。
	if hits := panelHeadsHits(); hits != 0 {
		t.Fatalf("sample first expand must be a replay miss, hits=%d", hits)
	}
	set2, err := app.ListRecoveryChains(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(set2.Chains) != len(set.Chains) || set2.LongestHead != set.LongestHead {
		t.Fatal("cached read must return the same chain set")
	}
	if hits := panelHeadsHits(); hits != 1 {
		t.Fatalf("sample second expand must be a cache hit, hits=%d", hits)
	}
}

// Test1039PanelHeadsReplayLimitErrorDegrades（任务 1039 验收①②）：
// 重放内部撞资源上限（SessionReplayLimitError）时，面板不再整页失败——
// panelSessionHeads 降级返回 degraded=true，ListRecoveryChains 保留
// Degraded 行、计数全零、不推荐为最长链。
//
// 20261021 GC开发专员条目（49MB events 日志 / head_count=2）实测形态：
// 1019 的降级只拦「文件大小 > 面板预算（256MB）」这一层，而真正的硬拒
// 发生在重放内部（replaySessionDAG 的资源上限），错误一路传到前端
// ListRecoveryChains reject ⇒ 整页「设置加载失败。重试」。这条测试把
// 重放上限收窄到夹具尺寸之下，钉住「重放被拒 ⇒ 降级行」而不是「整页挂」。
func Test1039PanelHeadsReplayLimitErrorDegrades(t *testing.T) {
	t.Skip("tl1236 DAG 读退役善后(1227 后续): 重放上限降级面依赖面板 heads 重放真实发生——tl-1173 P1 起新会话扁平、tl1227 ListSessionHeads 垫片恒空后面板无重放可拒（重放仅存于写侧/迁移器），场景不可达，显式退役非静默")
	isolateDesktopUserDirs(t)
	resetPanelHeadsScanForTest(t)
	// 面板预算放行（夹具远小于 256MB），但把重放上限收窄到夹具之下：
	// 重放走到 st.size > limits.maxBytes ⇒ SessionReplayLimitError。
	shrinkPanelHeadsBudgetForTest(t, 1<<30)

	dir := desktopSessionDir(globalWorkspaceRoot())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// 夹具先建（正常预算），再收窄重放上限——夹具自身的 Save 也要重放，
	// 预先收窄会让夹具都建不出来。
	path := desktopHealFixture(t, dir, "replaylimit1039.jsonl", 2)

	limits := agent.DefaultSessionReplayLimitsForTest()
	limits.MaxBytes = 8
	t.Cleanup(agent.SetSessionReplayLimitsForTest(limits))

	heads, degraded, err := panelSessionHeads(path)
	if err != nil {
		t.Fatalf("replay-limit refusal must degrade, not error: %v", err)
	}
	if !degraded || heads != nil {
		t.Fatalf("replay-limit refusal must degrade: degraded=%v heads=%d", degraded, len(heads))
	}

	set, err := NewApp().ListRecoveryChains(path)
	if err != nil {
		t.Fatalf("ListRecoveryChains must not fail on a replay-limit refusal: %v", err)
	}
	if len(set.Chains) != 1 || !set.Chains[0].Degraded {
		t.Fatalf("refused candidate must surface one degraded row: %+v", set.Chains)
	}
	if set.Chains[0].MessageCount != 0 || set.Chains[0].Turns != 0 {
		t.Fatalf("degraded row must carry zero counts: %+v", set.Chains[0])
	}
	if set.LongestPath == set.Chains[0].Path {
		t.Fatal("a degraded row must never be recommended as the longest chain")
	}
}
