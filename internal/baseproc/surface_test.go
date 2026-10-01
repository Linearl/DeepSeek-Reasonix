package baseproc

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"reasonix/internal/tool"
)

// stubTool is a minimal plain Tool for surface tests (name/description/
// schema/read-only plus a scripted Execute).
type stubTool struct {
	name     string
	desc     string
	schema   json.RawMessage
	readOnly bool
	exec     func(ctx context.Context, args json.RawMessage) (string, error)
}

func (t *stubTool) Name() string { return t.name }
func (t *stubTool) Description() string {
	if t.desc == "" {
		return "stub " + t.name
	}
	return t.desc
}
func (t *stubTool) Schema() json.RawMessage {
	if t.schema == nil {
		return json.RawMessage(`{"type":"object"}`)
	}
	return t.schema
}
func (t *stubTool) ReadOnly() bool { return t.readOnly }
func (t *stubTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	if t.exec != nil {
		return t.exec(ctx, args)
	}
	return "ok:" + t.name, nil
}

// newStubRegistry builds a registry holding the given stub tools.
func newStubRegistry(tools ...*stubTool) *tool.Registry {
	reg := tool.NewRegistry()
	for _, t := range tools {
		reg.Add(t)
	}
	return reg
}

func TestRegistrySurfaceCatalogScopes(t *testing.T) {
	// S1b scope vocabulary mirrors the in-process visibility filters:
	// provider = Registry.ContractEntries, all = AllContractEntries.
	reg := newStubRegistry(
		&stubTool{name: "alpha", desc: "first", readOnly: true},
		&stubTool{name: "beta", desc: "second"},
	)
	reg.SetProviderVisibleTools([]string{"alpha"})
	s := &RegistrySurface{Reg: reg}
	ctx := context.Background()

	all, err := s.Catalog(ctx, ScopeAll)
	if err != nil {
		t.Fatalf("catalog all: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("scope all returned %d tools, want 2", len(all))
	}
	provider, err := s.Catalog(ctx, ScopeProvider)
	if err != nil {
		t.Fatalf("catalog provider: %v", err)
	}
	if len(provider) != 1 || provider[0].Name != "alpha" {
		t.Fatalf("scope provider = %+v, want only alpha", provider)
	}
	if !provider[0].ReadOnly || provider[0].Description != "first" {
		t.Fatalf("descriptor lost fields: %+v", provider[0])
	}
	// ContractEntry → ToolDescriptor is a lossless field mapping (design R2:
	// both sides share one description shape).
	wantSchema := reg.AllContractEntries()
	gotNames := map[string]ToolDescriptor{}
	for _, d := range all {
		gotNames[d.Name] = d
	}
	for _, e := range wantSchema {
		d, ok := gotNames[e.Name]
		if !ok {
			t.Fatalf("catalog missing entry %q", e.Name)
		}
		if d.Description != e.Description || d.ReadOnly != e.ReadOnly || !reflect.DeepEqual([]byte(d.Schema), []byte(e.Schema)) {
			t.Fatalf("descriptor for %q = %+v, want contract entry %+v", e.Name, d, e)
		}
	}
}

func TestRegistrySurfaceCatalogRejectsUnknownScope(t *testing.T) {
	// A typo must not silently change visibility: unknown scope →
	// ErrUnknownScope (the serve handler maps it to -32602).
	s := &RegistrySurface{Reg: newStubRegistry(&stubTool{name: "alpha"})}
	if _, err := s.Catalog(context.Background(), "everything"); !errors.Is(err, ErrUnknownScope) {
		t.Fatalf("catalog scope err = %v, want ErrUnknownScope", err)
	}
	if _, err := s.Catalog(context.Background(), ""); !errors.Is(err, ErrUnknownScope) {
		t.Fatalf("empty scope err = %v, want ErrUnknownScope (callers name a scope)", err)
	}
}

func TestRegistrySurfaceWithoutRegistryFails(t *testing.T) {
	var nilSurface *RegistrySurface
	if _, err := nilSurface.Catalog(context.Background(), ScopeAll); err == nil {
		t.Fatal("nil surface catalog succeeded, want error")
	}
	if _, err := nilSurface.Execute(context.Background(), ToolCallParams{CallID: "c", Tool: "x"}, nil); err == nil {
		t.Fatal("nil surface execute succeeded, want error")
	}
	empty := &RegistrySurface{}
	if _, err := empty.Catalog(context.Background(), ScopeAll); err == nil {
		t.Fatal("registry-less surface catalog succeeded, want error")
	}
}
