package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanMemoryRecallCountsAndPointOfUse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "run.trajectory.jsonl")
	lines := []string{
		`{"seq":1,"event":{"kind":"tool_result","tool":{"args":"{\"command\":\"make check-fast --tag=MEMKEY-EARLY\"}"}}}`,
		`{"seq":2,"memory_recall":{"hits":[{"id":"a"},{"id":"b"}],"used_chars":420}}`,
		`{"seq":3,"event":{"kind":"tool_result","tool":{"args":"{\"path\":\"answer.txt\",\"content\":\"make check-fast --tag=MEMKEY-USED\"}"}}}`,
		`{"seq":4,"memory_recall":{"suppressed":"generic user turn"}}`,
		`{"seq":5,"event":{"kind":"text","text":"done, MEMKEY-TEXT too"}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stats := scanMemoryRecall(path, []string{"MEMKEY-USED", "MEMKEY-TEXT", "MEMKEY-EARLY", "MEMKEY-NEVER"}, false)
	if stats.RecallEvents != 1 || stats.RecallHits != 2 || stats.RecallChars != 420 || stats.Suppressed != 1 {
		t.Fatalf("stats = %+v, want 1 event / 2 hits / 420 chars / 1 suppressed", stats)
	}
	// MEMKEY-EARLY appears only BEFORE the recall: not point-of-use evidence.
	if stats.MarkersUsed != 2 {
		t.Fatalf("markers used = %d, want 2 (post-recall args + answer text only)", stats.MarkersUsed)
	}
}

func TestMemoryUtilitySectionPairsArms(t *testing.T) {
	dir := t.TempDir()
	on := []result{
		{task: task{ID: "helped"}, Passed: true, MemoryRecallEvents: 1, MemoryRecallChars: 300},
		{task: task{ID: "hurt"}, Passed: false, MemoryRecallEvents: 1, MemoryRecallChars: 500},
		{task: task{ID: "same"}, Passed: true, MemoryRecallEvents: 1, MemoryRecallChars: 100},
	}
	off := []result{
		{task: task{ID: "helped"}, Passed: false},
		{task: task{ID: "hurt"}, Passed: true},
		{task: task{ID: "same"}, Passed: true},
	}
	onPath, offPath := filepath.Join(dir, "on.json"), filepath.Join(dir, "off.json")
	for path, rows := range map[string][]result{onPath: on, offPath: off} {
		data, _ := json.Marshal(rows)
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	section := memoryUtilitySection(offPath, onPath) // order must not matter
	for _, want := range []string{"Memory utility", "3 paired tasks", "helpful** 1", "harmful** 1", "helped", "hurt"} {
		if !strings.Contains(section, want) {
			t.Fatalf("section missing %q:\n%s", want, section)
		}
	}
}

func TestSeedTaskMemoryBuildsIsolatedStateRoot(t *testing.T) {
	taskDir := t.TempDir()
	work := t.TempDir()
	for _, seed := range []string{"project/fact.md", "global/pref.md"} {
		p := filepath.Join(taskDir, "memory", seed)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("---\nname: x\ndescription: y\n---\n\nbody\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	env, err := seedTaskMemory(taskDir, work)
	if err != nil || len(env) != 1 || !strings.HasPrefix(env[0], "REASONIX_STATE_HOME=") {
		t.Fatalf("env = %v err = %v", env, err)
	}
	stateHome := strings.TrimPrefix(env[0], "REASONIX_STATE_HOME=")
	if _, err := os.Stat(filepath.Join(stateHome, "memory", "global", "pref.md")); err != nil {
		t.Fatalf("global seed missing: %v", err)
	}
	matches, _ := filepath.Glob(filepath.Join(stateHome, "projects", "*", "memory", "fact.md"))
	if len(matches) != 1 {
		t.Fatalf("project seed not under the work dir's slug: %v", matches)
	}

	if env, err := seedTaskMemory(t.TempDir(), work); err != nil || env != nil {
		t.Fatalf("task without seeds must be a no-op, got %v %v", env, err)
	}
}

// TestTaskExperimentEnvMemoryOffCannotReachSeeds pins the #11247 → #11251
// counterfactual contract: the memory-off arm's state home must exist (so the
// child never falls back to the developer's real store) but must NOT contain
// the task's seeded memories, which the shell tool's passed-through env would
// otherwise let the agent read straight from disk. The memory-on arms keep the
// seeded layout unchanged.
func TestTaskExperimentEnvMemoryOffCannotReachSeeds(t *testing.T) {
	taskDir := t.TempDir()
	work := t.TempDir()
	seedRel := filepath.Join("memory", "project", "fact.md")
	seedPath := filepath.Join(taskDir, seedRel)
	if err := os.MkdirAll(filepath.Dir(seedPath), 0o755); err != nil {
		t.Fatal(err)
	}
	const seedBody = "MEMKEY-OFFARM secret fact"
	if err := os.WriteFile(seedPath, []byte(seedBody), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := suiteConfig{policy: "memory-off"}
	env, note := taskExperimentEnv(cfg, task{ID: "mb-x", dir: taskDir}, work)
	if note != "" {
		t.Fatalf("unexpected note: %s", note)
	}
	var noMemory, stateHome string
	for _, e := range env {
		if e == "REASONIX_EXPERIMENT_NO_MEMORY=1" {
			noMemory = e
		}
		if rest, ok := strings.CutPrefix(e, "REASONIX_STATE_HOME="); ok {
			stateHome = rest
		}
	}
	if noMemory == "" || stateHome == "" {
		t.Fatalf("memory-off env must carry the flag and an isolated state home, got %v", env)
	}
	// The seeded store must be unreachable: no seed file anywhere under the
	// state home this arm will see.
	err := filepath.Walk(stateHome, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		if strings.Contains(string(data), seedBody) {
			t.Fatalf("memory-off arm can reach the seeded store: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// A seedless task's off arm still gets an isolated home (never the
	// developer's real store).
	env, note = taskExperimentEnv(cfg, task{ID: "mb-x", dir: t.TempDir()}, work)
	if note != "" {
		t.Fatalf("unexpected note: %s", note)
	}
	found := false
	for _, e := range env {
		if rest, ok := strings.CutPrefix(e, "REASONIX_STATE_HOME="); ok && rest != "" {
			found = true
		}
	}
	if !found {
		t.Fatalf("seedless memory-off task must still isolate the state home, got %v", env)
	}

	// Non-off policies keep the seeded layout (behavior unchanged).
	for _, policy := range []string{"", "ebm", "governor"} {
		cfg := suiteConfig{policy: policy}
		env, note := taskExperimentEnv(cfg, task{ID: "mb-x", dir: taskDir}, work)
		if note != "" {
			t.Fatalf("policy %q note: %s", policy, note)
		}
		stateHome := ""
		for _, e := range env {
			if rest, ok := strings.CutPrefix(e, "REASONIX_STATE_HOME="); ok {
				stateHome = rest
			}
		}
		if stateHome == "" {
			t.Fatalf("policy %q lost the seeded state home", policy)
		}
		matches, _ := filepath.Glob(filepath.Join(stateHome, "projects", "*", "memory", "fact.md"))
		if len(matches) != 1 {
			t.Fatalf("policy %q: seeded project memory missing under %s", policy, stateHome)
		}
	}
}
