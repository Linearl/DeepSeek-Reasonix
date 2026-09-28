package agent

import (
	"fmt"
	"runtime"
	"testing"
)

// TestGraphCacheSingleFootprint measures the per-graph resident memory the
// tuned capacity trades for hit rate (task 196fix2 condition 2: measure the
// 3-graph cost before committing). Synthetic nodes stand in for a replayed
// graph — sessionDAGState's memory is dominated by the node map, so bytes per
// node times an expected node count is the honest extrapolation for a real
// session (graph size scales with node count, never with log size directly).
// The measured numbers are quoted in the delivery letter.
func TestGraphCacheSingleFootprint(t *testing.T) {
	const nodes = 100_000
	before := heapInuseBytes(t)

	st := &sessionDAGState{
		path:  "synthetic.events.jsonl",
		nodes: make(map[string]*sessionDAGNode, nodes),
	}
	for i := 0; i < nodes; i++ {
		id := fmt.Sprintf("n%08d", i)
		st.nodes[id] = &sessionDAGNode{
			id:     id,
			parent: fmt.Sprintf("n%08d", i-1),
			head:   "head-0",
			writer: "writer-0",
			turn:   "turn-0",
			digest: id,
		}
	}
	// Observe the content, not just the length: with only len() reading the map
	// the compiler may dead-code the whole construction (a first run measured
	// a nonsensical 0 bytes/node that way).
	if probe := st.nodes["n00000005"]; probe == nil || probe.id != "n00000005" || probe.parent != "n00000004" {
		t.Fatalf("map content probe failed: %+v", probe)
	}
	if len(st.nodes) != nodes {
		t.Fatalf("nodes = %d, want %d", len(st.nodes), nodes)
	}

	after := heapInuseBytes(t)
	runtime.KeepAlive(st)
	if after <= before {
		t.Fatalf("heap did not grow after building %d nodes: before=%d after=%d (measurement unusable)", nodes, before, after)
	}
	total := after - before
	perNode := total / nodes
	if perNode < 32 {
		// Six strings alone cost more than this per node — a smaller reading
		// means the measurement lost the graph (DCE or a misread stat).
		t.Fatalf("per-node = %d bytes, want >= 32 (measurement lost the graph)", perNode)
	}
	t.Logf("synthetic %d-node graph = %d bytes (≈%.1f MiB); per-node ≈ %d bytes; 3 resident graphs of this size ≈ %.1f MiB — extrapolation for a real session = per-node × its node count (graph scales with nodes, not log bytes)",
		nodes, total, float64(total)/(1<<20), perNode, 3*float64(total)/(1<<20))
}

// heapInuseBytes forces a collection twice so the reading reflects live
// allocations rather than pending garbage from earlier tests.
func heapInuseBytes(t *testing.T) uint64 {
	t.Helper()
	runtime.GC()
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapInuse
}
