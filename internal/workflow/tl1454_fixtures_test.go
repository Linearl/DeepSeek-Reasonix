package workflow

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"reasonix/internal/event"
)

// tl1454 engine fixtures shared by the five-item tests.

// scriptRunner answers per node id; block makes a node wait for its ctx;
// usage reports spend through the production-shaped ctx sink.
type scriptRunner struct {
	mu      sync.Mutex
	answers map[string]string
	block   map[string]bool
	usage   map[string][2]int
	calls   []string
}

func (r *scriptRunner) Run(ctx context.Context, req SubagentRequest) (string, error) {
	r.mu.Lock()
	r.calls = append(r.calls, req.NodeID)
	answer, ok := r.answers[req.NodeID]
	block := r.block[req.NodeID]
	use := r.usage[req.NodeID]
	r.mu.Unlock()
	if use[0] > 0 || use[1] > 0 {
		ReportSubagentUsage(ctx, use[0], use[1])
	}
	if block {
		<-ctx.Done()
		return "", ctx.Err()
	}
	if !ok {
		answer = "done " + req.NodeID
	}
	return answer, nil
}

func (r *scriptRunner) callCount(id string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, c := range r.calls {
		if c == id {
			n++
		}
	}
	return n
}

// eventLog records node events as "node:state[:round][:detail]" lines.
type eventLog struct {
	mu    sync.Mutex
	lines []string
}

func (l *eventLog) Emit(e event.Event) {
	if e.Kind != event.WorkflowNode || e.Workflow == nil {
		return
	}
	w := e.Workflow
	line := w.NodeID + ":" + string(w.State)
	if w.Round > 0 {
		line += fmt.Sprintf(":r%d", w.Round)
	}
	if w.Detail != "" {
		line += ":" + w.Detail
	}
	l.mu.Lock()
	l.lines = append(l.lines, line)
	l.mu.Unlock()
}

func (l *eventLog) joined() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

// presetGateRunner passes "gh auth status" and fails anything else.
func presetGateRunner(_ context.Context, command string, _ time.Duration) (GateResult, error) {
	if command == "gh auth status" {
		return GateResult{ExitCode: 0, Output: "ok"}, nil
	}
	return GateResult{ExitCode: 1, Output: "failed"}, nil
}

func presetShapeRunner() *scriptRunner {
	return &scriptRunner{answers: map[string]string{
		"scan":   `{"findings":[{"where":"a.go:1","what":"bug","evidence":"e","severity":"high"}],"summary":"one bug"}`,
		"verify": `{"confirmed":true,"reason":"reproduced"}`,
	}}
}
