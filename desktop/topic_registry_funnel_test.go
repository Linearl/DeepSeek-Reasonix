package main

import (
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/topicregistry"
)

// tl1208 写口唯二收点的漏斗专测：登记/注销/置顶/重排四路都过收点，
// 且收点在改盘后同步 internal/topicregistry 唯一归属索引（读侧直查可见）。

// TestRegisterTopicFunnelRegistersAndSyncsIndex 钉：registerTopic 落盘的
// 同时索引可见（OwnerOf 直查命中，scope/root/pinned 与登记一致）。
func TestRegisterTopicFunnelRegistersAndSyncsIndex(t *testing.T) {
	isolateDesktopUserDirs(t)
	projectRoot := t.TempDir()
	topicID := "topic_funnel_register"

	if err := registerTopic("global", "", topicRegisterOpts{}, topicID); err != nil {
		t.Fatalf("register global: %v", err)
	}
	f := loadProjectsFile()
	if !containsDesktopString(f.GlobalTopics, topicID) {
		t.Fatalf("global topics = %#v, want %q", f.GlobalTopics, topicID)
	}
	owner, ok := topicRegistryOwnerOf(topicID)
	if !ok || owner.Scope != topicregistry.ScopeGlobal {
		t.Fatalf("index owner = %+v ok=%v, want global", owner, ok)
	}

	if err := registerTopic("project", projectRoot, topicRegisterOpts{ensureProject: true}, topicID); err != nil {
		t.Fatalf("register project: %v", err)
	}
	owner, ok = topicRegistryOwnerOf(topicID)
	// 唯一归属：tl1262 起 project 登记在写口同事务收敛 global 侧注册
	// （「遗留重复判 project」的裁决落到持久真相源，1074 家族影子不再落
	// 盘）；索引层继续由唯一归属 map 结构保证一 id 一行。
	if !ok || owner.Scope != topicregistry.ScopeProject || !sameProjectRoot(owner.Root, projectRoot) {
		t.Fatalf("index owner = %+v ok=%v, want project %q (unique ownership)", owner, ok, projectRoot)
	}
	if snap := topicregistry.Snapshot(); len(snap) != 1 || snap[0].TopicID != topicID {
		t.Fatalf("snapshot must hold exactly one row per topicId, got %+v", snap)
	}
}

// TestRegisterTopicRespectsTombstones 钉：respectTombstones 批量补登不复活
// 墓碑；默认登记（显式）清墓碑复活。
func TestRegisterTopicRespectsTombstones(t *testing.T) {
	isolateDesktopUserDirs(t)
	topicID := "topic_funnel_tombstone"
	if err := registerTopic("global", "", topicRegisterOpts{}, topicID); err != nil {
		t.Fatalf("seed register: %v", err)
	}
	if err := unregisterTopic(topicID, topicUnregisterOpts{tombstone: true}); err != nil {
		t.Fatalf("unregister: %v", err)
	}
	f := loadProjectsFile()
	if !containsDesktopString(f.DeletedTopics, topicID) {
		t.Fatalf("deletedTopics = %#v, want tombstone", f.DeletedTopics)
	}

	// 扫描补登（respectTombstones）：不复活。
	if err := registerTopic("global", "", topicRegisterOpts{respectTombstones: true}, topicID); err != nil {
		t.Fatalf("batch register: %v", err)
	}
	f = loadProjectsFile()
	if containsDesktopString(f.GlobalTopics, topicID) {
		t.Fatalf("scan register resurrected a tombstoned topic: %#v", f.GlobalTopics)
	}

	// 显式登记（恢复/新建语义）：清墓碑复活。
	if err := registerTopic("global", "", topicRegisterOpts{}, topicID); err != nil {
		t.Fatalf("explicit register: %v", err)
	}
	f = loadProjectsFile()
	if !containsDesktopString(f.GlobalTopics, topicID) || containsDesktopString(f.DeletedTopics, topicID) {
		t.Fatalf("explicit register must clear the tombstone: topics=%#v deleted=%#v", f.GlobalTopics, f.DeletedTopics)
	}
}

// TestUnregisterTopicScopedNoTombstone 钉：空壳清理语义（onlyScope + 不写
// 墓碑）——只摘自己 scope，其它 scope 注册保留，不产生墓碑。
func TestUnregisterTopicScopedNoTombstone(t *testing.T) {
	isolateDesktopUserDirs(t)
	projectRoot := t.TempDir()
	topicID := "topic_funnel_shell"
	if err := registerTopic("global", "", topicRegisterOpts{}, topicID); err != nil {
		t.Fatalf("register global: %v", err)
	}
	// tl1262 起 project 登记会在写口收敛 global 侧注册，双行前置改由测试
	// 直写（守卫只扫生产文件），让本测试继续只钉 scoped 清理语义：只摘自
	// 己 scope，其它 scope 注册保留，不产生墓碑。
	if err := updateProjectsFile(func(f *desktopProjectFile) (bool, error) {
		f.Projects = append(f.Projects, desktopProject{Root: projectRoot, Topics: []string{topicID}})
		return true, nil
	}); err != nil {
		t.Fatalf("seed project registration: %v", err)
	}

	if err := unregisterTopic(topicID, topicUnregisterOpts{
		onlyScope: &topicScopeRef{scope: "project", root: projectRoot},
	}); err != nil {
		t.Fatalf("scoped unregister: %v", err)
	}
	f := loadProjectsFile()
	if !containsDesktopString(f.GlobalTopics, topicID) {
		t.Fatalf("global registration must survive a scoped cleanup: %#v", f.GlobalTopics)
	}
	if containsDesktopString(f.DeletedTopics, topicID) {
		t.Fatalf("scoped shell cleanup must not write a tombstone: %#v", f.DeletedTopics)
	}
	for _, p := range f.Projects {
		if sameProjectRoot(p.Root, projectRoot) && containsDesktopString(p.Topics, topicID) {
			t.Fatalf("project registration should be removed: %#v", p.Topics)
		}
	}
	if _, ok := topicRegistryOwnerOf(topicID); !ok {
		t.Fatalf("global registration must stay addressable after scoped cleanup")
	}
}

// TestRegisterTopicPinUpdateInPlace 钉：置顶翻转走 pinUpdate 模式——置顶态
// 落盘，成员集合与顺序不动（对比前置插入会把 id 挪到列表头）。
func TestRegisterTopicPinUpdateInPlace(t *testing.T) {
	isolateDesktopUserDirs(t)
	topicID := "topic_funnel_pin"
	if err := registerTopic("global", "", topicRegisterOpts{}, topicID); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := registerTopic("global", "", topicRegisterOpts{}, "topic_funnel_pin_later"); err != nil {
		t.Fatalf("register later: %v", err)
	}
	f := loadProjectsFile()
	orderBefore := append([]string(nil), f.GlobalTopics...)

	if err := registerTopic("global", "", topicRegisterOpts{pinUpdate: true, pinned: true}, topicID); err != nil {
		t.Fatalf("pin: %v", err)
	}
	f = loadProjectsFile()
	if !containsDesktopString(f.GlobalPinnedTopics, topicID) {
		t.Fatalf("pinned topics = %#v, want %q", f.GlobalPinnedTopics, topicID)
	}
	if !sameStringList(f.GlobalTopics, orderBefore) {
		t.Fatalf("pin update must not reorder members: before=%#v after=%#v", orderBefore, f.GlobalTopics)
	}
	owner, ok := topicRegistryOwnerOf(topicID)
	if !ok || !owner.Pinned {
		t.Fatalf("index pinned state = %+v ok=%v, want pinned", owner, ok)
	}

	if err := registerTopic("global", "", topicRegisterOpts{pinUpdate: true, pinned: false}, topicID); err != nil {
		t.Fatalf("unpin: %v", err)
	}
	if f := loadProjectsFile(); containsDesktopString(f.GlobalPinnedTopics, topicID) {
		t.Fatalf("unpin left the topic pinned: %#v", f.GlobalPinnedTopics)
	}
}

// TestReorderTopicOrderIsPermutation 钉：重排收口是保序置换——成员集合
// 不变（缺项补尾），未知/重复 id 拒绝。
func TestReorderTopicOrderIsPermutation(t *testing.T) {
	isolateDesktopUserDirs(t)
	if err := registerTopic("global", "", topicRegisterOpts{}, "topic_r1", "topic_r2", "topic_r3"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := reorderTopicOrder("global", "", []string{"topic_r3", "topic_r1"}); err != nil {
		t.Fatalf("reorder: %v", err)
	}
	f := loadProjectsFile()
	want := []string{"topic_r3", "topic_r1", "topic_r2"}
	if !sameStringList(f.GlobalTopics, want) {
		t.Fatalf("order = %#v, want %#v (omitted id re-appended)", f.GlobalTopics, want)
	}
	if err := reorderTopicOrder("global", "", []string{"topic_unknown"}); err == nil {
		t.Fatal("unknown id must be rejected")
	}
	if err := reorderTopicOrder("global", "", []string{"topic_r1", "topic_r1"}); err == nil {
		t.Fatal("duplicate id must be rejected")
	}
}

// TestFunnelSyncSurvivesExternalEdit 钉 stamp 过期重建先于直查（desktop
// 接线面）：绕过收点直接改权威文件（另一进程/降级写形态）后，下一次直查
// 不服旧索引——ensureFresh 就地重建再应答；缓存文件带 format 门字段落盘。
func TestFunnelSyncSurvivesExternalEdit(t *testing.T) {
	isolateDesktopUserDirs(t)
	if err := registerTopic("global", "", topicRegisterOpts{}, "topic_warm"); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, ok := topicRegistryOwnerOf("topic_warm"); !ok {
		t.Fatal("warm index must know the registered topic")
	}
	// 绕过收点的外部写：直接把 topic_ext 挂进 global 列表并原子落盘。
	if err := updateProjectsFile(func(f *desktopProjectFile) (bool, error) {
		f.GlobalTopics = append([]string{"topic_external_edit"}, f.GlobalTopics...)
		return true, nil
	}); err != nil {
		t.Fatalf("external write: %v", err)
	}
	// 不调 Refresh，直接直查——重建必须发生在应答之前。
	owner, ok := topicRegistryOwnerOf("topic_external_edit")
	if !ok || owner.Scope != topicregistry.ScopeGlobal {
		t.Fatalf("owner = %+v ok=%v, want global (stale index was served)", owner, ok)
	}
	// 缓存文件落盘且格式可解析（listindex 兜底四要素的原子写面）。
	cachePath := filepath.Join(desktopConfigDir(), topicRegistryCacheFile)
	b, err := readFileUTF8(cachePath)
	if err != nil {
		t.Fatalf("cache file missing: %v", err)
	}
	if !strings.Contains(string(b), `"format":1`) {
		t.Fatalf("cache file lacks the format gate field: %s", string(b))
	}
}
