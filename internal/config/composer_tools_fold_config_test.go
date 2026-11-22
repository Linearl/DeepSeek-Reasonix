package config

import (
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// TestComposerToolsFoldConfigSetter pins the tl1718 工具组默认折叠 lab
// switch: a plain Desktop-section bool, off by default (铁律 2 — off = the
// width-driven fold byte-for-byte unchanged), toggled by
// SetExperimentalComposerToolsFold.
func TestComposerToolsFoldConfigSetter(t *testing.T) {
	c := Default()
	if c.Desktop.ExperimentalComposerToolsFold {
		t.Fatal("experimental_composer_tools_fold must default off (铁律 2)")
	}
	if err := c.SetExperimentalComposerToolsFold(true); err != nil {
		t.Fatalf("SetExperimentalComposerToolsFold: %v", err)
	}
	if !c.Desktop.ExperimentalComposerToolsFold {
		t.Fatal("setter did not persist the field")
	}
	if err := c.SetExperimentalComposerToolsFold(false); err != nil {
		t.Fatalf("SetExperimentalComposerToolsFold(false): %v", err)
	}
	if c.Desktop.ExperimentalComposerToolsFold {
		t.Fatal("off toggle did not persist")
	}
}

// TestComposerToolsFoldRenderIncludesKey pins the 81/123 lost-save rule for
// the new key: RenderTOML must emit it in BOTH states, and decode must
// restore it.
func TestComposerToolsFoldRenderIncludesKey(t *testing.T) {
	on := Default()
	on.Desktop.ExperimentalComposerToolsFold = true
	rendered := RenderTOML(on)
	if !strings.Contains(rendered, "experimental_composer_tools_fold = true") {
		t.Fatalf("render must carry the on state:\n%s", rendered)
	}
	off := Default()
	if out := RenderTOML(off); !strings.Contains(out, "experimental_composer_tools_fold = false") {
		t.Fatalf("off state must render explicitly (omit-on-off 丢档教训):\n%s", out)
	}
	var got Config
	if _, err := toml.Decode(rendered, &got); err != nil {
		t.Fatalf("rendered TOML does not parse: %v", err)
	}
	if !got.Desktop.ExperimentalComposerToolsFold {
		t.Fatal("experimental_composer_tools_fold did not round-trip")
	}
}

// TestComposerToolsFoldLabRegistry pins the tl1718 lab registry wiring: the
// render-table row carries the recommended tier and the key routes to the
// Desktop section (not Agent — the composer toolbar is a pure frontend
// surface; putting it in [agent] would leak a render-side gate into the agent
// config domain, 940/959 先例).
func TestComposerToolsFoldLabRegistry(t *testing.T) {
	found := false
	for _, f := range labFeatureTiers {
		if f.id == "composerToolsFold" {
			found = true
			if f.tier != LabTierRecommended {
				t.Fatalf("composerToolsFold must be the recommended tier (任务书: 徽章级别=推荐); got %q", f.tier)
			}
			want := false
			for _, k := range f.renderKeys {
				if k == "experimental_composer_tools_fold" {
					want = true
				}
			}
			if !want {
				t.Fatalf("composerToolsFold renderKeys must carry experimental_composer_tools_fold; got %v", f.renderKeys)
			}
		}
	}
	if !found {
		t.Fatal("composerToolsFold missing from labFeatureTiers (render.go)")
	}
	if _, ok := labBulkSetters["composerToolsFold"]; !ok {
		t.Fatal("composerToolsFold missing from labBulkSetters (批量面入表)")
	}
}
