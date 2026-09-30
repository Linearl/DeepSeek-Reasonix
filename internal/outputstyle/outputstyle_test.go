package outputstyle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	fileencoding "reasonix/internal/fileutil/encoding"
)

func TestResolveBuiltin(t *testing.T) {
	st, ok := Resolve("explanatory", nil)
	if !ok {
		t.Fatal("explanatory built-in should resolve")
	}
	if !st.Builtin || !st.KeepCoding || strings.TrimSpace(st.Body) == "" {
		t.Errorf("unexpected built-in shape: %+v", st)
	}
	// Case-insensitive.
	if _, ok := Resolve("LEARNING", nil); !ok {
		t.Error("resolve should be case-insensitive")
	}
}

func TestResolveDefaultIsNone(t *testing.T) {
	for _, name := range []string{"", "  ", "default"} {
		if _, ok := Resolve(name, nil); ok {
			t.Errorf("Resolve(%q) should be no-style", name)
		}
	}
}

func TestApply(t *testing.T) {
	append1 := Apply("BASE", OutputStyle{Body: "X", KeepCoding: true})
	if append1 != "BASE\n\nX" {
		t.Errorf("keep-coding append = %q", append1)
	}
	replace := Apply("BASE", OutputStyle{Body: "X", KeepCoding: false})
	if replace != "X" {
		t.Errorf("replace = %q, want X", replace)
	}
	if got := Apply("BASE", OutputStyle{Body: "   "}); got != "BASE" {
		t.Errorf("empty body should leave base untouched, got %q", got)
	}
}

func TestListIncludesBuiltinsSorted(t *testing.T) {
	got := List(nil)
	if len(got) < 3 {
		t.Fatalf("expected at least the 3 built-ins, got %d", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].Name > got[i].Name {
			t.Errorf("not sorted by name: %q before %q", got[i-1].Name, got[i].Name)
		}
	}
}

// Task 385d: the built-in set reaches five, matching the CC lineup
// (Default / Proactive / Explanatory / Learning / Concise). List sorts by
// name, so the expectation doubles as an ordering pin.
func TestBuiltinStyleSetIsFive(t *testing.T) {
	got := List(nil)
	want := []string{"concise", "default", "explanatory", "learning", "proactive"}
	if len(got) != len(want) {
		t.Fatalf("want %d built-in styles, got %d: %+v", len(want), len(got), got)
	}
	for i, name := range want {
		if got[i].Name != name {
			t.Errorf("style %d = %q, want %q (sorted by name)", i, got[i].Name, name)
		}
		if !got[i].Builtin {
			t.Errorf("%q must be marked builtin", name)
		}
		if strings.TrimSpace(got[i].Description) == "" {
			t.Errorf("%q must carry a selector-visible description", name)
		}
	}
}

// Task 385d: Proactive resolves case-insensitively and folds in append-style
// like the other coding-preserving built-ins.
func TestProactiveResolvesAndApplies(t *testing.T) {
	st, ok := Resolve("proactive", nil)
	if !ok {
		t.Fatal("proactive built-in should resolve")
	}
	if !st.Builtin || !st.KeepCoding {
		t.Errorf("proactive must be builtin + keep-coding (append): %+v", st)
	}
	if strings.TrimSpace(st.Body) == "" {
		t.Fatal("proactive must carry a body")
	}
	for _, want := range []string{"Proactive", "assumption", "forward progress"} {
		if !strings.Contains(st.Body, want) {
			t.Errorf("proactive body missing %q: %s", want, st.Body)
		}
	}
	if got := Apply("BASE", st); got != "BASE\n\n"+st.Body {
		t.Errorf("proactive must append, got %q", got)
	}
	if _, ok := Resolve("PROACTIVE", nil); !ok {
		t.Error("resolve must stay case-insensitive with the fifth style added")
	}
}

// Task 385d: Default is list-only — the no-injection path. Resolve refuses
// ""/"default" before consulting the slice, and boot guards the injection on
// `ok`, so Apply never receives it; the empty Body keeps Apply a no-op even
// if a caller applies the listed entry directly.
func TestDefaultStyleListedButNeverInjects(t *testing.T) {
	var listed *OutputStyle
	for _, st := range List(nil) {
		if st.Name == "default" {
			listed = &st
			break
		}
	}
	if listed == nil {
		t.Fatal("default must appear in the list (CC parity)")
	}
	if !listed.Builtin || listed.Body != "" {
		t.Errorf("default must be builtin with an empty body: %+v", listed)
	}
	for _, name := range []string{"", "default", "DEFAULT", "  default  "} {
		if _, ok := Resolve(name, nil); ok {
			t.Errorf("Resolve(%q) must report no-style so boot skips injection", name)
		}
	}
	if got := Apply("BASE", *listed); got != "BASE" {
		t.Errorf("applying the default entry must leave the prompt unchanged, got %q", got)
	}
}

// Task 385d: custom dirs keep working with five built-ins present — a custom
// file adds a sixth style, resolves, and the built-ins stay resolvable next
// to it (the override path itself is pinned by
// TestCustomFileOverridesBuiltinAndParses).
func TestCustomDirsStillWorkWithFiveBuiltins(t *testing.T) {
	dir := t.TempDir()
	md := "---\ndescription: persona from a custom dir\n---\nCustom body.\n"
	if err := os.WriteFile(filepath.Join(dir, "persona.md"), []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}

	got := List([]string{dir})
	if len(got) != 6 {
		t.Fatalf("want 5 built-ins + 1 custom = 6, got %d: %+v", len(got), got)
	}
	st, ok := Resolve("persona", []string{dir})
	if !ok || st.Builtin || st.Description != "persona from a custom dir" {
		t.Errorf("custom style must resolve from its dir: %+v ok=%v", st, ok)
	}
	for _, name := range []string{"proactive", "explanatory", "learning", "concise"} {
		if _, ok := Resolve(name, []string{dir}); !ok {
			t.Errorf("built-in %q must still resolve with a custom dir present", name)
		}
	}
}

func TestCustomFileOverridesBuiltinAndParses(t *testing.T) {
	dir := t.TempDir()
	// Override the built-in "explanatory" with a custom replace-style file.
	md := "---\ndescription: my persona\nkeep-coding-instructions: false\n---\nYou are a pirate. Answer in pirate speak.\n"
	if err := os.WriteFile(filepath.Join(dir, "explanatory.md"), []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}

	st, ok := Resolve("explanatory", []string{dir})
	if !ok {
		t.Fatal("custom explanatory should resolve")
	}
	if st.Builtin {
		t.Error("custom file should override the built-in (Builtin=false)")
	}
	if st.KeepCoding {
		t.Error("keep-coding-instructions: false should disable KeepCoding")
	}
	if st.Description != "my persona" || !strings.Contains(st.Body, "pirate") {
		t.Errorf("frontmatter/body not parsed: %+v", st)
	}
	if got := Apply("CODING PROMPT", st); got != st.Body {
		t.Errorf("a replace-style should drop the base prompt, got %q", got)
	}
}

func TestResolveDecodesGB18030CustomFile(t *testing.T) {
	dir := t.TempDir()
	body := "---\nname: concise-cn\ndescription: 中文风格\n---\n请用中文简洁回答。"
	if err := os.WriteFile(filepath.Join(dir, "concise-cn.md"), fileencoding.Encode(body, fileencoding.GB18030), 0o644); err != nil {
		t.Fatal(err)
	}

	st, ok := Resolve("concise-cn", []string{dir})
	if !ok {
		t.Fatal("custom style should resolve")
	}
	if st.Description != "中文风格" || st.Body != "请用中文简洁回答。" {
		t.Fatalf("decoded style = %+v", st)
	}
}

func TestParseFileNameFromFilename(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "snappy.md"), []byte("Be snappy."), 0o644); err != nil {
		t.Fatal(err)
	}
	st, ok := Resolve("snappy", []string{dir})
	if !ok || st.Name != "snappy" || !st.KeepCoding { // default keep-coding when unspecified
		t.Errorf("filename-derived style wrong: %+v ok=%v", st, ok)
	}
}

// Task 385a: both keep-coding-instructions states must load — absent means
// append (the safe default), false means replace. This is the acceptance case
// for custom .md files with and without the flag.
func TestListReportBothKeepCodingStatesLoad(t *testing.T) {
	dir := t.TempDir()
	withFlag := "---\ndescription: pure persona\nkeep-coding-instructions: false\n---\nAnswer as a pirate.\n"
	if err := os.WriteFile(filepath.Join(dir, "pirate.md"), []byte(withFlag), 0o644); err != nil {
		t.Fatal(err)
	}
	withoutFlag := "---\ndescription: keeps the coding prompt\n---\nBe brief.\n"
	if err := os.WriteFile(filepath.Join(dir, "brief.md"), []byte(withoutFlag), 0o644); err != nil {
		t.Fatal(err)
	}

	styles, issues := ListReport([]string{dir})
	if len(issues) != 0 {
		t.Fatalf("two valid files must load without issues, got %+v", issues)
	}
	byName := map[string]OutputStyle{}
	for _, st := range styles {
		byName[st.Name] = st
	}
	pirate, ok := byName["pirate"]
	if !ok || pirate.KeepCoding {
		t.Errorf("keep-coding-instructions: false must load with KeepCoding=false: %+v ok=%v", pirate, ok)
	}
	brief, ok := byName["brief"]
	if !ok || !brief.KeepCoding {
		t.Errorf("an absent keep-coding-instructions must default to KeepCoding=true: %+v ok=%v", brief, ok)
	}
	if pirate.Builtin || brief.Builtin {
		t.Error("custom files must be marked non-builtin")
	}
}

// Task 385a acceptance: a broken frontmatter is a visible error, not a silent
// no-op. List still skips such files (the boot path must not fail), and
// ListReport reports each one with its path and reason.
func TestListReportSurfacesUnloadableStyleFiles(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("good.md", "---\ndescription: fine\n---\nLoad me.\n")
	write("broken-yaml.md", "---\ndescription: \"unclosed quote\n---\nBody.\n")
	write("unclosed-fence.md", "---\nname: ghost\ndescription: never closed\nBody that swallows the fence.\n")
	write("empty-body.md", "---\nname: hollow\n---\n\n")

	styles, issues := ListReport([]string{dir})
	byName := map[string]OutputStyle{}
	for _, st := range styles {
		byName[st.Name] = st
	}
	if _, ok := byName["good"]; !ok {
		t.Fatalf("the valid file must still load: %+v", styles)
	}
	for _, name := range []string{"broken-yaml", "unclosed-fence", "hollow"} {
		if _, ok := byName[name]; ok {
			t.Errorf("unloadable file %q must not appear as a style", name)
		}
	}

	if len(issues) != 3 {
		t.Fatalf("want 3 issues, got %d: %+v", len(issues), issues)
	}
	reasonByName := map[string]string{}
	for _, is := range issues {
		if is.Path == "" || is.Reason == "" {
			t.Errorf("issue must carry path and reason: %+v", is)
		}
		reasonByName[is.Name] = is.Reason
	}
	// Issue.Name is the filename stem — what the file would have been called.
	for name, fragment := range map[string]string{
		"broken-yaml":    "invalid frontmatter",
		"unclosed-fence": "never closed",
		"empty-body":     "empty body",
	} {
		got, ok := reasonByName[name]
		if !ok {
			t.Errorf("missing issue for %q: %+v", name, reasonByName)
			continue
		}
		if !strings.Contains(got, fragment) {
			t.Errorf("issue for %q must say %q, got %q", name, fragment, got)
		}
	}

	// List keeps its silent-skip contract: the built-ins plus the one valid
	// file, and none of the broken ones.
	names := map[string]bool{}
	for _, st := range List([]string{dir}) {
		names[st.Name] = true
	}
	if !names["good"] {
		t.Errorf("the valid file must load")
	}
	for _, name := range []string{"broken-yaml", "unclosed-fence", "empty-body", "hollow"} {
		if names[name] {
			t.Errorf("List must skip unloadable file %q", name)
		}
	}
}
