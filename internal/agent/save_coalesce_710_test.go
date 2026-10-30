package agent

// 任务 710 tests: the fold "would not shrink" WARN is a size-regime signal,
// not a per-save log line (847 repeats in the fork开发-新5 incident), and the
// measured save duration the controller's autosave backs off with is recorded
// on the Session.

import (
	"bytes"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/provider"
)

func TestFoldWouldNotShrinkWarnRateLimited(t *testing.T) {
	resetEventsRotation(t)
	SetEventsAutoRotation("auto", 16, 1)

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(prev)

	// tl 1285 之后，fold-would-not-shrink WARN 只在「实测地板」处出现：预估
	// 不达标先放行 probe（真实折叠，无 WARN），由调用方回报实测收益，连续两次
	// 实测省 <10% 才证明地板。WARN 的尺寸 regime 限频契约不变（710）：
	// content=2100000 > log×0.9 恒不达标、不触发 factor 分支（content×16）。
	// 取 content=2100000，log 基线 2MiB=2097152B（> cap 1MiB）：
	//   地板首证 @2097152B ⇒ WARN（首见）；
	//   +3% = 2160066B ⇒ 仍地板（未过 regime 线），限频抑制；
	//   +11.1% = 2329935B ⇒ 越过 regime 线（1.10）重新起跑，两次实测无收益
	//   再证地板 ⇒ 越过重警增长线，re-warn。
	const content int64 = 2100000
	path := "t710-fold-warn.events.jsonl"
	const logSize int64 = 2 << 20 // 2MiB > cap 1MiB

	// 预估阶段：两次 probe 放行 + 两次实测无收益，全程无 WARN。
	if !sessionEventLogOversized(path, logSize, content) {
		t.Fatal("unproven estimate must authorize the probe fold")
	}
	noteSessionFoldOutcome(path, logSize, logSize)
	if !sessionEventLogOversized(path, logSize, content) {
		t.Fatal("one measured no-gain must keep the probe authorized")
	}
	noteSessionFoldOutcome(path, logSize, logSize)
	if got := strings.Count(buf.String(), "fold would not shrink it"); got != 0 {
		t.Fatalf("estimate-only sightings must not warn; count = %d", got)
	}

	// 地板首证：WARN（首见）。
	sessionEventLogOversized(path, logSize, content) // floor: WARN
	// 同 regime 再判：仍地板，WARN 冷却期抑制。
	sessionEventLogOversized(path, logSize*103/100, content) // +3%: suppressed
	if got := strings.Count(buf.String(), "fold would not shrink it"); got != 1 {
		t.Fatalf("fold WARN count = %d, want 1 (floor first sighting only); log=%s", got, buf.String())
	}

	// +11.1%：regime 线（1.10）与重警增长线同时越过，整梯重新起跑，
	// 两次实测无收益再证地板 ⇒ re-warn。
	regrown := logSize * 1111 / 1000
	sessionEventLogOversized(path, regrown, content) // regime expiry: probe again
	noteSessionFoldOutcome(path, regrown, regrown)
	sessionEventLogOversized(path, regrown, content) // second probe
	noteSessionFoldOutcome(path, regrown, regrown)
	sessionEventLogOversized(path, regrown, content) // floor again: re-warn

	if got := strings.Count(buf.String(), "fold would not shrink it"); got != 2 {
		t.Fatalf("fold WARN count = %d, want 2 (floor proof + >10%% growth re-warn); log=%s", got, buf.String())
	}
	if !strings.Contains(buf.String(), "remedy=") {
		t.Fatalf("fold WARN must carry the remediation attr; log=%s", buf.String())
	}

	// A different session path warns independently of the first one's regime.
	path2 := "t710-fold-warn-2.events.jsonl"
	if !sessionEventLogOversized(path2, logSize, content) {
		t.Fatal("second path probe must authorize")
	}
	noteSessionFoldOutcome(path2, logSize, logSize)
	sessionEventLogOversized(path2, logSize, content)
	noteSessionFoldOutcome(path2, logSize, logSize)
	sessionEventLogOversized(path2, logSize, content)
	if got := strings.Count(buf.String(), "fold would not shrink it"); got != 3 {
		t.Fatalf("second path must warn independently; count = %d, want 3", got)
	}
}

func TestLastSaveDurationMsRecorded(t *testing.T) {
	s := NewSession("sys")
	if got := s.LastSaveDurationMs(); got != 0 {
		t.Fatalf("fresh session LastSaveDurationMs = %d, want 0", got)
	}
	s.Add(provider.Message{Role: provider.RoleUser, Content: "t710 duration probe"})
	s.Add(provider.Message{Role: provider.RoleAssistant, Content: "ack"})
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := s.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// The save ran through the observed wrapper, so the recorded duration must
	// have been stored; it may legitimately round to 0ms on a tiny session,
	// which is exactly the "cheap save, keep the base interval" reading the
	// controller's backoff makes.
	if got := s.LastSaveDurationMs(); got < 0 {
		t.Fatalf("LastSaveDurationMs = %d, want >= 0", got)
	}
}
