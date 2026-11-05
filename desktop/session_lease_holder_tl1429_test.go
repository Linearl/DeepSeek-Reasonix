package main

// tl1429 拒绝日志 identity 钉(18:39 口径:日志必须给 identity,禁止恒 pid=0):
// 普通归档撞上自锁形态(登记在册句柄+pid=0 记录)时,removal-guard 拒绝日志
// 必须带 holder_class=self_lock + 登记条目的 kind 与时刻;登记摘除(模型化
// 真外部)时回到 unknown 保守态。布景 helper 复用 topic_archive_tl1429_test.go。

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"reasonix/internal/agent"
)

// 验收 b:拒绝日志直接给出持有者 identity。
func TestArchiveRefusalLogNamesSelfLockHolderIdentity(t *testing.T) {
	isolateDesktopUserDirs(t)
	logs := captureSlog(t)
	topicID := "topic_tl1429_refusal_log"
	sessionPath, _, release := stageSelfLockScene(t, "tl1429-log.jsonl", topicID, "TL1429 refusal log")
	t.Cleanup(release)

	err := NewApp().TrashTopic(topicID)
	if err == nil {
		t.Fatal("regular archive against a held lock must refuse")
	}
	if !errors.Is(err, agent.ErrSessionLeaseHeld) {
		t.Fatalf("refusal = %v, want the lease-held family", err)
	}
	// 普通(非 force)归档对该形态保持拒绝——只有 force/终局才撕锁。
	if _, statErr := os.Stat(sessionPath); statErr != nil {
		t.Fatalf("refused archive removed the session: %v", statErr)
	}

	var refusalHasIdentity bool
	for _, line := range strings.Split(logs.String(), "\n") {
		if !strings.Contains(line, "session removal guard refused by lease holder") {
			continue
		}
		var entry map[string]any
		if jsonErr := json.Unmarshal([]byte(line), &entry); jsonErr != nil {
			continue
		}
		if entry["holder_class"] == "self_lock" {
			refusalHasIdentity = true
			if entry["holder_lock_kind"] != "test_self_lock" {
				t.Fatalf("refusal log holder_lock_kind = %v, want test_self_lock", entry["holder_lock_kind"])
			}
			if _, ok := entry["holder_lock_since"]; !ok {
				t.Fatal("refusal log missing holder_lock_since — 登记条目时刻未 dump")
			}
		}
	}
	if !refusalHasIdentity {
		t.Fatalf("no refusal log line carries holder_class=self_lock — 恒 pid=0 零身份回潮; logs:\n%s", logs.String())
	}
}

// 对照钉:登记摘除(模型化真外部)后,拒绝日志回到 unknown(本进程不知情),
// 绝不给外部占用伪造 self_lock 身份。
func TestArchiveRefusalLogStaysUnknownForUnbookedHolder(t *testing.T) {
	isolateDesktopUserDirs(t)
	logs := captureSlog(t)
	topicID := "topic_tl1429_log_unknown"
	sessionPath, _, release := stageSelfLockScene(t, "tl1429-log-unknown.jsonl", topicID, "TL1429 log unknown")
	t.Cleanup(release)
	if !agent.DropSessionLeaseLockRegistrationForTest(sessionPath) {
		t.Fatal("drop seam reported nothing dropped")
	}

	if err := NewApp().TrashTopic(topicID); err == nil {
		t.Fatal("regular archive against a held lock must refuse")
	}
	sawRefusal := false
	for _, line := range strings.Split(logs.String(), "\n") {
		if !strings.Contains(line, "session removal guard refused by lease holder") {
			continue
		}
		var entry map[string]any
		if jsonErr := json.Unmarshal([]byte(line), &entry); jsonErr != nil {
			continue
		}
		if entry["holder_class"] == "self_lock" {
			t.Fatal("an unbooked holder was given a self_lock identity — 外部占用被伪造身份")
		}
		if entry["holder_class"] == "unknown" {
			sawRefusal = true
		}
	}
	if !sawRefusal {
		t.Fatalf("no unknown-class refusal line found; logs:\n%s", logs.String())
	}
}
