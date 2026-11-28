package servepool

// tl1867: 接管实例（`reasonix serve` 子进程）的 slog 此前只存在于 spawn 时挂的
// 8KB tailBuffer（servepool.go），且只有 spawn 未就绪那一瞬间会被倒进 desktop.log；
// 实例一旦 ready，其 stdout/stderr 输出（slog 默认走 stderr）随进程退出彻底丢失——
// 排查 J1/J2-J4 类接管问题时实例侧时序日志无从回放，只能复现重跑。
//
// 本文件给 tailBuffer 加一个持久化出口：异步、有界、按项目落盘的 serve 日志。
//
// 路径（抄既有惯例，不发明）：
//	<MemoryUserDir>/projects/<config.WorkspaceSlug(root)>/logs/serve.log
//   projects/<项目>/ 是仓内 on-disk state 的既有键（sessions/、sessions-v4/、
//   memory/、desktop/topic-state-v1.sqlite 各占一个子目录，config.WorkspaceSlug
//   是磁盘态 slug——servepool.WorkspaceSlug 是网关 URL 键，两者不同域，见
//   servepool.go 的注释）；logs/ 子目录对齐全局 logs/desktop/、logs/launcher/
//   的「logs/<子域>/<名>.log」形态。与 desktop.log 天然分离，按项目可定位。
//
// 轮转（对齐仓内先例）：算法照抄 desktop/desktop_log.go 的 rollingLogWriter——
// 大小触发、原地按下标移位（serve.log → serve.log.1 → … → serve.log.<n-1>），
// 最老一代掉崖删除；参数收紧为 1MB/代 × 共 5 代（当前 + 4 份滚动），单项目
// serve 日志磁盘上限 ~5MB，有界。
//
// 开销（异步写，不阻塞实例主路径）：Write 把字节块非阻塞投递进有界队列即返回，
// 专设 drainer goroutine 做磁盘写。队列满或磁盘写失败按块丢弃并计数，Close 时
// 落一行丢弃汇总——投递永不阻塞（exec 的 pipe copier 永不被磁盘速度反压，
// serve 进程的 stderr 写也就不会被反压阻塞）。
//
// 失败语义（detach-only，对齐 desktoplauncher/stderrlog.go 的「日志失败绝不
// 连累主路径」原则）：日志目录建不出/文件打不开时 newServeLogFile 返回 nil，
// spawn 退回 tailBuffer-only 行为（desktop.log 一行注明出口不可用）；日志永远是
// best-effort 诊断，丢日志不许丢 spawn。
//
// non-goals：不改 slog 全局通道/日志框架（出口只挂在 desktop 侧捕获点）；
// 不做日志查看 UI/检索；不追加观测字段；不动 desktop.log 的格式与轮转。

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"reasonix/internal/config"
)

const (
	// serveLogFileName is the per-project serve instance log name, under
	// projects/<slug>/logs/ (same <name>.log shape as logs/desktop/desktop.log).
	serveLogFileName = "serve.log"
	// serveLogMaxSizeBytes caps one generation — 1 MiB (desktop full log uses
	// 4 MiB; instance logs are diagnostics, tighter keeps the fleet bounded).
	serveLogMaxSizeBytes = int64(1 << 20)
	// serveLogMaxBackups is the total file count per project (current + rolled),
	// desktop/desktop_log.go semantics: serve.log + serve.log.1 … serve.log.<n-1>.
	// 5 files × 1 MiB = ~5 MiB per-project ceiling.
	serveLogMaxBackups = 5
	// serveLogQueueChunks is the async queue depth in chunks (one chunk ≈ one
	// pipe read, usually one slog line). 64 chunks is a generous burst buffer
	// while keeping worst-case memory (~32KB chunks) bounded at ~2 MiB per
	// running instance; overflow drops and counts rather than backpressuring.
	serveLogQueueChunks = 64
)

// serveLogPath resolves the persisted serve log path for a project root:
// <MemoryUserDir>/projects/<config.WorkspaceSlug(root)>/logs/serve.log.
// Empty when the user state dir or root doesn't resolve — the caller then
// runs tailBuffer-only (detach-only: no log, no breakage).
func serveLogPath(root string) string {
	base := config.MemoryUserDir()
	root = strings.TrimSpace(root)
	if base == "" || root == "" {
		return ""
	}
	return filepath.Join(base, "projects", config.WorkspaceSlug(filepath.Clean(root)), "logs", serveLogFileName)
}

// serveLogFile is an async, size-rotated, generation-capped file writer for
// one serve instance's captured output. Concurrency model:
//
//   - Write may be called from multiple exec pipe-copier goroutines at once
//     (cmd.Stdout and cmd.Stderr each get their own copier). It only enqueues
//     — never blocks, never errors, never backpressures the serve process.
//   - All disk I/O (append, rotation) happens on the single drain goroutine,
//     so file state needs no locking.
//   - Close stops intake, waits for the queue to drain onto disk, appends a
//     drop summary when anything was dropped, and closes the file. Writes
//     racing with Close (straggler copiers after the kill — the stop paths
//     use Process.Wait, which does not wait for exec's copiers) are dropped
//     safely instead of panicking on a closed channel.
type serveLogFile struct {
	path       string
	maxSize    int64
	maxBackups int
	queueDepth int

	mu     sync.Mutex
	closed bool
	ch     chan []byte
	done   chan struct{}

	dropped atomic.Int64 // chunks lost to queue overflow or write failure

	// f/size are owned exclusively by the drain goroutine — no lock.
	f    *os.File
	size int64
}

// newServeLogFile opens the log file synchronously (so open failure is the
// caller's decision point — detach-only) and starts the async drainer. The
// header line, when non-empty, is enqueued first and lands before any child
// output. Returns nil when the file cannot be opened; callers must tolerate
// nil and fall back to tailBuffer-only.
func newServeLogFile(path string, maxSizeBytes int64, maxBackups, queueDepth int, header string) *serveLogFile {
	w, err := newServeLogHandle(path, maxSizeBytes, maxBackups, queueDepth, header)
	if err != nil {
		return nil
	}
	w.startDrain()
	return w
}

// newServeLogHandle builds the writer with the file already open but WITHOUT
// starting the drainer — the construction seam newServeLogFile composes with
// startDrain, and package tests use directly to stage a deterministically
// full queue before any concurrent drain can run.
func newServeLogHandle(path string, maxSizeBytes int64, maxBackups, queueDepth int, header string) (*serveLogFile, error) {
	if path == "" {
		return nil, fmt.Errorf("serve log: empty path")
	}
	f, size, err := openServeLogFile(path)
	if err != nil {
		return nil, err
	}
	w := &serveLogFile{
		path:       path,
		maxSize:    maxSizeBytes,
		maxBackups: maxBackups,
		queueDepth: queueDepth,
		ch:         make(chan []byte, queueDepth),
		done:       make(chan struct{}),
		f:          f,
		size:       size,
	}
	if header != "" {
		// Construction-time enqueue cannot overflow: the queue was just made.
		w.ch <- []byte(header)
	}
	return w, nil
}

// startDrain launches the single goroutine that owns all file I/O.
func (w *serveLogFile) startDrain() {
	go w.drain()
}

// openServeLogFile creates the parent directory and opens the file for
// append, returning the current size for rotation accounting.
func openServeLogFile(path string) (*os.File, int64, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, 0, fmt.Errorf("serve log: create dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, 0, fmt.Errorf("serve log: open: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, 0, fmt.Errorf("serve log: stat: %w", err)
	}
	return f, info.Size(), nil
}

// Write implements io.Writer for the exec pipe copiers. It never blocks
// (select-default into the bounded queue), never fails the copier (errors are
// swallowed — io.MultiWriter semantics: the persisted outlet must never drag
// down the tailBuffer leg), and is safe after Close.
func (w *serveLogFile) Write(p []byte) (int, error) {
	w.enqueue(append([]byte(nil), p...))
	return len(p), nil
}

// noteChildPID records the serve process id right after Start, so a persisted
// run can be cross-referenced with serve.spawn and pid-based forensics
// (orphan/reap, takeover J-scenarios) from the log alone.
func (w *serveLogFile) noteChildPID(pid int) {
	w.enqueue([]byte(fmt.Sprintf("[serve-log] child pid=%d\n", pid)))
}

// enqueue drops the chunk (counted) when the instance is already closed or
// the queue is full. Holding mu across the non-blocking send is what makes
// the send/close(ch) pair in Close race-free.
func (w *serveLogFile) enqueue(b []byte) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	select {
	case w.ch <- b:
	default:
		w.dropped.Add(1)
	}
}

// Close stops intake and blocks until the drainer has flushed everything
// already queued and closed the file. Idempotent. Call sites are all
// process-stop paths, where a short bounded wait for the queue is fine.
func (w *serveLogFile) Close() {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return
	}
	w.closed = true
	close(w.ch)
	w.mu.Unlock()
	<-w.done
}

// drain is the single goroutine that touches the file.
func (w *serveLogFile) drain() {
	for b := range w.ch {
		w.writeChunk(b)
	}
	if d := w.dropped.Load(); d > 0 {
		_, _ = w.f.WriteString(fmt.Sprintf(
			"[serve-log] closed; %d chunk(s) dropped (async queue overflow or write failure) — the tail is approximate\n", d))
	}
	_ = w.f.Close()
	close(w.done)
}

// writeChunk appends one chunk with rotation accounting. Mirrors
// desktop/desktop_log.go rollingLogWriter.Write: rotate when the chunk would
// push the file past the cap, then append. A failed reopen counts the chunk
// as dropped and keeps the drainer alive for later retries.
func (w *serveLogFile) writeChunk(b []byte) {
	if w.size+int64(len(b)) > w.maxSize {
		w.rotate()
	}
	if w.f == nil {
		// rotate failed to reopen (e.g. dir removed under us): drop and go on.
		w.dropped.Add(1)
		return
	}
	n, err := w.f.Write(b)
	w.size += int64(n)
	if err != nil || n < len(b) {
		w.dropped.Add(1)
	}
}

// rotate shifts the rolled generations down one slot, deletes the oldest,
// and reopens the current file truncated — byte-for-byte the desktop full-log
// rolling algorithm (desktop/desktop_log.go rotateLocked), with the backup
// count coming from the serve-log constants instead.
func (w *serveLogFile) rotate() {
	if w.f != nil {
		_ = w.f.Close()
		w.f = nil
	}
	for i := w.maxBackups - 1; i >= 1; i-- {
		older := w.rolledPath(i)
		newer := w.path
		if i > 1 {
			newer = w.rolledPath(i - 1)
		}
		if _, err := os.Stat(newer); err != nil {
			continue
		}
		_ = os.Remove(older)
		_ = os.Rename(newer, older)
	}
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		w.f = nil
		w.size = 0
		return
	}
	w.f = f
	w.size = 0
}

func (w *serveLogFile) rolledPath(i int) string {
	return fmt.Sprintf("%s.%d", w.path, i)
}

// newServeLogFor builds the persisted outlet for one spawn. Config.ServeLogDir
// overrides the whole directory (test seam, same shape as PortFileDir);
// otherwise the per-project default path is used. Returns nil — and notes one
// desktop.log line — when the outlet is unavailable; spawn continues
// tailBuffer-only.
func (m *Manager) newServeLogFor(p *project) *serveLogFile {
	dir := m.cfg.ServeLogDir
	var path string
	if dir != "" {
		path = filepath.Join(dir, serveLogFileName)
	} else {
		path = serveLogPath(p.root)
	}
	if path == "" {
		return nil
	}
	header := fmt.Sprintf("[serve-log] spawn project=%q bin=%q time=%s\n",
		p.id, m.bin, time.Now().Format(time.RFC3339))
	w := newServeLogFile(path, serveLogMaxSizeBytes, serveLogMaxBackups, serveLogQueueChunks, header)
	if w == nil {
		// Detach-only: losing the persisted outlet must not fail the spawn.
		log.Printf("[servepool] serve log unavailable, persisting disabled project=%q dir=%q", p.id, filepath.Dir(path))
	}
	return w
}
