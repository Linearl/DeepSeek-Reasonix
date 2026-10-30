package builtin

// sanitize.go — the single entry choke point for model-controlled path and URL
// arguments across the built-in tools (task 1276).
//
// Every builtin tool argument that names a filesystem path or a fetch URL must
// pass through one of the helpers in this file before any I/O. The helpers
// consolidate the logic every tool used to repeat ad hoc: workspace-relative
// resolution (resolveIn / resolveReadablePath) followed by filepath.Clean, and
// — for the write family — the workspace boundary check (confine /
// confinePreview, see confine.go) on the cleaned value.
//
// filepath.Clean here is load-bearing beyond hygiene: CodeQL's go/path-injection
// query models a filepath.Clean result as the standard Go taint barrier, so
// funneling every tool path entry through this file is what keeps new alerts
// suppressed for future tool code (the historical 150 tainted-path findings
// were dismissed per-fingerprint as won't fix; this closes the pattern at the
// source instead). Adding a tool? Route its path/URL argument through a helper
// here and run the matching confine check — do not call resolveIn directly;
// sanitize_test.go fails a build that reintroduces a bypass.
//
// Read-side sandbox enforcement (confineRead deny roots + sensitive-file
// denylist) stays at each call site because the denial rendering is
// tool-specific (read_file synthesizes a not-found, ls renders an empty
// directory, glob filters per match); the helpers guarantee those checks
// always see the same cleaned path. Write-side enforcement is folded into
// sanitizeWriteTarget / sanitizePreviewTarget so Execute and Preview cannot
// drift apart.

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"

	"reasonix/internal/sandbox"
)

// sanitizePathArg normalizes a raw model-supplied path argument: a relative
// arg resolves against workDir (see resolveIn) and the result passes through
// filepath.Clean so traversal segments and redundant separators collapse into
// a canonical form. Absolute args are honored verbatim per the existing
// contract — the boundary itself is enforced by the confine family, not here —
// but they still come out cleaned. Tools without a per-tool confine check
// (worktree roots, write-access declarations) use this directly.
func sanitizePathArg(workDir, p string) string {
	if workDir == "" && p == "" {
		return p // preserve the historical empty-in/empty-out for process-cwd tools
	}
	return filepath.Clean(resolveIn(workDir, p))
}

// sanitizeReadPath is the read-family entry (read_file, ls, glob, grep,
// code_index, view_image): resolve into the workspace, clean, and keep the
// ResolvedPath display fields intact (external alias display must stay in its
// token/slash form, so only Path is cleaned). Pair the result with confineRead
// at the call site; see the file comment for why the deny check stays there.
func sanitizeReadPath(workDir, arg string, paths *PathResolver) ResolvedPath {
	rp := resolveReadablePath(workDir, arg, paths)
	rp.Path = filepath.Clean(rp.Path)
	return rp
}

// sanitizeWriteTarget is the write-family Execute entry (write_file, edit_file,
// multi_edit, move_file, notebook_edit, delete_range, delete_symbol,
// screenshot): resolve + clean (sanitizePathArg) and then run the full write
// boundary — workspace confinement, session-data guard, and the managed-config
// approval escape hatch — in one place (see confineWrite).
func sanitizeWriteTarget(ctx context.Context, workDir, arg string, rootSet *sandbox.WritableRootSet, roots []string, guard SessionDataGuard, managed ManagedConfigPaths) (string, error) {
	target := sanitizePathArg(workDir, arg)
	if err := confineWrite(ctx, effectiveWriteRoots(ctx, rootSet, roots), guard, managed, target); err != nil {
		return "", err
	}
	return target, nil
}

// sanitizePreviewTarget is the write-family Preview/DeclareEvidenceTarget
// entry. It is deliberately stricter than sanitizeWriteTarget: previews run
// before the permission gate and read old content into approval cards, so the
// managed-config approval escape hatch must not make targets readable here
// (see confinePreview).
func sanitizePreviewTarget(ctx context.Context, workDir, arg string, rootSet *sandbox.WritableRootSet, roots []string, guard SessionDataGuard, managed ManagedConfigPaths) (string, error) {
	target := sanitizePathArg(workDir, arg)
	if err := confinePreview(effectiveWriteRoots(ctx, rootSet, roots), guard, managed, target); err != nil {
		return "", err
	}
	return target, nil
}

// sanitizeFetchURL is the URL-family entry (web_fetch): parse a raw
// model-supplied fetch address and enforce the fetch policy — absolute http(s)
// only, so file:, custom schemes, and relative references never reach the
// request builder. The returned *url.URL is the sanitized value downstream
// requests must be built from; the dial itself keeps its SSRF IP guard
// (blockedFetchIP) which scheme parsing cannot replace.
func sanitizeFetchURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("url must be an absolute http(s) address")
	}
	return u, nil
}
