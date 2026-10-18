package config

import (
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// TestCapabilityGroupsConfigSetter pins the task-979 lab switch setter for the
// 953/954/955 shared key: the field is a plain bool toggled by
// SetExperimentalCapabilityGroups, default off (铁律 2).
func TestCapabilityGroupsConfigSetter(t *testing.T) {
	c := Default()
	if c.Agent.ExperimentalCapabilityGroups {
		t.Fatal("experimental_capability_groups must default off (铁律 2: off = zero behaviour)")
	}
	if err := c.SetExperimentalCapabilityGroups(true); err != nil {
		t.Fatalf("SetExperimentalCapabilityGroups: %v", err)
	}
	if !c.Agent.ExperimentalCapabilityGroups {
		t.Fatal("setter did not persist the field")
	}
	if err := c.SetExperimentalCapabilityGroups(false); err != nil {
		t.Fatalf("SetExperimentalCapabilityGroups(false): %v", err)
	}
	if c.Agent.ExperimentalCapabilityGroups {
		t.Fatal("off toggle did not persist")
	}
}

// TestCapabilityGroupsRenderIncludesKey pins the 81/123 lost-save rule for the
// key (it already rendered explicitly since 953; 979 keeps that contract under
// the new 表A home): RenderTOML must emit it in BOTH states, and the on state
// must round-trip through a decode.
func TestCapabilityGroupsRenderIncludesKey(t *testing.T) {
	off := Default()
	if out := RenderTOML(off); !strings.Contains(out, "experimental_capability_groups = false") {
		t.Fatalf("off state must render explicitly (omit-on-off 丢档教训):\n%s", out)
	}
	on := Default()
	on.Agent.ExperimentalCapabilityGroups = true
	renderedOn := RenderTOML(on)
	if !strings.Contains(renderedOn, "experimental_capability_groups = true") {
		t.Fatalf("render must carry the on state:\n%s", renderedOn)
	}
	var got Config
	if _, err := toml.Decode(renderedOn, &got); err != nil {
		t.Fatalf("rendered TOML does not parse: %v", err)
	}
	if !got.Agent.ExperimentalCapabilityGroups {
		t.Fatal("experimental_capability_groups did not round-trip")
	}
}

// TestCapabilityGroupsRegisteredInLabTiers pins the task-979 表A move: the key
// is a real lab feature entry (tool-opt group member) and NO longer carries a
// labNonFeatureKeys exemption — the render-table→lab-entry delivery check
// (task 815④) demands an entry once the key renders.
func TestCapabilityGroupsRegisteredInLabTiers(t *testing.T) {
	var entry *labFeatureTier
	for i := range labFeatureTiers {
		if labFeatureTiers[i].id == "capabilityGroups" {
			entry = &labFeatureTiers[i]
			break
		}
	}
	if entry == nil {
		t.Fatal("labFeatureTiers lost the task-979 capabilityGroups entry")
	}
	if entry.tier != LabTierOptional {
		t.Errorf("capabilityGroups tier = %q, 用户拍板提效类可选档 says optional", entry.tier)
	}
	if len(entry.renderKeys) != 1 || entry.renderKeys[0] != "experimental_capability_groups" {
		t.Errorf("capabilityGroups renderKeys = %v, want [experimental_capability_groups]", entry.renderKeys)
	}
	if _, exempt := labNonFeatureKeys["experimental_capability_groups"]; exempt {
		t.Error("experimental_capability_groups must drop its labNonFeatureKeys exemption once registered as a lab feature (cannot be both)")
	}
}
