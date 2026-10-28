package capability

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/skill"
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
	if stats.Rows != 3 {
		t.Fatalf("rows = %d, want every entry rendered: %+v", stats.Rows, stats)
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
		{ID: "skill:a", Kind: KindSkill, Name: "a", Description: strings.Repeat("x", 150), ShortDescription: "curated"},
	}}
	block, _ := DirectoryBlock(catalog, DirectoryOptions{})
	if !strings.Contains(block, "- skill:a — curated") {
		t.Fatalf("ShortDescription not preferred:\n%s", block)
	}
}

// Task 1224 phase 2 acceptance (全量注入): the 64KB hard cap is retired — a
// catalog well past the warning line renders EVERY row, reports its full size,
// and raises NearCap without dropping or truncating anything. 800 rows × ~91B
// ≈ 72KB clears the 64KB warn line (the same shape that truncated under the
// phase-1 cap).
func TestDirectoryBlockRendersEverythingPastWarningLine(t *testing.T) {
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
	if stats.Rows != 800 {
		t.Fatalf("rows = %d, want all 800 rendered past the warn line: %+v", stats.Rows, stats)
	}
	if !stats.NearCap {
		t.Fatalf("block past DirectoryWarnBytes must raise NearCap: %+v", stats)
	}
	if stats.Bytes < DirectoryWarnBytes {
		t.Fatalf("counter %dB under the warn line — fixture not load-bearing", stats.Bytes)
	}
	if strings.Contains(block, "truncated") {
		t.Fatalf("phase-2 block must never carry a truncation notice:\n%s", block[len(block)-200:])
	}
	rows := 0
	for _, line := range strings.Split(block, "\n") {
		if strings.HasPrefix(line, "- ") {
			rows++
		}
	}
	if rows != stats.Rows {
		t.Fatalf("rendered rows %d != counted rows %d", rows, stats.Rows)
	}
	for i := 0; i < 800; i += 97 { // spot-check first/middle/last rows survive
		if !strings.Contains(block, fmt.Sprintf("- tool:%s-%03d", strings.Repeat("n", 80), i)) {
			t.Fatalf("row %d missing from the rendered block", i)
		}
	}
}

// Task 1224 phase 2 acceptance (全量注入，.claude/.agents 注册面): skills
// discovered from the .claude and .agents convention dirs render in full —
// the injection carries the whole enabled surface, not just .reasonix rows.
func TestDirectoryBlockRendersConventionDirSurfaces(t *testing.T) {
	root := t.TempDir()
	writeSkill := func(rel, body string) {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeSkill(".claude/skills/claude-surface/SKILL.md", "---\ndescription: from .claude\n---\nbody")
	writeSkill(".agents/skills/agents-surface/SKILL.md", "---\ndescription: from .agents\n---\nbody")
	writeSkill(".reasonix/skills/reasonix-surface/SKILL.md", "---\ndescription: from .reasonix\n---\nbody")

	store := skill.New(skill.Options{ProjectRoot: root, HomeDir: t.TempDir(), DisableBuiltins: true})
	skills := store.List()
	if len(skills) != 3 {
		t.Fatalf("discovery found %d skills, want 3 convention-dir surfaces", len(skills))
	}
	block, stats := DirectoryBlock(BuildCatalog(CatalogOptions{Skills: skills}), DirectoryOptions{})
	if stats.Rows != 3 {
		t.Fatalf("directory rows = %d, want the full registration surface: %+v", stats.Rows, stats)
	}
	for _, want := range []string{"- skill:claude-surface — from .claude", "- skill:agents-surface — from .agents", "- skill:reasonix-surface — from .reasonix"} {
		if !strings.Contains(block, want) {
			t.Fatalf("convention-dir row missing from the standing directory:\n%s\nwant %q", block, want)
		}
	}
}

// Task 1224 phase 2 acceptance (警告线): NearCap fires on both independent
// axes (bytes past DirectoryWarnBytes, rows past DirectoryWarnRows) with the
// block still fully rendered, and stays off below the thresholds.
func TestDirectoryBlockWarnLineSignal(t *testing.T) {
	// Bytes axis: 470 wide tool rows × ~142B ≈ 66.7KB — past the 64KB warn
	// line, with the row count below DirectoryWarnRows so only the byte axis
	// fires. (Tool rows render the id only, so the rows must be wide, not just
	// numerous.)
	entries := make([]Entry, 0, 470)
	for i := 0; i < 470; i++ {
		id := fmt.Sprintf("tool:%s-%03d", strings.Repeat("n", 130), i)
		entries = append(entries, Entry{
			ID: id, Kind: KindTool, Name: id[len("tool:"):], ToolName: id[len("tool:"):],
		})
	}
	_, stats := DirectoryBlock(Catalog{Entries: entries}, DirectoryOptions{})
	if stats.Rows != 470 {
		t.Fatalf("over-line catalog must render whole: %+v", stats)
	}
	if !stats.NearCap {
		t.Fatalf("bytes past DirectoryWarnBytes must raise NearCap: %+v", stats)
	}

	// Count axis: 501 short tool rows ≈ 6.5KB — under the byte warn line, but
	// the row count crosses DirectoryWarnRows on its own.
	entries = entries[:0]
	for i := 0; i < 501; i++ {
		id := fmt.Sprintf("tool:t-%03d", i)
		entries = append(entries, Entry{
			ID: id, Kind: KindTool, Name: id[len("tool:"):], ToolName: id[len("tool:"):],
		})
	}
	_, stats = DirectoryBlock(Catalog{Entries: entries}, DirectoryOptions{})
	if stats.Bytes >= DirectoryWarnBytes {
		t.Fatalf("count-axis catalog must stay small: %+v", stats)
	}
	if !stats.NearCap {
		t.Fatalf("rows (%d) past DirectoryWarnRows must raise NearCap: %+v", stats.Rows, stats)
	}

	// Below both thresholds: no signal.
	_, stats = DirectoryBlock(Catalog{Entries: entries[:400]}, DirectoryOptions{})
	if stats.NearCap {
		t.Fatalf("400-row catalog must stay under every threshold: %+v", stats)
	}
}

// Task 1224 phase 1 acceptance ① carried forward: a real-world-sized catalog
// (401 entries, the population that dropped 233 rows under the old 20KB cap)
// renders in full at ~49KB — under the 64KB warning line, so today's watermark
// raises no signal.
func TestDirectoryBlockRealWorldWatermarkFullAndQuiet(t *testing.T) {
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
	if stats.Rows != 401 {
		t.Fatalf("rows = %d, want all 401 entries visible", stats.Rows)
	}
	if stats.Bytes <= 20*1024 {
		t.Fatalf("catalog is only %dB — fixture lost its real-world shape", stats.Bytes)
	}
	if stats.NearCap {
		t.Fatalf("current real-world watermark (~49KB) must sit under the warn line: %+v", stats)
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
