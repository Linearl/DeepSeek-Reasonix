package agent

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"reasonix/internal/provider"
	"reasonix/internal/store"
)

// tl 1172 验收落测（agent 侧原语；sweep 侧接线在
// desktop/events_idle_sweep_slim_test.go）。

// dagBigFixture writes a >4MiB DAG log (120 messages × ~40KiB) and returns 10
// evenly sampled messages for verbatim comparison. 构造与 107MB 实证会话同形
// （大体量 events；压缩不看折叠收益）。
func dagBigFixture(t *testing.T, sessionPath string) (samples map[string]string, count int) {
	t.Helper()
	base := time.Date(2026, 1, 8, 10, 0, 0, 0, time.UTC)
	big := strings.Repeat("zstd-slim-roundtrip-payload-", 1500) // ~40KiB
	entries := []sessionDAGEntry{{Type: sessionDAGTypeLog, Generation: 1, At: base}}
	parent := ""
	samples = make(map[string]string)
	for i := 0; i < 120; i++ {
		m := dagMsg(provider.RoleUser, fmt.Sprintf("%s#%04d", big, i), fmt.Sprintf("q%d", i))
		entries = append(entries, dagMessageEntry(t, SessionMainHead, parent, "t1", m, base.Add(time.Duration(i)*time.Second)))
		parent = m.ID
		if i%12 == 0 {
			samples[m.ID] = m.Content
		}
	}
	dagAppend(t, sessionPath, entries...)
	return samples, 120
}

func samplesPresent(msgs []provider.Message, samples map[string]string) error {
	byID := make(map[string]provider.Message, len(msgs))
	for _, m := range msgs {
		byID[m.ID] = m
	}
	for id, want := range samples {
		got, ok := byID[id]
		if !ok {
			return fmt.Errorf("sampled message %s missing", id)
		}
		if got.Content != want {
			return fmt.Errorf("sampled message %s content diverged: got %d bytes want %d bytes", id, len(got.Content), len(want))
		}
	}
	return nil
}

// 验收 1（体积）+ 验收 3（读路径/追加路径）：
//   - 压缩后体积下降 ≥50%，明文消失、归档在位；
//   - 压缩态直接 LoadSession（探针/预算/重放全链经 limitsForSessionLog 物化），
//     随机抽 10 条历史逐字一致；
//   - 「压缩 → 追加」直连形态：物化 → 追加 → 明文回归、归档清理、新旧消息俱全。
func TestSessionEventsZstCompressRoundTripAndAppend(t *testing.T) {
	path := dagTestSession(t)
	plain := store.SessionEventLog(path)
	samples, count := dagBigFixture(t, path)
	before, err := os.Stat(plain)
	if err != nil {
		t.Fatalf("stat plain before compress: %v", err)
	}
	if before.Size() < int64(4<<20) {
		t.Fatalf("fixture too small for the compress floor: %d", before.Size())
	}

	gotBefore, gotAfter, err := CompressSessionEventLogZst(path)
	if err != nil {
		t.Fatalf("compress: %v", err)
	}
	if gotBefore != before.Size() {
		t.Fatalf("compress before = %d, want %d", gotBefore, before.Size())
	}
	if _, statErr := os.Stat(plain); !os.IsNotExist(statErr) {
		t.Fatalf("plaintext must be gone after archive, stat err = %v", statErr)
	}
	zst := plain + ".zst"
	info, statErr := os.Stat(zst)
	if statErr != nil {
		t.Fatalf("archive missing after compress: %v", statErr)
	}
	if gotAfter != info.Size() {
		t.Fatalf("compress after = %d, want archive size %d", gotAfter, info.Size())
	}
	if reduction := 1 - float64(gotAfter)/float64(gotBefore); reduction < 0.5 {
		t.Fatalf("archive saved only %.1f%% (before=%d after=%d), want >=50%%", reduction*100, gotBefore, gotAfter)
	}

	// 读路径：压缩态直接加载，抽样逐字一致，且读动作把明文物化回来。
	s, err := LoadSession(path)
	if err != nil {
		t.Fatalf("load after compress: %v", err)
	}
	if len(samples) != 10 {
		t.Fatalf("fixture sampled %d messages, want 10", len(samples))
	}
	if len(s.Messages) != count {
		t.Fatalf("message count after roundtrip = %d, want %d", len(s.Messages), count)
	}
	if err := samplesPresent(s.Messages, samples); err != nil {
		t.Fatalf("roundtrip verbatim check: %v", err)
	}
	if _, statErr := os.Stat(plain); statErr != nil {
		t.Fatalf("read path did not materialize the plaintext back: %v", statErr)
	}
	if _, statErr := os.Stat(zst); !os.IsNotExist(statErr) {
		t.Fatalf("stale archive must be gone after materialization, stat err = %v", statErr)
	}

	// 追加路径（压缩 → 追加直连）：先回到归档态，再加载+追加+保存。
	if _, _, err := CompressSessionEventLogZst(path); err != nil {
		t.Fatalf("re-compress: %v", err)
	}
	s2, err := LoadSession(path)
	if err != nil {
		t.Fatalf("load compressed session for append: %v", err)
	}
	fresh := dagMsg(provider.RoleUser, "post-archive append", "qNEW")
	s2.Add(fresh)
	if err := s2.Save(path); err != nil {
		t.Fatalf("save after append: %v", err)
	}
	s3, err := LoadSession(path)
	if err != nil {
		t.Fatalf("reload after append: %v", err)
	}
	if len(s3.Messages) != count+1 {
		t.Fatalf("message count after append = %d, want %d", len(s3.Messages), count+1)
	}
	got := map[string]provider.Message{}
	for _, m := range s3.Messages {
		got[m.ID] = m
	}
	if m, ok := got[fresh.ID]; !ok || m.Content != fresh.Content {
		t.Fatalf("appended message missing or diverged after materialized append")
	}
	if err := samplesPresent(s3.Messages, samples); err != nil {
		t.Fatalf("history after append: %v", err)
	}
	if _, statErr := os.Stat(plain); statErr != nil {
		t.Fatalf("append did not restore the plaintext log: %v", statErr)
	}
}

// 验收 5（回归面）：小会话不被额外处理——地板之下压缩拒绝、原样保留。
func TestSessionEventsZstCompressFloorRefuses(t *testing.T) {
	path := dagTestSession(t)
	dagLinearLog(t, path)
	plain := store.SessionEventLog(path)
	before, err := os.Stat(plain)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	b, a, err := CompressSessionEventLogZst(path)
	if err == nil {
		t.Fatal("compress under the floor must refuse")
	}
	if b != before.Size() || a != before.Size() {
		t.Fatalf("refused compress reported sizes %d/%d, want both %d", b, a, before.Size())
	}
	if _, statErr := os.Stat(plain); statErr != nil {
		t.Fatalf("small session log must stay untouched: %v", statErr)
	}
	if _, statErr := os.Stat(plain + ".zst"); !os.IsNotExist(statErr) {
		t.Fatalf("small session must not gain an archive: %v", statErr)
	}
}

// 探针不误判：压缩态会话的 probe 必须经物化看到真实 DAG 头，绝不能把
// 「明文缺失」读成「无日志」——那会让保存路径另起新明文覆盖历史。
func TestSessionEventsZstProbeClassifiesAfterCompress(t *testing.T) {
	path := dagTestSession(t)
	dagBigFixture(t, path)
	if _, _, err := CompressSessionEventLogZst(path); err != nil {
		t.Fatalf("compress: %v", err)
	}
	if _, statErr := os.Stat(store.SessionEventLog(path)); !os.IsNotExist(statErr) {
		t.Fatalf("fixture must be archived before the probe, stat err = %v", statErr)
	}
	probe, err := probeSessionEventLog(path)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if !probe.dag {
		t.Fatalf("probe after compress must classify the DAG log, got dag=%v native=%v size=%d", probe.dag, probe.native, probe.size)
	}
}

// 坏归档现场保全：解不开的归档挪进 .damaged，明文绝不落半份——保存路径
// 看到的是显式错误，而不是「无日志」。
func TestSessionEventsZstBrokenArchiveGoesDamaged(t *testing.T) {
	path := dagTestSession(t)
	plain := store.SessionEventLog(path)
	zst := plain + ".zst"
	if err := os.WriteFile(zst, []byte("definitely-not-a-zstd-frame"), 0o600); err != nil {
		t.Fatalf("seed broken archive: %v", err)
	}
	_, err := ensureSessionEventLogPlain(path)
	if err == nil {
		t.Fatal("broken archive must surface an error, not an empty plaintext")
	}
	if _, statErr := os.Stat(plain); !os.IsNotExist(statErr) {
		t.Fatalf("broken archive must not materialize a plaintext, stat err = %v", statErr)
	}
	if _, statErr := os.Stat(zst); !os.IsNotExist(statErr) {
		t.Fatalf("broken archive must be moved aside, stat err = %v", statErr)
	}
	if _, statErr := os.Stat(zst + ".damaged"); statErr != nil {
		t.Fatalf("broken archive bytes must be preserved as .damaged: %v", statErr)
	}
}

// 并存态明文优先：压缩收尾删除失败留下的「两者并存」时，读取以明文为准、
// 不动归档——归档可能是压缩临界区的fresh 产物，读取侧按路径清它有丢数据
// 窗口（物化不持锁）。残件靠下次压缩覆盖或下次物化成功后清理。
func TestSessionEventsZstBothPresentPlaintextWins(t *testing.T) {
	path := dagTestSession(t)
	plain := store.SessionEventLog(path)
	dagLinearLog(t, path)
	zst := plain + ".zst"
	if err := os.WriteFile(zst, []byte{0x28, 0xb5, 0x2f, 0xfd, 0x00}, 0o600); err != nil {
		t.Fatalf("seed stale archive: %v", err)
	}
	materialized, err := ensureSessionEventLogPlain(path)
	if err != nil {
		t.Fatalf("ensure with both present: %v", err)
	}
	if materialized {
		t.Fatal("plaintext present: nothing to materialize")
	}
	if SessionEventLogZstSize(path) == 0 {
		t.Fatal("size口径 must report the plaintext while it exists")
	}
	if _, statErr := os.Stat(plain); statErr != nil {
		t.Fatalf("plaintext must survive the both-present read: %v", statErr)
	}
}

// 幂等：对归档态（明文已不在）再压一次不报错、不动归档——sweep 误触发的
// 安全网。
func TestSessionEventsZstCompressIdempotentOnArchived(t *testing.T) {
	path := dagTestSession(t)
	zst := store.SessionEventLog(path) + ".zst"
	if err := os.WriteFile(zst, []byte{0x28, 0xb5, 0x2f, 0xfd, 0x00}, 0o600); err != nil {
		t.Fatalf("seed archive: %v", err)
	}
	b, a, err := CompressSessionEventLogZst(path)
	if err != nil {
		t.Fatalf("compress on archived session must be a no-op success: %v", err)
	}
	if b != 0 || a == 0 {
		t.Fatalf("idempotent compress reported before=%d after=%d, want before=0 after=archive size", b, a)
	}
	if _, statErr := os.Stat(zst); errors.Is(statErr, os.ErrNotExist) {
		t.Fatal("archive must survive the idempotent call")
	}
}
