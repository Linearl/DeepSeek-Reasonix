package config

import (
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// Fork rule 2 (铁律 2): the switch ships default-off — the zero config is off.
// Off = bash never takes a workspace hold under optimistic_write, byte-for-byte
// the pre-task-575 behavior.
func TestExperimentalBashHeavyGuardDefaultsOff(t *testing.T) {
	var zero struct {
		ExperimentalBashHeavyGuard bool `toml:"experimental_bash_heavy_guard"`
	}
	if zero.ExperimentalBashHeavyGuard {
		t.Fatal("experimental_bash_heavy_guard must default to false (fork rule 2)")
	}
	if Default().Sandbox.ExperimentalBashHeavyGuard {
		t.Fatal("Default() must not enable experimental_bash_heavy_guard")
	}
}

// Task 575: enabling must reach the config file (81/123 lost-save rule) and
// round-trip through RenderTOML → decode; the untouched default renders only
// the commented hint.
func TestExperimentalBashHeavyGuardRoundTripThroughRender(t *testing.T) {
	off := RenderTOMLForScope(&Config{}, RenderScopeUser)
	if rendersKeyLine(off, "experimental_bash_heavy_guard") {
		t.Fatalf("default config must not emit the key itself, only the hint\n---\n%s", off)
	}
	if !strings.Contains(off, "# experimental_bash_heavy_guard = false") {
		t.Fatalf("default config must document the switch as a commented hint\n---\n%s", off)
	}

	on := &Config{}
	on.Sandbox.OptimisticWrite = true
	on.Sandbox.ExperimentalBashHeavyGuard = true
	rendered := RenderTOMLForScope(on, RenderScopeUser)
	if !rendersKeyLine(rendered, "experimental_bash_heavy_guard") {
		t.Fatalf("render table lost experimental_bash_heavy_guard — a settings save would silently drop it (81/123 lesson)\n---\n%s", rendered)
	}
	if !rendersKeyLine(rendered, "optimistic_write") {
		t.Fatalf("the guard is only meaningful with optimistic_write; the render must keep it\n---\n%s", rendered)
	}

	var got Config
	if _, err := toml.Decode(rendered, &got); err != nil {
		t.Fatalf("rendered TOML does not parse: %v\n---\n%s", err, rendered)
	}
	if !got.Sandbox.ExperimentalBashHeavyGuard {
		t.Error("sandbox.experimental_bash_heavy_guard did not round-trip")
	}
	if !got.Sandbox.OptimisticWrite {
		t.Error("sandbox.optimistic_write did not round-trip")
	}
}

// rendersKeyLine reports whether the render table physically emits key = …
// (comment lines skipped — the 任务562 lab-gate extraction uses the same rule,
// so the hint comment never counts as an emitted key).
func rendersKeyLine(rendered, key string) bool {
	for _, line := range strings.Split(rendered, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, key+" =") {
			return true
		}
	}
	return false
}
