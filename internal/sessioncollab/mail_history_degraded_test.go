package sessioncollab

// 任务511 修一验收（排查报告 §4 缺口 1 / §5 建议 1）：History 拿不到
// .mail.lock 时不再静默返回 nil——必须留 Warn（last_holder / wait_ms 结构化
// 字段）并把 degraded=true 返回给索引层，快照据此带 Degraded。

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"reasonix/internal/filelock"
)

// captureHandler 抓取 slog.Warn 记录：slog.Warn 走 Default handler，
// 测试期间临时替换并在结束时还原（本包测试串行，无并行污染）。
type captureHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *captureHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r.Clone())
	return nil
}

func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(string) slog.Handler      { return h }

func (h *captureHandler) find(msg string) (slog.Record, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.records {
		if r.Message == msg {
			return r, true
		}
	}
	return slog.Record{}, false
}

func TestHistoryHealthyPathReturnsRowsAndFalse(t *testing.T) {
	s := NewMailStore(t.TempDir())
	if _, err := s.Deliver(context.Background(), MailMessage{From: "sc_a", To: "sc_me", Body: "one"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Deliver(context.Background(), MailMessage{From: "sc_b", To: "sc_me", Body: "two"}); err != nil {
		t.Fatal(err)
	}
	rows, degraded := s.History(context.Background())
	if degraded {
		t.Fatal("healthy history must not be marked degraded")
	}
	if len(rows) != 2 {
		t.Fatalf("history rows = %d, want 2", len(rows))
	}
	if holder := s.LockHolderInfo(); !strings.HasPrefix(holder, "pid=") {
		t.Fatalf("lock holder sidecar must be stamped on exclusive acquire, got %q", holder)
	}
}

func TestHistoryLockBusyWarnsAndMarksDegraded(t *testing.T) {
	s := NewMailStore(t.TempDir())
	if _, err := s.Deliver(context.Background(), MailMessage{From: "sc_a", To: "sc_me", Body: "one"}); err != nil {
		t.Fatal(err)
	}
	// 先做一次成功读：sidecar 记下本进程，等价于「上一个持有者已留痕」。
	if _, degraded := s.History(context.Background()); degraded {
		t.Fatal("precondition: the first read must be healthy")
	}

	// 楔住 .mail.lock（独占）——等价于一个卡死的长持锁者。
	wedge, err := filelock.TryAcquire(filepath.Join(s.Dir(), ".mail.lock"))
	if err != nil {
		t.Fatalf("wedge the mail lock: %v", err)
	}
	defer wedge()

	captured := &captureHandler{}
	old := slog.Default()
	slog.SetDefault(slog.New(captured))
	defer slog.SetDefault(old)

	// ctx 给 300ms 预算：等锁 300ms 后有界放弃，不拖慢测试。
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	started := time.Now()
	rows, degraded := s.History(ctx)
	if !degraded {
		t.Fatal("lock-busy history must be marked degraded, not silently nil")
	}
	if rows != nil {
		t.Fatalf("degraded history carries no rows, got %d", len(rows))
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("degraded history waited %v, want ≈ the 300ms ctx budget", elapsed)
	}

	rec, ok := captured.find("session collab mail: degraded history read (lock busy)")
	if !ok {
		t.Fatal("lock-busy history must leave a Warn naming the degraded read")
	}
	holder := ""
	waitMS := ""
	rec.Attrs(func(a slog.Attr) bool {
		switch a.Key {
		case "last_holder":
			holder = a.Value.String()
		case "wait_ms":
			waitMS = a.Value.String()
		}
		return true
	})
	if !strings.Contains(holder, "pid=") {
		t.Fatalf("warn must name the last holder, got %q", holder)
	}
	if waitMS == "" || waitMS == "0" {
		t.Fatalf("warn must carry a nonzero wait_ms, got %q", waitMS)
	}
}
