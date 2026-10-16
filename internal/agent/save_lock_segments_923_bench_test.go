package agent

// 任务 923 分段量级实测：saveLocked 锁内各 CPU/IO 段在大会话形态下的成本表。
// 196/885 定案的痛点是「持锁者在锁内做 O(全量) 工作」；重放段已由 196 的 graph
// cache 治理，本 benchmark 针对剩余三段——transcript digest（全量 marshal+sha256）、
// DAG diff（全前缀双份 marshal 比较）、checkpoint/display index 全量重写——给出
// 实测读数，作为 923 切片取舍的量级依据。规模取 4000 条消息（710 合成样本同口径），
// 外推到 6434 条现场会话（023554）按线性标注。

import (
	"fmt"
	"path/filepath"
	"testing"

	"reasonix/internal/provider"
)

func t923SyntheticMessages(n int) []provider.Message {
	msgs := make([]provider.Message, 0, n+1)
	msgs = append(msgs, provider.Message{Role: provider.RoleSystem, Content: "sys"})
	for i := 0; i < n; i++ {
		role := provider.RoleUser
		content := fmt.Sprintf("t923 user turn %d: %s", i, string(make([]byte, 400)))
		if i%2 == 1 {
			role = provider.RoleAssistant
			content = fmt.Sprintf("t923 assistant turn %d: %s", i, string(make([]byte, 1200)))
		}
		msgs = append(msgs, provider.Message{Role: role, Content: content})
	}
	return msgs
}

// BenchmarkT923LockSegmentDigest measures the full-transcript digest pass
// (json.Marshal + sha256 per message) that every version-bumped schema-2 save
// pays inside the save-path lock via snapshotDigest → digestAndSizeSessionMessages.
func BenchmarkT923LockSegmentDigest(b *testing.B) {
	msgs := t923SyntheticMessages(4000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := digestAndSizeSessionMessages(msgs); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkT923LockSegmentDiff measures the DAG plan diff over the full
// common prefix (messagesEqualForStorage per message) that every schema-2
// save pays. With the 923 DeepEqual fast path this is the O(prefix) pointer
// walk; the pre-923 double-marshal shape measured 68.5ms/op on this input
// against 5.3ms/op for the fast path.
func BenchmarkT923LockSegmentDiff(b *testing.B) {
	persisted := t923SyntheticMessages(4000)
	msgs := append([]provider.Message(nil), persisted...)
	msgs = append(msgs,
		provider.Message{Role: provider.RoleUser, Content: "t923 append 1"},
		provider.Message{Role: provider.RoleAssistant, Content: "t923 append 2"},
	)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = diffDAGTranscript(persisted, msgs, sessionSaveSnapshot)
	}
}

// BenchmarkT923LockSegmentDigestIncremental measures the post-923 append-save
// digest cost: the incremental stream fed by AddBatch, read under mu. This is
// the shape every version-bumped save now pays instead of the 51.5ms full pass.
func BenchmarkT923LockSegmentDigestIncremental(b *testing.B) {
	s := NewSession("sys")
	s.AddBatch(t923SyntheticMessages(4000)...)
	// Seed the stream via a full pass, exactly like the first save does.
	msgs, version, rewriteVersion := s.snapshotWithVersion()
	_, _, h, err := digestAndSizeSessionMessagesState(msgs)
	if err != nil {
		b.Fatal(err)
	}
	s.seedDigestState(msgs, version, rewriteVersion, h, 0)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, ok := s.incrementalDigestAndSize(msgs, version, rewriteVersion); !ok {
			b.Fatal("incremental digest miss")
		}
	}
}

// BenchmarkT923LockSegmentCheckpointWrite measures the non-pureAppend
// checkpoint rewrite (writeSessionMessages: full marshal + disk write) that a
// patch-shaped tool-checkpoint save pays inside the lock on the schema-2 path.
func BenchmarkT923LockSegmentCheckpointWrite(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	msgs := t923SyntheticMessages(4000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := writeSessionMessages(path, msgs); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkT923LockSegmentDisplayIndexRebuild measures the full display-index
// rebuild (the non-append shape of refreshCheckpointDisplayIndex).
func BenchmarkT923LockSegmentDisplayIndexRebuild(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	msgs := t923SyntheticMessages(4000)
	digest, _, err := digestAndSizeSessionMessages(msgs)
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := refreshSessionDisplayIndex(path, msgs, digest, int64(i+1), -1); err != nil {
			b.Fatal(err)
		}
	}
}
