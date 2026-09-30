package boot

import (
	"fmt"
	"strings"
	"time"
)

// bootTiming attributes one build() to its subsystem stages so a slow startup
// ("~9s, where did it go?") can be answered from desktop.log without a
// profiler. Observation only: mark() never changes control flow and the
// summary is a single greppable slog line emitted at the end of a successful
// build.
type bootTiming struct {
	start  time.Time
	last   time.Time
	stages []bootStage
}

type bootStage struct {
	name string
	ms   int64
}

func newBootTiming() *bootTiming {
	now := time.Now()
	return &bootTiming{start: now, last: now}
}

// mark closes the open segment and opens the next one under name.
func (t *bootTiming) mark(name string) {
	if t == nil {
		return
	}
	now := time.Now()
	t.stages = append(t.stages, bootStage{name: name, ms: now.Sub(t.last).Milliseconds()})
	t.last = now
}

// stageMs returns the already-closed duration of a named stage (task 413:
// lets a mid-stage summary line carry the stage's own wall clock). A stage
// that has not been marked yet reads 0.
func (t *bootTiming) stageMs(name string) int64 {
	if t == nil {
		return 0
	}
	for _, s := range t.stages {
		if s.name == name {
			return s.ms
		}
	}
	return 0
}

// summary renders one line: total=…ms config=…ms extensions=…ms …
func (t *bootTiming) summary() string {
	if t == nil {
		return ""
	}
	total := time.Since(t.start).Milliseconds()
	parts := make([]string, 0, len(t.stages)+1)
	parts = append(parts, fmt.Sprintf("total=%dms", total))
	for _, s := range t.stages {
		parts = append(parts, fmt.Sprintf("%s=%dms", s.name, s.ms))
	}
	return strings.Join(parts, " ")
}
