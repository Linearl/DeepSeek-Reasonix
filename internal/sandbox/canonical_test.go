package sandbox

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// resetCanonicalEngine swaps the package-level seams for one test and
// restores them afterwards. Without it a stubbed evaluator or a shrunken
// budget would leak into sibling tests through the process-wide cache.
func resetCanonicalEngine(t *testing.T) {
	t.Helper()
	canonicalMu.Lock()
	canonicalCache = map[string]canonicalEntry{}
	canonicalFlights = map[string]*canonicalFlight{}
	canonicalMu.Unlock()
	oldEval, oldTimeout, oldTTL := canonicalEvalSymlinks, canonicalResolveTimeout, canonicalCacheTTL
	oldPositiveTTL := canonicalCachePositiveTTL
	t.Cleanup(func() {
		canonicalEvalSymlinks, canonicalResolveTimeout, canonicalCacheTTL = oldEval, oldTimeout, oldTTL
		canonicalCachePositiveTTL = oldPositiveTTL
		canonicalMu.Lock()
		canonicalCache = map[string]canonicalEntry{}
		canonicalFlights = map[string]*canonicalFlight{}
		canonicalMu.Unlock()
	})
}

// captureDefaultLogger redirects the default slog logger into a buffer for
// warning assertions and restores it at cleanup.
func captureDefaultLogger(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// mustSymlinkDir creates link -> target, falling back to a Windows directory
// junction when os.Symlink lacks privilege. Both failures are fatal: a silent
// skip would leave the anti-smuggling behavior untested.
func mustSymlinkDir(t *testing.T, target, link string) {
	t.Helper()
	symErr := os.Symlink(target, link)
	if symErr == nil {
		t.Cleanup(func() { os.Remove(link) })
		return
	}
	if runtime.GOOS != "windows" {
		t.Fatalf("create symlink %q -> %q: %v", link, target, symErr)
	}
	out, jerr := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput()
	if jerr != nil {
		t.Fatalf("create symlink or junction %q -> %q: symlink: %v; mklink: %v (%s)", link, target, symErr, jerr, out)
	}
	t.Cleanup(func() { os.Remove(link) })
}

// TestResolveCanonicalPathTimeoutFallsBackAndWarns pins the task 455fix
// contract: a hung filesystem walk must return the cleaned absolute path
// within the budget, emit exactly one warning, and cache the fallback so
// later callers do not re-enter the walk.
func TestResolveCanonicalPathTimeoutFallsBackAndWarns(t *testing.T) {
	resetCanonicalEngine(t)
	logs := captureDefaultLogger(t)

	walkGate := make(chan struct{})
	var walks int32
	canonicalEvalSymlinks = func(string) (string, error) {
		atomic.AddInt32(&walks, 1)
		<-walkGate // simulate an SMB reconnect that never finishes
		return "", errors.New("unreachable")
	}
	t.Cleanup(func() { close(walkGate) })
	canonicalResolveTimeout = 50 * time.Millisecond
	canonicalCacheTTL = 30 * time.Second

	target := filepath.Join(t.TempDir(), "gone", "deeper")

	start := time.Now()
	got, err := ResolveAbsPath(target)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("ResolveAbsPath returned error on timeout: %v", err)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("timeout fallback took %v, want within budget", elapsed)
	}
	if want := filepath.Clean(target); got != want {
		t.Fatalf("timeout fallback = %q, want cleaned abs %q", got, want)
	}
	// Warning: exactly one, and it names the path.
	if warns := strings.Count(logs.String(), "timed out"); warns != 1 {
		t.Fatalf("timeout produced %d warnings, want exactly 1; log:\n%s", warns, logs.String())
	}
	if !strings.Contains(logs.String(), "path=") || !strings.Contains(logs.String(), filepath.Base(target)) {
		t.Fatalf("warning does not mention the root path; log:\n%s", logs.String())
	}
	// Cache: the second call must be served from the TTL cache, not re-walked.
	got2, err := ResolveAbsPath(target)
	if err != nil {
		t.Fatalf("second ResolveAbsPath: %v", err)
	}
	if got2 != got {
		t.Fatalf("second call = %q, want cached %q", got2, got)
	}
	if n := atomic.LoadInt32(&walks); n != 1 {
		t.Fatalf("fallback result not cached: %d walks for two calls, want 1", n)
	}
}

// TestResolveCanonicalPathCacheTTL pins that results are cached for the TTL
// (success and failure alike) and that expiry triggers a fresh walk. The stub
// is generation-based, not call-count-based: one walk already touches the
// evaluator once per path ancestor.
func TestResolveCanonicalPathCacheTTL(t *testing.T) {
	resetCanonicalEngine(t)

	other := filepath.Join(t.TempDir(), "other")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	var generation, calls int32
	canonicalResolveTimeout = 2 * time.Second
	canonicalCacheTTL = 40 * time.Millisecond

	target := filepath.Join(t.TempDir(), "root", "child")
	fallback := filepath.Clean(target)
	moved := filepath.Join(other, "child")

	// Generation 0 = simulated dead root (every level fails). Generation 1
	// simulates "leaf missing, parent root answers as a symlink to other":
	// the walk must re-append the leaf tail onto the new identity.
	canonicalEvalSymlinks = func(p string) (string, error) {
		atomic.AddInt32(&calls, 1)
		if atomic.LoadInt32(&generation) == 0 {
			return "", errors.New("generation 0: simulated dead root")
		}
		if p == target {
			return "", errors.New("leaf does not exist yet")
		}
		return other, nil
	}

	got1, err := ResolveAbsPath(target)
	if err != nil {
		t.Fatal(err)
	}
	if got1 != fallback {
		t.Fatalf("failed walk should fall back to cleaned abs: got %q want %q", got1, fallback)
	}
	callsAfterFirst := atomic.LoadInt32(&calls)

	got2, err := ResolveAbsPath(target)
	if err != nil {
		t.Fatal(err)
	}
	if got2 != fallback {
		t.Fatalf("TTL cache should serve the failure: got %q want %q", got2, fallback)
	}
	if n := atomic.LoadInt32(&calls); n != callsAfterFirst {
		t.Fatalf("second call within TTL re-walked (%d -> %d evaluator calls), want a cache hit", callsAfterFirst, n)
	}

	atomic.StoreInt32(&generation, 1)  // the "drive" comes back online
	time.Sleep(100 * time.Millisecond) // past TTL

	got3, err := ResolveAbsPath(target)
	if err != nil {
		t.Fatal(err)
	}
	if got3 != moved {
		t.Fatalf("post-TTL call should re-walk and pick up the new identity: got %q want %q", got3, moved)
	}
	if n := atomic.LoadInt32(&calls); n <= callsAfterFirst {
		t.Fatalf("post-TTL call should re-walk (%d calls, was %d)", n, callsAfterFirst)
	}
}

// TestResolveCanonicalPathSingleFlight pins that concurrent callers on the
// same path share one in-flight walk instead of each touching the
// filesystem — the multi-tab concurrent-boot scenario from task 455.
func TestResolveCanonicalPathSingleFlight(t *testing.T) {
	resetCanonicalEngine(t)

	release := make(chan struct{})
	var once sync.Once
	closeRelease := func() { once.Do(func() { close(release) }) }
	t.Cleanup(closeRelease)
	var walks int32
	canonicalEvalSymlinks = func(p string) (string, error) {
		atomic.AddInt32(&walks, 1)
		<-release
		return p, nil
	}
	canonicalResolveTimeout = 5 * time.Second

	target := filepath.Join(t.TempDir(), "shared")
	want := filepath.Clean(target)

	const callers = 8
	results := make([]string, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	startGate := make(chan struct{})
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-startGate
			results[i], errs[i] = ResolveAbsPath(target)
		}(i)
	}
	close(startGate)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && atomic.LoadInt32(&walks) == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if atomic.LoadInt32(&walks) == 0 {
		t.Fatal("no walk started after gate opened")
	}
	time.Sleep(100 * time.Millisecond) // let the remaining callers pile onto the flight
	closeRelease()
	wg.Wait()

	if n := atomic.LoadInt32(&walks); n != 1 {
		t.Fatalf("%d concurrent calls produced %d walks, want exactly 1 (single flight)", callers, n)
	}
	for i := 0; i < callers; i++ {
		if errs[i] != nil {
			t.Fatalf("caller %d: %v", i, errs[i])
		}
		if results[i] != want {
			t.Fatalf("caller %d = %q, want %q", i, results[i], want)
		}
	}
}

// TestResolveCanonicalPathDistinctPathsWalkSeparately guards the flight key:
// different paths must not collide into one shared result.
func TestResolveCanonicalPathDistinctPathsWalkSeparately(t *testing.T) {
	resetCanonicalEngine(t)

	var walks int32
	canonicalEvalSymlinks = func(p string) (string, error) {
		atomic.AddInt32(&walks, 1)
		return p, nil
	}
	base := t.TempDir()
	a := filepath.Join(base, "a")
	b := filepath.Join(base, "b")

	gotA, _ := ResolveAbsPath(a)
	gotB, _ := ResolveAbsPath(b)
	if gotA != filepath.Clean(a) || gotB != filepath.Clean(b) {
		t.Fatalf("paths crossed: %q / %q", gotA, gotB)
	}
	if n := atomic.LoadInt32(&walks); n != 2 {
		t.Fatalf("distinct paths did %d walks, want 2", n)
	}
}

// TestResolveCanonicalPathLocalBehaviorRegression re-checks the pre-existing
// real-filesystem contract of the shared walk (formerly duplicated in
// confine.go realPath and write_path.go ResolveAbsPath).
func TestResolveCanonicalPathLocalBehaviorRegression(t *testing.T) {
	resetCanonicalEngine(t)

	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}

	// Existing directory resolves to itself.
	if got, err := ResolveAbsPath(real); err != nil || got != filepath.Clean(real) {
		t.Fatalf("existing dir = %q, %v", got, err)
	}
	// Not-yet-existing tail re-appends the deepest existing ancestor.
	child := filepath.Join(real, "newfile.txt")
	if got, err := ResolveAbsPath(child); err != nil || got != filepath.Clean(child) {
		t.Fatalf("nonexistent tail = %q, %v", got, err)
	}
	// Deep nonexistent chain falls back to the cleaned abs.
	deep := filepath.Join(base, "no", "such", "dir")
	if got, err := ResolveAbsPath(deep); err != nil || got != filepath.Clean(deep) {
		t.Fatalf("deep nonexistent = %q, %v", got, err)
	}

	// Symlinked directory resolves to the target: the anti-smuggling core.
	link := filepath.Join(base, "link")
	mustSymlinkDir(t, real, link)
	if got, err := ResolveAbsPath(link); err != nil || got != filepath.Clean(real) {
		t.Fatalf("symlink dir = %q, %v, want %q", got, err, filepath.Clean(real))
	}
	// ...including a not-yet-existing file under the symlink.
	underLink := filepath.Join(link, "created-later.txt")
	if got, err := ResolveAbsPath(underLink); err != nil || got != filepath.Clean(filepath.Join(real, "created-later.txt")) {
		t.Fatalf("write under symlink = %q, %v", got, err)
	}

	// Drive-letter casing: the walk must land on the volume-canonical
	// spelling (455 acceptance list), not echo the caller's casing.
	if runtime.GOOS == "windows" {
		lowerCased := strings.ToLower(real)
		want, evalErr := canonicalEvalSymlinks(real)
		if evalErr != nil {
			t.Fatal(evalErr)
		}
		got, err := ResolveAbsPath(lowerCased)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("lower-cased drive input %q resolved to %q, want volume-canonical %q", lowerCased, got, want)
		}
	}
}

// TestResolveAbsPathFreshBypassesCache pins the approval-identity contract:
// the fresh form must observe a root retargeted after the TTL cache warmed up
// (EnsureWriteDir relies on this to reject smuggled re-grants).
func TestResolveAbsPathFreshBypassesCache(t *testing.T) {
	resetCanonicalEngine(t)

	base := t.TempDir()
	real := filepath.Join(base, "real")
	other := filepath.Join(base, "other")
	for _, dir := range []string{real, other} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(base, "link")
	mustSymlinkDir(t, real, link)

	if got, err := ResolveAbsPath(link); err != nil || got != filepath.Clean(real) {
		t.Fatalf("warm cache = %q, %v, want %q", got, err, filepath.Clean(real))
	}

	// Retarget the link after the cache warmed up.
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	mustSymlinkDir(t, other, link)

	if got, err := ResolveAbsPath(link); err != nil || got != filepath.Clean(real) {
		t.Fatalf("TTL cache must still serve the warm identity: got %q, %v, want %q", got, err, filepath.Clean(real))
	}
	got, err := ResolveAbsPathFresh(link)
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Clean(other) {
		t.Fatalf("fresh resolution must observe the new identity: got %q, want %q", got, filepath.Clean(other))
	}
}

// 任务 928 现场复现（desktop.log 2026-10-16 canonical probe 风暴）：
// 同一物理文件的两种拼写（目录记录保持磁盘大小写、canonical key 全小写）
// 在 Windows 上必须共享一个缓存条目。修复前每个拼写各占一条、各走一次
// walk，历史会话工作集直接翻倍。
func TestResolveCanonicalPathCaseVariantSpellingsShareCacheEntry(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("case-insensitive path aliasing is Windows-specific behavior")
	}
	resetCanonicalEngine(t)

	var walks int32
	canonicalEvalSymlinks = func(p string) (string, error) {
		atomic.AddInt32(&walks, 1)
		return p, nil
	}
	canonicalResolveTimeout = 2 * time.Second
	canonicalCacheTTL = time.Minute

	upper := filepath.Join(t.TempDir(), "MixedCase", "session.jsonl")
	lower := strings.ToLower(upper)

	got1, err := ResolveAbsPath(upper)
	if err != nil {
		t.Fatal(err)
	}
	got2, err := ResolveAbsPath(lower)
	if err != nil {
		t.Fatal(err)
	}
	if n := atomic.LoadInt32(&walks); n != 1 {
		t.Fatalf("case-variant spellings did %d walks, want 1 shared cache entry (key self-splits on Windows)", n)
	}
	if got1 != got2 {
		t.Fatalf("case-variant spellings resolved differently: %q vs %q", got1, got2)
	}
}

// 任务 928 现场复现第二半：修复前缓存上限 1024 小于历史会话工作集
// （global-workspace 1440 条 + 各项目，~823 个 jsonl，双拼写翻倍），任意逐出
// 使同一路径在 TTL 内被反复重走——「缓存键自毁」的逐出形态。本测试用
// 1500 个路径钉死：生产容量必须能在 TTL 内保住整个工作集。
func TestResolveCanonicalPathWorkingSetSurvivesWithinTTL(t *testing.T) {
	resetCanonicalEngine(t)

	var walks int32
	canonicalEvalSymlinks = func(p string) (string, error) {
		atomic.AddInt32(&walks, 1)
		return p, nil
	}
	canonicalResolveTimeout = 2 * time.Second
	canonicalCacheTTL = time.Minute

	base := t.TempDir()
	const workingSet = 1500
	paths := make([]string, workingSet)
	for i := range paths {
		paths[i] = filepath.Join(base, "proj", "sessions", fmt.Sprintf("20261016-000000.%09d-session.jsonl", i))
	}
	for _, p := range paths {
		if _, err := ResolveAbsPath(p); err != nil {
			t.Fatal(err)
		}
	}
	first := atomic.LoadInt32(&walks)
	for _, p := range paths {
		if _, err := ResolveAbsPath(p); err != nil {
			t.Fatal(err)
		}
	}
	if n := atomic.LoadInt32(&walks); n != first {
		t.Fatalf("second pass over %d-path working set re-walked (%d -> %d), cache must hold the whole working set within TTL", workingSet, first, n)
	}
}

// 任务 928 残留①：正结果长 TTL。首层 eval 即成功的路径（已存在文件/目录）
// 走 canonicalCachePositiveTTL，不再每 30s 重走——持续触碰的热路径（每个
// 打开会话的保存漏斗）此前是永久跑步机。missing/timeout 保持短 TTL：
// 尚未创建的写目标可能以 symlink 出现，死盘需要重探。
func TestResolveCanonicalPathPositiveResultOutlivesShortTTL(t *testing.T) {
	resetCanonicalEngine(t)

	var generation, calls int32
	canonicalResolveTimeout = 2 * time.Second
	canonicalCacheTTL = 40 * time.Millisecond
	canonicalCachePositiveTTL = time.Minute

	base := t.TempDir()
	existing := filepath.Join(base, "exists")
	if err := os.MkdirAll(existing, 0o755); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(base, "created-later")

	canonicalEvalSymlinks = func(p string) (string, error) {
		atomic.AddInt32(&calls, 1)
		if atomic.LoadInt32(&generation) == 0 {
			if p == missing {
				return "", errors.New("generation 0: not created yet")
			}
			return p, nil
		}
		// Generation 1: existing became a symlink to `other`; missing appeared
		// as a symlink too. Only the SHORT-TTL (missing) entry may observe
		// this; the positive entry stays pinned for its long TTL.
		other := filepath.Join(base, "other")
		return filepath.Join(other, filepath.Base(p)), nil
	}

	if _, err := ResolveAbsPath(existing); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveAbsPath(missing); err != nil {
		t.Fatal(err)
	}
	callsAfterFirst := atomic.LoadInt32(&calls)

	time.Sleep(100 * time.Millisecond) // past the short TTL, well within the positive TTL
	atomic.StoreInt32(&generation, 1)

	gotExisting, err := ResolveAbsPath(existing)
	if err != nil {
		t.Fatal(err)
	}
	if gotExisting != filepath.Clean(existing) {
		t.Fatalf("positive result must stay pinned within its long TTL: got %q want %q", gotExisting, filepath.Clean(existing))
	}
	gotMissing, err := ResolveAbsPath(missing)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(base, "other", "created-later"); gotMissing != want {
		t.Fatalf("missing path must re-walk past the short TTL: got %q want %q", gotMissing, want)
	}
	if n := atomic.LoadInt32(&calls); n == callsAfterFirst {
		t.Fatal("post-TTL calls should have re-walked at least the missing path")
	}
}

// TestResolveAbsPathBlackHoleUNCBounded is the task 455 end-to-end guard:
// a write root on an unroutable UNC share must cost at most the resolution
// budget (250ms in production; the pre-fix behavior ate a ~21s SMB
// reconnect), fall back to the cleaned path, and serve later callers from
// the TTL cache.
func TestResolveAbsPathBlackHoleUNCBounded(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("UNC share reconnect budget is Windows-specific behavior")
	}
	resetCanonicalEngine(t) // production evaluator, production 250ms budget

	target := `\\10.255.255.1\task455blackhole\deep\nonexistent`
	want := filepath.Clean(target)

	start := time.Now()
	got, err := ResolveAbsPath(target)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("black-hole root returned error: %v", err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("black-hole root cost %v (unbounded; SMB reconnect budget leaked through)", elapsed)
	}
	if got != want {
		t.Fatalf("black-hole root = %q, want cleaned abs %q", got, want)
	}

	start = time.Now()
	got2, err := ResolveAbsPath(target)
	if err != nil {
		t.Fatal(err)
	}
	if cached := time.Since(start); cached > 50*time.Millisecond {
		t.Fatalf("second call took %v, want a TTL cache hit", cached)
	}
	if got2 != want {
		t.Fatalf("second call = %q, want %q", got2, want)
	}
}
