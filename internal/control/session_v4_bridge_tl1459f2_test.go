package control

// tl1459F2 对照钉（v4 桥裸字节指纹面——不经编码器的唯一 digest 计算点）。
//
// transcriptDigest 算前编码规范化（tl1459F2 口径，见 internal/agent
// save_digest_normalize_tl1459f2.go）后，同一转录的「存内非法字节视图」与
// 「存储往返 U+FFFD 视图」指纹一致：桥的变更检测不再把一次保存/重载误读成
// 内容变化而重复 append 尾部（修正前的分裂行为在 legacy 钉中固化，防回归）。
// 指纹的可解释差异仅此一处：bridgeSyncState 是进程内 memo，不触任何持久化
// digest，存量账本零影响。
import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	"reasonix/internal/provider"
)

// tl1459f2BridgeLegacyDigest is the pre-change raw-byte formula, kept verbatim
// so the pins prove both halves: the split existed pre-fix, and the normalized
// funnel no longer splits.
func tl1459f2BridgeLegacyDigest(messages []provider.Message) string {
	h := sha256.New()
	for _, m := range messages {
		h.Write([]byte(m.ID))
		h.Write([]byte{0})
		h.Write([]byte(m.Role))
		h.Write([]byte{0})
		h.Write([]byte(m.Content))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func TestTl1459F2V4BridgeTranscriptDigestStableAcrossStorageRoundTrip(t *testing.T) {
	raw := "前\xff后\xed\xa0\x80"
	msgs := []provider.Message{{Role: provider.RoleUser, ID: "m1", Content: raw}}
	// 存储往返：整值 marshal（编码器把非法字节强制替换为 U+FFFD）→ 解码回
	// 重载视图。会话保存/重载后桥看到的就是这个视图。
	line, err := json.Marshal(msgs)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var reloaded []provider.Message
	if err := json.Unmarshal(line, &reloaded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if legacyBefore, legacyAfter := tl1459f2BridgeLegacyDigest(msgs), tl1459f2BridgeLegacyDigest(reloaded); legacyBefore == legacyAfter {
		t.Fatalf("legacy raw-byte formula unexpectedly stable; the motivation pin is void")
	}
	before := transcriptDigest(msgs)
	after := transcriptDigest(reloaded)
	if before != after {
		t.Fatalf("bridge fingerprint split across the storage round trip:\n before=%s\n after=%s", before, after)
	}
}
