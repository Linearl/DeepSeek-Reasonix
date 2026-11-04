package evidence

// tl1354: the serial "current item must be the first unfinished item" rule used
// to hard-reject a list whose in_progress item sat behind untouched pending
// work, leaving the agent to decode the rule and retry (two same-day incidents).
// This file adds bounded fault tolerance: when the ONLY problem is that
// ordering slip, the host repairs it instead of rejecting. Every other failure
// (invented steps, dropped identities, second in_progress, subtree violations)
// stays a rejection — tolerance must not open an anti-forgery hole.

// RepairSerialTodoCurrentOrder attempts to repair exactly one failure class:
// the single in_progress item sitting behind pending work it should precede.
// It returns the repaired list and true, or the ORIGINAL list and false when
// the list is already valid, the error is anything else, or the repair would
// not re-validate. The caller keeps its rejection path on false.
func RepairSerialTodoCurrentOrder(todos []TodoItem) ([]TodoItem, bool) {
	if ValidateSerialTodos(todos) == nil {
		return todos, false
	}
	var repaired []TodoItem
	if hasExplicitTodoParents(todos) {
		repaired = repairTreeCurrentOrder(todos)
	} else {
		repaired = repairSegmentCurrentOrder(todos)
	}
	if repaired == nil || ValidateSerialTodos(repaired) != nil {
		return todos, false
	}
	return repaired, true
}

// NormalizeSerialTodoInput is the one entry point for turning a model-sent
// list into host state (tl1354). The ordering repair runs FIRST so the
// recorded shape matches what todo_write's Execute accepted — normalizing the
// raw input alone would rewrite statuses instead (promoting a pending item to
// current over the model's explicit choice), and the mismatch would deadlock
// the very next write's continuity check. The legacy normalizer then settles
// any remaining flat-list shape; it is idempotent on an already-valid list.
// Task 152: explicit parent_id trees skip the legacy normalizer (it would
// rewrite statuses the tree machine approved); the ordering repair itself
// never rewrites statuses, only positions.
func NormalizeSerialTodoInput(todos []TodoItem) []TodoItem {
	repaired, _ := RepairSerialTodoCurrentOrder(todos)
	if hasExplicitTodoParents(repaired) {
		return repaired
	}
	return NormalizeSerialTodos(repaired)
}

// soleInProgressIndex reports the index of the one in_progress item, or -1
// when there is none or more than one. Two currents is a different failure
// (its rejection already names the exact fix), not an ordering slip.
func soleInProgressIndex(todos []TodoItem) int {
	ip := -1
	for i, todo := range todos {
		if todoStatus(todo.Status) == "in_progress" {
			if ip >= 0 {
				return -1
			}
			ip = i
		}
	}
	return ip
}

// repairSegmentCurrentOrder repairs the flat/two-level list shapes the segment
// machine rejects: the current segment behind an unfinished segment (moved
// whole in front of the first unfinished one), or a sub-step running behind
// pending siblings inside its own phase (moved to the first unfinished slot).
// A list it cannot fix comes back unchanged, so the caller re-validates and
// falls back to the original rejection.
func repairSegmentCurrentOrder(todos []TodoItem) []TodoItem {
	ip := soleInProgressIndex(todos)
	if ip < 0 {
		return nil
	}
	out := append([]TodoItem(nil), todos...)
	segs := serialTodoSegments(out)
	si := segmentContaining(segs, ip)
	if si < 0 {
		return nil
	}
	seg := segs[si]
	// In-segment slip: a level-1 sub-step in_progress behind pending siblings.
	if seg.end-seg.head > 1 && ip > seg.head+1 {
		for j := seg.head + 1; j < ip; j++ {
			if !TodoTerminalStatus(out[j].Status) {
				item := out[ip]
				out = append(out[:ip], out[ip+1:]...)
				out = append(out[:j], append([]TodoItem{item}, out[j:]...)...)
				ip = j
				segs = serialTodoSegments(out)
				si = segmentContaining(segs, ip)
				if si < 0 {
					return nil
				}
				seg = segs[si]
				break
			}
		}
	}
	// Between-segment slip: an unfinished segment before the current segment.
	for k := 0; k < si; k++ {
		if serialSegmentCompleted(out, segs[k]) {
			continue
		}
		block := append([]TodoItem(nil), out[seg.head:seg.end]...)
		rest := append([]TodoItem(nil), out[:seg.head]...)
		rest = append(rest, out[seg.end:]...)
		target := segs[k].head
		// segs[k].head < seg.head, so removing the current segment first keeps
		// the target index valid in the shortened list.
		rebuilt := append([]TodoItem(nil), rest[:target]...)
		rebuilt = append(rebuilt, block...)
		rebuilt = append(rebuilt, rest[target:]...)
		return rebuilt
	}
	return out
}

func segmentContaining(segs []todoSegment, i int) int {
	for k, seg := range segs {
		if i >= seg.head && i < seg.end {
			return k
		}
	}
	return -1
}

// repairTreeCurrentOrder repairs the task-152 tree shape: pending work that is
// NOT an ancestor of the current item sitting in front of it. Pending
// ancestors stay where they are (a pending parent legitimately owns the
// running subtree). The blocked work is moved behind the current item together
// with its whole subtree — moving bare items could strand a child above its
// parent and fail parent-order resolution.
func repairTreeCurrentOrder(todos []TodoItem) []TodoItem {
	parents, err := resolveTodoParents(todos)
	if err != nil {
		return nil
	}
	ip := soleInProgressIndex(todos)
	if ip < 0 {
		return nil
	}
	isAncestor := func(child, anc int) bool {
		for p := parents[child]; p >= 0; p = parents[p] {
			if p == anc {
				return true
			}
		}
		return false
	}
	blocker := make([]bool, len(todos))
	any := false
	for i := 0; i < ip; i++ {
		if todoStatus(todos[i].Status) == "pending" && !isAncestor(ip, i) {
			blocker[i] = true
			any = true
		}
	}
	if !any {
		return nil
	}
	// Expand blockers to full subtrees. A blocker whose parent is itself a
	// blocker is covered when its root's subtree is collected.
	blocked := make([]bool, len(todos))
	for i := range todos {
		if !blocker[i] || (parents[i] >= 0 && blocker[parents[i]]) {
			continue
		}
		for j := i; j < len(todos); j++ {
			if j == i || isAncestor(j, i) {
				blocked[j] = true
			}
		}
	}
	// The current block is the in_progress item plus its whole subtree.
	current := make([]bool, len(todos))
	for j := range todos {
		if j == ip || isAncestor(j, ip) {
			current[j] = true
		}
	}
	out := make([]TodoItem, 0, len(todos))
	for i := range todos {
		if !blocked[i] && !current[i] {
			out = append(out, todos[i])
		}
	}
	for i := range todos {
		if current[i] {
			out = append(out, todos[i])
		}
	}
	for i := range todos {
		if blocked[i] {
			out = append(out, todos[i])
		}
	}
	return out
}
