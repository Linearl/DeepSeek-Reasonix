package builtin

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"

	fileenc "reasonix/internal/fileutil/encoding"
	"reasonix/internal/tool"
)

// editSource is the file state a read-modify-write tool works against, plus the
// route its write must take back. Read and write must stay paired: content read
// from the host's unsaved buffer has to return there, and content read from disk
// has to return to disk. Mixing the two silently drops one side's changes.
type editSource struct {
	content string
	enc     fileenc.Kind
	overlay bool
	id      fileIdentity
}

// readEditSource resolves path the way Execute and Preview must both see it:
// the host's unsaved editor buffer when the overlay can serve it, otherwise the
// decoded disk content. A non-UTF-8 file always stays on the disk route — the
// overlay contract is text-only, so routing GBK or UTF-16 through it would
// rewrite the file as UTF-8.
func readEditSource(ctx context.Context, overlay FileOverlay, path string) (source editSource, readErr error) {
	defer func() {
		if readErr != nil {
			if expected, ok := tool.ExpectedWriteSource(ctx); ok && expected.Path == path && !expected.Absent && os.IsNotExist(readErr) {
				readErr = &tool.OperationError{Diagnostic: tool.OperationDiagnostic{Code: tool.WriteEvidenceStale, Path: path, ExpectedSnapshot: expected.Snapshot, Recovery: "the expected source disappeared; re-read before retrying"}, Cause: ErrFileChanged}
				return
			}
			return
		}
		if expected, ok := tool.ExpectedWriteSource(ctx); ok && expected.Path == path {
			if expected.Absent || (expected.SourceTextDigest != "" && expected.SourceTextDigest != digestText(source.content)) || (expected.Snapshot != "" && expected.Snapshot != source.readSnapshot(path)) {
				readErr = &tool.OperationError{Diagnostic: tool.OperationDiagnostic{Code: tool.WriteEvidenceStale, Path: path, ExpectedSnapshot: expected.Snapshot, ActualSnapshot: source.readSnapshot(path), RequiredRanges: expected.Ranges, Recovery: "re-read the file, then retry this operation"}, Cause: fmt.Errorf("%w: source differs from the read-evidence preflight", ErrFileChanged)}
			}
		}
	}()
	id, err := diskIdentity(path)
	if err != nil {
		return editSource{}, err
	}
	if !id.existed {
		if overlay != nil && filepath.IsAbs(path) {
			if buffered, ok := overlay.ReadTextFile(ctx, path); ok {
				return editSource{content: buffered, enc: fileenc.UTF8, overlay: true, id: overlayIdentity(buffered)}, nil
			}
		}
		return editSource{enc: fileenc.UTF8, id: id}, &os.PathError{Op: "read", Path: path, Err: os.ErrNotExist}
	}
	content, enc, err := readFileEncoded(path)
	if err != nil {
		return editSource{}, err
	}
	if overlay != nil && enc == fileenc.UTF8 && filepath.IsAbs(path) {
		if buffered, ok := overlay.ReadTextFile(ctx, path); ok {
			return editSource{content: buffered, enc: enc, overlay: true, id: overlayIdentity(buffered)}, nil
		}
	}
	return editSource{content: content, enc: enc, id: id}, nil
}

func (s editSource) readSnapshot(path string) string {
	kind, prefix := tool.ReadSourceDisk, "raw-sha256:"
	if s.overlay {
		kind, prefix = tool.ReadSourceOverlay, "overlay:"
	}
	return tool.SourceSnapshot(kind, path, fmt.Sprintf("%s%x", prefix, s.id.sum))
}

// postWriteSnapshot derives the content version the file carries after a
// successful write of content on this source's route — the same derivation a
// follow-up edit's evidence declaration computes when it re-reads the file
// (editSource.readSnapshot). readBack (task 603) stamps the read-back window
// with it, so the window matches that edit's evidence target exactly: any
// change anywhere else still misses the match and keeps the stale rejection.
func (s editSource) postWriteSnapshot(path, content string) string {
	if s.overlay {
		return tool.SourceSnapshot(tool.ReadSourceOverlay, path, fmt.Sprintf("overlay:%x", sha256.Sum256([]byte(content))))
	}
	return tool.SourceSnapshot(tool.ReadSourceDisk, path, fmt.Sprintf("raw-sha256:%x", sha256.Sum256(fileenc.Encode(content, s.enc))))
}

// write persists content on the same route the source was read from. An overlay
// that declines a managed write leaves its outcome unknown. Standalone calls
// without durable recovery retain the legacy disk fallback.
func (s editSource) write(ctx context.Context, overlay FileOverlay, path, content string) error {
	if err := s.assertUnchanged(ctx, overlay, path); err != nil {
		return err
	}
	if s.overlay && overlay != nil {
		if err := s.recordWrite(ctx, path, content, "overlay", overlay); err != nil {
			return err
		}
		if err := s.assertUnchanged(ctx, overlay, path); err != nil {
			return err
		}
		if ok, err := overlay.WriteTextFile(ctx, path, content); ok {
			if err != nil && tool.HasWriteIntentHook(ctx) {
				return fmt.Errorf("write outcome unknown: %w", err)
			}
			return err
		}
		if tool.HasWriteIntentHook(ctx) {
			return fmt.Errorf("write outcome unknown: original overlay did not confirm the write")
		}
	}
	if err := s.recordWrite(ctx, path, content, "disk", overlay); err != nil {
		return err
	}
	if err := s.assertUnchanged(ctx, overlay, path); err != nil {
		return err
	}
	return writeFileEncoded(path, content, s.enc)
}
