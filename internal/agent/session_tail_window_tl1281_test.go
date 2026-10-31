package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"runtime/debug"
	"strconv"
	"strings"
	"testing"

	"reasonix/internal/provider"
)

// tl1281 验收测试与量具：非 native 转录（tl1227 后封存 schema-2 日志的
// checkpoint 兜底）的首画尾窗 producer 与干净截断快照跳过。
//
// 背景：tl1227 读侧单路径化后 LoadSessionTail 对封存 schema-2 退化为全量
// checkpoint 解码，骨架 tab 的冷读兜底/首绘/自愈 repair 全在付全量解码——
// tl1279 实测 literalStore 313.86MB 的主要来源（4 个 schema-2 大会话
// 65.88/32.53/172.99/103.94MB）。本文件钉住两件事：
//  1. oversize checkpoint 首画按尾窗截断（truncated=true，内容=全量尾窗），
//     小会话逐字节保持全量直通；
//  2. 干净截断会话的防抖快照零写入（不付 upgrade 全量重载），真变更自动
//     解除跳过走原 upgrade+save，无数据丢失。

// tl1281WriteCheckpoint writes a .jsonl checkpoint of ~mb MiB next to the
// schema-2 events log, matching the production shape: one provider.Message per
// line, ~1MiB bodies (the conversation-transcript shape the tl1279 profile
// attributed literalStore to).
func tl1281WriteCheckpoint(t testing.TB, sessionPath string, mb int) [][]byte {
	t.Helper()
	const bodyBytes = 1 << 20
	body := strings.Repeat("t", bodyBytes)
	perLine := bodyBytes + 128
	n := mb << 20 / perLine
	if n < 4 {
		n = 4
	}
	var raws [][]byte
	var b strings.Builder
	for i := 0; i < n; i++ {
		role := provider.RoleUser
		if i%2 == 1 {
			role = provider.RoleAssistant
		}
		m := provider.Message{Role: role, Content: body, ID: fmt.Sprintf("K%d", i)}
		line, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("marshal checkpoint line %d: %v", i, err)
		}
		b.Write(line)
		b.WriteByte('\n')
		raws = append(raws, line)
	}
	if err := os.WriteFile(sessionPath, []byte(b.String()), 0o600); err != nil {
		t.Fatalf("write checkpoint: %v", err)
	}
	return raws
}

// tl1281SealedSession builds a sealed schema-2 session: a schema-2 events log
// (probe.dag — tl1227 弃读封存, checkpoint fallback owns reads) plus a ~mb MiB
// checkpoint. Returns the session path and the checkpoint lines.
func tl1281SealedSession(t testing.TB, mb int) (string, [][]byte) {
	t.Helper()
	path := tl1228BuildDAGSession(t, 1) // schema-2 head log, small
	raws := tl1281WriteCheckpoint(t, path, mb)
	return path, raws
}

func TestTL1281TailWindowTruncatesOversizeCheckpoint(t *testing.T) {
	path, raws := tl1281SealedSession(t, 24)

	full, err := LoadSession(path)
	if err != nil {
		t.Fatalf("full load: %v", err)
	}
	if full.TailTruncated() {
		t.Fatal("full load must never report truncation")
	}
	tail, err := LoadSessionTail(path)
	if err != nil {
		t.Fatalf("tail load: %v", err)
	}
	if !tail.TailTruncated() {
		t.Fatal("oversize checkpoint tail load must report truncation; the decode-state acceptance would regress to a full decode")
	}
	fullMsgs := full.Snapshot()
	tailMsgs := tail.Snapshot()
	if len(tailMsgs) == 0 || len(tailMsgs) >= len(fullMsgs) {
		t.Fatalf("tail window = %d of %d messages, want a strict non-empty subset", len(tailMsgs), len(fullMsgs))
	}
	// The window must be exactly the transcript's trailing messages.
	for i := range tailMsgs {
		if tailMsgs[i].ID != fullMsgs[len(fullMsgs)-len(tailMsgs)+i].ID {
			t.Fatalf("tail window misaligned at %d: %q vs %q", i, tailMsgs[i].ID, fullMsgs[len(fullMsgs)-len(tailMsgs)+i].ID)
		}
	}
	// Every window message decodes standalone from the same checkpoint lines.
	if len(tailMsgs) > len(raws) {
		t.Fatalf("window holds %d messages but checkpoint only wrote %d lines", len(tailMsgs), len(raws))
	}
}

func TestTL1281TailWindowSmallCheckpointStaysFull(t *testing.T) {
	path, _ := tl1281SealedSession(t, 1)

	plain, err := LoadSession(path)
	if err != nil {
		t.Fatal(err)
	}
	tail, err := LoadSessionTail(path)
	if err != nil {
		t.Fatal(err)
	}
	if tail.TailTruncated() {
		t.Fatal("a sub-window checkpoint must not be reported truncated (831③ small-session byte-identity)")
	}
	if len(tail.Snapshot()) != len(plain.Snapshot()) {
		t.Fatalf("small-session tail = %d msgs, full = %d msgs", len(tail.Snapshot()), len(plain.Snapshot()))
	}
}

func TestTL1281SnapshotTailWindowCleanSkipsWrite(t *testing.T) {
	path, before := tl1281SealedSession(t, 24)
	s, err := LoadSessionTail(path)
	if err != nil {
		t.Fatal(err)
	}
	if !s.TailTruncated() {
		t.Fatal("fixture premise: tail session expected")
	}
	if !s.snapshotTailWindowClean(path) {
		t.Fatal("a freshly loaded, unchanged tail window must qualify for the snapshot skip")
	}
	if s.HasUnsavedChanges(path) {
		t.Fatal("a clean tail window must not report unsaved changes (the idle-eviction gate relies on this)")
	}
	digestBefore := checkpointFingerprint(t, path)
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatalf("snapshot of a clean tail window: %v", err)
	}
	if digestBefore != checkpointFingerprint(t, path) {
		t.Fatal("the snapshot skip must not touch the checkpoint bytes; a rewrite here would mean the upgrade full reload still ran")
	}
	if !s.TailTruncated() {
		t.Fatal("skipped snapshot must not upgrade the session")
	}

	// A real change disarms the skip and the save runs the ordinary upgrade:
	// full history plus the new turn, nothing lost.
	s.Add(provider.Message{Role: provider.RoleUser, Content: "next turn", ID: "K-new"})
	if s.snapshotTailWindowClean(path) {
		t.Fatal("a version bump must disarm the tail-window snapshot skip")
	}
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatalf("save after change: %v", err)
	}
	if s.TailTruncated() {
		t.Fatal("a real save must have upgraded the transcript before writing")
	}
	reloaded, err := LoadSession(path)
	if err != nil {
		t.Fatal(err)
	}
	msgs := reloaded.Snapshot()
	if len(msgs) != len(before)+1 {
		t.Fatalf("reloaded transcript = %d messages, want the full %d + the new turn", len(msgs), len(before))
	}
	if msgs[len(msgs)-1].ID != "K-new" {
		t.Fatalf("last message = %q, want the new turn", msgs[len(msgs)-1].ID)
	}
}

func TestTL1281SnapshotTailWindowCleanDisarmedByDiskChange(t *testing.T) {
	path, _ := tl1281SealedSession(t, 24)
	s, err := LoadSessionTail(path)
	if err != nil {
		t.Fatal(err)
	}
	if !s.snapshotTailWindowClean(path) {
		t.Fatal("fixture premise: clean tail window expected")
	}
	// An external writer appends to the checkpoint: the window is no longer a
	// projection of the bytes it was decoded from.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(strings.Repeat("z", 1<<10) + "\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if s.snapshotTailWindowClean(path) {
		t.Fatal("an appended checkpoint must disarm the tail-window snapshot skip")
	}
}

func checkpointFingerprint(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return strconv.Itoa(len(data)) + ":" + fmt.Sprint(len(strings.Split(string(data), "\n")))
}

// TestTL1281MemoryBaselineReading is the controlled A/B for the receipt:
// identical build, identical fixtures (three sealed schema-2 sessions sized
// like the tl1280 acceptance samples), two retention legs —
//   - leg "before_full_decode": LoadSessionDisplayMessages over all three,
//     results retained — the pre-change cold-path/repair decode state
//     (background tabs fully decoded);
//   - leg "after_tail_window": LoadSessionTail over all three, windows
//     retained — the post-change background state (bounded first-paint
//     windows; the eviction gate then frees even these).
//
// Each leg GC-settles, reports HeapInuse, and drops a heap profile next to the
// test for the literalStore row (`go tool pprof -top -inuse_space <file>` —
// the acceptance-metric attribution). Gated by REASONIX_TL1281_MEMBASELINE=1;
// CI never pays the fixture.
func TestTL1281MemoryBaselineReading(t *testing.T) {
	if os.Getenv("REASONIX_TL1281_MEMBASELINE") != "1" {
		t.Skip("set REASONIX_TL1281_MEMBASELINE=1 (and optionally REASONIX_TL1281_SESSION_MB) for the controlled reading")
	}
	mb := 24
	if v, err := strconv.Atoi(os.Getenv("REASONIX_TL1281_SESSION_MB")); err == nil && v > 0 {
		mb = v
	}
	sessions := make([]string, 0, 3)
	for i := 0; i < 3; i++ {
		path, _ := tl1281SealedSession(t, mb)
		sessions = append(sessions, path)
	}

	settle := func() runtime.MemStats {
		runtime.GC()
		runtime.GC()
		// Double GC alone leaves the previous leg's freed spans in HeapInuse;
		// the scavenge returns them so each leg measures its own retention.
		debug.FreeOSMemory()
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		return ms
	}
	writeProfile := func(name string) string {
		out := filepath.Join(os.TempDir(), name)
		f, err := os.Create(out)
		if err != nil {
			t.Fatalf("profile %s: %v", out, err)
		}
		defer f.Close()
		if err := pprof.Lookup("heap").WriteTo(f, 0); err != nil {
			t.Fatalf("write heap profile: %v", err)
		}
		return out
	}

	// before: every background tab decoded in full and retained.
	retainedFull := make([][]provider.Message, 0, len(sessions))
	for _, path := range sessions {
		msgs, _, _, err := LoadSessionDisplayMessages(path)
		if err != nil {
			t.Fatalf("before leg %s: %v", path, err)
		}
		retainedFull = append(retainedFull, msgs)
	}
	// Liveness, not scope, decides collectability: without KeepAlive the
	// compiler treats retainedFull as dead after its last use and settle()'s
	// GC frees the very decode state this leg must hold (measured: 72MB of
	// retained content read back as a 4.8MB HeapInuse). A straight-line call
	// AFTER the reading — a defer would pin the leg until function exit and
	// bleed it into the after leg (measured: after leg read 340MB vs the
	// windows' ~57MB because the before leg was still pinned).
	ms := settle()
	beforeHeap := float64(ms.HeapInuse) / (1 << 20)
	runtime.KeepAlive(retainedFull)
	beforeProfile := writeProfile("tl1281-before.pprof")

	// Free the before-leg retention so the after leg measures its own state.
	retainedFull = nil
	ms = settle()

	retainedTail := make([][]provider.Message, 0, len(sessions))
	truncated := 0
	for _, path := range sessions {
		sess, err := LoadSessionTail(path)
		if err != nil {
			t.Fatalf("after leg %s: %v", path, err)
		}
		if sess.TailTruncated() {
			truncated++
		}
		retainedTail = append(retainedTail, sess.Snapshot())
	}
	ms = settle()
	afterHeap := float64(ms.HeapInuse) / (1 << 20)
	runtime.KeepAlive(retainedTail)
	afterProfile := writeProfile("tl1281-after.pprof")
	retainedTail = nil

	t.Logf("TL1281 leg=before_full_decode sessions=%d fixture_mb=%d heap_inuse=%.1fMB profile=%s",
		len(sessions), mb, beforeHeap, beforeProfile)
	t.Logf("TL1281 leg=after_tail_window   sessions=%d fixture_mb=%d truncated=%d heap_inuse=%.1fMB profile=%s",
		len(sessions), mb, truncated, afterHeap, afterProfile)
	if truncated != len(sessions) {
		t.Fatalf("after leg truncated %d of %d sessions; the tail window did not engage on every acceptance fixture", truncated, len(sessions))
	}
}
