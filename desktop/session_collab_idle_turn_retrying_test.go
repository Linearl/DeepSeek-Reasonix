package main

// 任务1017 批C-D（2026-10-20）：failed_retrying 中间态回执。
// 现场缺口：开轮 attempt 1/3 失败只进进程日志，发送方 get_message_status
// 永远停在 queued_followup 绿灯——失败不可感知。桥现在每次持续失败（未到
// 3 振）都经 retrying 钩子写 failed_retrying 回执（带 attempts），第 3 振
// 由既有 exhausted 回执覆盖为终态。

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/sessioncollab"
)

// TestIdleTurnBridgeRetryingHookFiresBelowBudget：第 1、2 振各触发一次
// retrying（attempts=1,2），第 3 振只触发 exhausted、不再触发 retrying。
func TestIdleTurnBridgeRetryingHookFiresBelowBudget(t *testing.T) {
	b := newIdleTurnBridge()
	start := time.Now()

	var mu sync.Mutex
	retrying := []int{}
	exhausted := 0
	target := idleTurnTargetView{
		contactID:          "c1",
		queuedID:           "item-1",
		queuedCollabMsgID:  "msg_retry",
		queuedCollabMailTo: "sc_to",
		queuedSource:       "collab:sc_from",
		run: func(context.Context, string) error {
			return errors.New("boom")
		},
		retrying: func(contactID, itemID, collabMsgID, collabMailTo, source string, attempts int, lastErr error) {
			mu.Lock()
			defer mu.Unlock()
			retrying = append(retrying, attempts)
		},
		exhausted: func(contactID, itemID, collabMsgID, collabMailTo, source string, attempts int, lastErr error) {
			mu.Lock()
			defer mu.Unlock()
			exhausted++
		},
	}

	// 起时钟 + 三趟连续失败（每趟间隔 ≥ 4s tick 的节奏即可，预算按失败次数算）。
	b.sweepTargets([]idleTurnTargetView{target}, start)
	b.sweepTargets([]idleTurnTargetView{target}, start.Add(sessionCollabIdleTurnDelay))
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && b.inFlightNow("c1") {
		time.Sleep(10 * time.Millisecond)
	}
	for i := 1; i <= 2; i++ {
		at := start.Add(sessionCollabIdleTurnDelay + time.Duration(i)*4*time.Second)
		b.sweepTargets([]idleTurnTargetView{target}, at)
		deadline = time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) && b.inFlightNow("c1") {
			time.Sleep(10 * time.Millisecond)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(retrying) != 2 || retrying[0] != 1 || retrying[1] != 2 {
		t.Fatalf("retrying must fire for attempts 1 and 2, got %v", retrying)
	}
	if exhausted != 1 {
		t.Fatalf("exhausted must fire exactly once at strike 3, got %d", exhausted)
	}
}

// TestRecordIdleTurnRetryingWritesReceipt（真实落盘）：经 REASONIX_STATE_HOME
// 钉住真实 mailDir，recordIdleTurnRetrying 写出的回执可被 DeliveryReceipt 读回，
// outcome=failed_retrying 且 attempts 原样保存。
func TestRecordIdleTurnRetryingWritesReceipt(t *testing.T) {
	state := t.TempDir()
	t.Setenv("REASONIX_STATE_HOME", state)
	mailDir := config.SessionCollabMailDir()

	p := &sessionCollabPump{}
	p.recordIdleTurnRetrying("sc_to", "item-1", "msg_mid", "sc_to", "collab:sc_from", 2, errors.New("context canceled"))

	store := sessioncollab.NewMailStore(mailDir)
	got, ok := store.DeliveryReceipt("msg_mid")
	if !ok {
		t.Fatal("the retrying receipt must be persisted for get_message_status")
	}
	if got.Outcome != sessioncollab.ReceiptFailedRetrying {
		t.Fatalf("outcome = %q, want failed_retrying", got.Outcome)
	}
	if got.Attempts != 2 {
		t.Fatalf("attempts = %d, want 2", got.Attempts)
	}
	if got.To != "sc_to" || got.From != "sc_from" {
		t.Fatalf("parties = %q -> %q, want sc_from -> sc_to", got.From, got.To)
	}

	// 本地队列条目（无 collab 消息 id）：零回执、零副作用。
	p.recordIdleTurnRetrying("sc_to", "item-2", "", "sc_to", "", 1, errors.New("x"))
	entries, err := os.ReadDir(filepath.Join(mailDir, "receipts"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("a local queue entry must not write a receipt, entries = %d", len(entries))
	}
}
