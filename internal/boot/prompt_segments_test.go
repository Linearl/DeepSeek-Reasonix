package boot

import (
	"strings"
	"testing"
	"time"
)

// tl1229 回归：prompt 分段收集器只计时不改行为。用可注入时钟形态钉死记账
// 语义（mark(X)=自上一 mark 以来的窗口记到段尾名 X）与汇总行格式。

func TestPromptSegmentsMarkAccounting(t *testing.T) {
	p := &promptSegments{start: time.Unix(0, 0), last: time.Unix(0, 0)}
	p.markAt("shell", 3*time.Millisecond)
	p.markAt("base_prompt", 12*time.Millisecond)
	p.markAt("env_probes", 100*time.Millisecond)
	got := p.segmentsSummary()
	want := "shell=3ms base_prompt=12ms env_probes=100ms"
	if got != want {
		t.Fatalf("segments = %q, want %q", got, want)
	}
	if s := p.summary(); !strings.HasPrefix(s, "total=") || !strings.Contains(s, "env_probes=100ms") {
		t.Fatalf("summary = %q, want total prefix plus segments", s)
	}
}

func TestPromptSegmentsNilAndEmptySafe(t *testing.T) {
	var p *promptSegments
	p.mark("shell")     // must not panic
	if s := p.summary(); s != "" {
		t.Fatalf("nil summary = %q, want empty", s)
	}
	empty := newPromptSegments()
	if s := empty.summary(); !strings.HasPrefix(s, "total=") {
		t.Fatalf("empty summary = %q, want bare total", s)
	}
}

func TestPromptSegmentsCoverAllNamedStages(t *testing.T) {
	// The full stage walk emits every documented segment name in order; the
	// boot-side instrumentation depends on this exact vocabulary.
	p := newPromptSegments()
	for _, name := range []string{
		"shell", "base_prompt", "env_probes", "memory_migrate", "memory_pinned",
		"memory_load", "host_checks", "memory_compose", "skills", "collab_block", "persona",
	} {
		p.mark(name)
	}
	summary := p.summary()
	for _, name := range []string{
		"shell", "base_prompt", "env_probes", "memory_migrate", "memory_pinned",
		"memory_load", "host_checks", "memory_compose", "skills", "collab_block", "persona",
	} {
		if !strings.Contains(summary, name+"=") {
			t.Fatalf("summary missing segment %q: %s", name, summary)
		}
	}
}
