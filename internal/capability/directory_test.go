package capability

import (
	"fmt"
	"strings"
	"testing"
)

// Task 969 acceptance ②: standing summaries are capped at 100B — over-limit
// descriptions are clipped, and the clip never splits a grapheme cluster.
func TestDirectoryBlockClipsSummariesTo100Bytes(t *testing.T) {
	long := strings.Repeat("a", 150)
	cjk := strings.Repeat("汉", 120) // 3 bytes each
	catalog := Catalog{Entries: []Entry{
		{ID: "skill:long", Kind: KindSkill, Name: "long", Description: long},
		{ID: "skill:cjk", Kind: KindSkill, Name: "cjk", Description: cjk},
		{ID: "skill:short", Kind: KindSkill, Name: "short", Description: "short summary"},
	}}
	block, stats := DirectoryBlock(catalog, DirectoryOptions{})
	if stats.Truncated {
		t.Fatalf("small catalog truncated: %+v", stats)
	}
	for _, line := range strings.Split(block, "\n") {
		if !strings.HasPrefix(line, "- skill:") {
			continue
		}
		idx := strings.Index(line, " — ")
		if idx < 0 {
			continue
		}
		if summary := line[idx+len(" — "):]; len(summary) > DirectorySummaryBytes {
			t.Fatalf("summary exceeds %dB: %q (%dB)", DirectorySummaryBytes, line, len(summary))
		}
	}
	if !strings.Contains(block, "- skill:long — "+strings.Repeat("a", 97)+"…") {
		t.Fatalf("long summary not clipped to 100B with ellipsis:\n%s", block)
	}
	if !strings.Contains(block, "- skill:short — short summary") {
		t.Fatalf("short summary altered:\n%s", block)
	}
	// 120 CJK chars = 360B; the clip must land on a whole grapheme, so the
	// rendered line stays valid UTF-8 and ≤100B.
	cjkLine := ""
	for _, line := range strings.Split(block, "\n") {
		if strings.HasPrefix(line, "- skill:cjk — ") {
			cjkLine = line
		}
	}
	if cjkLine == "" {
		t.Fatalf("cjk row missing:\n%s", block)
	}
	summary := cjkLine[len("- skill:cjk — "):]
	if len(summary) > DirectorySummaryBytes {
		t.Fatalf("cjk summary %dB exceeds cap", len(summary))
	}
}

// Task 969: a curated ShortDescription wins over the mechanical clip.
func TestDirectoryBlockPrefersShortDescription(t *testing.T) {
	catalog := Catalog{Entries: []Entry{
		{ID: "skill:curated", Kind: KindSkill, Name: "curated",
			Description:      strings.Repeat("d", 200),
			ShortDescription: "curated one-liner"},
	}}
	block, _ := DirectoryBlock(catalog, DirectoryOptions{})
	if !strings.Contains(block, "- skill:curated — curated one-liner") {
		t.Fatalf("ShortDescription not used:\n%s", block)
	}
	if strings.Contains(block, strings.Repeat("d", 50)) {
		t.Fatalf("long description leaked past ShortDescription:\n%s", block)
	}
}

// Task 969: standing surface policy — tools/sources render id/name only, MCP
// servers get a status row, concrete MCP tools stay behind inspect, and
// provider-visible core tools are not duplicated into the directory.
func TestDirectoryBlockRowPolicy(t *testing.T) {
	catalog := Catalog{Entries: []Entry{
		{ID: "tool:gh_pr_list", Kind: KindTool, Name: "gh_pr_list", ToolName: "gh_pr_list", Description: "full description must not inline"},
		{ID: "tool:write", Kind: KindTool, Name: "write", ToolName: "write"},
		{ID: "mcp-server:github", Kind: KindMCPServer, Name: "github", Status: StatusReady},
		{ID: "mcp-server:flaky", Kind: KindMCPServer, Name: "flaky", Status: StatusFailed},
		{ID: "mcp-tool:github/gh-issue", Kind: KindMCPTool, Name: "github/gh-issue", Description: "hidden behind inspect"},
		{ID: "skill:manual", Kind: KindSkill, Name: "manual", Description: "manual only", Invocation: "manual"},
	}}
	block, _ := DirectoryBlock(catalog, DirectoryOptions{
		ProviderVisible: func(name string) bool { return name == "write" },
	})
	for _, want := range []string{"- tool:gh_pr_list", "- mcp-server:github", "- mcp-server:flaky — failed"} {
		if !strings.Contains(block, want) {
			t.Fatalf("directory missing %q:\n%s", want, block)
		}
	}
	for _, banned := range []string{"full description must not inline", "hidden behind inspect", "tool:write", "skill:manual"} {
		if strings.Contains(block, banned) {
			t.Fatalf("directory leaked %q:\n%s", banned, block)
		}
	}
}

// Task 969 acceptance ①: the mechanical byte counter — the block never exceeds
// the cap; overflow drops whole rows and reports the truncation. 800 rows ×
// ~91B ≈ 72KB clears the 64KB budget (task 1224 phase 1); the pre-1224 shape
// of this test (400 rows ≈ 37KB) no longer truncates, which is the raise
// working as intended.
func TestDirectoryBlockEnforcesByteCap(t *testing.T) {
	entries := make([]Entry, 0, 800)
	for i := 0; i < 800; i++ {
		id := fmt.Sprintf("tool:%s-%03d", strings.Repeat("n", 80), i)
		entries = append(entries, Entry{
			ID:          id,
			Kind:        KindTool,
			Name:        id[len("tool:"):],
			ToolName:    id[len("tool:"):],
			Description: strings.Repeat("z", 100),
		})
	}
	block, stats := DirectoryBlock(Catalog{Entries: entries}, DirectoryOptions{})
	if !stats.Truncated || stats.Dropped == 0 {
		t.Fatalf("oversized catalog not truncated: %+v", stats)
	}
	if stats.Bytes > DirectoryMaxBytes {
		t.Fatalf("counter %dB exceeds cap %dB", stats.Bytes, DirectoryMaxBytes)
	}
	// The rendered block adds the fence and the truncation notice on top of the
	// counted rows; the rows themselves stay within the cap.
	rows := 0
	for _, line := range strings.Split(block, "\n") {
		if strings.HasPrefix(line, "- ") {
			rows++
		}
	}
	if rows != stats.Rows {
		t.Fatalf("rendered rows %d != counted rows %d", rows, stats.Rows)
	}
	if !strings.Contains(block, "directory truncated") {
		t.Fatalf("truncation notice missing:\n%s", block[len(block)-200:])
	}
}

// Task 1224 phase 1 acceptance ①: the raised budget keeps a real-world-sized
// catalog (401 entries, the population that dropped 233 rows under the old
// 20KB cap) fully visible, and the same catalog proves the raise is
// load-bearing by still exceeding the old 20KB cap.
func TestDirectoryBlockRaisedBudgetKeepsFullCatalog(t *testing.T) {
	entries := make([]Entry, 0, 401)
	for i := 0; i < 401; i++ {
		id := fmt.Sprintf("skill:capability-%03d", i)
		entries = append(entries, Entry{
			ID:          id,
			Kind:        KindSkill,
			Name:        id[len("skill:"):],
			Description: strings.Repeat("d", 95), // rows render at ~121B, the measured ~122B/row shape
		})
	}
	block, stats := DirectoryBlock(Catalog{Entries: entries}, DirectoryOptions{})
	if stats.Truncated || stats.Dropped != 0 {
		t.Fatalf("401-entry catalog truncated under the raised budget: %+v", stats)
	}
	if stats.Rows != 401 {
		t.Fatalf("rows = %d, want all 401 entries visible", stats.Rows)
	}
	if stats.Bytes <= 20*1024 {
		t.Fatalf("catalog is only %dB — the raise is not load-bearing at this size", stats.Bytes)
	}
	if stats.Bytes > DirectoryMaxBytes {
		t.Fatalf("counter %dB exceeds cap %dB", stats.Bytes, DirectoryMaxBytes)
	}
	if stats.NearCap {
		t.Fatalf("current real-world watermark (~49KB) must sit under the warn thresholds: %+v", stats)
	}
	rows := 0
	for _, line := range strings.Split(block, "\n") {
		if strings.HasPrefix(line, "- skill:") {
			rows++
		}
	}
	if rows != 401 {
		t.Fatalf("rendered rows = %d, want 401", rows)
	}
}

// Task 1224 phase 1 acceptance ②: the near-cap collision signal fires on both
// independent axes (bytes past DirectoryWarnBytes, wanted rows past
// DirectoryWarnRows) while the block is still fully rendered, and stays off
// below the thresholds.
func TestDirectoryBlockNearCapSignal(t *testing.T) {
	// Bytes axis: 440 long-id tool rows × ~142B ≈ 62KB — past the 60KB warn
	// line, under the 64KB cap, with the row count below DirectoryWarnRows so
	// only the byte axis fires. (Tool rows render the id only, so the rows
	// must be wide, not just numerous.)
	entries := make([]Entry, 0, 440)
	for i := 0; i < 440; i++ {
		id := fmt.Sprintf("tool:%s-%03d", strings.Repeat("n", 130), i)
		entries = append(entries, Entry{
			ID: id, Kind: KindTool, Name: id[len("tool:"):], ToolName: id[len("tool:"):],
		})
	}
	_, stats := DirectoryBlock(Catalog{Entries: entries}, DirectoryOptions{})
	if stats.Truncated {
		t.Fatalf("62KB catalog must render whole under the 64KB cap: %+v", stats)
	}
	if !stats.NearCap {
		t.Fatalf("bytes past DirectoryWarnBytes must raise NearCap: %+v", stats)
	}

	// Count axis: 501 short tool rows ≈ 6.5KB — under the byte warn line, but
	// the wanted row count crosses DirectoryWarnRows on its own.
	entries = entries[:0]
	for i := 0; i < 501; i++ {
		id := fmt.Sprintf("tool:t-%03d", i)
		entries = append(entries, Entry{
			ID: id, Kind: KindTool, Name: id[len("tool:"):], ToolName: id[len("tool:"):],
		})
	}
	_, stats = DirectoryBlock(Catalog{Entries: entries}, DirectoryOptions{})
	if stats.Truncated || stats.Bytes >= DirectoryWarnBytes {
		t.Fatalf("count-axis catalog must stay small and whole: %+v", stats)
	}
	if !stats.NearCap {
		t.Fatalf("wanted rows (%d+%d) past DirectoryWarnRows must raise NearCap: %+v", stats.Rows, stats.Dropped, stats)
	}

	// Below both thresholds: no signal.
	_, stats = DirectoryBlock(Catalog{Entries: entries[:400]}, DirectoryOptions{})
	if stats.NearCap || stats.Truncated {
		t.Fatalf("400-row catalog must stay under every threshold: %+v", stats)
	}
}

// Ratchet: an empty or all-skipped catalog renders nothing rather than an
// empty fence.
func TestDirectoryBlockEmptyRendersNothing(t *testing.T) {
	block, stats := DirectoryBlock(Catalog{}, DirectoryOptions{})
	if block != "" || stats != (DirectoryStats{}) {
		t.Fatalf("empty catalog = %q %+v", block, stats)
	}
	block, stats = DirectoryBlock(Catalog{Entries: []Entry{
		{ID: "mcp-tool:a/b", Kind: KindMCPTool, Name: "a/b"},
	}}, DirectoryOptions{})
	if block != "" || stats.Rows != 0 {
		t.Fatalf("all-skipped catalog = %q %+v", block, stats)
	}
}
