package config

import "testing"

// tl1228 阶段一内存上限：图缓存字节顶从 2048/1024 MiB 降到 64/64 MiB。
// tl1223 heap 实测 applyMessage 链 397MB（inuse 64.1%）的最大留存方就是这层
// 缓存，阶段一验收 ≤150MB。本测试把新默认钉死，防止后续无意识回抬。
func TestDagGraphCacheDefaultsTL1228(t *testing.T) {
	if got := DagGraphCacheMaxMB(nil); got != DagGraphCacheMaxMBDefault {
		t.Fatalf("nil-config total = %d, want built-in default %d", got, DagGraphCacheMaxMBDefault)
	}
	if DagGraphCacheMaxMBDefault != 64 {
		t.Fatalf("total default = %d, want 64 (tl1228 ≤150MB acceptance)", DagGraphCacheMaxMBDefault)
	}
	if got := DagGraphCacheEntryMaxMB(nil); got != DagGraphCacheEntryMaxMBDefault {
		t.Fatalf("nil-config entry = %d, want built-in default %d", got, DagGraphCacheEntryMaxMBDefault)
	}
	if DagGraphCacheEntryMaxMBDefault != 64 {
		t.Fatalf("entry default = %d, want 64 (tl1228 ≤150MB acceptance)", DagGraphCacheEntryMaxMBDefault)
	}
	// The entry ceiling stays clamped under the total even when a file stores
	// a bigger per-entry value: the worst retained set must remain the total.
	cfg := &Config{}
	cfg.Agent.DagGraphCacheMaxMB = 32
	cfg.Agent.DagGraphCacheEntryMaxMB = 65536
	if got := DagGraphCacheEntryMaxMB(cfg); got != 32 {
		t.Fatalf("entry clamp = %d, want 32 (clamped to the stored total)", got)
	}
}
