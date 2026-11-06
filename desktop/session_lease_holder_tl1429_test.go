package main

// tl1429 拒绝日志 identity 钉(18:39 口径:日志必须给 identity,禁止恒 pid=0)。
// tl1429b 行为更新:普通归档撞上自锁形态(登记在册句柄+pid=0 记录)不再永久
// 拒绝——heal 链的 sweep 撤掉本进程句柄后 retry reclaim 自愈(端到端钉见
// topic_archive_tl1429b_test.go 验收①);本钉随之更新为——归档成功,且过程中
// 的 removal-guard 拒绝日志仍带 holder_class=self_lock + 登记条目 kind/时刻
// (heal 在拒绝日志之后发生,过程留痕=事后归因依据)。登记摘除(模型化真外部)
// 时归档保持拒绝且日志回到 unknown 保守态。

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"reasonix/internal/agent"
)

// 验收 b(identity 日志,tl1429b 更新态):归档成功+过程日志给 self_lock 身份。
func TestArchiveRefusalLogNamesSelfLockHolderIdentity(t *testing.T) {
	isolateDesktopUserDirs(t)
	logs := captureSlog(t)
	topicID := "topic_tl1429_refusal_log"
	sessionPath, projectRoot, release := stageSelfLockScene(t, "tl1429-log.jsonl", topicID, "TL1429 refusal log")
	t.Cleanup(release)

	// tl1429b:正常归档对自锁形态自愈成功(不再要求用户升级 force)。
	if err := NewApp().TrashTopic(topicID); err != nil {
		t.Fatalf("normal TrashTopic against a booked self-lock = %v, want success (heal 自愈)", err)
	}
	if _, statErr := os.Stat(sessionPath); !os.IsNotExist(statErr) {
		t.Fatalf("session transcript survived the archive: %v", statErr)
	}
	if got := loadTopicTitle(projectRoot, topicID); got != "" {
		t.Fatalf("archived topic title = %q, want cleared", got)
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
