package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"reasonix/internal/sandbox"
	"reasonix/internal/tool"
)

func init() { tool.RegisterBuiltin(editFile{}) }

// editFile replaces an exact string in a file. roots confines the target to the
// workspace when non-empty (see writeFile); guard rejects Reasonix session-data
// targets (see SessionDataGuard); workDir, when non-empty, is the directory a
// relative path resolves against (see resolveIn). readBack, when true (the
// experimental_tool_optimizations family, task 603), lets a call pass readBack
// to get the edited region rendered back with surrounding context and filed as
// fresh read evidence; the zero value keeps the tool byte-identical to the
// pre-family surface.
type editFile struct {
	roots   []string
	rootSet *sandbox.WritableRootSet
	guard   SessionDataGuard
	managed ManagedConfigPaths
	workDir string
	overlay FileOverlay
	readBack bool
}

func (editFile) Name() string { return "edit_file" }

func (e editFile) Description() string {
	return "Replace an exact string in a file with another. old_string must occur exactly once; add surrounding context to disambiguate. Use for targeted edits instead of rewriting the whole file. For replacing or deleting a whole line block you may instead pass line_range (e.g. \"278-292\") with the source_token from your latest read_file; anchor_head/anchor_tail prefixes are recommended so drifted line numbers are rejected instead of editing the wrong block. For multiple edits in one file (同文件多处修改), prefer multi_edit (atomic batch)."
}

func (e editFile) Schema() json.RawMessage {
	readBackField := ""
	if e.readBack {
		readBackField = `,"readBack":{"type":"boolean","description":"Set true to have the result read back the edited region with ±20 surrounding lines. The returned window counts as having read the file: a follow-up edit on this file passes the read-evidence gate without a separate read_file call (any outside change is still caught)."}`
	}
	return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"File path"},"old_string":{"type":"string","description":"Exact text to replace (must be unique in the file). Required unless line_range is used."},"new_string":{"type":"string","description":"Replacement text (may be empty to delete). With line_range, empty deletes the range."},` + expectedContentSchemaField() + `,"source_token":{"type":"string","description":"The source_token printed by the read_file that showed you this file. Citing it names the exact version you are editing, so a change made outside this session is caught instead of silently overwritten. REQUIRED for line_range edits."},"line_range":{"type":"string","description":"Optional line-range variant: \"start-end\" (1-based, inclusive, e.g. \"278-292\"). Replaces those lines with new_string (empty = delete the range); avoids re-emitting the old block. Requires source_token; anchor_head/anchor_tail strongly recommended."},"anchor_head":{"type":"string","description":"With line_range: expected content prefix of the first line in the range. A mismatch rejects the edit instead of touching the wrong block."},"anchor_tail":{"type":"string","description":"With line_range: expected content prefix of the last line in the range."}` + readBackField + `},"required":["path"]}`)
}

func (editFile) ReadOnly() bool { return false }

func (e editFile) DeclareWriteAccess(args json.RawMessage) (tool.WriteAccessDeclaration, error) {
	return declareFilePathWriteAccess(e.workDir, args)
}

func (e editFile) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Path        string `json:"path"`
		OldString   string `json:"old_string"`
		NewString   string `json:"new_string"`
		Expected    string `json:"expected"`
		SourceToken string `json:"source_token"`
		LineRange   string `json:"line_range"`
		AnchorHead  string `json:"anchor_head"`
		AnchorTail  string `json:"anchor_tail"`
		ReadBack    bool   `json:"readBack"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if p.Path == "" {
		return "", fmt.Errorf("path is required")
	}
	if p.LineRange != "" && p.OldString != "" {
		return "", fmt.Errorf("pass either old_string or line_range, not both")
	}
	if p.LineRange == "" && p.OldString == "" {
		return "", fmt.Errorf("old_string is required (or use line_range with source_token for a whole-block replace/delete)")
	}
	if p.LineRange != "" && p.SourceToken == "" {
		return "", fmt.Errorf("line_range edits require the source_token from the read_file that showed you this file; re-read it and cite the token it prints")
	}
	// The readBack parameter only exists while the experimental_tool_optimizations
	// family is lit (task 603). With the family off the field is ignored —
	// identical bytes in, identical bytes out (铁律 2).
	useReadBack := p.ReadBack && e.readBack
	p.Path = resolveIn(e.workDir, p.Path)
	if err := confineWrite(ctx, effectiveWriteRoots(ctx, e.rootSet, e.roots), e.guard, e.managed, p.Path); err != nil {
		return "", err
	}
	if err := checkOptimisticExpected(ctx, e.overlay, p.Path, p.Expected, "edit_file"); err != nil {
		return "", err
	}

	src, err := readEditSource(ctx, e.overlay, p.Path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", p.Path, err)
	}

	if p.LineRange != "" {
		updated, inserted, lerr := applyLineRangeEdit(src.content, p.LineRange, p.AnchorHead, p.AnchorTail, p.NewString)
		if lerr != nil {
			return "", lerr
		}
		if err := src.write(ctx, e.overlay, p.Path, updated); err != nil {
			return "", fmt.Errorf("write %s: %w", p.Path, err)
		}
		verb := "replaced"
		if p.NewString == "" {
			verb = "deleted"
		}
		start, _, _ := parseLineRange(p.LineRange)
		summary := fmt.Sprintf("edited %s (line_range %s %s with %d lines)\n%s", p.Path, p.LineRange, verb, inserted, lineRangeSnippet(updated, start, inserted))
		if useReadBack {
			summary = withReadBack(ctx, summary, p.Path, updated, src.postWriteSnapshot(p.Path, updated), start, start+inserted-1)
		}
		return summary, nil
	}

	applied := applyOldStringEdit(src.content, p.OldString, p.NewString, false)
	switch {
	case applied.applied == 1:
		// ok
	case applied.matches == 0:
		return "", oldStringNotFoundError(p.Path, p.OldString, src.content)
	default:
		return "", oldStringNotUniqueError(p.Path, p.OldString, src.content, applied.matches, false)
	}

	if err := src.write(ctx, e.overlay, p.Path, applied.updated); err != nil {
		return "", fmt.Errorf("write %s: %w", p.Path, err)
	}
	summary := fmt.Sprintf("edited %s", p.Path)
	if applied.fuzzy {
		summary += " (fuzzy match)"
	}
	summary = withActualPostWriteReceipts(summary, []editReplacementReceipt{applied.receipt})
	if useReadBack {
		spanStart, spanEnd := readBackSpanLines(applied.updated, applied.updatedStart, applied.updatedEnd)
		summary = withReadBack(ctx, summary, p.Path, applied.updated, src.postWriteSnapshot(p.Path, applied.updated), spanStart, spanEnd)
	}
	return summary, nil
}

// readBackSpanLines converts the applied replacement's byte span in content to
// a 1-based inclusive line span (0/0 when the span is unset — withReadBack
// then keeps the result untouched).
func readBackSpanLines(content string, start, end int) (int, int) {
	if start < 0 || end < start || end > len(content) {
		return 0, 0
	}
	first := strings.Count(content[:start], "\n") + 1
	last := strings.Count(content[:end], "\n") + 1
	return first, last
}
