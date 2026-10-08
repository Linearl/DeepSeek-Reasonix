package skill

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/tool"
)

// Task 660 parity guard: CapabilityArgumentsIndex must return exactly the same
// contracts as the per-id CapabilityArguments lookups, because the search
// scorer swapped the latter for the former. Covers plain and run-as=subagent
// skills, and the miss case (index only contains discoverable skills).
func TestCapabilityArgumentsIndexMatchesPerID(t *testing.T) {
	root := t.TempDir()
	writeSkill := func(name, frontmatter string) {
		t.Helper()
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		content := "---\nname: " + name + "\ndescription: parity skill " + name + "\n" + frontmatter + "\n---\n\nbody\n"
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	writeSkill("plain", "")
	writeSkill("subagentish", "runas: subagent")

	store := New(Options{
		HomeDir:         root,
		ReasonixHomeDir: filepath.Join(root, "reasonix-home"),
		ProjectRoot:     filepath.Join(root, "project"),
		CustomPaths:     []string{root},
		DisableBuiltins: true,
	})
	runSkill := NewRunSkillTool(store, nil)
	readOnly := NewReadOnlySkillTool(store, nil)

	for _, tc := range []struct {
		name  string
		tool  tool.Tool
		flvor string
	}{{"run_skill", runSkill, "run"}, {"read_only_skill", readOnly, "readonly"}} {
		indexer, ok := tc.tool.(tool.CapabilityArgumentIndexer)
		if !ok {
			t.Fatalf("%s does not implement CapabilityArgumentIndexer", tc.name)
		}
		provider, ok := tc.tool.(tool.CapabilityArgumentProvider)
		if !ok {
			t.Fatalf("%s does not implement CapabilityArgumentProvider", tc.name)
		}
		index := indexer.CapabilityArgumentsIndex()
		listed := store.List()
		if len(index) != len(listed) {
			t.Fatalf("%s index size %d != discovered %d", tc.name, len(index), len(listed))
		}
		for _, sk := range listed {
			perID, ok := provider.CapabilityArguments("skill:" + sk.Name)
			if !ok {
				t.Fatalf("%s: per-id lookup missed %q", tc.name, sk.Name)
			}
			batched, ok := index[sk.Name]
			if !ok {
				t.Fatalf("%s: index missed %q", tc.name, sk.Name)
			}
			if string(perID.Schema) != string(batched.Schema) {
				t.Fatalf("%s: schema drift for %q\nper-id:  %s\nbatched: %s", tc.name, sk.Name, perID.Schema, batched.Schema)
			}
			if string(perID.Example) != string(batched.Example) {
				t.Fatalf("%s: example drift for %q", tc.name, sk.Name)
			}
		}
		// The run-as distinction must survive both paths: a subagent skill
		// requires "arguments" in the schema.
		for _, sk := range listed {
			var raw map[string]any
			if err := json.Unmarshal(index[sk.Name].Schema, &raw); err != nil {
				t.Fatalf("schema decode: %v", err)
			}
			_, hasRequired := raw["required"]
			wantRequired := sk.RunAs == RunSubagent
			if hasRequired != wantRequired {
				t.Fatalf("%s: skill %q runAs=%q required=%v want %v", tc.name, sk.Name, sk.RunAs, hasRequired, wantRequired)
			}
		}
		// Miss case mirrors CapabilityArguments: unknown names are absent.
		if _, ok := index["no-such-skill"]; ok {
			t.Fatalf("%s: index contains unknown skill", tc.name)
		}
	}
}
