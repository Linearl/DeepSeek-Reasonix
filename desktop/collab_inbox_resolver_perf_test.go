package main

// 任务 600（收件箱性能优化）验收测试。调研实证：旧桌面层 resolver 对每条无身份
// 戳的消息做一次全机会话目录扫描（单次 3.4~18s，生产日志单次面板读 18.6~39.5
// 分钟）。修复 = 构店时一次扫描查表成 map（resolver O(1)）+ 扫描 30s TTL 缓存。
//
// 这里钉四个契约：
//  1. 一次 List（多发送方、全部无身份戳）→ 扫描函数恰好被调 1 次；
//  2. 桶归类与扫描一次的语义一致（heartbeat→automation、system→system、
//    其余→mention）；
//  3. TTL 内重复取用不重扫，过期后重扫（可注入时钟验证）；
//  4. liveness 花名册与扫描结果同源（任务 464 清理规则的 oracle 契约不变）。

import (
	"context"
	"testing"
	"time"

	"reasonix/internal/collabinbox"
	"reasonix/internal/sessioncollab"
)

func TestNewCollabInboxStoreScansOnceAndClassifiesByTable(t *testing.T) {
	mailDir := t.TempDir()
	scans := 0
	scan := func() []sessioncollab.Identity {
		scans++
		return []sessioncollab.Identity{
			{ContactID: "sc_beat", IdentityType: "heartbeat", SessionPath: "x/beat.jsonl"},
			{ContactID: "sc_sys", IdentityType: "system", SessionPath: "x/sys.jsonl"},
			{ContactID: "sc_peer", SessionPath: "x/peer.jsonl"}, // identity-less: mention bucket
			{ContactID: "", SessionPath: "x/nameless.jsonl"},    // no contact: neither map nor roster
		}
	}
	store := newCollabInboxStore(mailDir, scan)

	// Three delivered messages, NONE carrying a kind stamp or approver — every
	// one of them reaches the resolver under the old per-message implementation
	// (调研报告 §2：74% 消息命中 resolver).
	mail := sessioncollab.NewMailStore(mailDir)
	for _, m := range []sessioncollab.MailMessage{
		{From: "sc_beat", To: "sc_main", Body: "heartbeat ping"},
		{From: "sc_sys", To: "sc_main", Body: "platform notice"},
		{From: "sc_peer", To: "sc_main", Body: "plain dispatch"},
	} {
		if _, err := mail.Deliver(context.Background(), m); err != nil {
			t.Fatalf("deliver %q: %v", m.Body, err)
		}
	}

	snap, err := store.List(context.Background(), collabinbox.Query{}, false)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if scans != 1 {
		t.Fatalf("identity scan ran %d times for one List, want exactly 1 (resolver must be a table lookup, 任务 600)", scans)
	}
	buckets := map[string]string{}
	for _, e := range snap.Entries {
		buckets[e.From] = e.Bucket
	}
	want := map[string]string{"sc_beat": "automation", "sc_sys": "system", "sc_peer": "mention"}
	for from, bucket := range want {
		if buckets[from] != bucket {
			t.Fatalf("sender %s classified as %q, want %q (table lookup must preserve task-348 classification)", from, buckets[from], bucket)
		}
	}

	// 桶归类沿用同一张表：第二次 List 不再扫描（store 内 map 已建好）。
	if _, err := store.List(context.Background(), collabinbox.Query{}, false); err != nil {
		t.Fatalf("second list: %v", err)
	}
	if scans != 1 {
		t.Fatalf("identity scan ran %d times after two Lists, want 1", scans)
	}
}

func TestNewCollabInboxStoreLiveContactsFromSameScan(t *testing.T) {
	mailDir := t.TempDir()
	scans := 0
	scan := func() []sessioncollab.Identity {
		scans++
		return []sessioncollab.Identity{
			{ContactID: "sc_alive", SessionPath: "x/alive.jsonl"},
			{ContactID: "sc_ghost", SessionPath: "x/ghost.jsonl"},
		}
	}
	store := newCollabInboxStore(mailDir, scan)
	live := store.LiveContacts()
	if scans != 1 {
		t.Fatalf("scan ran %d times, want 1", scans)
	}
	if !live["sc_alive"] || !live["sc_ghost"] {
		t.Fatalf("live roster = %v, want both scanned contacts (任务 464 oracle must stay scan-fed)", live)
	}
	if live["sc_stranger"] {
		t.Fatal("live roster contains a contact the scan never reported")
	}
}

func TestIdentityScanCachedWithinTTL(t *testing.T) {
	// 注入扫描源与时钟，隔离包级缓存状态。
	restoreFn := identityScanFn
	restoreNow := identityScanNow
	restoreCache := identityScanCache
	restoreAt := identityScanAt
	t.Cleanup(func() {
		identityScanFn = restoreFn
		identityScanNow = restoreNow
		identityScanCache = restoreCache
		identityScanAt = restoreAt
	})

	now := time.UnixMilli(1_000_000)
	identityScanNow = func() time.Time { return now }
	scans := 0
	identityScanFn = func() []sessioncollab.Identity {
		scans++
		return []sessioncollab.Identity{{ContactID: "sc_x", SessionPath: "x.jsonl"}}
	}
	identityScanCache = nil
	identityScanAt = time.Time{}

	// 冷启动：扫一次。
	if ids := scanIdentityDirectoryCached(); len(ids) != 1 || scans != 1 {
		t.Fatalf("cold call: ids=%d scans=%d, want 1/1", len(ids), scans)
	}
	// TTL 内：直接命中缓存。
	now = now.Add(10 * time.Second)
	if ids := scanIdentityDirectoryCached(); len(ids) != 1 || scans != 1 {
		t.Fatalf("warm call inside TTL: ids=%d scans=%d, want cache hit (1/1)", len(ids), scans)
	}
	// 过期：恰好越过 identityScanTTL 才重扫。
	now = now.Add(identityScanTTL)
	if ids := scanIdentityDirectoryCached(); len(ids) != 1 || scans != 2 {
		t.Fatalf("call after TTL: ids=%d scans=%d, want rescan (1/2)", len(ids), scans)
	}
}

func TestIdentityScanCachedLatchesEmptyRoster(t *testing.T) {
	// 空扫描结果是合法状态，同样要被缓存锁存——否则空花名册的机器每次构店
	// 都重扫（探针实查：desktop TestMain 沙箱目录扫出空表时 cache==nil 失锁）。
	restoreFn, restoreNow, restoreCache, restoreAt := identityScanFn, identityScanNow, identityScanCache, identityScanAt
	t.Cleanup(func() {
		identityScanFn = restoreFn
		identityScanNow = restoreNow
		identityScanCache = restoreCache
		identityScanAt = restoreAt
	})

	identityScanFn = func() []sessioncollab.Identity { return nil }
	identityScanCache = nil
	identityScanAt = time.Time{}

	if ids := scanIdentityDirectoryCached(); len(ids) != 0 {
		t.Fatalf("cold call: ids=%d, want empty", len(ids))
	}
	// 第二次取用必须命中缓存（不再触底层扫描——若实现以 nil 判定未缓存，
	// 这里会拿到非空？不，底层恒空；用计数源证明没有重扫）。
	scans := 0
	identityScanFn = func() []sessioncollab.Identity {
		scans++
		return nil
	}
	if ids := scanIdentityDirectoryCached(); len(ids) != 0 {
		t.Fatalf("warm call: ids=%d, want empty", len(ids))
	}
	if scans != 0 {
		t.Fatalf("empty roster rescanned %d times inside TTL, want 0 (empty result must latch)", scans)
	}
}
