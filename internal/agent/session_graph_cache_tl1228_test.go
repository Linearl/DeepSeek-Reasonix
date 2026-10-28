package agent

import (
	"path/filepath"
	"testing"

	"reasonix/internal/store"
)

// tl1228 阶段一内存上限（agent 层）：内置字节顶降到 64/64 MiB 后，
//   - 未推送配置时生效顶就是 64 MiB（desktop 侧 config 默认同步降到同值，
//     两层一致）；
//   - 记账字节在任何 Put 序列后都不得超过总顶（LRU 驱逐到顶下）；
//   - 超过单条顶的状态拒绝入缓存（一个巨会话不得钉死整个预算）。
func TestSessionGraphCacheTL1228ByteDefaults(t *testing.T) {
	// 0 reads as "never pushed" and clamps to the built-in default — the same
	// path a desktop boot takes when the file stores nothing.
	SetSessionGraphCacheMaxBytes(0)
	SetSessionGraphCacheEntryMaxBytes(0)
	_, totalCap, entryCap, _, _ := SessionGraphCacheByteStats()
	if totalCap != sessionGraphCacheMaxBytesDefault {
		t.Fatalf("total cap = %d, want built-in %d", totalCap, sessionGraphCacheMaxBytesDefault)
	}
	if totalCap != int64(64)<<20 {
		t.Fatalf("total cap = %d, want 64MiB (tl1228 ≤150MB acceptance)", totalCap)
	}
	if entryCap != int64(64)<<20 {
		t.Fatalf("entry cap = %d, want 64MiB (tl1228 ≤150MB acceptance)", entryCap)
	}
}

func TestSessionGraphCacheTL1228RetainedSetBounded(t *testing.T) {
	SetSessionGraphCacheCapacity(sessionGraphCacheCapacityDefault)
	SetSessionGraphCacheMaxBytes(sessionGraphCacheMaxBytesDefault)
	SetSessionGraphCacheEntryMaxBytes(sessionGraphCacheEntryMaxBytesDefault)
	defer func() {
		SetSessionGraphCacheMaxBytes(0)
		SetSessionGraphCacheEntryMaxBytes(0)
	}()

	// Six states of 24 MiB accounted each: capacity 3 and the 64 MiB total cap
	// both bite, so the retained set must stay ≤ the total cap throughout.
	paths := make([]string, 0, 6)
	cleanup := func() {
		for _, p := range paths {
			InvalidateSessionGraph(p)
		}
	}
	defer cleanup()
	for i := 0; i < 6; i++ {
		sessionPath := filepath.Join(t.TempDir(), "s.jsonl")
		paths = append(paths, sessionPath)
		st := newSessionDAGState(store.SessionEventLog(sessionPath))
		st.size = 24 << 20
		sessionGraphCachePut(st.path, st)
		bytesNow, totalCap, _, entries, _ := SessionGraphCacheByteStats()
		if bytesNow > totalCap {
			t.Fatalf("after put %d: retained %d > cap %d", i, bytesNow, totalCap)
		}
		if entries > sessionGraphCacheCapacityDefault {
			t.Fatalf("after put %d: %d entries > capacity %d", i, entries, sessionGraphCacheCapacityDefault)
		}
	}

	// One monster state above the entry ceiling is refused outright.
	monsterPath := filepath.Join(t.TempDir(), "big.jsonl")
	paths = append(paths, monsterPath)
	before, _, _, _, refusalsBefore := func() (int64, int64, int64, int, uint64) {
		b, c, e, n, r := SessionGraphCacheByteStats()
		return b, c, e, n, r
	}()
	st := newSessionDAGState(store.SessionEventLog(monsterPath))
	st.size = 96 << 20 // above the 64 MiB entry ceiling
	sessionGraphCachePut(st.path, st)
	bytesNow, _, _, _, refusalsAfter := SessionGraphCacheByteStats()
	if bytesNow != before {
		t.Fatalf("over-entry-cap state admitted: bytes %d -> %d", before, bytesNow)
	}
	if refusalsAfter != refusalsBefore+1 {
		t.Fatalf("entry refusal counter did not move: %d -> %d", refusalsBefore, refusalsAfter)
	}
}
