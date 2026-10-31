package agent

// tl 1285 tests —— fold 预估不达标判据反向缺陷的修复契约：
//   验收①  >cap 大日志不再被 'left in place' 永久豁免（probe 放行 + regime 再武装）；
//   验收②  fold/rotation 后实收体积下降可量化（实测回报驱动 ladder + 地板 zstd 归档）；
//   验收③  小日志 fold 收益判据不被误伤（under-cap / profitable / factor 分支零改动）。
//
// 驱动协议与 save 路径逐字一致：gate 返回 true ⇒ 调用方折叠 ⇒ noteSessionFoldOutcome
// 回报 (before, after)。测试直接驱动这对原语，不经过整场 save。

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/provider"
)

const (
	probePath = `C:\p\sessions\whale-tl1285.jsonl`
	// whale 形态（20261026 实测样本的等比缩小）：cap 1MiB 之下 log 2MiB、
	// content 1.99MiB（93.6%，省 6.4% <10% ⇒ 预估不达标），factor 16 不触发。
	probeWhaleLog     = int64(2) << 20
	probeWhaleContent = probeWhaleLog * 995 / 1000
)

// driveNoGainProbes simulates the save path exactly: the gate authorizes a
// probe fold, the fold runs, the caller reports a measured no-gain outcome.
func driveNoGainProbes(t *testing.T, path string, logSize int64, times int) {
	t.Helper()
	for i := 0; i < times; i++ {
		if !sessionEventLogOversized(path, logSize, probeWhaleContent) {
			t.Fatalf("probe %d: gate must authorize the fold while the floor is unproven", i)
		}
		noteSessionFoldOutcome(path, logSize, logSize)
	}
}

// 验收①：预估不达标的第一反应是放行 probe（真实折叠），不是豁免；豁免只在
// 连续 foldProbeMaxStrikes 次实测无收益后、且只在本 regime 内成立；日志再涨
// ~10% 整梯重新起跑——「永久豁免」不复存在。WARN（grep 契约）只在实测地板
// 处出现，预估阶段不再发。
func TestFoldProbeLadderNoPermanentExemption(t *testing.T) {
	resetEventsRotation(t)
	resetFoldProbeMemoForTest()
	resetRotateWarnMemo(t)
	buf := captureSlogForRotateWarn(t)
	SetEventsAutoRotation("auto", 16, 1)

	// 第一次判定：放行 probe，绝不豁免（旧缺陷在这里直接返回 false + WARN）。
	if !sessionEventLogOversized(probePath, probeWhaleLog, probeWhaleContent) {
		t.Fatal("unproven estimate must authorize the probe fold, not exempt the log")
	}
	if got := strings.Count(buf.String(), "fold would not shrink it"); got != 0 {
		t.Fatalf("estimate-only sighting must not warn (old verdict), got %d", got)
	}

	// 连续两次实测无收益 ⇒ 地板证明。
	driveNoGainProbes(t, probePath, probeWhaleLog, 1)
	if !sessionEventLogOversized(probePath, probeWhaleLog, probeWhaleContent) {
		t.Fatal("one measured no-gain is not the floor yet: probe must stay authorized")
	}
	noteSessionFoldOutcome(probePath, probeWhaleLog, probeWhaleLog)

	// 地板：本 regime 内不折叠，WARN 恰好一条（710 限频契约不变）。
	for i := 0; i < 3; i++ {
		if sessionEventLogOversized(probePath, probeWhaleLog, probeWhaleContent) {
			t.Fatal("measured floor must skip the fold within the regime")
		}
	}
	if got := strings.Count(buf.String(), "fold would not shrink it"); got != 1 {
		t.Fatalf("floor WARN count = %d, want exactly 1 (size-regime limited)", got)
	}
	if !strings.Contains(buf.String(), "remedy=") {
		t.Fatalf("floor WARN must carry the remediation attr: %q", buf.String())
	}

	// regime 再武装：日志再涨 ~10%，豁免失效，probe 重新放行——永不永久。
	regrown := probeWhaleLog * 111 / 100
	if !sessionEventLogOversized(probePath, regrown, probeWhaleContent) {
		t.Fatal("regime expiry must re-arm the probe ladder (no permanent exemption)")
	}
}

// 验收②（回报侧）：实测省到 ≥10% ⇒ 估计错了——连败清零、归档闩清零、继续
// 正常折叠；实测无收益才计败。ladder 只信测量。
func TestFoldProbeLadderTrustsMeasurementOverEstimate(t *testing.T) {
	resetEventsRotation(t)
	resetFoldProbeMemoForTest()
	SetEventsAutoRotation("auto", 16, 1)

	// 一次实测无收益：strikes=1。
	driveNoGainProbes(t, probePath, probeWhaleLog, 1)
	// 一次实测有收益（折后 40%）：连败清零。
	noteSessionFoldOutcome(probePath, probeWhaleLog, probeWhaleLog*4/10)
	key := foldProbeKey(probePath)
	foldProbeMemo.mu.Lock()
	strikes := foldProbeMemo.strikes[key]
	latched := foldProbeMemo.archivedAt[key]
	foldProbeMemo.mu.Unlock()
	if strikes != 0 {
		t.Fatalf("measured gain must clear the strike count, got %d", strikes)
	}
	if latched != 0 {
		t.Fatalf("measured gain must clear the floor-archive latch, got %d", latched)
	}
	// 清零之后同一形态重新起跑：再要豁免必须重新攒满两次实测无收益——
	// 一次实测无收益（strikes=1）不得继承旧地板直接豁免。
	driveNoGainProbes(t, probePath, probeWhaleLog, 1)
	if !sessionEventLogOversized(probePath, probeWhaleLog, probeWhaleContent) {
		t.Fatal("floor must be re-proven from cleared strikes: one no-gain is not enough")
	}
	noteSessionFoldOutcome(probePath, probeWhaleLog, probeWhaleLog)
	if sessionEventLogOversized(probePath, probeWhaleLog, probeWhaleContent) {
		t.Fatal("two consecutive measured no-gains after the reset must prove the floor")
	}
}

// 验收③：小日志 / profitable / factor / manual 各分支零改动——under-cap 不进
// ladder、不记账；EventsFoldProfitable 本体未被触碰。
func TestFoldProbeLadderLeavesSmallLogsAlone(t *testing.T) {
	resetEventsRotation(t)
	resetFoldProbeMemoForTest()
	SetEventsAutoRotation("auto", 16, 1)

	// under-cap：判定 false 且完全不入账。
	if sessionEventLogOversized(probePath, 900<<10, 800<<10) {
		t.Fatal("under-cap log must not rotate")
	}
	key := foldProbeKey(probePath)
	foldProbeMemo.mu.Lock()
	_, hasRegime := foldProbeMemo.regime[key]
	_, hasStrikes := foldProbeMemo.strikes[key]
	foldProbeMemo.mu.Unlock()
	if hasRegime || hasStrikes {
		t.Fatal("under-cap sighting must not book any ladder state")
	}

	// profitable over-cap（省 ≥10%）：第一分支直接放行，同样不入账。
	if !sessionEventLogOversized(probePath, 1600<<10, 800<<10) {
		t.Fatal("profitable fold must rotate")
	}
	foldProbeMemo.mu.Lock()
	_, hasRegime = foldProbeMemo.regime[key]
	foldProbeMemo.mu.Unlock()
	if hasRegime {
		t.Fatal("estimate-positive sighting must not book ladder state")
	}

	// factor 分支与 manual 模式由既有用例（TestEventsRotation*）钉死，这里只
	// 守住 ladder 的新增面：off 模式永不放行。
	SetEventsAutoRotation("off", 16, 1)
	if sessionEventLogOversized(probePath, probeWhaleLog, probeWhaleContent) {
		t.Fatal("off mode must never rotate")
	}
}

// 验收②（兜底侧）：实测地板 + 仍超 cap ⇒ save 尾部无损 zstd 归档，实收体积
// 下降可量化（bytesBefore/bytesAfter/freedBytes 落账）；每 regime 至多一次
// （materialize 后同尺寸不重压）；regime 再武装后可再压。
func TestFoldFloorArchiveFallbackQuantifiedShrink(t *testing.T) {
	resetEventsRotation(t)
	resetFoldProbeMemoForTest()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(prev)
	SetEventsAutoRotation("auto", 16, 1)

	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "tl1285floor.jsonl")
	plain := SessionEventLogPath(sessionPath)
	// 可压 fixture：重复 JSONL 行，~5MiB > cap(1MiB) 且 ≥ 压缩地板(4MiB)。
	line := []byte("{\"schema_version\":1,\"type\":\"append\",\"messages\":[{\"role\":\"user\",\"content\":\"tl1285 floor archive fixture line\"}]}\n")
	var b []byte
	for int64(len(b)) < 5<<20 {
		b = append(b, line...)
	}
	if err := os.WriteFile(plain, b, 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	before, err := os.Stat(plain)
	if err != nil {
		t.Fatalf("stat fixture: %v", err)
	}

	// 实测地板：两次真实无收益（与 save 路径同协议），再由 gate 判一次地板
	// （floorSeen 置位——save 尾部消费的是这份判定，不是保存后尺寸的重推）。
	driveNoGainProbes(t, sessionPath, before.Size(), 2)
	if sessionEventLogOversized(sessionPath, before.Size(), before.Size()) {
		t.Fatal("measured floor must skip the fold")
	}

	// save 尾部兜底：归档落地，明文退场，体积真实下降。
	(&Session{}).maybeFloorArchiveEventLog(sessionPath)
	if _, err := os.Stat(plain); !os.IsNotExist(err) {
		t.Fatalf("plaintext must be archived away, stat err=%v", err)
	}
	zst := sessionEventLogZstPathFor(sessionPath)
	zi, err := os.Stat(zst)
	if err != nil {
		t.Fatalf("archive missing after floor fallback: %v", err)
	}
	if zi.Size() >= before.Size() {
		t.Fatalf("archive must shrink on-disk bytes: before=%d after=%d", before.Size(), zi.Size())
	}
	out := buf.String()
	if !strings.Contains(out, "fold-floor fallback archived") ||
		!strings.Contains(out, "bytesBefore=") || !strings.Contains(out, "freedBytes=") {
		t.Fatalf("fallback must log the quantified before/after: %q", out)
	}

	// 每 regime 一次：物化回来后同尺寸再触发不得重压（floorSeen 已被消费 +
	// archivedAt 闩双保险）。
	if _, err := ensureSessionEventLogPlain(sessionPath); err != nil {
		t.Fatalf("materialize: %v", err)
	}
	mi, err := os.Stat(plain)
	if err != nil {
		t.Fatalf("stat materialized: %v", err)
	}
	// 先无判定（floorSeen 未置位）：不得动。
	(&Session{}).maybeFloorArchiveEventLog(sessionPath)
	if _, err := os.Stat(plain); err != nil {
		t.Fatalf("tail without a floor verdict must not archive: %v", err)
	}
	// 再有判定但同尺寸（archivedAt 闩）：仍不得重压。
	if sessionEventLogOversized(sessionPath, mi.Size(), mi.Size()) {
		t.Fatal("floor must persist within the regime")
	}
	(&Session{}).maybeFloorArchiveEventLog(sessionPath)
	if _, err := os.Stat(plain); err != nil {
		t.Fatalf("latched floor must not re-archive at the same size: %v", err)
	}
	if mi.Size() != before.Size() {
		t.Fatalf("materialized size must equal the archived plain_bytes: %d vs %d", mi.Size(), before.Size())
	}

	// regime 再武装：涨 ~11% ⇒ 闩与连败清零，重新攒两次实测无收益、gate 再判
	// 地板（floorSeen 置位）后可再压。
	grown := mi.Size() * 111 / 100
	f, err := os.OpenFile(plain, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatalf("open for regrowth: %v", err)
	}
	for int64(len(b)) < grown-mi.Size() {
		b = append(b, line...)
	}
	if _, err := f.Write(b[:grown-mi.Size()]); err != nil {
		f.Close()
		t.Fatalf("append regrowth: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close regrowth: %v", err)
	}
	driveNoGainProbes(t, sessionPath, grown, 2)
	if sessionEventLogOversized(sessionPath, grown, grown) {
		t.Fatal("re-proven floor must skip the fold")
	}
	(&Session{}).maybeFloorArchiveEventLog(sessionPath)
	if _, err := os.Stat(plain); !os.IsNotExist(err) {
		t.Fatalf("re-armed regime must archive again, stat err=%v", err)
	}
}

// 键口径：DAG 侧 gate 以已解析日志路径入账（st.path），save 尾部兜底以会话
// 路径查账——两者必须落到同一本账，否则 DAG 会话永远到不了归档兜底。
func TestFoldProbeKeyNormalizesCallerShapes(t *testing.T) {
	resetEventsRotation(t)
	resetFoldProbeMemoForTest()
	SetEventsAutoRotation("auto", 16, 1)

	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "tl1285key.jsonl")
	logPath := SessionEventLogPath(sessionPath)

	// DAG 形态：日志路径记账到地板。
	driveNoGainProbes(t, logPath, probeWhaleLog, 2)
	// 会话路径查账：同一本账，地板可见。
	if !foldProbeFloorReached(sessionPath, probeWhaleLog) {
		t.Fatal("session-path key must read the state booked under the log-path key")
	}
	if sessionEventLogOversized(sessionPath, probeWhaleLog, probeWhaleContent) {
		t.Fatal("gate on the session-path shape must honor the floor booked by the log-path shape")
	}
}

// 端到端：真实 save 路径上的整链——两次 Rewrite 触发 probe 折叠（实测无收益
// 入账），第三次 Rewrite 走实测地板（gate false），save 尾部归档兜底开火，
// 明文退场、归档可物化。这是 20261026 事故形态（大会话每次保存反复 WARN）
// 在修复后的直证。
func TestFoldFloorFallbackThroughRealSave(t *testing.T) {
	resetEventsRotation(t)
	resetFoldProbeMemoForTest()
	resetRotateWarnMemo(t)
	SetEventsAutoRotation("auto", 16, 1)

	dir := t.TempDir()
	path := filepath.Join(dir, "tl1285e2e.jsonl")
	s := NewSession("sys")
	big := strings.Repeat("tl1285 e2e floor fixture payload ", 180_000) // ~6MB
	s.Add(provider.Message{Role: provider.RoleUser, Content: big})
	s.Add(provider.Message{Role: provider.RoleAssistant, Content: "ack"})
	if err := s.Save(path); err != nil {
		t.Fatalf("initial save: %v", err)
	}
	plain := SessionEventLogPath(path)
	info, err := os.Stat(plain)
	if err != nil {
		t.Fatalf("log after initial save: %v", err)
	}
	if info.Size() <= 4<<20 {
		t.Fatalf("fixture too small to pass the cap and compress floor: %d", info.Size())
	}

	// 两次历史改写：gate 放行 probe，compact 落地 ≈content ⇒ 实测无收益入账。
	for i := 0; i < 2; i++ {
		msgs := append([]provider.Message(nil), s.Messages...)
		msgs[len(msgs)-1].Content = fmt.Sprintf("bump %d", i)
		s.Rewrite(msgs, "tl1285-e2e")
		if err := s.SaveRewrite(path); err != nil {
			t.Fatalf("rewrite save %d: %v", i, err)
		}
	}
	key := foldProbeKey(path)
	foldProbeMemo.mu.Lock()
	strikes := foldProbeMemo.strikes[key]
	foldProbeMemo.mu.Unlock()
	if strikes != 2 {
		t.Fatalf("two real probe folds must book two strikes, got %d", strikes)
	}

	// 第三次改写：实测地板（gate false）+ save 尾部归档兜底。
	msgs := append([]provider.Message(nil), s.Messages...)
	msgs[len(msgs)-1].Content = "final bump"
	s.Rewrite(msgs, "tl1285-e2e")
	if err := s.SaveRewrite(path); err != nil {
		t.Fatalf("floor save: %v", err)
	}
	if _, err := os.Stat(plain); !os.IsNotExist(err) {
		t.Fatalf("floor fallback must archive the plaintext through the real save path, stat err=%v", err)
	}
	if _, err := os.Stat(sessionEventLogZstPathFor(path)); err != nil {
		t.Fatalf("archive missing after the real-save floor fallback: %v", err)
	}
	// 无损可逆：物化回来内容完整（头校验 + 尺寸强校验在原语内）。
	if _, err := ensureSessionEventLogPlain(path); err != nil {
		t.Fatalf("materialize after fallback: %v", err)
	}
	// 调研报告 §7-1/§7-2（消费方对账）：归档-物化往返后 (a) 头部仍可探为
	// native、schema 不变（探针不中 ⇒ 掉 checkpoint ⇒ 丢 100 轮以前）；
	// (b) 重放出的消息序列与保存侧在内存中的转录语义等价。
	probe, err := probeSessionEventLog(path)
	if err != nil || !probe.native || probe.schemaVersion != sessionEventSchemaVersion {
		t.Fatalf("probe must stay native schema-1 after the archive round-trip: probe=%+v err=%v", probe, err)
	}
	reloaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("reload after archive round-trip: %v", err)
	}
	got := reloaded.Snapshot()
	want := s.Snapshot()
	if len(got) != len(want) {
		t.Fatalf("archive round-trip must preserve the message count: %d vs %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Role != want[i].Role || got[i].Content != want[i].Content {
			t.Fatalf("archive round-trip must preserve message %d: got %q/%q want %q/%q",
				i, got[i].Role, got[i].Content, want[i].Role, want[i].Content)
		}
	}
}

// tl1291 回归：fresh process 从未 make 过梯子的四个 map——唯一的 make 在
// resetFoldProbeMemoForTest，而默认 manual 模式下 gate 不经 probe，首个
// oversized 保存的尾部 noteSessionFoldOutcome 直接首写 nil map
// （session_events_rotation.go:253），包59（1.38.3-20261031-0810）打开
// 大日志会话必崩且崩后 compact 已落盘、revision 未记录，会话损坏。把四
// map 显式压回 nil 模拟 fresh process，四个加锁写入口都必须自愈。既有用
// 例全部先 reset（即 make）再驱动，恰好掩盖了该缺口——本用例禁止先 reset。
func TestFoldProbeLadderSurvivesFreshProcessNilMaps(t *testing.T) {
	foldProbeMemo.mu.Lock()
	foldProbeMemo.regime = nil
	foldProbeMemo.strikes = nil
	foldProbeMemo.archivedAt = nil
	foldProbeMemo.floorSeen = nil
	foldProbeMemo.mu.Unlock()
	defer resetFoldProbeMemoForTest()

	const path = `C:\p\sessions\tl1291-fresh-nil.jsonl`

	// 保存尾部（manual 模式的首个触点）：panic 即回归。
	noteSessionFoldOutcome(path, 100, 50)
	// auto 模式 gate 探针（另一个首触点）。
	if foldProbeFloorReached(path, 100) {
		t.Fatal("fresh ladder must not report the floor reached on first probe")
	}
	// gate 递交地板判定 + save 尾部消费 + 地板归档闩。
	foldProbeNoteFloorSeen(path)
	if !foldProbeTakeFloorSeen(path) {
		t.Fatal("floorSeen latch lost across lazy init")
	}
	foldProbeNoteFloorArchived(path, 50)

	foldProbeMemo.mu.Lock()
	defer foldProbeMemo.mu.Unlock()
	if got := foldProbeMemo.regime[foldProbeKey(path)]; got != 100 {
		t.Fatalf("regime base not recorded after lazy init: got %d", got)
	}
	if got := foldProbeMemo.archivedAt[foldProbeKey(path)]; got != 50 {
		t.Fatalf("archivedAt latch not recorded after lazy init: got %d", got)
	}
	if got := foldProbeMemo.strikes[foldProbeKey(path)]; got != 0 {
		t.Fatalf("profitable fold must clear strikes: got %d", got)
	}
}
