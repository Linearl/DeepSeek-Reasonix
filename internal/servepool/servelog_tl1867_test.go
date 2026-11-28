package servepool

// tl1867 包级测试：实例 slog 持久化出口（servelog.go）的落盘、轮转、并发、
// 丢弃计数与 spawn 接线。构造失败一律 t.Fatal，无静默 SKIP。

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/config"
)

// --- serveLogFile 单元行为 ---

func TestServeLogFilePersistsOnCloseTL1867(t *testing.T) {
	path := filepath.Join(t.TempDir(), serveLogFileName)
	w := newServeLogFile(path, 1<<20, serveLogMaxBackups, 16, "[serve-log] header\n")
	if w == nil {
		t.Fatal("newServeLogFile = nil, want a usable outlet (temp dir is writable)")
	}
	if n, err := w.Write([]byte("first line\n")); err != nil || n != len("first line\n") {
		t.Fatalf("Write = (%d, %v), want (%d, nil)", n, err, len("first line\n"))
	}
	_, _ = w.Write([]byte("second line\n"))
	w.Close()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read persisted log: %v", err)
	}
	got := string(data)
	if !strings.HasPrefix(got, "[serve-log] header\n") {
		t.Fatalf("header line missing or not first:\n%s", got)
	}
	if !strings.Contains(got, "first line\n") || !strings.Contains(got, "second line\n") {
		t.Fatalf("written lines missing after Close:\n%s", got)
	}
}

func TestServeLogFileWriteAfterCloseIsSafeTL1867(t *testing.T) {
	path := filepath.Join(t.TempDir(), serveLogFileName)
	w := newServeLogFile(path, 1<<20, serveLogMaxBackups, 8, "")
	if w == nil {
		t.Fatal("newServeLogFile = nil, want a usable outlet")
	}
	w.Close()
	// Straggler copier semantics: a write racing (or following) Close must
	// neither panic on the closed channel nor fail the copier.
	n, err := w.Write([]byte("late straggler\n"))
	if err != nil {
		t.Fatalf("Write after Close err = %v, want nil (copier must never be failed by the log leg)", err)
	}
	if n != len("late straggler\n") {
		t.Fatalf("Write after Close n = %d, want full len", n)
	}
	// Second Close must stay idempotent instead of panicking.
	w.Close()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if strings.Contains(string(data), "late straggler") {
		t.Fatalf("post-close write must not reach the closed file:\n%s", data)
	}
}

func TestServeLogFileRotationBoundTL1867(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, serveLogFileName)
	const (
		maxSize   = int64(32) // 4 lines per generation at 8 bytes per line
		maxFiles  = 3         // current + 2 rolled
		lineCount = 20
	)
	// 白盒：同步驱动 writeChunk（不起 drainer），代际布局完全确定——
	// 异步/丢弃行为由 TestServeLogFileDropAccountingTL1867 与 spawn 测试覆盖。
	w, err := newServeLogHandle(path, maxSize, maxFiles, 16, "")
	if err != nil {
		t.Fatalf("newServeLogHandle: %v", err)
	}
	for i := 0; i < lineCount; i++ {
		w.writeChunk([]byte(fmt.Sprintf("line-%02d\n", i)))
	}
	if got := w.dropped.Load(); got != 0 {
		t.Fatalf("dropped = %d, want 0 (synchronous writeChunk never overflows the queue)", got)
	}
	if w.f != nil {
		_ = w.f.Close()
		w.f = nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	files := 0
	var total int64
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), serveLogFileName) {
			continue
		}
		files++
		if info, err := e.Info(); err == nil {
			total += info.Size()
		}
	}
	if files > maxFiles {
		t.Fatalf("serve.log* file count = %d, want ≤ %d (disk must stay bounded)", files, maxFiles)
	}
	cliff := path + fmt.Sprintf(".%d", maxFiles) // the first index past the cap
	if _, err := os.Stat(cliff); err == nil {
		t.Fatalf("%s exists — the oldest generation must fall off at maxBackups=%d", filepath.Base(cliff), maxFiles)
	}
	read := func(p string) string {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		return string(b)
	}
	// 代际内容按确定性算术钉死：20 行 × 8B，32B/代 → 5 次轮转后
	// current=line-16..19、.1=line-12..15、.2=line-08..11，更老代际掉崖。
	for _, tc := range []struct {
		path    string
		firstNm int
		lastNm  int
	}{
		{path, 16, 19},
		{path + ".1", 12, 15},
		{path + ".2", 8, 11},
	} {
		got := read(tc.path)
		for i := tc.firstNm; i <= tc.lastNm; i++ {
			if !strings.Contains(got, fmt.Sprintf("line-%02d", i)) {
				t.Fatalf("%s missing line-%02d:\n%s", filepath.Base(tc.path), i, got)
			}
		}
	}
	if total > maxSize*int64(maxFiles) {
		t.Fatalf("total on-disk size %d exceeds cap %d", total, maxSize*int64(maxFiles))
	}
}

func TestServeLogFileConcurrentWritersTL1867(t *testing.T) {
	path := filepath.Join(t.TempDir(), serveLogFileName)
	// Queue deeper than every write in the test: even if the drainer never
	// ran until Close, all chunks fit — the exact-count assert below is
	// deterministic, not timing-dependent.
	const (
		writers = 4
		lines   = 100
		queue   = writers * lines
	)
	w := newServeLogFile(path, 1<<20, serveLogMaxBackups, queue, "")
	if w == nil {
		t.Fatal("newServeLogFile = nil, want a usable outlet")
	}
	done := make(chan struct{})
	for g := 0; g < writers; g++ {
		g := g
		go func() {
			defer func() { done <- struct{}{} }()
			for i := 0; i < lines; i++ {
				if _, err := fmt.Fprintf(w, "g%d-line-%03d\n", g, i); err != nil {
					t.Errorf("writer %d line %d: %v", g, i, err)
					return
				}
			}
		}()
	}
	for g := 0; g < writers; g++ {
		<-done
	}
	w.Close()
	if w.dropped.Load() != 0 {
		t.Fatalf("dropped = %d, want 0 (queue sized for the whole test)", w.dropped.Load())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	for g := 0; g < writers; g++ {
		for i := 0; i < lines; i++ {
			want := fmt.Sprintf("g%d-line-%03d\n", g, i)
			if !strings.Contains(string(data), want) {
				t.Fatalf("concurrent write %q lost", want)
			}
		}
	}
}

func TestServeLogFileDropAccountingTL1867(t *testing.T) {
	path := filepath.Join(t.TempDir(), serveLogFileName)
	// Handle without drainer: the queue can be staged deterministically.
	w, err := newServeLogHandle(path, 1<<20, serveLogMaxBackups, 2, "")
	if err != nil {
		t.Fatalf("newServeLogHandle: %v", err)
	}
	w.ch <- []byte("queued-1\n")
	w.ch <- []byte("queued-2\n")
	// 标记块用 "spilled"：汇总行文案含 "queue overflow" 字样，断言会被撞词。
	n, err := w.Write([]byte("spilled\n"))
	if err != nil {
		t.Fatalf("Write err = %v, want nil (overflow must not fail the copier)", err)
	}
	if n != len("spilled\n") {
		t.Fatalf("Write n = %d, want full len", n)
	}
	if got := w.dropped.Load(); got != 1 {
		t.Fatalf("dropped = %d, want 1", got)
	}
	w.startDrain()
	w.Close()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	got := string(data)
	if !strings.Contains(got, "queued-1") || !strings.Contains(got, "queued-2") {
		t.Fatalf("queued chunks lost:\n%s", got)
	}
	if strings.Contains(got, "spilled") {
		t.Fatalf("overflow chunk must not reach the file:\n%s", got)
	}
	if !strings.Contains(got, "1 chunk(s) dropped") {
		t.Fatalf("closing drop summary missing:\n%s", got)
	}
}

func TestServeLogPathShapeTL1867(t *testing.T) {
	root := filepath.Join("D:\\", "work", "some project")
	got := serveLogPath(root)
	want := filepath.Join(config.MemoryUserDir(), "projects", config.WorkspaceSlug(filepath.Clean(root)), "logs", serveLogFileName)
	if got != want {
		t.Fatalf("serveLogPath = %q, want %q (projects/<config.WorkspaceSlug>/logs 形态)", got, want)
	}
	if serveLogPath("") != "" {
		t.Fatalf("serveLogPath(empty root) = %q, want empty", serveLogPath(""))
	}
}

// --- spawn 接线（helper 子进程，沿用 1049 的 TestMain 分发）---

// TestSpawnServeLogPersistsAcrossExitTL1867 is the acceptance-shaped test:
// a serve subprocess becomes ready, RUNS for a while (stdout/stderr captured
// through the tee), then the pool stops it — i.e. the process exits — and the
// persisted per-project log still carries the instance-side lines. Before
// tl1867 everything except the not-ready spawn tail died with the process.
func TestSpawnServeLogPersistsAcrossExitTL1867(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	logDir := t.TempDir()
	t.Setenv("REASONIX_SERVEPOOL_HELPER_READY", "1") // child inherits: ready helper mode
	m, err := NewManager(Config{
		ReasonixBin:  self,
		ProjectRoots: []string{root},
		SpawnTimeout: 10 * time.Second,
		ServeLogDir:  logDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	t.Cleanup(func() {
		if !closed {
			m.Close()
		}
	})
	if err := m.Open(WorkspaceSlug(root)); err != nil {
		t.Fatalf("Open(ready helper) = %v, want ready", err)
	}
	// 联调场景形态：实例已运行，池停（杀进程）= 实例退出；落盘文件必须留下
	// 实例侧时序日志。
	m.Close()
	closed = true

	data, err := os.ReadFile(filepath.Join(logDir, serveLogFileName))
	if err != nil {
		t.Fatalf("read persisted serve log: %v", err)
	}
	got := string(data)
	for _, want := range []string{
		"[serve-log] spawn project=",    // run 头行（时序定位）
		"[serve-log] child pid=",        // 与 serve.spawn 对账
		"SERVEPOOL1867 instance stdout", // 实例 stdout
		"SERVEPOOL1867 instance stderr", // 实例 stderr（slog 默认通道）
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("persisted serve.log missing %q\n--- file ---\n%s", want, got)
		}
	}
}

// TestSpawnFailurePersistsCapturedOutputTL1867 pins the spawn-failure flush:
// the never-ready helper (1049) prints markers, the pool times out and kills
// it, and the persisted log must keep the full capture — not just the 400
// rune summary that rides the 503 error.
func TestSpawnFailurePersistsCapturedOutputTL1867(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	logDir := t.TempDir()
	m, err := NewManager(Config{
		ReasonixBin:  self, // 1049 helper mode: prints markers, never ready
		ProjectRoots: []string{root},
		SpawnTimeout: 700 * time.Millisecond,
		ServeLogDir:  logDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	t.Cleanup(func() {
		if !closed {
			m.Close()
		}
	})
	if err := m.Open(WorkspaceSlug(root)); err == nil {
		t.Fatal("Open = nil, want timeout error")
	}
	m.Close()
	closed = true

	data, err := os.ReadFile(filepath.Join(logDir, serveLogFileName))
	if err != nil {
		t.Fatalf("read persisted serve log: %v", err)
	}
	got := string(data)
	if !strings.Contains(got, "SERVEPOOL1049 fake serve output") {
		t.Fatalf("persisted log lost the captured failure output:\n%s", got)
	}
	if !strings.Contains(got, "[serve-log] spawn") {
		t.Fatalf("persisted log missing the spawn header:\n%s", got)
	}
}

// runReadyServeHelperTL1867 mimics a serve that becomes ready (binds a real
// port and writes the port file), emits instance-side lines on both streams
// (stderr being the default slog channel), then stays alive until the pool
// kills it — so the persisted-log tests prove output captured from a RUNNING
// instance survives its exit. Dispatched from TestMain's "serve" mode (see
// spawn_timeout_1049_test.go) via REASONIX_SERVEPOOL_HELPER_READY=1.
func runReadyServeHelperTL1867() {
	portFile := ""
	for i := 2; i+1 < len(os.Args); i++ {
		if os.Args[i] == "--port-file" {
			portFile = os.Args[i+1]
		}
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		os.Exit(3)
	}
	if portFile != "" {
		_ = os.WriteFile(portFile, []byte(ln.Addr().String()), 0o600)
	}
	fmt.Fprintln(os.Stdout, "SERVEPOOL1867 instance stdout line")
	fmt.Fprintln(os.Stderr, "SERVEPOOL1867 instance stderr slog line")
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		_ = conn.Close()
	}
}
