package agent

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"reasonix/internal/tool"
)

// tl1561 分层阈值验收钉（用户拍板：关键 100K / 常规 40K / 流式 8K）。
// 矩阵覆盖：三档映射 × 截断/不截断 × marker 可读路径 × 归档读回一致 × 小返回零回退。

// archivePathInMarker extracts the archive=<path> field from a truncation
// marker. archive=none renders when no archive exists.
func archivePathInMarker(marker string) string {
	idx := strings.Index(marker, " archive=")
	if idx < 0 {
		return ""
	}
	rest := marker[idx+len(" archive="):]
	end := strings.Index(rest, " — ")
	if end < 0 {
		return ""
	}
	return strings.TrimSuffix(rest[:end], " ")
}

func TestToolOutputBudgetTiers(t *testing.T) {
	cases := []struct {
		tool string
		want int
	}{
		// 关键档 100K：返回即任务对象（read_file/web_fetch/use_capability/heartbeat_task_list）。
		{"read_file", criticalToolOutputBytes},
		{"web_fetch", criticalToolOutputBytes},
		{"use_capability", criticalToolOutputBytes},
		{"heartbeat_task_list", criticalToolOutputBytes},
		// 流式档 8K：后台任务增量轮询（旧增量已被上次轮询消费）。
		{"bash_output", streamingToolOutputBytes},
		// 常规档 40K：默认 fail-safe——未知工具不意外放大。
		{"bash", regularToolOutputBytes},
		{"grep", regularToolOutputBytes},
		{"task", regularToolOutputBytes},
		{"mcp-tool:some/large", regularToolOutputBytes},
		{"", regularToolOutputBytes},
	}
	for _, tc := range cases {
		if got := toolOutputBudgetBytes(tc.tool); got != tc.want {
			t.Errorf("toolOutputBudgetBytes(%q) = %d, want %d", tc.tool, got, tc.want)
		}
	}
	if !(streamingToolOutputBytes < regularToolOutputBytes && regularToolOutputBytes < criticalToolOutputBytes) {
		t.Fatalf("tier ordering broken: streaming=%d regular=%d critical=%d", streamingToolOutputBytes, regularToolOutputBytes, criticalToolOutputBytes)
	}
	if maxToolOutputBytes != criticalToolOutputBytes {
		t.Fatalf("maxToolOutputBytes=%d must stay the widest tier (critical) as the global upper bound", maxToolOutputBytes)
	}
}

// 三阈值×截断/不截断：每档各构造一例恰好低于预算（原样直通）与一例超预算
//（截断发生）。equal 预算字节必须原样直通。
func TestTieredTruncationBoundaries(t *testing.T) {
	cases := []struct {
		name   string
		tool   string
		budget int
	}{
		{name: "critical", tool: "read_file", budget: criticalToolOutputBytes},
		{name: "regular", tool: "bash", budget: regularToolOutputBytes},
		{name: "streaming", tool: "bash_output", budget: streamingToolOutputBytes},
	}
	for _, tc := range cases {
		t.Run(tc.name+"/below", func(t *testing.T) {
			in := strings.Repeat("x", tc.budget-1)
			got, notice := truncateToolOutputFor(in, tc.tool, "call-below")
			if got != in || notice != "" {
				t.Fatalf("%s below-budget output was rewritten: bytes=%d notice=%q", tc.tool, len(got), notice)
			}
		})
		t.Run(tc.name+"/equal", func(t *testing.T) {
			in := strings.Repeat("x", tc.budget)
			got, notice := truncateToolOutputFor(in, tc.tool, "call-equal")
			if got != in || notice != "" {
				t.Fatalf("%s at-budget output was rewritten: bytes=%d notice=%q", tc.tool, len(got), notice)
			}
		})
		t.Run(tc.name+"/over", func(t *testing.T) {
			in := strings.Repeat("x", tc.budget+4096)
			got, notice := truncateToolOutputFor(in, tc.tool, "call-over")
			if notice == "" {
				t.Fatalf("%s over-budget output did not truncate", tc.tool)
			}
			if len(got) > tc.budget {
				t.Fatalf("%s truncated output exceeds its tier budget: %d > %d", tc.tool, len(got), tc.budget)
			}
			if !strings.Contains(got, "…[truncated tool="+tc.tool+" ") {
				t.Fatalf("%s truncated output misses its marker: %.400s", tc.tool, got)
			}
		})
	}
}

// tl 现场钉：heartbeat_task_list 78KB（原事故量级）经 use_capability 关键档
// 不再截断——按提示二次调用 result_ref 报 not found 的缺口从源头消失。
func TestUseCapabilitySiteScale78KPassesUntruncated(t *testing.T) {
	payload := strings.Repeat("task-row-data-界-🧪\n", 4000) // ~78KB, 与 20261111 事故同量级
	got, notice := truncateToolOutputFor(payload, "use_capability", "call-site78k")
	if notice != "" {
		t.Fatalf("78KB capability result must pass untruncated in the critical tier, got notice=%q", notice)
	}
	if got != payload {
		t.Fatalf("78KB capability result was rewritten: got %d bytes want %d", len(got), len(payload))
	}
}

// 回读一致钉（验收②）：关键档大返回 → 接线层截断 → marker 携带可读归档路径 →
// 按路径读回逐字节等于完整原文；read_file 按行窗口续读可达原文尾部。
func TestTieredTruncationArchiveReadsBackIdentical(t *testing.T) {
	a, _ := newArchiveTestAgent(t, true)
	raw := "HEAD-SENTINEL-🧪\n" + strings.Repeat("body-line-界\n", 9000) + "TAIL-SENTINEL-🧪\n"
	if len(raw) <= criticalToolOutputBytes {
		t.Fatalf("fixture too small for the critical tier: %d bytes", len(raw))
	}
	body, _, original := a.boundProviderVisibleResult(raw, "use_capability", "call-readback", "{}")
	if original == "" {
		t.Fatal("truncation did not retain the original for paging")
	}
	if !strings.Contains(body, "archive=") || strings.Contains(body, "archive=none") {
		t.Fatalf("truncation marker lacks a readable archive path: %.400s", body)
	}
	archive := archivePathInMarker(body)
	if archive == "" {
		t.Fatalf("archive path unparsable from marker: %.400s", body)
	}
	got, err := os.ReadFile(archive)
	if err != nil {
		t.Fatalf("archived original not readable at %q: %v", archive, err)
	}
	// boundProviderVisibleResult archives the pre-summary raw; for non-CI text
	// the archive must be byte-identical to the original tool output.
	if string(got) != raw {
		t.Fatalf("archived original differs: got %d bytes want %d", len(got), len(raw))
	}
	// 按提示用 read_file 按行窗口续读归档文件：尾部哨兵必须可达。
	read, ok := tool.LookupBuiltin("read_file")
	if !ok {
		t.Fatal("read_file builtin is not registered")
	}
	var total int
	for start := 1; start <= 40000; {
		out, err := read.Execute(t.Context(), []byte(fmt.Sprintf(`{"path":%q,"offset":%d,"limit":2000}`, archive, start)))
		if err != nil {
			t.Fatalf("read_file on archive failed at offset %d: %v", start, err)
		}
		if strings.Contains(out, "TAIL-SENTINEL-🧪") {
			total = start
			break
		}
		start += 2000
	}
	if total == 0 {
		t.Fatal("read_file windowed paging never reached the tail sentinel in the archive")
	}
}

// 归档触发与档位 lockstep：流式档 8K 超限时 marker 同样带可读路径，而不是
// 只有关键/常规档才有归档。
func TestStreamingTierTruncationArchivesOriginal(t *testing.T) {
	a, _ := newArchiveTestAgent(t, true)
	raw := strings.Repeat("stream-frame-🧪\n", 1200) // ~17KB, well over the 8KiB streaming tier
	body, _, original := a.boundProviderVisibleResult(raw, "bash_output", "call-stream", "{}")
	if original == "" {
		t.Fatal("streaming-tier truncation did not retain the original")
	}
	archive := archivePathInMarker(body)
	if archive == "" {
		t.Fatalf("streaming-tier marker lacks an archive path: %.400s", body)
	}
	got, err := os.ReadFile(archive)
	if err != nil || string(got) != raw {
		t.Fatalf("streaming-tier archive differs: err=%v got=%d want=%d", err, len(got), len(raw))
	}
}

// 小返回零回退（验收⑤）：任何档位下低于自身预算的输出字节级原样直通。
func TestSmallResultsPassUnchangedAcrossTiers(t *testing.T) {
	for _, toolName := range []string{"bash", "read_file", "bash_output", "use_capability"} {
		in := "tiny result 界\n"
		got, notice := truncateToolOutputFor(in, toolName, "call-tiny")
		if got != in || notice != "" {
			t.Fatalf("small result for %s was rewritten: %q / %q", toolName, got, notice)
		}
	}
}

// 全局上界钉：任何工具任何档位，截断后的模型可见输出都不超过全局上界
//（maxToolOutputBytes = 关键档）， CACHE_GUARD 合同依赖此上界。
func TestTruncatedOutputNeverExceedsGlobalBound(t *testing.T) {
	in := strings.Repeat("z", 512*1024)
	for _, toolName := range []string{"bash", "read_file", "bash_output", "use_capability", "grep"} {
		got, _ := truncateToolOutputFor(in, toolName, "call-bound")
		if len(got) > maxToolOutputBytes {
			t.Fatalf("%s truncated output %d exceeds the global bound %d", toolName, len(got), maxToolOutputBytes)
		}
	}
}

// marker 格式回归：三档 marker 均不再携带 result_ref 恢复承诺（第一轮退役的
// 通道），归档字段恒存在（none 或真实路径）。
var markerArchiveFieldRE = regexp.MustCompile(`archive=(none|[^ ]+)`)

func TestTierMarkersNeverPromiseResultRef(t *testing.T) {
	for _, toolName := range []string{"read_file", "bash", "bash_output", "use_capability"} {
		in := strings.Repeat("m", toolOutputBudgetBytes(toolName)+4096)
		got, notice := truncateToolOutputFor(in, toolName, "call-marker")
		if notice == "" {
			t.Fatalf("%s fixture did not truncate", toolName)
		}
		if strings.Contains(got, "result_ref") {
			t.Fatalf("%s marker still names the retired result_ref channel: %.400s", toolName, got)
		}
		if !markerArchiveFieldRE.MatchString(got) {
			t.Fatalf("%s marker misses an archive field: %.400s", toolName, got)
		}
	}
}
