package agent

// tl1434 追加 —— 运行中节点「工作过程」增量可见的写侧半边。
//
// 装机实测缺口：子代理 transcript .jsonl 只在终态 SaveOutcome 落盘，运行中的
// 工作流节点因此无盘上过程可读（读面降级矩阵如实报「尚未保存」）。本文件把
// 该边界转成能力：工作流派发（ctx 显式装载标记，见 workflow runner runNode）
// 在子代理运行期间把内存 Session 周期快照到 transcript 路径。
//
// 复用而非新造：落盘走 Session.Save 的 CAS 快照协议——与父会话 autosave 同一条
// 已装机验证的写路径（进程内并发保存是受支持模式：Session.mu 护 Messages，
// save-path mutex 护写，event log append-only、读者容 torn tail）。不自拼字节、
// 不新增存储格式，「Windows 并发追加安全」由既有协议继承。
//
// 时序契约：stop 必须先于终态 SaveOutcome——本文件的挂载点（task.go runSession
// 闭包）以 defer stop() 保证；终态保存仍是权威与唯一终态，autosave 失败不失败
// 节点（warn 一次即停，避免每周期刷错误）。读侧只读铁律不受影响：这是派发方
// 写自己的 transcript，与查看面零写入断言互不相干。

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// subagentTranscriptAutosaveInterval 是运行中快照的周期。包级变量仅供测试
// 缩短节奏（与 sessionFileLockWait 同一口径）；生产固定值。
var subagentTranscriptAutosaveInterval = 1500 * time.Millisecond

// subagentAutosaveKey is the ctx key for the workflow-dispatch autosave marker.
// Unexported: the only door in is WithSubagentTranscriptAutosave.
type subagentAutosaveKey struct{}

// WithSubagentTranscriptAutosave marks the dispatch so the child's transcript
// is snapshotted to disk periodically while it runs (workflow node trace needs
// a readable mid-run process). Ordinary delegation never installs the marker
// and keeps the terminal-only persistence shape byte-for-byte.
func WithSubagentTranscriptAutosave(ctx context.Context) context.Context {
	return context.WithValue(ctx, subagentAutosaveKey{}, true)
}

// subagentTranscriptAutosaveFrom reports whether the dispatch carries the
// autosave marker.
func subagentTranscriptAutosaveFrom(ctx context.Context) bool {
	v, _ := ctx.Value(subagentAutosaveKey{}).(bool)
	return v
}

// startTranscriptAutosave arms the periodic snapshot saver for one persisted
// run: every interval, a dirty-checked Session.Save lands whatever the child
// has produced so far. The returned stop func must be called (exactly once)
// before the terminal SaveOutcome; it closes the ticker AND waits out any
// in-flight snapshot, so the terminal save truly has no concurrent writer —
// not merely a serialized one.
func (s *SubagentStore) startTranscriptAutosave(run *SubagentRun) (stop func()) {
	path := s.sessionPath(run.Ref)
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		ticker := time.NewTicker(subagentTranscriptAutosaveInterval)
		defer ticker.Stop()
		saved := 0
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				n := run.Session.Len()
				if n == saved {
					continue
				}
				if err := run.Session.Save(path); err != nil {
					// Best-effort: the terminal SaveOutcome stays authoritative.
					// Stop instead of retrying every tick — a persistent failure
					// must not turn into a warn-per-interval log storm (710 教训).
					slog.Warn("subagent transcript autosave failed; the terminal save remains authoritative",
						"ref", run.Ref, "err", err)
					return
				}
				saved = n
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			close(done)
			<-finished
		})
	}
}
