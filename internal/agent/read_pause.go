package agent

import (
	"errors"

	"reasonix/internal/provider"
	"reasonix/internal/readcoord"
	"reasonix/internal/tool"
)

// finishReadRun records the verdict before closeReadStatuses cancels active
// tasks. The LocalOnly sentinel keeps older provider projections safe too.
func (a *Agent) finishReadRun(err error) {
	defer a.closeReadStatuses()
	defer func() { a.reads.deliveries, a.reads.visible = nil, nil }()
	var incomplete *IncompleteReadError
	if err == nil {
		a.recordReadCompletion()
	}
	if !errors.As(err, &incomplete) || !a.readPipelineActive() {
		return
	}
	pause := &provider.ReadPause{ID: a.reads.tasks.binding, Code: tool.ReadHardStop, Reads: []provider.PausedRead{}}
	for _, ob := range a.turn.readShadow.coord.Snapshot() {
		if ob.State.Terminal() {
			continue
		}
		if len(pause.Reads) == 32 {
			pause.Omitted++
			continue
		}
		reason := "incomplete"
		if ob.Stop != nil {
			reason = ob.Stop.Code
		}
		var missing []tool.ReadRange
		if ob.Requirement.WholeFile && ob.SourceEnd != nil {
			missing = []tool.ReadRange{{Start: 0, End: *ob.SourceEnd}}
		} else {
			missing = ob.Requirement.Ranges
		}
		pause.Reads = append(pause.Reads, provider.PausedRead{ReadID: ob.Key, Path: ob.Scope.CanonicalPath, Snapshot: ob.Version, Intent: string(ob.Requirement.Intent), Covered: boundedReadRanges(ob.Covered), Missing: boundedReadRanges(readcoord.Subtract(missing, ob.Covered)), Reason: reason})
	}
	incomplete.Pause = pause
	a.sess.conversation.Add(provider.Message{Role: provider.RoleTool, ToolCallID: provider.LocalOnlyToolID, Name: provider.LocalOnlyToolName, LocalOnly: true, ReadPause: pause})
}

func (a *Agent) recordReadCompletion() {
	if !a.readPipelineActive() {
		return
	}
	receipt := &provider.ReadCompletion{ID: a.reads.tasks.binding, Reads: []provider.CompletedRead{}}
	for _, ob := range a.turn.readShadow.coord.Snapshot() {
		if ob.Requirement.WholeFile {
			continue
		}
		// Only partial windows need a terminal explanation. No source end
		// means the reader did not establish full coverage.
		if ob.SourceEnd != nil && readcoord.Covers(ob.Covered, []tool.ReadRange{{Start: 0, End: *ob.SourceEnd}}) {
			continue
		}
		if len(receipt.Reads) == 32 {
			receipt.Omitted++
			continue
		}
		receipt.Reads = append(receipt.Reads, provider.CompletedRead{ReadID: ob.Key, Path: ob.Scope.CanonicalPath, Snapshot: ob.Version, Intent: string(ob.Requirement.Intent), Verdict: "partial_read_sufficient", Covered: boundedReadRanges(ob.Covered), SourceEnd: ob.SourceEnd})
	}
	if len(receipt.Reads) > 0 {
		a.sess.conversation.Add(provider.Message{Role: provider.RoleTool, ToolCallID: provider.LocalOnlyToolID, Name: provider.LocalOnlyToolName, LocalOnly: true, ReadCompletion: receipt})
	}
}

func boundedReadRanges(ranges []tool.ReadRange) [][2]int {
	result := make([][2]int, 0, min(64, len(ranges)))
	for _, r := range ranges[:min(64, len(ranges))] {
		result = append(result, [2]int{r.Start, r.End})
	}
	return result
}
