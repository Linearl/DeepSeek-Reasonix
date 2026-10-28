package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"reasonix/internal/event"
	"reasonix/internal/evidence"
	"reasonix/internal/provider"
	"reasonix/internal/runtimepolicy"
	"reasonix/internal/sandbox"
	"reasonix/internal/shellsafe"
	"reasonix/internal/tool"
)

// evidenceCheck is the host's verdict on whether a writer has the
// model-visible evidence its own declaration requires. It never grants
// permission: authorization, sandbox, uniqueness, and atomic replace stay
// independent.
type evidenceCheck struct {
	Target    tool.EvidenceTargetInfo
	Satisfied bool
	// Supported is false when the writer cannot declare what it replaces; the
	// caller then keeps the existing boundary rather than assuming safety.
	Supported   bool
	Path        string
	Missing     []tool.ReadRange
	Reason      string
	Recovery    string
	Diagnostic  *tool.OperationDiagnostic
	NativeError error
}

// maxEvidenceReadPages bounds how much the host will read to prove a whole-file
// overwrite. Beyond it the check reports unsupported instead of scanning a huge
// file on every write.
const maxEvidenceReadPages = 16

// evidenceObservationBoundary returns the observation boundary the write
// evidence checks run against. Strict mode (the default) returns the frozen
// batch boundary, so a read from the same provider batch never counts — the
// pre-789 baseline, byte for byte. Relaxed mode (task 789) widens the check to
// the ledger's current sequence, so a read that already completed earlier in
// this same batch counts as valid evidence; a read the model has not received
// yet (later in the batch) still cannot pay for an earlier write.
func (a *Agent) evidenceObservationBoundary(ctx context.Context) uint64 {
	frozen := observationBoundary(ctx, a.task.ledger.ObservationBoundary())
	if a.editEvidenceRelaxed {
		return a.task.ledger.ObservationBoundary()
	}
	return frozen
}

// checkOperationEvidence asks the real writer what it is about to replace and
// verifies that the model has seen that exact content in this turn, before the
// batch boundary.
func (a *Agent) checkOperationEvidence(ctx context.Context, call provider.ToolCall, target tool.Tool, boundary uint64) evidenceCheck {
	if a == nil || a.task.ledger == nil || target == nil {
		return evidenceCheck{}
	}
	if target.ReadOnly() {
		return evidenceCheck{Satisfied: true, Supported: true}
	}
	declarer, ok := target.(tool.EvidenceDeclarer)
	if !ok {
		return evidenceCheck{}
	}
	info, err := declarer.DeclareEvidenceTarget(ctx, json.RawMessage(call.Arguments))
	if err != nil {
		var operationErr *tool.OperationError
		if errors.As(err, &operationErr) {
			d := operationErr.Diagnostic
			return evidenceCheck{Supported: true, Path: d.Path, Reason: d.Code, Recovery: d.Recovery, Diagnostic: &d, NativeError: err}
		}
		// A writer that cannot name what it replaces is never granted a pass
		// while a read requirement is outstanding; with nothing outstanding the
		// writer's own validation still reports the concrete error.
		return evidenceCheck{Reason: "target_invalid", Recovery: err.Error()}
	}
	if info.Path == "" {
		return evidenceCheck{Satisfied: true, Supported: true}
	}
	info.Path = filepath.Clean(info.Path)
	check := evidenceCheck{Supported: true, Path: info.Path, Target: info}
	if info.PreservesContent && info.Snapshot != "" {
		check.Satisfied = true
		return check
	}
	if info.WholeFile && a.rebuildAuthorized(info.Path) {
		// The user explicitly asked to rebuild this file; the model cannot grant
		// this to itself, and the instruction must name the file.
		check.Satisfied = true
		check.Reason = "user_authorized_rebuild"
		return check
	}
	if info.WholeFile && len(info.Ranges) == 0 && len(info.Hashes) > 0 {
		info.Ranges = []tool.ReadRange{{Start: 0, End: len(info.Hashes)}}
	}
	check.Target = info
	if len(info.Ranges) == 0 && !info.WholeFile {
		// The writer creates a new file: there is no prior content to have seen.
		check.Satisfied = true
		return check
	}
	if info.WholeFile && len(info.Hashes) == 0 {
		hashes, ok := a.currentFileHashes(ctx, info.Path)
		if !ok {
			check.Reason = "whole_file_unverifiable"
			check.Recovery = fmt.Sprintf("the current content of %s could not be verified; read it first with read_file", info.Path)
			return check
		}
		info.Hashes = hashes
		info.Ranges = []tool.ReadRange{{Start: 0, End: len(hashes)}}
	}
	check.Target = info

	if token := citedSourceToken(call.Arguments); token != "" {
		return a.checkCitedSourceToken(call, check, info, token)
	}
	observations := a.eligibleObservations(info.Path, boundary)
	if info.Snapshot != "" {
		observations = slices.DeleteFunc(observations, func(o evidence.TextObservation) bool {
			return o.Snapshot != info.Snapshot && (info.WholeFile || o.Snapshot != "")
		})
	}
	if len(observations) == 0 {
		check.Reason = "no_eligible_read"
		check.Missing = info.Ranges
		check.Recovery = fmt.Sprintf("read %s in a previous provider round, then retry; reads from the same batch do not count", info.Path)
		return check
	}
	if satisfied, missing := evidenceCoversTarget(observations, info); satisfied {
		check.Satisfied = true
		return check
	} else {
		check.Missing = missing
	}
	check.Reason = "stale_or_partial_evidence"
	// Task 859: the receipt names the reason, not just the rule. The dominant
	// cause of staleness is the model's own earlier edit, so the guidance says
	// so — a bare "re-read" invited the model to reword the same call instead
	// of re-reading (the capability_id lesson).
	check.Recovery = fmt.Sprintf("the file changed since your last read — usually by your own earlier edit; re-read the missing lines of %s, then retry against the current content", info.Path)
	if info.WholeFile {
		check.Recovery = "the file changed since your last read — usually by your own earlier edit; use read_file with intent=full and complete its pages before retrying the overwrite"
	}
	return check
}

// eligibleObservations returns the model-visible windows for path recorded
// after the last write to it and before the frozen batch boundary.
func (a *Agent) eligibleObservations(path string, boundary uint64) []evidence.TextObservation {
	canonical := filepath.Clean(path)
	writeIndex, hasWrite := a.task.ledger.LatestSuccessfulWriteIndex([]string{path})
	var writeSequence uint64
	if hasWrite {
		writeSequence, _ = a.task.ledger.ReceiptSequence(writeIndex)
	}
	var out []evidence.TextObservation
	for _, o := range a.task.ledger.TextObservations() {
		if filepath.Clean(o.Path) != canonical || o.Sequence <= writeSequence || o.Sequence > boundary {
			continue
		}
		out = append(out, o)
	}
	return out
}

// evidenceCoversTarget reports whether the observations prove the exact
// current content the writer is about to replace. Windows from different
// snapshots are never stitched together.
func evidenceCoversTarget(observations []evidence.TextObservation, target tool.EvidenceTargetInfo) (bool, []tool.ReadRange) {
	if target.Snapshot != "" {
		observations = slices.DeleteFunc(slices.Clone(observations), func(o evidence.TextObservation) bool {
			return o.Snapshot != target.Snapshot && (target.WholeFile || o.Snapshot != "")
		})
	}
	if len(target.Hashes) == 0 {
		return false, target.Ranges
	}
	windows := stitchBySnapshot(observations)
	index := 0
	var missing []tool.ReadRange
	for _, r := range target.Ranges {
		end := index + r.Lines()
		if end > len(target.Hashes) {
			return false, target.Ranges
		}
		covered := false
		for _, w := range windows {
			start := r.Start - (w.startLine - 1)
			if start >= 0 && start+r.Lines() <= len(w.hashes) && slices.Equal(w.hashes[start:start+r.Lines()], target.Hashes[index:end]) {
				covered = true
				break
			}
		}
		if !covered {
			missing = append(missing, r)
		}
		index = end
	}
	return len(missing) == 0 && index == len(target.Hashes), missing
}

type hashWindow struct {
	startLine int
	hashes    []string
}

// stitchBySnapshot joins windows that share a content snapshot into the largest
// contiguous windows they can prove. Observations without a snapshot stay on
// their own: the host cannot vouch that they describe the same content version.
func stitchBySnapshot(observations []evidence.TextObservation) []hashWindow {
	bySnapshot := map[string][]evidence.TextObservation{}
	var singles []hashWindow
	for _, o := range observations {
		if o.Snapshot == "" {
			singles = append(singles, hashWindow{startLine: o.StartLine, hashes: o.LineHashes})
			continue
		}
		bySnapshot[o.Snapshot] = append(bySnapshot[o.Snapshot], o)
	}
	out := singles
	for _, group := range bySnapshot {
		slices.SortFunc(group, func(a, b evidence.TextObservation) int { return a.StartLine - b.StartLine })
		merged := hashWindow{startLine: group[0].StartLine, hashes: append([]string(nil), group[0].LineHashes...)}
		for _, o := range group[1:] {
			end := merged.startLine + len(merged.hashes)
			if o.StartLine > end {
				out = append(out, merged)
				merged = hashWindow{startLine: o.StartLine, hashes: append([]string(nil), o.LineHashes...)}
				continue
			}
			if o.StartLine+len(o.LineHashes) <= end {
				continue
			}
			overlap := end - o.StartLine
			merged.hashes = append(merged.hashes, o.LineHashes[overlap:]...)
		}
		out = append(out, merged)
	}
	return out
}

// currentFileHashes reads the file through the real reader so encoding, overlay
// routing, and line decoding match what a model would have seen. It is bounded
// and reports false rather than scanning an unbounded file.
func (a *Agent) currentFileHashes(ctx context.Context, path string) ([]string, bool) {
	reader, ok := a.svc.tools.Get("read_file")
	if !ok {
		return nil, false
	}
	var hashes []string
	offset := 0
	for range maxEvidenceReadPages {
		args, err := json.Marshal(map[string]any{"path": path, "offset": offset, "limit": readEvidencePageLines})
		if err != nil {
			return nil, false
		}
		out, err := reader.Execute(ctx, args)
		if err != nil {
			return nil, false
		}
		window, ok := tool.ParseReadWindow(out)
		if !ok {
			return nil, false
		}
		for _, line := range window.Lines {
			hashes = append(hashes, hashLine(line))
		}
		trailer := tool.ParseReadTrailer(out)
		if !trailer.HasMore {
			return hashes, true
		}
		if trailer.NextOffset <= offset {
			return nil, false
		}
		offset = trailer.NextOffset
	}
	return nil, false
}

const readEvidencePageLines = 2000

// Evidence error-detail bounds (tl-1218 ①). The appended current-content
// sample is hard-bounded so a blocked message can never grow unbounded: at
// most 5 lines per file, 200 bytes per line (bytes, the conservative reading
// of the ≤200-char cap), 2KB total, and ranges deeper than 4000 lines keep
// the plain message instead of paying a huge read for a decoration.
const (
	evidenceDetailMaxLines      = 5
	evidenceDetailMaxLineBytes  = 200
	evidenceDetailMaxTotalBytes = 2048
	evidenceDetailMaxReadLines  = 4000
	evidenceDetailPageLines     = 500
)

// evidenceLineDetail renders the current on-disk content of the missing or
// mismatched lines (tl-1218 ①, experimental_multi_edit_error_detail). The
// file goes through the real read_file so encoding and overlay routing match
// what a model would have seen — the same channel currentFileHashes uses.
// Best effort: any read failure returns "" and the caller keeps the baseline
// message (which never includes source text).
func (a *Agent) evidenceLineDetail(ctx context.Context, check evidenceCheck) string {
	if a == nil || !a.multiEditErrorDetail || check.Path == "" || len(check.Missing) == 0 {
		return ""
	}
	if a.svc.tools == nil {
		return ""
	}
	reader, ok := a.svc.tools.Get("read_file")
	if !ok {
		return ""
	}
	// Wanted line indexes (0-based), deduplicated, in file order, capped at
	// the per-file line bound.
	var wanted []int
	inWanted := map[int]bool{}
	for _, r := range check.Missing {
		for line := r.Start; line < r.End && len(wanted) < evidenceDetailMaxLines; line++ {
			if line < 0 || inWanted[line] {
				continue
			}
			inWanted[line] = true
			wanted = append(wanted, line)
		}
		if len(wanted) >= evidenceDetailMaxLines {
			break
		}
	}
	if len(wanted) == 0 {
		return ""
	}
	text := map[int]string{}
	got := map[int]bool{}
	remaining := len(wanted)
	offset := 0
	for remaining > 0 && offset < evidenceDetailMaxReadLines {
		args, err := json.Marshal(map[string]any{"path": check.Path, "offset": offset, "limit": evidenceDetailPageLines})
		if err != nil {
			return ""
		}
		out, err := reader.Execute(ctx, args)
		if err != nil {
			return ""
		}
		window, ok := tool.ParseReadWindow(out)
		if !ok {
			return ""
		}
		for i, line := range window.Lines {
			idx := offset + i
			if inWanted[idx] && !got[idx] {
				text[idx] = line
				got[idx] = true
				remaining--
			}
		}
		trailer := tool.ParseReadTrailer(out)
		if !trailer.HasMore {
			break
		}
		if trailer.NextOffset <= offset {
			return ""
		}
		offset = trailer.NextOffset
	}
	var b strings.Builder
	total := 0
	count := 0
	for _, idx := range wanted {
		if !got[idx] {
			continue
		}
		line := text[idx]
		if len(line) > evidenceDetailMaxLineBytes {
			line = line[:evidenceDetailMaxLineBytes-3] + "..."
		}
		row := fmt.Sprintf("  L%d: %s\n", idx+1, line)
		if count >= evidenceDetailMaxLines || total+len(row) > evidenceDetailMaxTotalBytes {
			break
		}
		b.WriteString(row)
		total += len(row)
		count++
	}
	if count == 0 {
		return ""
	}
	return "current content of the missing lines (read from disk just now):\n" + strings.TrimRight(b.String(), "\n")
}

func hashLine(line string) string {
	sum := sha256.Sum256([]byte(line))
	return hex.EncodeToString(sum[:])
}

// describeEvidence renders a bounded, actionable summary for the blocked tool
// result; it never includes source text.
func describeEvidence(check evidenceCheck, toolName string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "blocked: [evidence required] %s targets %s, but the model has not seen its current content", toolName, check.Path)
	if len(check.Missing) > 0 {
		b.WriteString(" (missing lines ")
		for i, r := range check.Missing {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "%d-%d", r.Start+1, r.End)
		}
		b.WriteString(")")
	}
	if check.Recovery != "" {
		b.WriteString("; " + check.Recovery)
	}
	return b.String()
}

// applyEvidenceGates blocks a writer whose own declaration says it would
// replace content the model has not seen this turn. It is off unless the run
// enabled it, and it never replaces authorization, sandbox, or uniqueness.
func (a *Agent) applyEvidenceGates(ctx context.Context, plan *toolCallPlan) (toolOutcome, bool) {
	if a == nil || !a.reads.gates || a.task.ledger == nil || a.svc.tools == nil {
		return toolOutcome{}, false
	}
	resolved := plan.execTool
	if resolved == nil {
		resolved, _, _ = a.svc.tools.ResolveCall(plan.call.Name)
	}
	if resolved == nil {
		return toolOutcome{}, false
	}
	if ownsAnchoredEvidence(resolved) {
		// The anchor audit owns this writer, but it keys on the model-visible
		// name: a proxy that reaches the writer under another name must be
		// audited here or both gates skip it.
		if plan.evidenceName != "" && plan.evidenceName != plan.call.Name {
			call := provider.ToolCall{Name: plan.evidenceName, Arguments: string(plan.evidenceArgs)}
			if out, blocked := a.staleAnchorEditBlock(ctx, call); blocked {
				return toolOutcome{output: out, blocked: true, errMsg: "blocked: fresh read required"}, true
			}
		}
		return toolOutcome{}, false
	}
	boundary := a.evidenceObservationBoundary(ctx)
	call := plan.call
	if plan.evidenceName != "" {
		call.Name, call.Arguments = plan.evidenceName, string(plan.evidenceArgs)
	}
	check, memoized := a.turn.evidenceBlocked.memoizedCheck(call, boundary)
	if !memoized || plan.evidenceName != plan.call.Name {
		check = a.checkOperationEvidence(ctx, call, resolved, boundary)
		if plan.evidenceName == plan.call.Name {
			a.turn.evidenceBlocked.memoCheck(call, boundary, check)
		}
	}
	switch {
	case check.NativeError != nil:
		a.recordRepeatFailure(call, resolved, check.NativeError)
		return a.blockedEvidenceOutcome(ctx, check, call), true
	case check.Satisfied:
		plan.expectedWriteSource = check.Target
		return toolOutcome{}, false
	case !check.Supported:
		return a.blockUndeclaredWriter(ctx, plan, call, resolved, boundary)
	}
	a.turn.evidenceBlocked.record(check, call, boundary)
	return a.blockedEvidenceOutcome(ctx, check, call), true
}

// blockUndeclaredWriter keeps a writer that cannot name what it changes from
// becoming the way around another writer's outstanding read requirement. The
// rejection is bounded like every other one: an opaque writer that keeps
// hitting it converges on the user instead of costing a provider round each
// time.
func (a *Agent) blockUndeclaredWriter(ctx context.Context, plan *toolCallPlan, call provider.ToolCall, resolved tool.Tool, boundary uint64) (toolOutcome, bool) {
	if !evidence.ClassifyToolCall(call.Name, json.RawMessage(call.Arguments), plan.readOnly || resolved.ReadOnly()).ContentMutation {
		return toolOutcome{}, false
	}
	outstanding := a.outstandingReadEvidence(ctx, boundary)
	if (call.Name == "bash" || call.Name == "shell") && !toolHooksMayMutateWorkspace(a.svc.hooks) {
		outstanding = a.narrowToDeclaredShellWrites(call, outstanding)
	}
	// tl-1218 ② narrow mode: an outstanding requirement only blocks calls
	// whose provable write scope involves that file. An unrelated call
	// (cross-session messaging etc.) passes instead of riding the collective
	// block. Off (default) keeps the baseline narrowing above, byte-identical.
	if a.multiEditEvidenceNarrow && len(outstanding) > 0 {
		if touches, known := a.evidenceCallScope(call, outstanding); known && !touches {
			return toolOutcome{}, false
		}
	}
	if len(outstanding) == 0 || plan.readOnly {
		return toolOutcome{}, false
	}
	// Task 789 relaxed mode: an outstanding (often frozen-strict) read
	// requirement no longer hard-blocks a bash/shell write — "edit one line of
	// a throwaway script" must not force a whole-file rewrite through
	// write_file. The block degrades to a host-side warning; the hard gate on
	// the dedicated file tools is unchanged.
	if a.editEvidenceRelaxed && (call.Name == "bash" || call.Name == "shell") {
		a.warnRelaxedShellWrite(call, outstanding)
		return toolOutcome{}, false
	}
	msg := fmt.Sprintf("blocked: [evidence required] %s cannot declare which files it changes while a read-evidence requirement is outstanding (%s); use the exact file tool for those paths",
		plan.call.Name, strings.Join(outstanding, ", "))
	d := &tool.OperationDiagnostic{Code: tool.WriteEvidenceMissing, Path: outstanding[0], Recovery: "declare the exact write paths or use the dedicated file tool"}
	a.noteOperationFailure(plan.operationID(), d.Code, d)
	if recovery := d.ModelFacing(); recovery != "" {
		msg += "\n" + recovery
	}
	return toolOutcome{output: msg, blocked: true, errMsg: firstLine(msg), diagnostic: d}, true
}

// narrowToDeclaredShellWrites drops requirements a statically analyzable shell
// command provably does not touch. An unanalyzable command keeps them all.
func (a *Agent) narrowToDeclaredShellWrites(call provider.ToolCall, outstanding []string) []string {
	var args struct {
		Command string `json:"command"`
	}
	if json.Unmarshal([]byte(call.Arguments), &args) != nil {
		return outstanding
	}
	paths, known := shellsafe.StaticWritePaths(args.Command)
	if !known {
		return outstanding
	}
	return slices.DeleteFunc(outstanding, func(path string) bool {
		for _, candidate := range paths {
			if evidencePathsOverlap(resolveMaybeRelative(a.writeWorkspaceRoot, candidate), path) {
				return false
			}
		}
		return true
	})
}

func evidencePathsOverlap(left, right string) bool {
	l, le := sandbox.ResolveAbsPath(left)
	r, re := sandbox.ResolveAbsPath(right)
	if le != nil || re != nil || l == r {
		return true
	}
	li, le := os.Stat(l)
	ri, re := os.Stat(r)
	return le == nil && re == nil && os.SameFile(li, ri)
}

// evidenceCallScope classifies one call against the outstanding requirement
// paths for the tl-1218 ② narrow mode:
//
//	known=true,  touches=false → provably unrelated, passes
//	known=true,  touches=true  → involves an outstanding file, stays blocked
//	known=false                → scope unprovable (unanalyzable shell, failed
//	                             declaration), keeps the collective block
//
// Scope proof: shell commands go through shellsafe.StaticWritePaths (an
// unanalyzable command is unprovable); writers implementing
// tool.EvidenceDeclarer name their exact target; every other call (cross-
// session messaging, unknown MCP tools, read-only helpers) is classified as
// unrelated — the outstanding requirement protects one file's evidence, and
// the evidence gate still re-verifies the actual write against current
// content whenever that file is written again.
func (a *Agent) evidenceCallScope(call provider.ToolCall, outstanding []string) (touches, known bool) {
	if a == nil || a.svc.tools == nil {
		return false, false
	}
	name := call.Name
	if name == "bash" || name == "shell" {
		var args struct {
			Command string `json:"command"`
		}
		if json.Unmarshal([]byte(call.Arguments), &args) != nil {
			return false, false
		}
		paths, analyzable := shellsafe.StaticWritePaths(args.Command)
		if !analyzable {
			return false, false
		}
		return pathsOverlapAny(a.writeWorkspaceRoot, paths, outstanding), true
	}
	resolved, _, ambiguous := a.svc.tools.ResolveCall(name)
	if resolved == nil || len(ambiguous) > 0 {
		return false, false
	}
	declarer, ok := resolved.(tool.EvidenceDeclarer)
	if !ok {
		return false, true
	}
	info, err := declarer.DeclareEvidenceTarget(context.Background(), json.RawMessage(call.Arguments))
	if err != nil || info.Path == "" {
		return false, false
	}
	return pathsOverlapAny(a.writeWorkspaceRoot, []string{info.Path}, outstanding), true
}

// pathsOverlapAny reports whether any candidate path overlaps any outstanding
// requirement path (shell candidates resolve against the workspace root first,
// matching narrowToDeclaredShellWrites).
func pathsOverlapAny(workspaceRoot string, candidates, outstanding []string) bool {
	for _, candidate := range candidates {
		resolved := resolveMaybeRelative(workspaceRoot, candidate)
		for _, path := range outstanding {
			if evidencePathsOverlap(resolved, path) {
				return true
			}
		}
	}
	return false
}

// evidenceNarrowCascadePass reports whether the tl-1218 ② narrow mode lets
// this call through the evidence-only mutation barrier: only writes whose
// provable scope involves an outstanding requirement's file stay blocked.
// The switch off (default) always returns false — the baseline collective
// block stands byte-identical.
func (a *Agent) evidenceNarrowCascadePass(call provider.ToolCall) bool {
	if a == nil || !a.multiEditEvidenceNarrow {
		return false
	}
	outstanding := a.turn.evidenceBlocked.snapshot()
	if len(outstanding) == 0 {
		return true
	}
	touches, known := a.evidenceCallScope(call, outstanding)
	return known && !touches
}

// recordRebuildAuthorization captures the files an AllowRebuild instruction
// named, so later writes authorize by membership instead of re-parsing text.
// Only the turn that owns the user's instruction records it; a sub-agent's
// input is the model's own task prompt, so it may only inherit a host set.
func (a *Agent) recordRebuildAuthorization() {
	if a == nil || !a.turn.constraints.AllowRebuild || len(a.turn.constraints.RebuildPaths) > 0 {
		return
	}
	if a.classifierTaskText != "" {
		return
	}
	a.turn.constraints.RebuildPaths = runtimepolicy.ParseRebuildPaths(a.turn.turnInput, a.writeWorkspaceRoot)
}

// rebuildAuthorized reports whether the user's own instruction explicitly asked
// to rewrite this file. It is a membership test over the host-recorded set; the
// model can never grant the waiver through prompt text.
func (a *Agent) rebuildAuthorized(path string) bool {
	if a == nil || !a.turn.constraints.AllowRebuild {
		return false
	}
	return slices.Contains(a.turn.constraints.RebuildPaths, filepath.Clean(path))
}

// preflightEvidenceBatch evaluates every writer's declared evidence once for
// the batch. A blocked call is reported without ever starting, so a batch that
// mixes a read with a write cannot let the read pay for the write. Relaxed
// mode (task 789) keeps the reconciliation and the memo but skips the verdict:
// interception moves to the execution-time gate, where a read that already ran
// earlier in this same batch can pay for the write — a batch without such a
// read is still blocked there, with the identical diagnostic.
func (a *Agent) preflightEvidenceBatch(ctx context.Context, calls []provider.ToolCall) map[int]toolOutcome {
	blocked := map[int]toolOutcome{}
	if a == nil || !a.reads.gates || a.task.ledger == nil || a.svc.tools == nil {
		return blocked
	}
	boundary := a.evidenceObservationBoundary(ctx)
	a.outstandingReadEvidence(ctx, boundary)
	for i, call := range calls {
		resolved, _, ambiguous := a.svc.tools.ResolveCall(call.Name)
		if resolved == nil || len(ambiguous) > 0 || resolved.ReadOnly() {
			continue
		}
		if ownsAnchoredEvidence(resolved) {
			// The anchor safety audit owns this writer.
			continue
		}
		check := a.checkOperationEvidence(ctx, call, resolved, boundary)
		a.turn.evidenceBlocked.memoCheck(call, boundary, check)
		if !check.Supported || check.Satisfied || check.NativeError != nil {
			continue
		}
		if a.editEvidenceRelaxed {
			continue
		}
		a.turn.evidenceBlocked.record(check, call, boundary)
		blocked[i] = a.blockedEvidenceOutcome(ctx, check, call)
	}
	return blocked
}

// blockedEvidenceOutcome renders one rejection. The operation ID is derived
// from what the call targets, not from the provider's per-round call ID, so
// the same rejected edit is recognizable when the model sends it again.
// tl-1218 ①: with the error-detail switch lit, the message additionally
// carries the current on-disk content of the missing/mismatched lines
// (bounded — see evidenceLineDetail); off keeps the copy byte-identical.
func (a *Agent) blockedEvidenceOutcome(ctx context.Context, check evidenceCheck, call provider.ToolCall) toolOutcome {
	code := tool.WriteEvidenceMissing
	if check.Reason == "stale_or_partial_evidence" {
		code = tool.WriteEvidenceStale
	}
	operationID := evidence.OperationID(call.Name, json.RawMessage(call.Arguments))
	d := &tool.OperationDiagnostic{Code: code, Path: check.Path, ActualSnapshot: check.Target.Snapshot, RequiredRanges: slices.Clone(check.Missing), Recovery: check.Recovery}
	if check.Diagnostic != nil {
		copy := *check.Diagnostic
		d = &copy
	}
	if ops := a.operations(); ops != nil {
		ops.Open(operationID, call.Name, []string{check.Path})
	}
	a.noteOperationFailure(operationID, d.Code, d)
	msg := describeEvidence(check, call.Name)
	if check.NativeError != nil {
		msg = "error: " + check.NativeError.Error()
	}
	if detail := a.evidenceLineDetail(ctx, check); detail != "" {
		msg += "\n" + detail
	}
	if recovery := d.ModelFacing(); recovery != "" {
		msg += "\n" + recovery
	}
	return toolOutcome{output: msg, blocked: true, errMsg: firstLine(msg), diagnostic: d}
}

// warnRelaxedShellWrite records the downgraded block (task 789 relaxed mode)
// as a host-side warning: a slog line plus a content-free audit counter. The
// call itself proceeds — this never touches the model-visible output.
func (a *Agent) warnRelaxedShellWrite(call provider.ToolCall, outstanding []string) {
	operationID := evidence.OperationID(call.Name, json.RawMessage(call.Arguments))
	slog.Warn("agent: relaxed evidence gate allowed a shell write with an outstanding read requirement (task 789)",
		"tool", call.Name, "outstanding", len(outstanding))
	event.RecordOperationAudit(a.svc.sink, evidence.OperationAudit{
		Metric:      evidence.MetricEvidenceRelaxedShellWrite,
		OperationID: operationID,
		Tool:        call.Name,
	})
}

func ownsAnchoredEvidence(target tool.Tool) bool {
	if wrapped, ok := target.(pathBoundWriter); ok {
		return ownsAnchoredEvidence(wrapped.inner)
	}
	_, ok := target.(tool.AnchoredTextTarget)
	return ok
}
