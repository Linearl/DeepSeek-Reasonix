package topicregistry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 测试源视图助手：desktop-projects.json 的极简投影。
func view(global []string, projects ...ProjectTopics) func() SourceView {
	return func() SourceView {
		return SourceView{Global: global, Projects: projects}
	}
}

func withTempCache(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "topic-registry.json")
	return path
}

func mustStat(t *testing.T, path string) (int64, time.Time) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.Size(), info.ModTime()
}

// TestDedupeUniqueOwnership 钉唯一归属裁决：跨 scope 重复（1074 家族遗留
// 形态）判给 project，global 侧为影子；墓碑条目不出门。
func TestDedupeUniqueOwnership(t *testing.T) {
	got := Dedupe(SourceView{
		Global:       []string{"topicA", "topicDup", "", "  "},
		GlobalPinned: []string{"topicA"},
		Projects: []ProjectTopics{
			{Root: `D:\proj`, Topics: []string{"topicDup", "topicB"}, Pinned: []string{"topicB"}},
		},
		Deleted: map[string]bool{"topicDead": true},
	})
	byID := map[string]Entry{}
	for _, e := range got {
		if _, dup := byID[e.TopicID]; dup {
			t.Fatalf("topic %q produced more than one entry: %+v", e.TopicID, got)
		}
		byID[e.TopicID] = e
	}
	if len(byID) != 3 {
		t.Fatalf("want 3 unique entries, got %d: %+v", len(byID), got)
	}
	if e := byID["topicA"]; e.Scope != ScopeGlobal || !e.Pinned {
		t.Fatalf("topicA want global+pinned, got %+v", e)
	}
	// 跨 scope 重复：project 赢（global 行是影子）。
	if e := byID["topicDup"]; e.Scope != ScopeProject {
		t.Fatalf("topicDup want project ownership, got %+v", e)
	}
	if e := byID["topicB"]; e.Scope != ScopeProject || e.Root == "" || !e.Pinned {
		t.Fatalf("topicB want project+pinned, got %+v", e)
	}
	if _, ok := byID["topicDead"]; ok {
		t.Fatalf("tombstoned topic leaked into the index: %+v", got)
	}
}

// TestFormatVersionGate 钉 format 版本门：未知格式缓存整份丢弃（视为缺失），
// 当前格式正常装载。
func TestFormatVersionGate(t *testing.T) {
	dir := t.TempDir()
	cache := filepath.Join(dir, "topic-registry.json")

	writeCache := func(format int) {
		t.Helper()
		b, err := json.Marshal(cacheFile{Format: format, Entries: []Entry{{TopicID: "topicX", Scope: ScopeGlobal}}})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(cache, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// 当前格式：装载成功。
	writeCache(FormatVersion)
	ResetForTest()
	Configure(cache, "", nil)
	if _, ok := OwnerOf("topicX"); !ok {
		t.Fatal("current-format cache should serve topicX")
	}

	// 未知格式（未来版本/降级读新）：版本门丢弃，恒 miss、不报错。
	writeCache(FormatVersion + 99)
	ResetForTest()
	Configure(cache, "", nil)
	if _, ok := OwnerOf("topicX"); ok {
		t.Fatal("unknown-format cache must be discarded by the format gate")
	}
}

// TestStampExpiryRebuildBeforeQuery 钉 stamp 过期 + 重建先于直查：
// 权威源文件变更后，第一次 OwnerOf 必须重建（读到新归属），不得服旧缓存。
func TestStampExpiryRebuildBeforeQuery(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "desktop-projects.json")
	cache := filepath.Join(dir, "topic-registry.json")

	projects := []ProjectTopics{{Root: `D:\proj`, Topics: []string{"topicA"}}}
	Configure(cache, source, view([]string{"topicG"}, projects...))

	if e, ok := OwnerOf("topicA"); !ok || e.Scope != ScopeProject {
		t.Fatalf("initial ownership wrong: %+v ok=%v", e, ok)
	}

	// 权威源变更：topicA 从项目挪回 global（模拟一次外部/降级写）。
	Configure(cache, source, view([]string{"topicA"}, []ProjectTopics{{Root: `D:\proj`, Topics: []string{"topicB"}}}...))
	if err := os.WriteFile(source, []byte(`{"changed":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// 不调 Refresh，直接直查——重建必须发生在应答之前。
	if e, ok := OwnerOf("topicA"); !ok || e.Scope != ScopeGlobal {
		t.Fatalf("stale index served: %+v ok=%v (rebuild must precede the query)", e, ok)
	}
	if _, ok := OwnerOf("topicB"); !ok {
		t.Fatal("topicB missing after rebuild")
	}
}

// TestUnchangedSourceServesWarmIndex 钉 warm path：源未变时直查不重建
// （Refresh 返回 false），服务端读不付重建成本。
func TestUnchangedSourceServesWarmIndex(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "desktop-projects.json")
	if err := os.WriteFile(source, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	Configure(filepath.Join(dir, "topic-registry.json"), source, view([]string{"topicA"}))
	if !Refresh() {
		t.Fatal("first refresh after configure must rebuild")
	}
	if Refresh() {
		t.Fatal("unchanged source must not rebuild")
	}
}

// TestAtomicCacheWriteNoTmpLeftover 钉原子写：缓存落盘后不残留 .tmp 中间件，
// 文件内容是完整 JSON（可解析、格式正确）。
func TestAtomicCacheWriteNoTmpLeftover(t *testing.T) {
	dir := t.TempDir()
	cache := filepath.Join(dir, "topic-registry.json")
	Configure(cache, "", view([]string{"topicA"}))
	if err := Register(ScopeGlobal, "", "topicA", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cache + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("tmp file must not survive an atomic write: %v", err)
	}
	b, err := os.ReadFile(cache)
	if err != nil {
		t.Fatalf("cache file missing: %v", err)
	}
	var cf cacheFile
	if err := json.Unmarshal(b, &cf); err != nil {
		t.Fatalf("cache file is not complete JSON: %v", err)
	}
	if cf.Format != FormatVersion || len(cf.Entries) != 1 || cf.Entries[0].TopicID != "topicA" {
		t.Fatalf("unexpected cache content: %+v", cf)
	}
	_, mtime := mustStat(t, cache)
	if time.Since(mtime) > time.Minute {
		t.Fatalf("cache file unexpectedly stale: %v", mtime)
	}
}

// TestScaleThresholdSkipsPersist 钉规模阈值：条目超限时跳过缓存落盘
// （磁盘上不出现缓存文件），内存索引照常应答。
func TestScaleThresholdSkipsPersist(t *testing.T) {
	dir := t.TempDir()
	cache := filepath.Join(dir, "topic-registry.json")
	big := make([]string, MaxPersistEntries+1)
	for i := range big {
		big[i] = "topic-" + itoa(i)
	}
	Configure(cache, "", view(big))
	if got := len(Snapshot()); got != MaxPersistEntries+1 {
		t.Fatalf("in-memory index must serve beyond threshold, got %d", got)
	}
	if _, err := os.Stat(cache); !os.IsNotExist(err) {
		t.Fatalf("cache file must be skipped beyond the scale threshold: %v", err)
	}
}

// TestRegisterMoveAcrossScopes 钉写口语义：跨 scope 重新登记 = 移动
// （唯一归属随之迁移），旧 scope 不残留。
func TestRegisterMoveAcrossScopes(t *testing.T) {
	dir := t.TempDir()
	cache := filepath.Join(dir, "topic-registry.json")
	Configure(cache, "", view(nil))

	if err := Register(ScopeGlobal, "", "topicA", false); err != nil {
		t.Fatal(err)
	}
	if err := Register(ScopeProject, `D:\proj`, "topicA", true); err != nil {
		t.Fatal(err)
	}
	snap := Snapshot()
	if len(snap) != 1 {
		t.Fatalf("unique ownership violated: %+v", snap)
	}
	if snap[0].Scope != ScopeProject || !snap[0].Pinned {
		t.Fatalf("move did not land on project: %+v", snap[0])
	}
	if err := Unregister("topicA"); err != nil {
		t.Fatal(err)
	}
	if _, ok := OwnerOf("topicA"); ok {
		t.Fatal("unregister left the id addressable")
	}
	// 幂等：再注销一次不报错。
	if err := Unregister("topicA"); err != nil {
		t.Fatal(err)
	}
}

// TestUnconfiguredPackageMisses 钉 headless 兜底：未 Configure 时（无源、
// 无缓存）直查恒 miss 且不 panic——读侧回落既有启发式的契约。
func TestUnconfiguredPackageMisses(t *testing.T) {
	ResetForTest()
	if _, ok := OwnerOf("anything"); ok {
		t.Fatal("unconfigured package must miss")
	}
	if len(Snapshot()) != 0 {
		t.Fatal("unconfigured snapshot must be empty")
	}
	if err := Register(ScopeGlobal, "", "x", false); err == nil {
		t.Fatal("register without configure has no rebuild source and must refuse")
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}
