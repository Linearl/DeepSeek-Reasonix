package agent

// 任务 923 切片 B：增量 transcript digest。
//
// 背景：每次 version 变化的 save 都要在 save-path 锁内对整个 transcript 做
// json.Marshal + sha256（digestAndSizeSessionMessages），4000 条消息合成会话
// 实测 51.5ms/次，现场 6434 条（023554）线性外推 ~83ms/次，且每个保存周期都付。
//
// 方案：Session 持有一个活跃 sha256 流状态，append 漏斗（Add/AddBatch）在持锁
// 路径上只喂新增消息；save 侧在状态与快照完全一致时直接取摘要，否则回退全量
// 重算并在算完后重新播种。
//
// 正确性契约（全部 fail-open）：
//   - 状态有效 ⟺ digestHash != nil && digestCount == len(msgs)
//     && digestVersion == version && digestRewriteVersion == rewriteVersion。
//   - 所有会改动序列化字节的 Session 变更点都会 bump version 或 rewriteVersion
//     （923 审计：原地内容修改全部 bump；ID/CreatedAt 修改被 messageForSessionIdentity
//     fold 排除在摘要字节之外）——因此任一失配都意味着「哈希流不再描述当前消息」，
//     读侧直接判失效。
//   - 摘要字节与 digestAndSizeSessionMessages 完全一致：同一 identity fold、
//     同一条消息一份 marshal + '\n'。
//   - 并发：喂状态与读状态都在 s.mu 下；save 播种时校验 version/rewriteVersion/
//     len 未变，否则放弃播种（下次 save 再全量一次）。

import (
	"crypto/sha256"
	"encoding/json"
	"hash"

	"reasonix/internal/provider"
)

// feedDigestAppendLocked extends the incremental transcript hash with the
// messages just appended by an append funnel. It must run under s.mu after the
// append and the version bump. When the prior state does not describe exactly
// the pre-append transcript (any intervening rewrite, in-place edit, or a
// never-seeded session), the state is dropped and the next save pays the full
// pass — correctness never depends on the incremental path firing.
func (s *Session) feedDigestAppendLocked(appended []provider.Message, preCount int, preVersion uint64, preRewrite int) {
	if s.digestHash == nil {
		return
	}
	if s.digestCount != preCount || s.digestVersion != preVersion || s.digestRewriteVersion != preRewrite {
		// The stream no longer describes the transcript (an edit bypassed the
		// append funnels). Invalidate rather than corrupt: an unextended stale
		// hash would misreport the next save's identity.
		s.digestHash = nil
		return
	}
	for _, m := range appended {
		b, err := json.Marshal(messageForSessionIdentity(m))
		if err != nil {
			// Practically unreachable for provider.Message; fail open to the
			// full pass rather than persist a truncated stream.
			s.digestHash = nil
			return
		}
		s.digestHash.Write(b)
		s.digestHash.Write([]byte{'\n'})
		s.digestSize += int64(len(b)) + 1
	}
	s.digestCount = len(s.Messages)
	s.digestVersion = s.version
	s.digestRewriteVersion = s.rewriteVersion
}

// incrementalDigestAndSize returns the transcript digest and encoded size from
// the maintained stream when it provably describes exactly msgs at the given
// version; ok=false means the caller must run the full pass.
func (s *Session) incrementalDigestAndSize(msgs []provider.Message, version uint64, rewriteVersion int) (digest [sha256.Size]byte, size int64, ok bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.digestHash == nil || s.digestCount != len(msgs) ||
		s.digestVersion != version || s.digestRewriteVersion != rewriteVersion {
		return digest, 0, false
	}
	copy(digest[:], s.digestHash.Sum(nil))
	return digest, s.digestSize, true
}

// seedDigestState installs the hasher a full digest pass just produced as the
// new incremental baseline, but only if the session still matches what was
// hashed — a concurrent mutation between the snapshot and here leaves the state
// unset and the next save re-pays the full pass (which is the status quo cost).
func (s *Session) seedDigestState(msgs []provider.Message, version uint64, rewriteVersion int, h hash.Hash, size int64) {
	if h == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.version != version || s.rewriteVersion != rewriteVersion || len(s.Messages) != len(msgs) {
		return
	}
	s.digestHash = h
	s.digestCount = len(msgs)
	s.digestVersion = version
	s.digestRewriteVersion = rewriteVersion
	s.digestSize = size
}

// digestAndSizeSessionMessagesState is digestAndSizeSessionMessages that also
// hands back the live hasher so callers can seed the incremental baseline
// without re-encoding the transcript a second time.
func digestAndSizeSessionMessagesState(msgs []provider.Message) ([sha256.Size]byte, int64, hash.Hash, error) {
	h := sha256.New()
	size := int64(0)
	for _, m := range msgs {
		m = messageForSessionIdentity(m)
		b, err := json.Marshal(m)
		if err != nil {
			return [sha256.Size]byte{}, 0, nil, err
		}
		if _, err := h.Write(b); err != nil {
			return [sha256.Size]byte{}, 0, nil, err
		}
		if _, err := h.Write([]byte{'\n'}); err != nil {
			return [sha256.Size]byte{}, 0, nil, err
		}
		size += int64(len(b)) + 1
	}
	var out [sha256.Size]byte
	copy(out[:], h.Sum(nil))
	return out, size, h, nil
}
