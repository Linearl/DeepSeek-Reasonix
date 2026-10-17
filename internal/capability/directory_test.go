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

// Task 969 acceptance ①: the 20KB mechanical counter — the block never exceeds
// the cap; overflow drops whole rows and reports the truncation.
func TestDirectoryBlockEnforces20KBCap(t *testing.T) {
	entries := make([]Entry, 0, 400)
	for i := 0; i < 400; i++ {
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
