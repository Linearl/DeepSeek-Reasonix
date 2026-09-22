package agent

// snapshotNodes returns a copy of the node pointer slice under the read lock
// (task 239 H1-1). The shared-cached graph is mutated in place by replayFrom,
// so a reader must never range the live map concurrently: take a consistent
// snapshot instead. The nodes themselves are append-only once created (only
// the map grows during a replay), so pointer visibility is safe after the
// snapshot.
func (st *sessionDAGState) snapshotNodes() []*sessionDAGNode {
	st.mu.RLock()
	defer st.mu.RUnlock()
	out := make([]*sessionDAGNode, 0, len(st.nodes))
	for _, n := range st.nodes {
		out = append(out, n)
	}
	return out
}
