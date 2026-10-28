package boot

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"reasonix/internal/agent/testutil"
	"reasonix/internal/event"
	"reasonix/internal/plugin"
	"reasonix/internal/skill"
)

// TestRuntimeAssemblyReuseFeedsDiscovery is the task-363A behavior proof: a
// second build whose Options carry the first build's ReusedAssembly skips
// prompt reassembly — the sentinel prompt flows through untouched — and the
// stage-timings line reports the finer agent segments. Gate-off callers just
// leave ReuseAssembly nil and get the legacy path.
func TestRuntimeAssemblyReuseFeedsDiscovery(t *testing.T) {
	isolateConfigHome(t)
	dir := robustTempDir(t)
	t.Chdir(dir)

	var hs atomic.Int32
	srv := handshakeStub(t, "tele-a", 0, &hs)
	defer srv.Close()
	writeFile(t, dir, "reasonix.toml", mcpCapabilityTestProviderConfig+`
[[plugins]]
name = "tele-a"
type = "http"
url = "`+srv.URL+`"
auto_start = true
`)
	registerBootTokenProfileTestProvider()
	setBootTokenProfileTestProvider(t, testutil.NewMock("mcp-runtime-reuse", testutil.Turn{Text: "ok"}))

	var logBuf bytes.Buffer
	prevLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(prevLogger) })

	baseOpts := func() Options {
		return Options{Sink: event.Discard, MCPHostProfile: plugin.HostProfileInteractive}
	}

	res1, err := BuildRuntime(context.Background(), baseOpts())
	if err != nil {
		t.Fatalf("round1 BuildRuntime: %v", err)
	}
	if res1.Assembly == nil {
		t.Fatal("round1 returned nil Assembly; BuildWithAssembly would store nothing")
	}
	firstPrompt := ""
	if res1.Snapshot != nil {
		firstPrompt = res1.Snapshot.SystemPrompt()
	}
	if strings.TrimSpace(firstPrompt) == "" {
		t.Fatal("round1 produced an empty system prompt; nothing to prove reuse against")
	}

	// Second round: hand back the assembly with a sentinel prompt. If the
	// reuse path is live, the sentinel wins verbatim.
	assembly := res1.Assembly
	assembly.SystemPrompt = "SENTINEL-PROMPT-FROM-POOL"
	logBuf.Reset()
	opts2 := baseOpts()
	opts2.RuntimeReload = RuntimeReload{ReuseAssembly: assembly, PreviousPlan: FullReusePlan()}
	res2, err := BuildRuntime(context.Background(), opts2)
	if err != nil {
		t.Fatalf("round2 BuildRuntime: %v", err)
	}
	if res2.Snapshot == nil {
		t.Fatal("round2 returned nil Snapshot")
	}
	// 任务 1005 C4 后 sessionCollab 默认开：装配会在基础 prompt（＝复用来的
	// sentinel）之后追加「# 通讯录 / contact directory」等段，因此按前缀断言
	// —— 哨兵仍逐字作为基础段喂入，追加段不改变复用语义（旧默认全关时二者等长，
	// 断言退化为全等）。
	if got := res2.Snapshot.SystemPrompt(); !strings.HasPrefix(got, "SENTINEL-PROMPT-FROM-POOL") {
		t.Fatalf("reuse path did not feed the sentinel prompt through as the base prompt (len=%d first60=%q)", len(got), first60(got))
	}
	if logs := logBuf.String(); !strings.Contains(logs, "agent:tools=") {
		t.Fatalf("stage timings missing agent:tools segment, got:\n%s", logs)
	}
}

// slashToolDescription digs the slash_command tool description out of a build's
// registry — it names every available slash entry, skills included.
func slashToolDescription(t *testing.T, res *BuildResult) string {
	t.Helper()
	if res.Assembly == nil || res.Assembly.Registry == nil {
		t.Fatal("build returned no assembly registry; nothing to inspect")
	}
	tl, ok := res.Assembly.Registry.Get("slash_command")
	if !ok {
		t.Fatal("slash_command tool missing from registry")
	}
	return tl.Description()
}

// Task 919 gate-1 proof: the hot path (assembly reuse) must build the slash
// tool from the assembly's cached skill directory, not from a fresh disk scan.
// Round 1 discovers a project skill; the skill file is then deleted; round 2
// reuses the assembly and must STILL list it (cache hit). The control round
// without an assembly must NOT list it — proving the file deletion is
// detectable and round 2's listing came from the cache.
func TestRuntimeAssemblyReuseCarriesSlashSkills(t *testing.T) {
	isolateConfigHome(t)
	dir := robustTempDir(t)
	t.Chdir(dir)
	registerBootTokenProfileTestProvider()
	setBootTokenProfileTestProvider(t, testutil.NewMock("slash-reuse", testutil.Turn{Text: "ok"}))
	writeFile(t, dir, "reasonix.toml", mcpCapabilityTestProviderConfig)
	writeFile(t, dir, ".reasonix/skills/reusingskill.md",
		"---\ndescription: a skill discovered in round one\n---\nplaybook body")

	baseOpts := func() Options {
		return Options{Sink: event.Discard, MCPHostProfile: plugin.HostProfileInteractive}
	}
	res1, err := BuildRuntime(context.Background(), baseOpts())
	if err != nil {
		t.Fatalf("round1 BuildRuntime: %v", err)
	}
	if res1.Assembly == nil || res1.Assembly.SlashSkills == nil {
		t.Fatal("round1 assembly must carry SlashSkills (populated by the cold-path scan)")
	}
	if desc := slashToolDescription(t, res1); !strings.Contains(desc, "reusingskill") {
		t.Fatalf("round1 slash tool must list the project skill, got: %s", desc)
	}

	// Remove the skill from disk. A re-scan would no longer see it; the
	// cached directory must.
	if err := os.Remove(filepath.Join(dir, ".reasonix", "skills", "reusingskill.md")); err != nil {
		t.Fatalf("remove skill file: %v", err)
	}

	// Reuse round: the sentinel prompt already proved the reuse branch runs;
	// here the slash tool must serve the cached directory.
	assembly := res1.Assembly
	assembly.SystemPrompt = "SENTINEL-SLASH-REUSE"
	opts2 := baseOpts()
	opts2.RuntimeReload = RuntimeReload{ReuseAssembly: assembly, PreviousPlan: FullReusePlan()}
	res2, err := BuildRuntime(context.Background(), opts2)
	if err != nil {
		t.Fatalf("round2 BuildRuntime: %v", err)
	}
	// 任务 1005 C4 后 sessionCollab 默认开：装配会在基础 prompt（＝复用的
	// sentinel）之后追加通讯录段，故按前缀判定「走了复用分支」（同
	// TestRuntimeAssemblyReuseFeedsDiscovery 口径）。
	if res2.Snapshot == nil || !strings.HasPrefix(res2.Snapshot.SystemPrompt(), "SENTINEL-SLASH-REUSE") {
		t.Fatal("round2 did not take the reuse branch; the slash assertion below would prove nothing")
	}
	if desc := slashToolDescription(t, res2); !strings.Contains(desc, "reusingskill") {
		t.Fatalf("reuse-path slash tool lost the cached skill (re-scanned instead of reusing); got: %s", desc)
	}

	// Control: a cold build over the same directory must NOT list the deleted
	// skill — the file removal is real, so round 2's listing is a cache hit.
	res3, err := BuildRuntime(context.Background(), baseOpts())
	if err != nil {
		t.Fatalf("control BuildRuntime: %v", err)
	}
	if desc := slashToolDescription(t, res3); strings.Contains(desc, "reusingskill") {
		t.Fatalf("control cold build still lists a deleted skill; test setup broken: %s", desc)
	}
}

func first60(s string) string {
	if len(s) < 60 {
		return s
	}
	return s[:60]
}

// tl1224 禁用仍注入修复（回归钉）：the reuse branch used to build its live
// skill store with NO config filters, so a no-op/provider/MCP-only rebuild
// re-admitted config-disabled skills and excluded roots into the per-turn
// injection (skills catalog + capability directory read Controller.Skills /
// the store's List). Round 1 boots with both skills enabled; round 2 reuses
// the assembly AFTER the config gained disabled_skills + excluded_paths — the
// rebuilt controller's live skill view must honor both filters.
func TestRuntimeAssemblyReuseStoreAppliesDisabledAndExcluded(t *testing.T) {
	isolateConfigHome(t)
	dir := robustTempDir(t)
	t.Chdir(dir)
	registerBootTokenProfileTestProvider()
	setBootTokenProfileTestProvider(t, testutil.NewMock("reuse-filter", testutil.Turn{Text: "ok"}))
	writeFile(t, dir, "reasonix.toml", mcpCapabilityTestProviderConfig)
	writeFile(t, dir, ".reasonix/skills/stayingskill.md",
		"---\ndescription: stays enabled across the rebuild\n---\nplaybook body")
	writeFile(t, dir, ".reasonix/skills/toggledskill.md",
		"---\ndescription: disabled in config after round one\n---\nplaybook body")
	writeFile(t, dir, ".agents/skills/excludedskill.md",
		"---\ndescription: lives under an excluded root\n---\nplaybook body")

	baseOpts := func() Options {
		return Options{Sink: event.Discard, MCPHostProfile: plugin.HostProfileInteractive}
	}
	res1, err := BuildRuntime(context.Background(), baseOpts())
	if err != nil {
		t.Fatalf("round1 BuildRuntime: %v", err)
	}
	t.Cleanup(res1.Controller.Close)
	if names := skillNames(res1.Controller.Skills()); !names["toggledskill"] || !names["excludedskill"] || !names["stayingskill"] {
		t.Fatalf("round1 must discover all three skills, got %v", names)
	}

	// The config change a UI toggle (or a hand edit) persists between builds.
	writeFile(t, dir, "reasonix.toml", mcpCapabilityTestProviderConfig+`
[skills]
disabled_skills = ["toggledskill"]
excluded_paths = ["`+filepath.ToSlash(filepath.Join(dir, ".agents", "skills"))+`"]
`)

	assembly := res1.Assembly
	assembly.SystemPrompt = "SENTINEL-REUSE-FILTER"
	opts2 := baseOpts()
	opts2.RuntimeReload = RuntimeReload{ReuseAssembly: assembly, PreviousPlan: FullReusePlan()}
	res2, err := BuildRuntime(context.Background(), opts2)
	if err != nil {
		t.Fatalf("round2 BuildRuntime: %v", err)
	}
	t.Cleanup(res2.Controller.Close)
	if res2.Snapshot == nil || !strings.HasPrefix(res2.Snapshot.SystemPrompt(), "SENTINEL-REUSE-FILTER") {
		t.Fatal("round2 did not take the reuse branch; the filter assertions below would prove nothing")
	}
	names := skillNames(res2.Controller.Skills())
	if names["toggledskill"] {
		t.Fatalf("config-disabled skill re-entered the injection view after a reuse rebuild: %v", names)
	}
	if names["excludedskill"] {
		t.Fatalf("excluded-root skill re-entered the injection view after a reuse rebuild: %v", names)
	}
	if !names["stayingskill"] {
		t.Fatalf("enabled skill must survive the reuse rebuild: %v", names)
	}
	// Management surface keeps seeing the disabled skill (it lives on a
	// non-excluded root) so it can be re-enabled; the excluded root stays
	// hidden from every surface — that is what excluded_paths means.
	all := skillNames(res2.Controller.AllSkills())
	if !all["toggledskill"] {
		t.Fatalf("AllSkills must still list the disabled skill for management: %v", all)
	}
	if all["excludedskill"] {
		t.Fatalf("excluded root must stay hidden from the management surface too: %v", all)
	}
}

func skillNames(skills []skill.Skill) map[string]bool {
	out := map[string]bool{}
	for _, sk := range skills {
		out[sk.Name] = true
	}
	return out
}
