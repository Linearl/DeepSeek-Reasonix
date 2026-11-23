package agent

import (
	"context"
	"strings"
	"testing"
)

// tl1770 观测增强（用户拍板 20261123 14:43）验收。
//
// 现场：压缩 usage 只发事件（UsageSource=compaction）不落日志行，800K
// miss 与 800K hit 的成本差几十上百倍却无从对账。契约：runSummaryRequest
// 系（全部摘要请求形态的共用流出口）在流结束时打一条
// "summary request cache readout"，带 prompt_tokens / cache_hit_tokens /
// cache_miss_tokens / ttft_ms / duration_ms；usage 缺失时字段落 0 而不是
// 缺行；失败请求也打（err 字段 + WARN 级），观察面不改任何行为。
// 日志捕获复用 network_error_recovery_e2e_test.go 的 captureSlog/syncBuffer。

func TestSummaryRequestCacheReadoutLoggedOnSuccess(t *testing.T) {
	prov := &tl1714StubProvider{reply: "- goal: x"}
	sess := foldableSessionOverForce(6)
	a := agentOverForce(t, prov, sess)
	buf := captureSlog(t)

	summary, usage, err := a.summarize(context.Background(), sess.Messages[1:], "")
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	if usage == nil || usage.PromptTokens != 100 {
		t.Fatalf("usage = %+v, want the stub's prompt_tokens=100 bill", usage)
	}
	if summary != "- goal: x" {
		t.Fatalf("summary = %q", summary)
	}
	out := buf.String()
	if !strings.Contains(out, "summary request cache readout") {
		t.Fatalf("readout line missing:\n%s", out)
	}
	if !strings.Contains(out, "prompt_tokens=100") {
		t.Fatalf("prompt_tokens=100 missing:\n%s", out)
	}
	if !strings.Contains(out, "cache_hit_tokens=0") || !strings.Contains(out, "cache_miss_tokens=0") {
		t.Fatalf("cache hit/miss fields missing (stub usage carries none — fields must read 0, not absent):\n%s", out)
	}
	if !strings.Contains(out, "ttft_ms=") || !strings.Contains(out, "duration_ms=") {
		t.Fatalf("ttft/duration fields missing:\n%s", out)
	}
	if !strings.Contains(out, "out_tokens=5") {
		t.Fatalf("out_tokens=5 missing:\n%s", out)
	}
	if strings.Contains(out, "level=WARN") {
		t.Fatalf("success readout must not be WARN:\n%s", out)
	}
}

func TestSummaryRequestCacheReadoutLoggedOnError(t *testing.T) {
	prov := &tl1714StubProvider{emptyCalls: 1, toolCall: true, billOut: 1026, reply: "- goal: x"}
	sess := foldableSessionOverForce(6)
	a := agentOverForce(t, prov, sess)
	buf := captureSlog(t)

	// Direct runSummaryRequest (no summarizeOnce replay): the first request's
	// empty-output failure must still carry its own readout line.
	_, _, err := a.runSummaryRequest(context.Background(), a.summaryRequest(sess.Messages[1:], ""))
	if err == nil {
		t.Fatal("expected the empty-output failure")
	}
	out := buf.String()
	if !strings.Contains(out, "summary request cache readout") {
		t.Fatalf("readout line missing on the failure path:\n%s", out)
	}
	if !strings.Contains(out, "level=WARN") {
		t.Fatalf("failure readout must be WARN:\n%s", out)
	}
	if !strings.Contains(out, "err=") {
		t.Fatalf("err field missing:\n%s", out)
	}
	if !strings.Contains(out, "prompt_tokens=100") {
		t.Fatalf("failed request still carries its billed prompt readout:\n%s", out)
	}
}
