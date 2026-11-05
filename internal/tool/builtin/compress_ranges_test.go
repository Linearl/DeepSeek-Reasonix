package builtin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"reasonix/internal/tool"
)

// tl1419: the compress_ranges tool face — schema/execute protocol over the
// session-bound RangesFolder, and the registration invariants that keep the
// default-off contract (boot-gated injection, serial barrier lane).

type rangesFolderStub struct {
	request tool.RangesFoldRequest
	result  tool.RangesFoldResult
	err     error
}

func (s *rangesFolderStub) RangesFoldCompact(_ context.Context, request tool.RangesFoldRequest) (tool.RangesFoldResult, error) {
	s.request = request
	return s.result, s.err
}

func TestCompressRangesToolProtocol(t *testing.T) {
	stub := &rangesFolderStub{result: tool.RangesFoldResult{
		Status: "ok", SegmentTokens: 3100, SourceTokens: 11645, ProjectionTokens: 8718, FoldIndex: 1,
	}}
	ctx := tool.WithRangesFolder(context.Background(), stub)
	out, err := (CompressRangesTool{}).Execute(ctx, argsJSON(t, map[string]any{
		"focus": "  keep decisions and open questions  ",
	}))
	if err != nil {
		t.Fatalf("compress_ranges Execute: %v", err)
	}
	if stub.request.Focus != "keep decisions and open questions" {
		t.Fatalf("forwarded focus = %q", stub.request.Focus)
	}
	var got tool.RangesFoldResult
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if got != stub.result {
		t.Fatalf("result = %+v, want %+v", got, stub.result)
	}
	if !(CompressRangesTool{}).ReadOnly() || !(CompressRangesTool{}).PlanModeSafe() {
		t.Fatal("compress_ranges must be workspace-read-only and Plan Mode safe")
	}
}

// 拒约(洪水闸/开关门)必须原样到模型手里:status=rejected + 带读数的拒因,
// 不允许被吞成协议错误。
func TestCompressRangesToolCarriesRejection(t *testing.T) {
	stub := &rangesFolderStub{result: tool.RangesFoldResult{
		Status: "rejected", Reason: "below-savings-floor: this fold would only save about 212 billed tokens",
	}}
	ctx := tool.WithRangesFolder(context.Background(), stub)
	out, err := (CompressRangesTool{}).Execute(ctx, argsJSON(t, map[string]any{}))
	if err != nil {
		t.Fatalf("Execute must not error on a gate rejection: %v", err)
	}
	var got tool.RangesFoldResult
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if got.Status != "rejected" || !strings.Contains(got.Reason, "below-savings-floor") {
		t.Fatalf("rejection did not survive the tool face: %+v", got)
	}
}

func TestCompressRangesToolRejectsInvalidArgs(t *testing.T) {
	if _, err := (CompressRangesTool{}).Execute(context.Background(), json.RawMessage("{not json")); err == nil {
		t.Fatal("invalid JSON args must error")
	}
	if _, err := (CompressRangesTool{}).Execute(context.Background(), argsJSON(t, map[string]any{
		"focus": strings.Repeat("f", maxCompressFocusBytes+1),
	})); err == nil {
		t.Fatal("oversized focus must error")
	}
	if _, err := (CompressRangesTool{}).Execute(context.Background(), argsJSON(t, map[string]any{
		"focus": "fine",
	})); err == nil {
		t.Fatal("missing session folder must error")
	}
}

// 注入不变量(开关默认关零动作的执行面):compress_ranges 不得进编译期
// builtin 注册表——它只随 experimental_model_ranges_compact 由 boot 条件
// 注册(curate_context 先例)。谁要是改成 RegisterBuiltin,默认关的模型可见
// 面就破了,这条测试即红。
func TestCompressRangesIsBootGatedNotABuiltin(t *testing.T) {
	for _, b := range tool.Builtins() {
		if b.Name() == "compress_ranges" {
			t.Fatal("compress_ranges must not self-register as a compile-time builtin (boot-gated injection keeps the default-off surface byte-identical)")
		}
	}
}

// 批次车道不变量:与 compress/curate_context 同族,投影安装有状态——必须
// 登记 barrier(ReadOnly=true + ConcurrentSafe=false),并行批把它扇出会与
// 其他工具的投影读交错。
func TestCompressRangesIsASerialBarrier(t *testing.T) {
	spec := tool.SafetyOfName("compress_ranges")
	if !spec.ReadOnly || spec.ConcurrentSafe {
		t.Fatalf("safety spec = %+v, want ReadOnly + not ConcurrentSafe (serial barrier)", spec)
	}
}
