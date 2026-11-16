package control

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// ExternalFolderRefLocalPath resolves a registered external-folder token path
// to the local filesystem path authorized for this controller session.
func (c *Controller) ExternalFolderRefLocalPath(tokenPath string) (path, displayPath string, ok bool) {
	_, rel, abs, ok := c.externalFolderRefTarget(tokenPath)
	if !ok {
		return "", "", false
	}
	path, ok = canonicalExternalFolderPath(abs, filepath.Join(abs, filepath.FromSlash(rel)))
	if !ok {
		return "", "", false
	}
	return path, externalFolderDisplayPath(abs, rel), true
}

// AuthorizedExternalFolderLocalPath validates an absolute path against the
// directories explicitly registered by the user for this controller session.
func (c *Controller) AuthorizedExternalFolderLocalPath(path string) (string, bool) {
	if c == nil || strings.TrimSpace(path) == "" {
		return "", false
	}
	c.externalFolderRefsMu.RLock()
	roots := make([]string, 0, len(c.externalFolderRefs))
	for _, root := range c.externalFolderRefs {
		roots = append(roots, root)
	}
	c.externalFolderRefsMu.RUnlock()
	for _, root := range roots {
		if canonical, ok := canonicalExternalFolderPath(root, path); ok {
			return canonical, true
		}
	}
	return "", false
}

func canonicalExternalFolderPath(root, candidate string) (string, bool) {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", false
	}
	realCandidate, err := evalSymlinksReattachMissing(candidate)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(realRoot, realCandidate)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", false
	}
	return filepath.Clean(realCandidate), true
}

// evalSymlinksReattachMissing resolves all existing ancestors but permits a
// not-yet-created leaf: a dangling final symlink (or missing leaf) is treated
// as missing and the walk climbs, re-attaching the unresolved tail. That
// lenient edge is exactly why this is NOT the same function as
// config/path_access.go's evalSymlinksStrictForWrite — the two were one
// same-named pair whose boundary semantics had forked (tl1617 双实现显式双
// 命名): the config one errors on a dangling leaf so a project config write
// can never punch through it; this one re-attaches, which is correct for the
// display/open-path flows here. Either behavior is fine for its own caller,
// sharing a name was the bug.
func evalSymlinksReattachMissing(path string) (string, error) {
	path = filepath.Clean(path)
	current := path
	missing := make([]string, 0, 2)
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			for _, component := range slices.Backward(missing) {
				resolved = filepath.Join(resolved, component)
			}
			return filepath.Clean(resolved), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", err
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}
