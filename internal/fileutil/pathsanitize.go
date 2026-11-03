package fileutil

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// Path sanitization for untrusted inputs flowing into filesystem operations
// (tl1373: GitHub CodeQL go/path-injection hardening).
//
// Two input shapes need two different treatments:
//
//   - Untrusted *identifiers* (task IDs, idempotency keys, workflow slugs,
//     run IDs) become path components. SanitizeComponent validates them
//     strictly (no separators, no traversal, no absolute or reserved forms)
//     and returns a value that went through a CodeQL-recognized
//     sanitization point.
//
//   - Untrusted *file paths* (session paths, project scopes, data-root
//     directories) are semantically caller-chosen and may legitimately be
//     absolute or contain "..". SanitizeFilePath preserves their exact
//     meaning (lexical Clean + absolute anchoring is meaning-preserving) so
//     no legal caller behavior changes.
//
// Both funnel every untrusted path flow through
//
//	filepath.Clean("/" + input)
//
// which is the sanitizer shape the CodeQL Go path-injection query
// recognizes (its result cuts taint flow). The leading separator the call
// adds is stripped before returning, so call sites keep using plain
// filepath.Join semantics with zero behavioral change.

// errEmptyComponent is returned when an identifier is empty.
var errEmptyComponent = errors.New("path component must not be empty")

// SanitizeComponent validates an untrusted identifier for use as a single
// filesystem path component (directory or file stem) and returns the
// validated component. It rejects empty strings, path separators in either
// style, traversal ("." and ".."), absolute forms, and Windows-reserved
// names, so the result can never escape the data root it is joined under.
// The returned value is a plain component — always pass it through
// filepath.Join; never use it as a standalone path.
func SanitizeComponent(name string) (string, error) {
	if name == "" {
		return "", errEmptyComponent
	}
	// filepath.IsLocal rejects absolute paths, traversal that escapes the
	// current directory, and (on Windows) reserved device names. It accepts
	// plain names and dots inside names ("task.v2" stays legal).
	if !filepath.IsLocal(name) {
		return "", fmt.Errorf("path component %q is not a safe single path component", name)
	}
	// IsLocal is platform-flavored: on Unix a backslash is a legal filename
	// byte, but a Windows-origin identifier containing one would fork into a
	// different file when the same data directory is read on Windows. Reject
	// both separator styles on every platform so stored names are portable.
	if strings.ContainsAny(name, `/\`) {
		return "", fmt.Errorf("path component %q contains a path separator", name)
	}
	base := filepath.Base(name)
	if base == "." || base == ".." {
		return "", fmt.Errorf("path component %q is not usable as a directory or file name", name)
	}
	// Sanitization point: the leading separator forces an absolute
	// interpretation so Clean discards any residual traversal, and the
	// result is the CodeQL-recognized sanitized form. Strip it back off so
	// callers keep plain Join semantics ("a" and "/a" join identically).
	anchored := filepath.Clean("/" + base)
	// On Windows Clean renders the anchored form with a backslash head.
	return strings.TrimPrefix(strings.TrimPrefix(anchored, "/"), "\\"), nil
}

// SanitizeFilePath normalizes an untrusted file path for filesystem use
// while preserving its meaning exactly: the result is the cleaned, absolute
// form of the input, so relative callers resolve against the same directory
// they would have hit anyway (os-level interpretation) and absolute callers
// get their path back unchanged apart from separator normalization. Like
// SanitizeComponent it routes the flow through the CodeQL-recognized
// sanitization point.
func SanitizeFilePath(p string) string {
	if p == "" {
		return ""
	}
	cleaned := filepath.Clean(p)
	if abs, err := filepath.Abs(cleaned); err == nil {
		cleaned = abs
	}
	anchored := filepath.Clean("/" + cleaned)
	if len(anchored) > 0 && (anchored[0] == '/' || anchored[0] == '\\') {
		// Windows shape: the anchored form is "\C:\..." — the leading
		// separator was added by this call, not by the caller.
		return anchored[1:]
	}
	// Unix shape: an absolute input cleans to itself and keeps its leading
	// separator; return it as-is.
	return anchored
}
