package agent

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// captureSlogForRotateWarn redirects the default logger into a buffer for the
// duration of the test (these tests are not parallel) and returns the buffer.
func captureSlogForRotateWarn(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(old) })
	return &buf
}

// resetRotateWarnMemo isolates the package-level memo between tests.
func resetRotateWarnMemo(t *testing.T) {
	t.Helper()
	eventsRotateWarnMu.Lock()
	eventsRotateWarnMemo = make(map[string]eventsRotateWarnState)
	eventsRotateWarnMu.Unlock()
}

// 任务 373 R4 备料：瘦不动会话（live 超 cap、fold 不回本）每次保存都命中同
// 一条 WARN——2026-10-10 实测单日 847 行。限频契约（键=路径+文案）：首次必
// 发；冷却期内同键静默并累计抑制数；冷却期满再发时抑制数随行带出；不同路
// 径、不同文案互不牵连。
func TestWarnRotateSkipOnceCooldownPerPath(t *testing.T) {
	resetRotateWarnMemo(t)
	buf := captureSlogForRotateWarn(t)

	const (
		pathA  = `C:\p\sessions\a.events.jsonl`
		pathB  = `C:\p\sessions\b.events.jsonl`
		fold   = "session: oversized event log left in place (fold would not shrink it)"
		offMsg = "session: oversized event log left in place (events auto rotation off)"
	)

	// 首次必发。
	warnRotateSkipOnce(pathA, fold, "path", pathA, "logSize", 1)
	if got := strings.Count(buf.String(), fold); got != 1 {
		t.Fatalf("first emission count = %d, want 1", got)
	}
	// 冷却期内：静默，不产生第二条。
	warnRotateSkipOnce(pathA, fold, "path", pathA, "logSize", 1)
	warnRotateSkipOnce(pathA, fold, "path", pathA, "logSize", 1)
	if got := strings.Count(buf.String(), fold); got != 1 {
		t.Fatalf("suppressed repeats leaked: count = %d, want 1", got)
	}
	// 冷却期满（把 lastAt 拨回 16 分钟前）：再发，抑制数随行带出。
	eventsRotateWarnMu.Lock()
	memo := eventsRotateWarnMemo[pathA+"\x00"+fold]
	memo.lastAt = time.Now().Add(-16 * time.Minute)
	eventsRotateWarnMemo[pathA+"\x00"+fold] = memo
	eventsRotateWarnMu.Unlock()
	warnRotateSkipOnce(pathA, fold, "path", pathA, "logSize", 1)
	out := buf.String()
	if got := strings.Count(out, fold); got != 2 {
		t.Fatalf("post-cooldown emission count = %d, want 2", got)
	}
	if !strings.Contains(out, "suppressed=2") {
		t.Fatalf("post-cooldown emission must carry suppressed=2: %q", out)
	}
	// 不同路径独立记账：B 的首警不受 A 的冷却影响。
	warnRotateSkipOnce(pathB, fold, "path", pathB, "logSize", 1)
	if got := strings.Count(buf.String(), `path=`+pathB); got != 1 {
		t.Fatalf("path B emission count = %d, want 1 (independent memo)", got)
	}
	// 同路径换文案：新信息各得一次首警，不继承旧文案的冷却。
	warnRotateSkipOnce(pathA, offMsg, "path", pathA, "logSize", 1)
	if got := strings.Count(buf.String(), offMsg); got != 1 {
		t.Fatalf("different message on the same path must warn independently: count = %d, want 1", got)
	}
}

// 会话保存路径的整链验证：auto 模式 cap 之下 fold 不回本的日志，
// sessionEventLogOversized 返回 false 且 WARN 走限频——三次判定只有一条日志；
// off 模式文案独立冷却，各自首警必发。
func TestSessionEventLogOversizedFoldNoShrinkWarnsOnce(t *testing.T) {
	resetRotateWarnMemo(t)
	buf := captureSlogForRotateWarn(t)
	t.Cleanup(func() { SetEventsAutoRotation("manual", 4, 0) })
	SetEventsAutoRotation("auto", 4, 50)

	// logSize 100MB，cap 50MB 超限；content 60MB → 60*2 >= 100 → fold 不回本。
	const path = `C:\p\sessions\whale.events.jsonl`
	const logSize = int64(100) << 20
	const content = int64(60) << 20
	for i := 0; i < 3; i++ {
		if sessionEventLogOversized(path, logSize, content) {
			t.Fatal("fold-no-shrink log must not rotate (gate returns false)")
		}
	}
	if got := strings.Count(buf.String(), "fold would not shrink it"); got != 1 {
		t.Fatalf("fold-no-shrink warn count = %d, want exactly 1 across 3 saves", got)
	}
	// 同路径换 off 模式文案：键不同，首警独立必发（off 文案的判定走 4x 因子，
	// 250MB log / 60MB content 超因子）。
	SetEventsAutoRotation("off", 4, 0)
	sessionEventLogOversized(path, int64(250)<<20, content)
	if got := strings.Count(buf.String(), "events auto rotation off"); got != 1 {
		t.Fatalf("off-mode warn must warn independently per message key: count = %d, want 1", got)
	}
	// off 模式连续判定：同键第二判走冷却，不再出现第二条。
	sessionEventLogOversized(path, int64(250)<<20, content)
	if got := strings.Count(buf.String(), "events auto rotation off"); got != 1 {
		t.Fatalf("off-mode warn repeat must stay suppressed: count = %d, want 1", got)
	}
}
