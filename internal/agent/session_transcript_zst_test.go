package agent

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/provider"
	"reasonix/internal/store"
)

// tl 1174 验收落测（agent 侧原语；sweep 开关门在 desktop/transcript_zstd_sweep_test.go）。
//
// 四路径矩阵（任务书红线）：
//   冷压缩   —— TestSessionTranscriptZstColdCompressRoundTrip（含 ≥5× 实测）
//   冷读透明 —— TestSessionTranscriptZstColdReadTransparent
//   热不碰   —— TestSessionTranscriptZstHotNotCompressed
//   恢复物化 —— TestSessionTranscriptZstAppendMaterializes
//   开关关   —— TestSessionTranscriptOffByteIdentical（无归档、无额外文件、逐字节一致）
//   坏档回退 —— TestSessionTranscriptZstCorruptArchivePreserved
//   发现面   —— TestListSessionOrderDiscoversArchivedTranscript

// transcriptBigFixture writes a >1MiB checkpoint-only transcript (no event log,
// so the .jsonl IS the read face) and returns the verbatim contents of 10
// evenly sampled messages plus the exact on-disk bytes.
func transcriptBigFixture(t *testing.T, sessionPath string) (samples []string, onDisk []byte) {
	t.Helper()
	big := strings.Repeat("tl1174-cold-archive-payload-", 1500) // ~40KiB per message
	msgs := make([]provider.Message, 0, 96)
	for i := 0; i < 96; i++ {
		msgs = append(msgs, dagMsg(provider.RoleUser, fmt.Sprintf("%s#%04d", big, i), fmt.Sprintf("m%d", i)))
	}
	if err := writeSessionMessages(sessionPath, msgs); err != nil {
		t.Fatalf("write fixture transcript: %v", err)
	}
	onDisk, err := os.ReadFile(sessionPath)
	if err != nil {
		t.Fatalf("read fixture back: %v", err)
	}
	if int64(len(onDisk)) < SessionTranscriptZstCompressFloor {
		t.Fatalf("fixture too small for the compress floor: %d", len(onDisk))
	}
	for i := 0; i < 10; i++ {
		samples = append(samples, msgs[i*9].Content)
	}
	return samples, onDisk
}

func ageTranscriptCold(t *testing.T, sessionPath string, now time.Time) {
	t.Helper()
	cold := now.Add(-2 * SessionTranscriptColdAfter) // 2× 阈值前，稳过冷门
	if err := os.Chtimes(sessionPath, cold, cold); err != nil {
		t.Fatalf("age transcript: %v", err)
	}
}

func assertGone(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected %s to be gone, stat err = %v", path, err)
	}
}

// 验收（冷压缩）：冷会话整文件压缩为 .jsonl.zst，体积下降 ≥5×（实测非理论），
// 明文消失、归档在位、归档保留原 mtime（冷度事实源不被压缩污染）。
func TestSessionTranscriptZstColdCompressRoundTrip(t *testing.T) {
	now := time.Now()
	path := filepath.Join(t.TempDir(), "cold-session.jsonl")
	samples, onDisk := transcriptBigFixture(t, path)
	ageTranscriptCold(t, path, now)

	before, after, err := CompressSessionTranscriptZst(path, now)
	if err != nil {
		t.Fatalf("compress: %v", err)
	}
	if before != int64(len(onDisk)) {
		t.Fatalf("compress before = %d, want %d", before, len(onDisk))
	}
	assertGone(t, path) // 明文必须消失（不删明文=没省字节）
	zst := store.SessionTranscriptZst(path)
	info, statErr := os.Stat(zst)
	if statErr != nil {
		t.Fatalf("archive missing after compress: %v", statErr)
	}
	if after != info.Size() {
		t.Fatalf("compress after = %d, want archive size %d", after, info.Size())
	}
	if ratio := float64(before) / float64(after); ratio < 5 {
		t.Fatalf("compression ratio %.1fx (before=%d after=%d), want >=5x on the repetitive fixture", ratio, before, after)
	}
	if got := info.ModTime(); now.Sub(got) < SessionTranscriptColdAfter {
		t.Fatalf("archive mtime %v lost the original cold stamp (age %s)", got, now.Sub(got))
	}
	_ = samples // 逐字比对在冷读测试里做
}

// 验收（冷读透明）：压缩态直接 LoadSession——物化回明文、归档清理、抽样
// 10 条历史逐字一致（任务书验收 4 的比对口径）。
func TestSessionTranscriptZstColdReadTransparent(t *testing.T) {
	now := time.Now()
	path := filepath.Join(t.TempDir(), "coldread-session.jsonl")
	samples, _ := transcriptBigFixture(t, path)
	ageTranscriptCold(t, path, now)
	if _, _, err := CompressSessionTranscriptZst(path, now); err != nil {
		t.Fatalf("compress: %v", err)
	}

	s, err := LoadSession(path)
	if err != nil {
		t.Fatalf("cold load must be transparent: %v", err)
	}
	if len(s.Messages) != 96 {
		t.Fatalf("loaded %d messages, want 96", len(s.Messages))
	}
	byContent := make(map[string]bool, len(s.Messages))
	for _, m := range s.Messages {
		byContent[m.Content] = true
	}
	for i, want := range samples {
		if !byContent[want] {
			t.Fatalf("sampled history message %d diverged after cold read", i)
		}
	}
	// 物化语义：明文回归、归档退场（稳态只存其一）。
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("plaintext must materialize on read: %v", err)
	}
	assertGone(t, store.SessionTranscriptZst(path))
}

// 验收（热不碰）：冷阈值内的会话拒绝压缩——明文逐字节不动、无归档产生。
func TestSessionTranscriptZstHotNotCompressed(t *testing.T) {
	now := time.Now()
	path := filepath.Join(t.TempDir(), "hot-session.jsonl")
	_, onDisk := transcriptBigFixture(t, path)
	// mtime=now（刚活跃），热。

	beforeBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read hot transcript: %v", err)
	}
	if !bytes.Equal(beforeBytes, onDisk) {
		t.Fatalf("fixture drifted before the hot attempt")
	}
	gotBefore, gotAfter, compressErr := CompressSessionTranscriptZst(path, now)
	if compressErr == nil {
		t.Fatalf("hot transcript must be refused, got before=%d after=%d", gotBefore, gotAfter)
	}
	afterBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("hot transcript must stay readable: %v", err)
	}
	if !bytes.Equal(beforeBytes, afterBytes) {
		t.Fatalf("hot transcript bytes changed: before=%d after=%d", len(beforeBytes), len(afterBytes))
	}
	assertGone(t, store.SessionTranscriptZst(path))
}

// 验收（恢复活跃即物化 + 追加成功）：压缩 → 打开（物化）→ 追加保存 → 明文
// 回归、归档清理、10 条历史逐字一致 + 新消息在位。
func TestSessionTranscriptZstAppendMaterializes(t *testing.T) {
	now := time.Now()
	path := filepath.Join(t.TempDir(), "resume-session.jsonl")
	samples, _ := transcriptBigFixture(t, path)
	ageTranscriptCold(t, path, now)
	if _, _, err := CompressSessionTranscriptZst(path, now); err != nil {
		t.Fatalf("compress: %v", err)
	}

	// 恢复活跃：打开即物化（冷读透明），然后追加一条新消息保存。
	s, err := LoadSession(path)
	if err != nil {
		t.Fatalf("resume open: %v", err)
	}
	resumed := append(append([]provider.Message{}, s.Messages...),
		dagMsg(provider.RoleUser, "tl1174-resume-new-turn", "m-new"))
	if err := writeSessionMessages(path, resumed); err != nil {
		t.Fatalf("append save after materialize: %v", err)
	}

	// 追加后的世界：明文在、归档不在（写路径清理过期归档）。
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("plaintext missing after resume append: %v", err)
	}
	assertGone(t, store.SessionTranscriptZst(path))
	reloaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("reload after append: %v", err)
	}
	if len(reloaded.Messages) != 97 {
		t.Fatalf("reloaded %d messages, want 96 history + 1 new", len(reloaded.Messages))
	}
	byContent := make(map[string]bool, len(reloaded.Messages))
	for _, m := range reloaded.Messages {
		byContent[m.Content] = true
	}
	for i, want := range samples {
		if !byContent[want] {
			t.Fatalf("history sample %d lost across compress→materialize→append", i)
		}
	}
	if !byContent["tl1174-resume-new-turn"] {
		t.Fatalf("the appended turn is missing after resume")
	}
}

// 验收（开关关=逐字节一致，铁律 2）：关态下读写钩子只 stat——无归档产生、
// 无额外文件、转录字节与引入前一致（load 是纯读；写路径照旧全量重写同一内容）。
func TestSessionTranscriptOffByteIdentical(t *testing.T) {
	path := filepath.Join(t.TempDir(), "off-session.jsonl")
	_, onDisk := transcriptBigFixture(t, path)
	dir := filepath.Dir(path)

	if materialized, err := ensureSessionTranscriptPlain(path); err != nil || materialized {
		t.Fatalf("off-state ensure must be a no-op short-circuit, got (%v, %v)", materialized, err)
	}
	s, err := LoadSession(path)
	if err != nil {
		t.Fatalf("off-state load: %v", err)
	}
	if len(s.Messages) != 96 {
		t.Fatalf("off-state load got %d messages, want 96", len(s.Messages))
	}
	afterBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("off-state reread: %v", err)
	}
	if !bytes.Equal(onDisk, afterBytes) {
		t.Fatalf("off-state transcript bytes diverged: before=%d after=%d", len(onDisk), len(afterBytes))
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(path) {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("off-state must not create any extra file, dir now holds %v", names)
	}
	// 写路径（全量重写）在关态下也不留归档：写后同目录仍只有明文一份。
	if err := writeSessionMessages(path, s.Messages); err != nil {
		t.Fatalf("off-state save: %v", err)
	}
	entries, err = os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir after save: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("off-state save must not leave archive residue, dir holds %d files", len(entries))
	}
}

// 验收（解压失败回退）：坏归档挪 .damaged 保留现场、明文不落盘——绝不静默
// 丢弃，也绝不让读路径误判「无转录」后另起空明文覆盖历史。
func TestSessionTranscriptZstCorruptArchivePreserved(t *testing.T) {
	now := time.Now()
	path := filepath.Join(t.TempDir(), "corrupt-session.jsonl")
	_, onDisk := transcriptBigFixture(t, path)
	ageTranscriptCold(t, path, now)
	if _, _, err := CompressSessionTranscriptZst(path, now); err != nil {
		t.Fatalf("compress: %v", err)
	}
	zst := store.SessionTranscriptZst(path)
	// 破坏归档：保留帧头不可解（截掉大半字节）。
	broken := make([]byte, 0, 24)
	if raw, err := os.ReadFile(zst); err == nil && len(raw) > 24 {
		broken = raw[:24]
	} else {
		broken = []byte("not-a-zstd-frame-at-all")
	}
	if err := os.WriteFile(zst, broken, 0o600); err != nil {
		t.Fatalf("corrupt archive: %v", err)
	}

	if materialized, err := ensureSessionTranscriptPlain(path); err == nil {
		t.Fatalf("corrupt archive must fail materialization, got materialized=%v", materialized)
	}
	assertGone(t, path) // 坏档下不落半份明文
	if _, err := os.Stat(zst + ".damaged"); err != nil {
		t.Fatalf("broken archive must be preserved for manual recovery: %v", err)
	}
	_ = onDisk
}

// 验收（发现面）：归档态会话照常被发现且 Path 归一为明文名；并存残件态
// （明文+归档）明文优先、归档不重复列出。
func TestListSessionOrderDiscoversArchivedTranscript(t *testing.T) {
	now := time.Now()
	dir := t.TempDir()
	plainSession := filepath.Join(dir, "live-session.jsonl")
	_, _ = transcriptBigFixture(t, plainSession)

	archivedSession := filepath.Join(dir, "frozen-session.jsonl")
	_, _ = transcriptBigFixture(t, archivedSession)
	ageTranscriptCold(t, archivedSession, now)
	if _, _, err := CompressSessionTranscriptZst(archivedSession, now); err != nil {
		t.Fatalf("compress: %v", err)
	}

	infos, err := ListSessionOrder(dir)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	byPath := make(map[string]SessionOrderInfo, len(infos))
	for _, info := range infos {
		byPath[info.Path] = info
	}
	if _, ok := byPath[plainSession]; !ok {
		t.Fatalf("plain session missing from discovery: %v", byPath)
	}
	if _, ok := byPath[archivedSession]; !ok {
		t.Fatalf("archived session must stay discoverable under its plain name: %v", byPath)
	}
	for p := range byPath {
		if strings.HasSuffix(p, store.SessionTranscriptZstSuffix) {
			t.Fatalf("discovery must normalize archive paths to the plain spelling, got %q", p)
		}
	}

	// 并存残件态：明文在 ⇒ 归档跳过（明文优先），不会双列出。
	residue := filepath.Join(dir, "residue-session.jsonl")
	_, _ = transcriptBigFixture(t, residue)
	ageTranscriptCold(t, residue, now)
	if _, _, err := CompressSessionTranscriptZst(residue, now); err != nil {
		t.Fatalf("compress residue: %v", err)
	}
	if err := os.WriteFile(residue, []byte("fresh-plaintext-wins\n"), 0o600); err != nil {
		t.Fatalf("resurrect plaintext residue: %v", err)
	}
	infos, err = ListSessionOrder(dir)
	if err != nil {
		t.Fatalf("list with residue: %v", err)
	}
	count := 0
	for _, info := range infos {
		if info.Path == residue {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("residue pair must list exactly once under the plain path, got %d", count)
	}
}

// 验收（地板）：低于地板的冷转录拒绝压缩——明文原样保留（防御性回退面）。
func TestSessionTranscriptZstUnderFloorKeptPlain(t *testing.T) {
	now := time.Now()
	path := filepath.Join(t.TempDir(), "tiny-session.jsonl")
	if err := writeSessionMessages(path, []provider.Message{dagMsg(provider.RoleUser, "tiny", "m0")}); err != nil {
		t.Fatalf("write tiny fixture: %v", err)
	}
	ageTranscriptCold(t, path, now)
	before, after, err := CompressSessionTranscriptZst(path, now)
	if err == nil {
		t.Fatalf("under-floor transcript must be refused, got before=%d after=%d", before, after)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("under-floor plaintext must stay: %v", err)
	}
	assertGone(t, store.SessionTranscriptZst(path))
}
