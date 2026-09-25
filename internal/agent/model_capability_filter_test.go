package agent

import (
	"context"
	"strings"
	"testing"
)

// Task 244 B9 (experimental_model_capability_filter): an image-bearing
// dispatch to an explicit per-task model without vision must become an
// explained rejection when the switch is on, and stay the historical silent
// path when it is off. The check is a pure seam; the two sub-session runners
// call it right before the images attach.

func b9Tool(enabled bool, vision map[string]bool, known map[string]bool) *TaskTool {
	t := NewTaskToolWithOptions(TaskToolOptions{})
	if enabled {
		t.modelCapabilityFilter = func() bool { return true }
	}
	t.visionForModel = func(ref string) (bool, bool) {
		v, vis := vision[ref]
		if !known[ref] {
			return false, false
		}
		return v && vis, true
	}
	return t
}

func b9Ctx(images int) context.Context {
	if images == 0 {
		return context.Background()
	}
	list := make([]string, 0, images)
	for i := 0; i < images; i++ {
		list = append(list, "data:image/png;base64,AAAA")
	}
	return WithSubagentImageCandidates(context.Background(), list)
}

func TestModelCapabilityFilterOffKeepsSilentPath(t *testing.T) {
	// Off = nil probe or disabled switch: the historical behaviour (silent
	// image drop on a text-only model) must stay byte-identical.
	for name, tool := range map[string]*TaskTool{
		"nil probe":  b9Tool(false, nil, nil),
		"probe off":  {modelCapabilityFilter: func() bool { return false }, visionForModel: func(string) (bool, bool) { return false, true }},
		"nil vision": {modelCapabilityFilter: func() bool { return true }},
	} {
		if err := tool.checkModelImageCapability(b9Ctx(2), "text-model"); err != nil {
			t.Fatalf("%s: off state must never reject, got %v", name, err)
		}
	}
}

func TestModelCapabilityFilterOnRejectsWithReason(t *testing.T) {
	tool := b9Tool(true,
		map[string]bool{"text-model": false, "vision-model": true},
		map[string]bool{"text-model": true, "vision-model": true, "ghost-model": false})

	err := tool.checkModelImageCapability(b9Ctx(2), "text-model")
	if err == nil {
		t.Fatal("on state must reject an image task on a text-only model")
	}
	for _, want := range []string{"text-model", "no vision capability", "2 image", "experimental_model_capability_filter"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("rejection must explain %q, got: %v", want, err)
		}
	}

	// Vision-capable model passes.
	if err := tool.checkModelImageCapability(b9Ctx(1), "vision-model"); err != nil {
		t.Fatalf("vision model must pass, got %v", err)
	}
	// No images: nothing to filter.
	if err := tool.checkModelImageCapability(b9Ctx(0), "text-model"); err != nil {
		t.Fatalf("imageless task must pass, got %v", err)
	}
	// modelRef=="" inherits the parent model — no per-task model to judge.
	if err := tool.checkModelImageCapability(b9Ctx(2), ""); err != nil {
		t.Fatalf("inherited model must pass, got %v", err)
	}
	// Unknown ref: uncertainty never refuses (known=false path).
	if err := tool.checkModelImageCapability(b9Ctx(2), "ghost-model"); err != nil {
		t.Fatalf("unknown ref must pass conservatively, got %v", err)
	}
}
