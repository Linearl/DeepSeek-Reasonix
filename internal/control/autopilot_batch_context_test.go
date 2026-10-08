package control

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/config"
)

// Task 394: the autopilot batch-round context header — injection appears and
// disappears with the experimental dial, stays bounded with oldest-first
// truncation, and the two levels differ exactly by the conventions and
// acceptance reminders (plus the plan pointer). Attended sessions get nothing
// in every dial state.

const batchManifestFixture = `# Batch 7 plan

Some human-readable prose the header must not swallow.

<!-- reasonix-batch-manifest
batch: 400s-batch-2
open: 394 注入头 @wt-394; 401 卡片 @wt-401
in-flight: 402 面板 @wt-402
done: 400 批量派单; 399 守护收敛
conventions: 交付回执写 docs/report/zcode交付/；commit 用「394：」前缀
acceptance: 开关关态等价断言；大小有界断言
-->

More prose below the section.
`

func batchContextFixture(t *testing.T, level, planContent string) (*Controller, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if level != "" {
		if err := cfg.SetExperimentalAutopilotBatchContext(level); err != nil {
			t.Fatal(err)
		}
	}
	plan := ""
	if planContent != "" {
		plan = filepath.Join(home, "batch-plan.md")
		if err := os.WriteFile(plan, []byte(planContent), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := cfg.SetAutopilotBatchPlan(plan); err != nil {
			t.Fatal(err)
		}
	}
	if err := cfg.SaveTo(filepath.Join(home, "config.toml")); err != nil {
		t.Fatal(err)
	}
	return &Controller{autopilot: true}, plan
}

func TestBatchContextHeaderIsEmptyWhenDialOff(t *testing.T) {
	// 关态等价: with the dial off (default) the header is empty for every
	// input, so the round prompt is composed byte-for-byte as before — the
	// hooks only prepend a non-empty header.
	for _, level := range []string{"", "off"} {
		c, _ := batchContextFixture(t, level, batchManifestFixture)
		if got := c.autopilotBatchContextHeader(); got != "" {
			t.Fatalf("level %q must inject nothing, got: %s", level, got)
		}
	}
}

func TestBatchContextHeaderRequiresAutopilot(t *testing.T) {
	// 非 autopilot 会话零变更: an attended session gets no header even with
	// the dial fully on and a plan configured.
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.SetExperimentalAutopilotBatchContext("full"); err != nil {
		t.Fatal(err)
	}
	plan := filepath.Join(home, "batch-plan.md")
	if err := os.WriteFile(plan, []byte(batchManifestFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := cfg.SetAutopilotBatchPlan(plan); err != nil {
		t.Fatal(err)
	}
	if err := cfg.SaveTo(filepath.Join(home, "config.toml")); err != nil {
		t.Fatal(err)
	}
	attended := &Controller{}
	if got := attended.autopilotBatchContextHeader(); got != "" {
		t.Fatalf("attended session must never get a header, got: %s", got)
	}
}

func TestBatchContextMinimalCarriesChainTailAndThreads(t *testing.T) {
	c, plan := batchContextFixture(t, "minimal", batchManifestFixture)
	header := c.autopilotBatchContextHeader()
	for _, want := range []string{
		"<batch-context>",
		"batch: 400s-batch-2",
		"open threads: 394 注入头 @wt-394; 401 卡片 @wt-401",
		"in-flight: 402 面板 @wt-402",
		"</batch-context>",
	} {
		if !strings.Contains(header, want) {
			t.Fatalf("minimal header missing %q, got:\n%s", want, header)
		}
	}
	for _, absent := range []string{"conventions:", "acceptance:", "plan (delivery conventions pointer)"} {
		if strings.Contains(header, absent) {
			t.Fatalf("minimal header must not carry %q, got:\n%s", absent, header)
		}
	}
	// The plan path is the batch id fallback source, so the file name may
	// appear as the id — but never as a full conventions pointer at minimal.
	if strings.Contains(header, "plan:") {
		t.Fatalf("minimal header must not carry the plan pointer, got:\n%s", header)
	}
	_ = plan
}

func TestBatchContextFullAddsConventionsAcceptanceAndPointer(t *testing.T) {
	// 两档生效差异: full = minimal + done count + conventions + acceptance +
	// the plan path pointer (交付约定指针).
	c, _ := batchContextFixture(t, "full", batchManifestFixture)
	header := c.autopilotBatchContextHeader()
	for _, want := range []string{
		"batch: 400s-batch-2",
		"open threads: 394 注入头 @wt-394",
		"done: 2 entries already completed",
		"conventions: 交付回执写 docs/report/zcode交付/；commit 用「394：」前缀",
		"acceptance: 开关关态等价断言；大小有界断言",
		"plan (delivery conventions pointer): ",
	} {
		if !strings.Contains(header, want) {
			t.Fatalf("full header missing %q, got:\n%s", want, header)
		}
	}
}

func TestBatchContextHeaderIsBoundedOldestTruncated(t *testing.T) {
	// 大小有界: a pathological plan floods the open list — the header stays
	// under the cap and keeps the NEWEST entries (tail kept, oldest cut).
	var lines []string
	lines = append(lines, "<!-- reasonix-batch-manifest")
	lines = append(lines, "batch: flood")
	for i := 0; i < 400; i++ {
		lines = append(lines, "open: filler entry "+itoa(i)+" "+strings.Repeat("x", 60)+" @wt-flood")
	}
	lines = append(lines, "-->")
	c, _ := batchContextFixture(t, "minimal", strings.Join(lines, "\n"))
	header := c.autopilotBatchContextHeader()
	// Body is capped at batchContextCap; the wrapper adds a fixed overhead.
	if len(header) > batchContextCap+160 {
		t.Fatalf("header must stay bounded, got %d bytes (cap %d)", len(header), batchContextCap)
	}
	if !strings.Contains(header, "filler entry 399") {
		t.Fatal("the newest entry (399) must survive the truncation")
	}
	if strings.Contains(header, "filler entry 0 ") {
		t.Fatal("the oldest entry (0) must be truncated first")
	}
	if !strings.Contains(header, "older entries truncated") {
		t.Fatal("truncation must be marked")
	}
}

func TestBatchContextHeaderWithoutPlanStillInjects(t *testing.T) {
	// No plan configured: the header degrades to the in-process parts only —
	// no batch id, no threads, no crash.
	c, _ := batchContextFixture(t, "minimal", "")
	header := c.autopilotBatchContextHeader()
	if !strings.Contains(header, "<batch-context>") {
		t.Fatalf("dial on must still inject the (degraded) header, got: %q", header)
	}
	if strings.Contains(header, "batch:") || strings.Contains(header, "open threads:") {
		t.Fatalf("header without a plan must not invent batch state, got:\n%s", header)
	}
}

func TestBatchContextPlanUnreadableProceedsWithoutIt(t *testing.T) {
	// A configured-but-missing plan file says so at full level instead of
	// failing the round (same contract as the task-388 proxy manifest).
	c, plan := batchContextFixture(t, "full", "unused — the file below is removed")
	if err := os.Remove(plan); err != nil {
		t.Fatal(err)
	}
	header := c.autopilotBatchContextHeader()
	if !strings.Contains(header, "unreadable or without a reasonix-batch-manifest section") {
		t.Fatalf("missing plan must be declared, got:\n%s", header)
	}
}

func TestParseBatchManifestSection(t *testing.T) {
	m, ok := parseBatchManifest(batchManifestFixture)
	if !ok {
		t.Fatal("the fixture carries a closed manifest section")
	}
	if m.batch != "400s-batch-2" {
		t.Fatalf("batch id = %q", m.batch)
	}
	if len(m.open) != 2 || m.open[0] != "394 注入头 @wt-394" || m.open[1] != "401 卡片 @wt-401" {
		t.Fatalf("open entries = %v", m.open)
	}
	if len(m.inFlight) != 1 || m.inFlight[0] != "402 面板 @wt-402" {
		t.Fatalf("in-flight entries = %v", m.inFlight)
	}
	if len(m.done) != 2 {
		t.Fatalf("done entries = %v", m.done)
	}
	if m.conventions != "交付回执写 docs/report/zcode交付/；commit 用「394：」前缀" {
		t.Fatalf("conventions = %q", m.conventions)
	}
	if m.acceptance != "开关关态等价断言；大小有界断言" {
		t.Fatalf("acceptance = %q", m.acceptance)
	}
	// Prose outside the section must not leak into any field.
	if strings.Contains(m.conventions, "prose") || strings.Contains(m.batch, "Batch 7") {
		t.Fatal("prose outside the manifest section leaked into the parsed fields")
	}
}

func TestParseBatchManifestRequiresClosedSection(t *testing.T) {
	if _, ok := parseBatchManifest("no section at all"); ok {
		t.Fatal("no section must read as no manifest")
	}
	if _, ok := parseBatchManifest("<!-- reasonix-batch-manifest\nbatch: x\nnever closed"); ok {
		t.Fatal("an unclosed section must be rejected, not swallow the file")
	}
}

func TestParseBatchManifestCaseInsensitiveKeysAndJoin(t *testing.T) {
	content := "<!-- reasonix-batch-manifest\nBatch: b1\nOPEN: a @t1\nopen: b @t2\nIN-FLIGHT: c @t3\n# comment line\nunknown: ignored\n-->"
	m, ok := parseBatchManifest(content)
	if !ok {
		t.Fatal("section must parse")
	}
	if m.batch != "b1" {
		t.Fatalf("batch = %q", m.batch)
	}
	if len(m.open) != 2 || m.open[0] != "a @t1" || m.open[1] != "b @t2" {
		t.Fatalf("open = %v", m.open)
	}
	if len(m.inFlight) != 1 {
		t.Fatalf("in-flight = %v", m.inFlight)
	}
	if m.conventions != "" || m.acceptance != "" {
		t.Fatalf("unset fields must stay empty, got %q / %q", m.conventions, m.acceptance)
	}
}

func TestBatchChainTailNilSafe(t *testing.T) {
	var nilController *Controller
	if got := nilController.batchChainTail(); got != "" {
		t.Fatalf("nil controller chain tail = %q", got)
	}
	c := &Controller{}
	if got := c.batchChainTail(); got != "" {
		t.Fatalf("nil executor chain tail = %q", got)
	}
}

func TestBatchShortIDClipsLongChainIds(t *testing.T) {
	long := "0123456789abcdef"
	if got := batchShortID(long); got != "0123456789ab" {
		t.Fatalf("short id = %q", got)
	}
	if got := batchShortID("short"); got != "short" {
		t.Fatalf("short id = %q", got)
	}
}

func TestTruncateOldestFirstSharedRules(t *testing.T) {
	if got := truncateOldestFirst("small", 100); got != "small" {
		t.Fatalf("under-cap content must be unchanged, got %q", got)
	}
	lines := make([]string, 0, 300)
	for i := 0; i < 300; i++ {
		lines = append(lines, "line "+itoa(i)+" "+strings.Repeat("y", 40))
	}
	joined := strings.Join(lines, "\n")
	capped := truncateOldestFirst(joined, 2048)
	if len(capped) > 2048 {
		t.Fatalf("capped content exceeds the limit: %d", len(capped))
	}
	if !strings.Contains(capped, "line 299") || strings.Contains(capped, "line 0 ") {
		t.Fatal("cap must keep the newest tail and cut the oldest first")
	}
	// A single monster line is hard-clipped from the left, tail kept.
	monster := strings.Repeat("z", 5000)
	hard := truncateOldestFirst(monster, 1024)
	if len(hard) > 2048 || !strings.HasSuffix(hard, strings.Repeat("z", 500)) {
		t.Fatalf("monster line must be tail-clipped, got %d bytes", len(hard))
	}
}

func TestBatchHeaderCarriesChainTailFromLiveSession(t *testing.T) {
	// The chain tail rides the header as soon as the executor holds a
	// schema-2 session head — this is what pins round N+1 to round N. A
	// controller without an executor (or with a schema-1 session) must not
	// print a fake tail.
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.SetExperimentalAutopilotBatchContext("minimal"); err != nil {
		t.Fatal(err)
	}
	if err := cfg.SaveTo(filepath.Join(home, "config.toml")); err != nil {
		t.Fatal(err)
	}
	c := &Controller{autopilot: true}
	if header := c.autopilotBatchContextHeader(); strings.Contains(header, "prev-round chain tail") {
		t.Fatal("a session without a schema-2 head must not print a fake chain tail")
	}
}
