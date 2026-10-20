package main

// 任务1017 批A（2026-10-20）：pump 启动即回收两把协作锁的 stale holder sidecar，
// 不再等下一次取件/读面板。2026-10-19 现场：崩溃重启后 sidecar 残留 1h43m，
// 期间每个读到它的排障者都被指向一个早已死亡的进程。本测试用 REASONIX_STATE_HOME
// 钉住真实 mailDir 解析路径，断言两把锁的 sidecar 同一趟回收。

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReclaimStaleCollabLockSidecarsAtBoot(t *testing.T) {
	state := t.TempDir()
	t.Setenv("REASONIX_STATE_HOME", state)
	mailDir := filepath.Join(state, "session-chat")
	if err := os.MkdirAll(mailDir, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Format(time.RFC3339)
	stale := fmt.Sprintf("pid=%d held_since=%s", 999999999, now) // 超出现实 pid 空间，确定性死亡

	inboxHolder := filepath.Join(mailDir, ".collab-inbox.lock.holder")
	mailHolder := filepath.Join(mailDir, ".mail.lock.holder")
	for _, path := range []string{inboxHolder, mailHolder} {
		if err := os.WriteFile(path, []byte(stale), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	reclaimStaleCollabLockSidecarsAtBoot()

	for _, path := range []string{inboxHolder, mailHolder} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s must be reclaimed at boot, stat err=%v", filepath.Base(path), err)
		}
	}
}
