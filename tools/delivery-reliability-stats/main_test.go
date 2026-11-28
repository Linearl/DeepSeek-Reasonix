package main

// tl1747：统计工具自测——合成 slog 文本行喂 scan，四类读数逐一断言。
// 全部 t.Fatal 断言，无静默 SKIP。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(dir, name string) error {
	return os.WriteFile(filepath.Join(dir, name), []byte("time=2026-11-28T10:00:00.000 level=INFO msg=x\n"), 0o644)
}

const sampleLog = `time=2026-11-28T10:00:00.123 level=WARN msg="session-collab delivery decision" delivery_decision=failed_retrying decision_reason=enqueue_error session=sc_a topic=topic_x msg_id=m1 from=sc_s err="boom"
time=2026-11-28T10:05:00.456 level=WARN msg="session-collab delivery decision" delivery_decision=failed_retrying decision_reason=contact_pass_failed session=sc_a topic=topic_x retry_attempt=1 retry_cap=0
time=2026-11-28T10:10:00.789 level=WARN msg="session-collab delivery decision" delivery_decision=failed_retrying decision_reason=session_open_failed session=sc_b topic= retry_attempt=2 retry_cap=0
time=2026-11-28T11:00:00.001 level=WARN msg="session-collab delivery decision" delivery_decision=degraded decision_reason=open_retry_exhausted session=sc_a topic= msg_id=m1 source=collab:sc_s item_id=i1 attempts=3 retry_cap=3 err="unpublished"
time=2026-11-28T11:30:00.002 level=INFO msg="session-collab delivery decision" delivery_decision=delivered decision_reason=contact_retry_recovered session=sc_a topic=topic_x retry_attempt=2 retry_cap=0
time=2026-11-28T12:00:00.003 level=INFO msg="session-collab delivery decision" delivery_decision=cancelled decision_reason=inbox_cancel session=sc_r topic= msg_id=m9 cancel_source=desktop source=collab:sc_s
time=2026-11-28T12:05:00.004 level=INFO msg="session-collab delivery decision" delivery_decision=cancelled decision_reason=inbox_cancel session= topic= msg_id=m10 cancel_source=edit_resend source=
time=2026-11-28T12:06:00.005 level=INFO msg="session-collab delivery decision" delivery_decision=cancelled decision_reason=inbox_cancel session=sc_r2 topic= msg_id=m11 cancel_source=collab:sc_other source=collab:sc_other
time=2026-11-29T09:00:00.005 level=WARN msg="session-collab delivery decision" delivery_decision=failed_retrying decision_reason=contact_pass_failed session=sc_c topic= retry_attempt=1 retry_cap=0
time=2026-11-29T09:01:00.006 level=INFO msg="session-collab delivery decision" delivery_decision=queued decision_reason=delivery_followup session=sc_c topic= msg_id=m11 from=sc_s
`

func TestScanCountsAllFourReadings(t *testing.T) {
	c := newCounts()
	if err := scan(strings.NewReader(sampleLog), "", c); err != nil {
		t.Fatal(err)
	}
	// ① failed_retrying 计数（按 reason 细分）。
	if got := c.failedRetryByReason["enqueue_error"]; got != 1 {
		t.Fatalf("enqueue_error = %d, want 1 (%+v)", got, c.failedRetryByReason)
	}
	if got := c.failedRetryByReason["contact_pass_failed"]; got != 2 {
		t.Fatalf("contact_pass_failed = %d, want 2", got)
	}
	if got := c.failedRetryByReason["session_open_failed"]; got != 1 {
		t.Fatalf("session_open_failed = %d, want 1", got)
	}
	// ② exhausted 计数。
	if c.exhausted != 1 {
		t.Fatalf("exhausted = %d, want 1", c.exhausted)
	}
	// ③ cancel 来源分布。
	if got := c.cancelBySource["desktop"]; got != 1 {
		t.Fatalf("cancel desktop = %d, want 1 (%+v)", got, c.cancelBySource)
	}
	if got := c.cancelBySource["edit_resend"]; got != 1 {
		t.Fatalf("cancel edit_resend = %d, want 1", got)
	}
	if got := c.cancelBySource["collab"]; got != 1 {
		t.Fatalf("collab: prefixed sources must aggregate to collab, got %d (%+v)", got, c.cancelBySource)
	}
	// ④ 重试恢复率读数：分子=1（recovered），分母=3（contact 级失败）。
	if c.recovered != 1 {
		t.Fatalf("recovered = %d, want 1", c.recovered)
	}
	contactFailed := c.failedRetryByReason["contact_pass_failed"] + c.failedRetryByReason["session_open_failed"]
	if contactFailed != 3 {
		t.Fatalf("contact-level failures = %d, want 3", contactFailed)
	}
	if c.recoveredAttempts != 2 {
		t.Fatalf("recoveredAttempts = %d, want 2 (chain recovered from attempt 2)", c.recoveredAttempts)
	}
}

func TestScanSinceDropsEarlierDays(t *testing.T) {
	c := newCounts()
	if err := scan(strings.NewReader(sampleLog), "2026-11-29", c); err != nil {
		t.Fatal(err)
	}
	if got := c.failedRetryByReason["contact_pass_failed"]; got != 1 {
		t.Fatalf("since-filtered contact_pass_failed = %d, want 1", got)
	}
	if c.exhausted != 0 || len(c.cancelBySource) != 0 {
		t.Fatalf("since filter must drop 11-28 exhausted/cancel rows, got exhausted=%d cancels=%+v", c.exhausted, c.cancelBySource)
	}
}

func TestScanIgnoresNonDeliveryLines(t *testing.T) {
	c := newCounts()
	noisy := "time=2026-11-28T10:00:00.000 level=INFO msg=\"unrelated\" delivery_decision_absent=1\n" +
		"garbage line without time prefix\n"
	if err := scan(strings.NewReader(noisy), "", c); err != nil {
		t.Fatal(err)
	}
	if c.exhausted != 0 || len(c.failedRetryByReason) != 0 || len(c.cancelBySource) != 0 {
		t.Fatalf("non-delivery lines must not count, got %+v", c)
	}
}

func TestExpandArgsDirectoryPicksRotations(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"desktop.log", "desktop.log.1", "other.txt"} {
		if err := writeFile(dir, name); err != nil {
			t.Fatal(err)
		}
	}
	files := expandArgs([]string{dir})
	if len(files) != 2 {
		t.Fatalf("dir expansion = %v, want the two desktop.log files", files)
	}
}
