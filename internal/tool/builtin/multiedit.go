package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"reasonix/internal/sandbox"
	"reasonix/internal/tool"
)

func init() { tool.RegisterBuiltin(multiEdit{}) }

// multiEdit applies a batch of edits to one file. roots confines the target to
// the workspace when non-empty (see writeFile); guard rejects Reasonix
// session-data targets (see SessionDataGuard); workDir, when non-empty, is the
// directory a relative path resolves against (see resolveIn). readBack, when
// true (the experimental_tool_optimizations family, task 603), gets the
// edited region rendered back with surrounding context and filed as fresh
// read evidence; since task 875 the read-back is the DEFAULT with the family
// lit (an omitted readBack reads back, readBack:false opts out). The zero
// value keeps the tool byte-identical to the pre-family surface.
type multiEdit struct {
	roots    []string
	rootSet  *sandbox.WritableRootSet
	guard    SessionDataGuard
	managed  ManagedConfigPaths
	workDir  string
	overlay  FileOverlay
	readBack bool
	// skipFailed lights the tl-1218 ③ skip-failed mode
	// (experimental_multi_edit_skip_failed): one failed edit no longer aborts
	// the batch — the step is skipped, later steps still run against the
	// buffer as left by the applied ones, and the final response reports every
	// edit (index + applied/failed + reason). The zero value keeps the atomic
	// all-or-nothing semantics verbatim.
	skipFailed bool
}

// editStep is one edit in a multi_edit operation. Mirrors edit_file's args
// plus a per-step replace_all toggle so a single call can mix targeted and
// sweep replacements (e.g. rename a function with replace_all, then patch
// one specific call site with a unique-match edit).
type editStep struct {
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all,omitempty"`
}

func (multiEdit) Name() string { return "multi_edit" }

func (multiEdit) Description() string {
	return "WHEN TO USE: modifying 2+ places in the same file — use this INSTEAD of chained edit_file calls (atomic, fewer calls, per-step errors). Chinese phrasings like 同文件多处修改, 批量修改, 一次改完整个文件 all mean this tool. Apply a list of edits to a single file atomically: each edit runs against the result of the previous one, all in memory; the file is rewritten only if every edit succeeds. Cheaper and safer than chaining edit_file calls — a failure in step 3 leaves the file untouched instead of half-edited."
}

func (m multiEdit) Schema() json.RawMessage {
	readBackField := ""
	if m.readBack {
		readBackField = `,
  "readBack":{"type":"boolean","default":true,"description":"Defaults to true: the result reads back the edited region (first to last edit) with ±20 surrounding lines. The returned window counts as having read the file: a follow-up edit on this file passes the read-evidence gate without a separate read_file call (any outside change is still caught). Set false only to opt out (e.g. very large edits)."}`
	}
	return json.RawMessage(`{
"type":"object",
"properties":{
  "path":{"type":"string","description":"File path"},
  "expected":{"type":"string","description":"Field not shown in schema; see shared optimistic-write baseline."},
  "edits":{
    "type":"array",
    "minItems":1,
    "description":"Ordered edits. Each step sees the file as left by the previous step.",
    "items":{
      "type":"object",
      "properties":{
        "old_string":{"type":"string","description":"Exact text to find. Without replace_all, must match exactly once."},
        "new_string":{"type":"string","description":"Replacement text (empty deletes)."},
        "replace_all":{"type":"boolean","description":"Replace every occurrence instead of requiring uniqueness."}
      },
      "required":["old_string","new_string"]
    }
  },
  "source_token":{"type":"string","description":"Optional: the source_token printed by the read_file that showed you this file. Citing it names the exact version you are editing, so a change made outside this session is caught instead of silently overwritten."}` + readBackField + `
},
"required":["path","edits"]
}`)
}

func (multiEdit) ReadOnly() bool { return false }

func (m multiEdit) DeclareWriteAccess(args json.RawMessage) (tool.WriteAccessDeclaration, error) {
	return declareFilePathWriteAccess(m.workDir, args)
}

func (m multiEdit) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Path     string     `json:"path"`
		Edits    []editStep `json:"edits"`
		Expected string     `json:"expected"`
		ReadBack *bool      `json:"readBack"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if p.Path == "" {
		return "", fmt.Errorf("path is required")
	}
	if len(p.Edits) == 0 {
		return "", fmt.Errorf("edits must not be empty")
	}
	// The readBack parameter only exists while the experimental_tool_optimizations
	// family is lit (task 603). With the family off the field is ignored —
	// identical bytes in, identical bytes out (铁律 2). With the family lit,
	// task 875 flips the default: an omitted readBack reads back (the value
	// users actually want in back-to-back edits), readBack:false opts out.
	useReadBack := m.readBack && (p.ReadBack == nil || *p.ReadBack)
	p.Path = resolveIn(m.workDir, p.Path)
	if err := confineWrite(ctx, effectiveWriteRoots(ctx, m.rootSet, m.roots), m.guard, m.managed, p.Path); err != nil {
		return "", err
	}
	if err := checkOptimisticExpected(ctx, m.overlay, p.Path, p.Expected, "multi_edit"); err != nil {
		return "", err
	}

	src, err := readEditSource(ctx, m.overlay, p.Path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", p.Path, err)
	}
	content := src.content

	// Apply edits in order against the running in-memory buffer. With
	// skipFailed off (the default) any failure returns before the write,
	// leaving the file untouched — that's the atomicity guarantee that makes
	// multi_edit preferable to chained edit_file calls. With skipFailed on
	// (tl-1218 ③) a failed step is skipped and later steps still run; the
	// file is written only when at least one edit applied, and the response
	// carries a per-edit report.
	applied := 0
	usedFuzzy := false
	receipts := make([]editReplacementReceipt, 0, len(p.Edits))
	var spanStart, spanEnd int // union of replaced regions in the CURRENT buffer
	var failures []editStepFailure
	stepApplied := make([]int, len(p.Edits))
	for i, step := range p.Edits {
		if step.OldString == "" {
			err := fmt.Errorf("edit %d: old_string is required", i+1)
			if m.skipFailed {
				failures = append(failures, editStepFailure{index: i + 1, erred: err, reason: err.Error()})
				continue
			}
			return "", err
		}
		result := applyOldStringEdit(content, step.OldString, step.NewString, step.ReplaceAll)
		switch {
		case result.applied > 0:
			if useReadBack && result.updatedStart >= 0 {
				spanStart, spanEnd = unionReadBackSpan(spanStart, spanEnd, content, result)
			}
			content = result.updated
			applied += result.applied
			stepApplied[i] = result.applied
			usedFuzzy = usedFuzzy || result.fuzzy
			receipts = append(receipts, result.receipt)
		case result.matches == 0:
			err := oldStringNotFoundError(p.Path, step.OldString, content)
			if m.skipFailed {
				failures = append(failures, editStepFailure{index: i + 1, erred: fmt.Errorf("edit %d: %w", i+1, err), reason: err.Error()})
				continue
			}
			return "", fmt.Errorf("edit %d: %w", i+1, err)
		default:
			err := oldStringNotUniqueError(p.Path, step.OldString, content, result.matches, true)
			if m.skipFailed {
				failures = append(failures, editStepFailure{index: i + 1, erred: fmt.Errorf("edit %d: %w", i+1, err), reason: err.Error()})
				continue
			}
			return "", fmt.Errorf("edit %d: %w", i+1, err)
		}
	}
	// Skip-failed mode with NOTHING applied: the batch-level report would be
	// empty prose, so the call fails exactly like the baseline — the first
	// failure's own error (diagnostics intact), file untouched.
	if len(failures) > 0 && applied == 0 {
		return "", failures[0].erred
	}

	if err := src.write(ctx, m.overlay, p.Path, content); err != nil {
		return "", fmt.Errorf("write %s: %w", p.Path, err)
	}
	// With skip-failed mode on and steps skipped, the applied-step count (not
	// the total) leads the summary; the per-edit report below names each
	// skipped step. Pure-success batches keep len(p.Edits), byte-identical.
	summary := fmt.Sprintf("multi_edit %s: %d edits applied (%d total replacements)", p.Path, len(p.Edits)-len(failures), applied)
	if usedFuzzy {
		summary += " (fuzzy match)"
	}
	summary = withActualPostWriteReceipts(summary, receipts)
	if useReadBack && spanStart >= 0 {
		first, last := readBackSpanLines(content, spanStart, spanEnd)
		summary = withReadBack(ctx, summary, p.Path, content, src.postWriteSnapshot(p.Path, content), first, last)
	}
	if len(failures) > 0 {
		summary += "\n" + formatEditStepReport(stepApplied, failures)
	}
	return summary, nil
}

// editStepFailure records one skipped step for the tl-1218 ③ per-edit report.
type editStepFailure struct {
	index  int
	erred  error
	reason string
}

// Per-edit report bounds (tl-1218 ③): reasons echo the per-edit errors, which
// can quote file content, so each reason is truncated at 300 bytes and the
// whole report at 4KB.
const (
	editReportMaxReasonBytes = 300
	editReportMaxTotalBytes  = 4096
)

// formatEditStepReport renders the tl-1218 ③ per-edit report: one line per
// edit with its index and outcome (applied replacement count or the failure
// reason). Only emitted when at least one step failed, so a fully successful
// multi_edit keeps its baseline output verbatim.
func formatEditStepReport(appliedCounts []int, failures []editStepFailure) string {
	failedByIndex := make(map[int]string, len(failures))
	for _, f := range failures {
		reason := f.reason
		// The 300-byte cap covers the WHOLE row (index prefix included), so a
		// truncated line still reads as one bounded entry.
		prefix := fmt.Sprintf("  edit %d: failed: ", f.index)
		if room := editReportMaxReasonBytes - len(prefix); len(reason) > room {
			reason = reason[:room-3] + "..."
		}
		failedByIndex[f.index] = reason
	}
	var b strings.Builder
	b.WriteString("per-edit report (failed edits were skipped; the file was written with the applied ones):\n")
	total := 0
	for i := range appliedCounts {
		var row string
		if reason, failed := failedByIndex[i+1]; failed {
			row = fmt.Sprintf("  edit %d: failed: %s\n", i+1, reason)
		} else {
			row = fmt.Sprintf("  edit %d: applied (%d replacement(s))\n", i+1, appliedCounts[i])
		}
		if total+len(row) > editReportMaxTotalBytes {
			b.WriteString("  …(report truncated at 4KB)\n")
			break
		}
		b.WriteString(row)
		total += len(row)
	}
	return strings.TrimRight(b.String(), "\n")
}

// unionReadBackSpan folds one step's replaced region into the running union.
// Region coordinates live in the buffer the step read; earlier spans already
// sit in that same buffer, so those wholly before the replacement merge with
// its new bounds, those wholly after shift by the replacement's length delta,
// and a partial overlap merges into the replacement's own bounds.
func unionReadBackSpan(start, end int, buffer string, result editApplyResult) (int, int) {
	ms, me := result.matchedStart, result.matchedEnd
	us, ue := result.updatedStart, result.updatedEnd
	if ms < 0 || me < ms || us < 0 || ue < us || me > len(buffer) {
		return start, end
	}
	delta := (ue - us) - (me - ms)
	switch {
	case start < 0:
		return us, ue
	case end <= ms:
		return start, ue
	case start >= me:
		return us, max(end+delta, ue)
	default:
		return min(start, us), max(end, ue)
	}
}
