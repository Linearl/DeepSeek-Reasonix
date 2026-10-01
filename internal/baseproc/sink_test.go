package baseproc

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/tool"
)

// sinkRecorder captures emitted events (the tabEventSink shape under test).
type sinkRecorder struct {
	mu     sync.Mutex
	events []event.Event
}

func (r *sinkRecorder) Emit(e event.Event) {
	r.mu.Lock()
	r.events = append(r.events, e)
	r.mu.Unlock()
}

func (r *sinkRecorder) all() []event.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]event.Event(nil), r.events...)
}

// TestWithToolProgressEmitsExistingShape: the adapter stamps the exact
// event.ToolProgress{ID, Output} pair the in-process agent emits today
// (execute_one), so the frontend needs zero change (design R6).
func TestWithToolProgressEmitsExistingShape(t *testing.T) {
	rec := &sinkRecorder{}
	ctx := WithToolProgress(context.Background(), rec, "call-42")
	emit, ok := tool.ProgressFrom(ctx)
	if !ok {
		t.Fatal("stamped ctx lost the progress sink")
	}
	emit("chunk-1")

	events := rec.all()
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	e := events[0]
	if e.Kind != event.ToolProgress || e.Tool.ID != "call-42" || e.Tool.Output != "chunk-1" {
		t.Fatalf("event = %+v, want ToolProgress ID=call-42 Output=chunk-1", e)
	}
}

func TestWithToolProgressNilSafe(t *testing.T) {
	rec := &sinkRecorder{}
	if _, ok := tool.ProgressFrom(WithToolProgress(context.Background(), nil, "c")); ok {
		t.Fatal("nil sink stamped a progress function")
	}
	if _, ok := tool.ProgressFrom(WithToolProgress(context.Background(), rec, "")); ok {
		t.Fatal("empty call id stamped a progress function")
	}
	if _, ok := tool.ProgressFrom(context.Background()); ok {
		t.Fatal("plain ctx claims a progress sink")
	}
	if len(rec.all()) != 0 {
		t.Fatalf("nil-safe paths emitted events: %v", rec.all())
	}
}

// TestSinkAdapterInlineRemoteParity is the end-to-end sink对照: a ToolCall
// driven through baseproc.WithToolProgress delivers identical ToolProgress
// events (kind, id, chunk order) whether the switch is off (inline, chunks
// straight from the tool) or on (remote, chunks back as base.toolProgress
// notifications routed by call_id).
func TestSinkAdapterInlineRemoteParity(t *testing.T) {
	newStreamer := func() *stubTool {
		return &stubTool{name: "streamer", exec: func(ctx context.Context, _ json.RawMessage) (string, error) {
			emit, ok := tool.ProgressFrom(ctx)
			if !ok {
				return "", errors.New("no progress sink")
			}
			emit("c1")
			emit("c2")
			return "done", nil
		}}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	inlineRec, remoteRec := &sinkRecorder{}, &sinkRecorder{}
	inline := InlineBaseClient{Surface: &RegistrySurface{Reg: newStubRegistry(newStreamer())}}
	remote, _ := remoteToolClient(t, &RegistrySurface{Reg: newStubRegistry(newStreamer())})
	mustHello(t, remote)

	wantRes, err := inline.ToolCall(WithToolProgress(ctx, inlineRec, "call-9"), ToolCallParams{CallID: "call-9", Tool: "streamer"})
	if err != nil {
		t.Fatalf("inline call: %v", err)
	}
	gotRes, err := remote.ToolCall(WithToolProgress(ctx, remoteRec, "call-9"), ToolCallParams{CallID: "call-9", Tool: "streamer"})
	if err != nil {
		t.Fatalf("remote call: %v", err)
	}
	if wantRes != gotRes {
		t.Fatalf("results diverge: inline=%+v remote=%+v", wantRes, gotRes)
	}
	want := []event.Event{{
		Kind: event.ToolProgress, Tool: event.Tool{ID: "call-9", Output: "c1"},
	}, {
		Kind: event.ToolProgress, Tool: event.Tool{ID: "call-9", Output: "c2"},
	}}
	if !reflect.DeepEqual(inlineRec.all(), want) {
		t.Fatalf("inline events = %+v, want %+v", inlineRec.all(), want)
	}
	if !reflect.DeepEqual(remoteRec.all(), want) {
		t.Fatalf("remote events = %+v, want %+v", remoteRec.all(), want)
	}
}
