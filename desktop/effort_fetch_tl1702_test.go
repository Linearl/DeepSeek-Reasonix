package main

// tl1702 验收：压缩期的 effort 刷新降级噪音归零。
//
// 现场（20261119 板面 00:4x）：压缩重活期 effort_fetch.go 的 2s 有界读
// （task 421）排队超时 → 降级 default=auto + "effort read timed out" notice
// ——压缩全程刷 notice，而 effort 值本身没有错。修复：压缩 pass 在飞时
// 跳过同步刷新直接用缓存（无缓存用既有 default），全程不发 notice；后台
// single-flight refresh 照旧养缓存，压缩结束后读数走正常 task-421 路径。
// 2s 超时本体与 421 防卡读语义不动（非压缩读逐字节不变）。
//
// 钉三件事：
// ① 压缩期 + 缓存在（fast path 已过期）：立即返回缓存值、零 notice、
//    不碰有界读（挂死源也不触发超时分支）；
// ② 压缩期 + 冷缓存：立即返回 default（auto/unsupported）、零 notice；
// ③ 压缩结束：正常路径恢复，读到源的新值（压缩后 effort 显示仍正确）。

import (
	"testing"
	"time"
)

// effortCompactionCtrl is the fixture controller: the shared stubSessionAPI
// plus the tl1702 probe the effort fetch consults.
type effortCompactionCtrl struct {
	stubSessionAPI
	running bool
}

func (c effortCompactionCtrl) CompactionRunning() bool { return c.running }

func TestEffortForTabDuringCompactionServesCacheWithoutNotice(t *testing.T) {
	f := newEffortFetchFixture(t)
	f.app.effortReadStub = func(string) EffortInfo { return effortInfoFor("high") }
	if got := f.app.EffortForTab(f.tab.ID); got.Current != "high" {
		t.Fatalf("priming read current = %q, want high", got.Current)
	}

	// 压缩挂上 + fast path 过期（stale-serve 窗口缩到 0）：无 tl1702 时这里
	// 会走有界读并被挂死源拖到超时 → 缓存回退 + notice；有 tl1702 时同步
	// 刷新被整个跳过。
	f.tab.Ctrl = effortCompactionCtrl{stubSessionAPI{}, true}
	f.app.effortStaleServeAgeOverride = time.Millisecond
	time.Sleep(5 * time.Millisecond)
	f.app.effortReadStub = func(string) EffortInfo {
		time.Sleep(2 * time.Second) // 挂死源：有界读必超时；压缩分支根本不调它
		return effortInfoFor("low")
	}

	started := time.Now()
	got := f.app.EffortForTab(f.tab.ID)
	if elapsed := time.Since(started); elapsed >= f.app.effortReadLimit() {
		t.Fatalf("EffortForTab took %s during compaction, want an instant cache serve", elapsed)
	}
	if got.Current != "high" {
		t.Fatalf("compaction-phase current = %q, want the cached high", got.Current)
	}
	f.assertNoNotice(t)
}

func TestEffortForTabDuringCompactionColdCacheServesDefaultWithoutNotice(t *testing.T) {
	f := newEffortFetchFixture(t)
	f.tab.Ctrl = effortCompactionCtrl{stubSessionAPI{}, true}
	f.app.effortReadStub = func(string) EffortInfo {
		time.Sleep(2 * time.Second)
		return effortInfoFor("high")
	}

	started := time.Now()
	got := f.app.EffortForTab(f.tab.ID)
	if elapsed := time.Since(started); elapsed >= f.app.effortReadLimit() {
		t.Fatalf("EffortForTab took %s on a cold cache during compaction, want an instant default", elapsed)
	}
	if got.Supported {
		t.Fatal("compaction-phase default must not claim support")
	}
	if got.Current != "auto" || len(got.Levels) != 0 {
		t.Fatalf("compaction-phase default = %+v, want Current auto with empty levels", got)
	}
	f.assertNoNotice(t)
}

func TestEffortForTabAfterCompactionReadsNormally(t *testing.T) {
	f := newEffortFetchFixture(t)
	f.tab.Ctrl = effortCompactionCtrl{stubSessionAPI{}, true}
	f.app.effortReadStub = func(string) EffortInfo { return effortInfoFor("low") }
	if got := f.app.EffortForTab(f.tab.ID); got.Supported {
		t.Fatalf("cold-cache compaction read = %+v, want the unsupported default", got)
	}

	// 压缩结束：正常路径恢复。fast path 过期绕开后台 refresh 可能已落的
	// 缓存，直接读源的新值。
	f.tab.Ctrl = effortCompactionCtrl{stubSessionAPI{}, false}
	f.app.effortStaleServeAgeOverride = time.Millisecond
	time.Sleep(5 * time.Millisecond)
	f.app.effortReadStub = func(string) EffortInfo { return effortInfoFor("high") }
	got := f.app.EffortForTab(f.tab.ID)
	if got.Current != "high" {
		t.Fatalf("post-compaction current = %q, want the fresh high from the normal path", got.Current)
	}
	f.assertNoNotice(t)
}
