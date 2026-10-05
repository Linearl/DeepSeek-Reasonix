package memory

import (
	"sync"
	"testing"
)

// The index and the direct store scan must be indistinguishable: same hits,
// same shadow, same suppression reasons.
func TestSetAutoRecallMatchesStoreScan(t *testing.T) {
	store := recallTestStore(t)
	recallTestWrite(t, store.Dir, Memory{
		ID: "mem-a", Name: "fast-check", Title: "Fast check command",
		Description: "The canonical fast check", Type: TypeProject, Scope: FactScopeProject,
		Body: "Run make check-fast before pushing.",
	})
	set := &Set{Store: store}
	for _, query := range []string{"what is the fast check command", "continue", "总结一下"} {
		direct := AutoRecall(store, query, RecallOptions{})
		indexed := set.AutoRecall(query, RecallOptions{})
		if len(direct.Hits) != len(indexed.Hits) || direct.Suppressed != indexed.Suppressed ||
			len(direct.ShadowHits) != len(indexed.ShadowHits) {
			t.Fatalf("index diverged from scan for %q: direct=%+v indexed=%+v", query, direct, indexed)
		}
	}
	if hits := set.AutoRecall("fast check command", RecallOptions{}).Hits; len(hits) != 1 {
		t.Fatalf("indexed recall should hit: %+v", hits)
	}

	var nilSet *Set
	if r := nilSet.AutoRecall("anything relevant", RecallOptions{}); r.Suppressed == "" {
		t.Fatalf("nil set must suppress, got %+v", r)
	}
}

// Writes rebuild the snapshot (and with it the index) through memory.Load —
// the invalidation contract that keeps Markdown the source of truth.
func TestLoadRebuildsIndexAfterWrite(t *testing.T) {
	root := t.TempDir()
	user, proj := root+"/user", root+"/project"
	mustMkdir(t, proj+"/.git")
	set := Load(Options{CWD: proj, UserDir: user})
	if r := set.AutoRecall("release branch for hotfixes", RecallOptions{}); len(r.Hits) != 0 {
		t.Fatalf("empty store recalled: %+v", r)
	}
	if _, err := set.Store.Save(Memory{Name: "release-branch", Description: "current release branch",
		Body: "Hotfixes target the release branch release/1.21."}); err != nil {
		t.Fatal(err)
	}
	reloaded := Load(Options{CWD: proj, UserDir: user})
	if r := reloaded.AutoRecall("release branch for hotfixes", RecallOptions{}); len(r.Hits) != 1 {
		t.Fatalf("reloaded index missed the new fact: %+v", r)
	}
}

// 378B1 懒构建契约：Load 不再预建索引（boot/压缩重建/每次记忆写入因此不再
// 付全量读取+分词的成本）；首次召回构建并缓存；后续召回复用同一索引。
func TestLoadDefersRecallIndexBuildUntilFirstRecall(t *testing.T) {
	root := t.TempDir()
	user, proj := root+"/user", root+"/project"
	mustMkdir(t, proj+"/.git")
	store := StoreFor(user, proj)
	if _, err := store.Save(Memory{Name: "release-branch", Description: "current release branch",
		Body: "Hotfixes target the release branch release/1.21."}); err != nil {
		t.Fatal(err)
	}
	set := Load(Options{CWD: proj, UserDir: user})
	if set.recall != nil || set.recallBuilt {
		t.Fatalf("Load must not build the recall index eagerly (built=%v)", set.recallBuilt)
	}
	if r := set.AutoRecall("release branch for hotfixes", RecallOptions{}); len(r.Hits) != 1 {
		t.Fatalf("first recall should hit the fact: %+v", r)
	}
	if !set.recallBuilt || set.recall == nil {
		t.Fatalf("first recall must build and cache the index")
	}
	cached := set.recall
	if r := set.AutoRecall("release branch for hotfixes", RecallOptions{}); len(r.Hits) != 1 {
		t.Fatalf("cached recall should still hit: %+v", r)
	}
	if set.recall != cached {
		t.Fatalf("second recall must reuse the cached index")
	}
}

// 空库：懒构建得到 nil 索引后记为已构建——后续召回不再重扫磁盘。改造前
// 空库每轮召回都要重新 ListAll，这里把该隐性 IO 一并钉住。
func TestRecallIndexEmptyStoreBuildsOnce(t *testing.T) {
	root := t.TempDir()
	user, proj := root+"/user", root+"/project"
	mustMkdir(t, proj+"/.git")
	set := Load(Options{CWD: proj, UserDir: user})
	if r := set.AutoRecall("anything relevant", RecallOptions{}); r.Suppressed == "" {
		t.Fatalf("empty store must suppress: %+v", r)
	}
	if !set.recallBuilt || set.recall != nil {
		t.Fatalf("empty store should cache the nil index once (built=%v)", set.recallBuilt)
	}
}

// 失效即换快照：旧快照已缓存索引后发生写入，写入路径 Load 出的新快照首召
// 即见新事实；旧快照保持不可变，不会漏进新事实。
func TestRecallIndexInvalidationIsSnapshotSwap(t *testing.T) {
	root := t.TempDir()
	user, proj := root+"/user", root+"/project"
	mustMkdir(t, proj+"/.git")
	store := StoreFor(user, proj)
	if _, err := store.Save(Memory{Name: "release-branch", Description: "current release branch",
		Body: "Hotfixes target the release branch release/1.21."}); err != nil {
		t.Fatal(err)
	}
	set := Load(Options{CWD: proj, UserDir: user})
	if r := set.AutoRecall("release branch for hotfixes", RecallOptions{}); len(r.Hits) != 1 {
		t.Fatalf("pre-write recall should hit: %+v", r)
	}
	if _, err := set.Store.Save(Memory{Name: "package-manager", Description: "which tool installs deps",
		Body: "Use pnpm to install dependencies in this repository."}); err != nil {
		t.Fatal(err)
	}
	if r := set.AutoRecall("which tool installs dependencies", RecallOptions{}); len(r.Hits) != 0 {
		t.Fatalf("old snapshot must stay immutable after a write: %+v", r.Hits)
	}
	reloaded := Load(Options{CWD: proj, UserDir: user})
	if r := reloaded.AutoRecall("which tool installs dependencies", RecallOptions{}); len(r.Hits) != 1 {
		t.Fatalf("post-write snapshot must see the new fact on first recall: %+v", r)
	}
}

// 并发首召：多个 goroutine 同时打同一快照的懒构建路径，-race 下无数据竞争、
// 结果一致。
func TestSetAutoRecallConcurrentFirstBuild(t *testing.T) {
	root := t.TempDir()
	user, proj := root+"/user", root+"/project"
	mustMkdir(t, proj+"/.git")
	store := StoreFor(user, proj)
	if _, err := store.Save(Memory{Name: "release-branch", Description: "current release branch",
		Body: "Hotfixes target the release branch release/1.21."}); err != nil {
		t.Fatal(err)
	}
	set := Load(Options{CWD: proj, UserDir: user})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				if r := set.AutoRecall("release branch for hotfixes", RecallOptions{}); len(r.Hits) != 1 {
					t.Errorf("concurrent recall diverged: %+v", r)
					return
				}
			}
		}()
	}
	wg.Wait()
}
