package telemetry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/event"
)

// readPendingCounters flushes one turn's counters through reporter.append and
// parses the resulting pending payload.
func readPendingCounters(t *testing.T, home string) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(home, pendingDirName))
	if err != nil || len(entries) == 0 {
		t.Fatalf("pending files = %d, err = %v", len(entries), err)
	}
	b, err := os.ReadFile(filepath.Join(home, pendingDirName, entries[len(entries)-1].Name()))
	if err != nil {
		t.Fatal(err)
	}
	var payload pendingPayload
	if err := json.Unmarshal(b, &payload); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, counter := range payload.Counters {
		got[counter.Signal] = counter.Bucket
	}
	return got
}

func TestFirstResponseLatencyRecorded(t *testing.T) {
	// Task 370: the telemetry surface had whole-turn latency only — no
	// TTFT-equivalent. Reasoning, text and tool dispatch each count as the
	// turn's first output.
	for tc, first := range map[string]event.Event{
		"reasoning":     {Kind: event.Reasoning, Text: "…"},
		"text":          {Kind: event.Text, Text: "answer"},
		"tool_dispatch": {Kind: event.ToolDispatch, Tool: event.Tool{ID: "t1", Name: "bash"}},
	} {
		t.Run(tc, func(t *testing.T) {
			home := t.TempDir()
			reporter := &Reporter{home: home, version: "v1.20.0"}
			sink := reporter.Wrap(&readinessSink{})
			sink.Emit(event.Event{Kind: event.TurnStarted})
			sink.Emit(first)
			sink.Emit(event.Event{Kind: event.TurnDone})
			buckets := readPendingCounters(t, home)
			bucket, ok := buckets["cli_first_response_latency"]
			if !ok {
				t.Fatalf("cli_first_response_latency missing from %v", buckets)
			}
			// Sub-second test run lands in the first bucket.
			if bucket != "lt_1s" {
				t.Fatalf("bucket = %q, want lt_1s", bucket)
			}
			if _, ok := buckets["cli_turn_latency"]; !ok {
				t.Fatalf("cli_turn_latency missing: whole-turn signal must stay")
			}
		})
	}
}

func TestFirstResponseLatencyAbsentWithoutOutput(t *testing.T) {
	// A turn that produced nothing records no first-response counter — the
	// empty-turn case stays covered by empty_final + cli_turn_latency.
	home := t.TempDir()
	reporter := &Reporter{home: home, version: "v1.20.0"}
	sink := reporter.Wrap(&readinessSink{})
	sink.Emit(event.Event{Kind: event.TurnStarted})
	sink.Emit(event.Event{Kind: event.TurnDone})
	buckets := readPendingCounters(t, home)
	if _, ok := buckets["cli_first_response_latency"]; ok {
		t.Fatalf("cli_first_response_latency recorded for an output-less turn: %v", buckets)
	}
	if buckets["turns"] != "count" {
		t.Fatalf("turns counter missing: %v", buckets)
	}
}
