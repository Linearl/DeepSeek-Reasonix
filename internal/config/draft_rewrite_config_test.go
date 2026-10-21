package config

import (
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// TestDraftRewriteConfigSetter pins the 任务 1024 草稿优化 lab switch: a plain
// bool, off by default (铁律 2 — off = no button, composer layout unchanged),
// toggled by SetExperimentalDraftRewrite.
func TestDraftRewriteConfigSetter(t *testing.T) {
	c := Default()
	if c.Agent.ExperimentalDraftRewrite {
		t.Fatal("experimental_draft_rewrite must default off (铁律 2)")
	}
	if err := c.SetExperimentalDraftRewrite(true); err != nil {
		t.Fatalf("SetExperimentalDraftRewrite: %v", err)
	}
	if !c.Agent.ExperimentalDraftRewrite {
		t.Fatal("setter did not persist the field")
	}
	if err := c.SetExperimentalDraftRewrite(false); err != nil {
		t.Fatalf("SetExperimentalDraftRewrite(false): %v", err)
	}
	if c.Agent.ExperimentalDraftRewrite {
		t.Fatal("off toggle did not persist")
	}
}

// TestDraftRewriteRenderIncludesKey pins the 81/123 lost-save rule for the new
// key: RenderTOML must emit it in BOTH states, and decode must restore it.
func TestDraftRewriteRenderIncludesKey(t *testing.T) {
	on := Default()
	on.Agent.ExperimentalDraftRewrite = true
	rendered := RenderTOML(on)
	if !strings.Contains(rendered, "experimental_draft_rewrite = true") {
		t.Fatalf("render must carry the on state:\n%s", rendered)
	}
	off := Default()
	if out := RenderTOML(off); !strings.Contains(out, "experimental_draft_rewrite = false") {
		t.Fatalf("off state must render explicitly (omit-on-off 丢档教训):\n%s", out)
	}
	var got Config
	if _, err := toml.Decode(rendered, &got); err != nil {
		t.Fatalf("rendered TOML does not parse: %v", err)
	}
	if !got.Agent.ExperimentalDraftRewrite {
		t.Fatal("experimental_draft_rewrite did not round-trip")
	}
}
