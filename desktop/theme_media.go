package main

import (
	"embed"
	"fmt"
	"sort"
	"sync"
)

// Media themes are read-only preset packs embedded into the desktop binary.
// They are the third gallery group beside base directions and official themes:
// an editorial / media surface-style set (tl1343 Broadsheet Rose; the tl1362
// style research feeds future entries here). Unlike official themes they are
// token-only — no background/preview images — so the loader reuses the Theme
// Pack manifest validator without the official image budget gates.
//
// Fail-closed contract (mirrors theme_official.go): an invalid media entry is
// dropped from the registry (never listed, never activatable) without
// affecting startup; the build-time test in theme_media_test.go fails long
// before that can ship.

//go:embed themes/media
var mediaThemesFS embed.FS

const (
	mediaThemeDirName  = "themes/media"
	themeKindMedia     = "media"
	mediaExpectedCount = 1 // release gate: all embedded media presets
	mediaThemeIDPrefix = "media-"
)

// Fixed gallery / list order (not alphabetical).
var mediaThemeOrderFixed = []string{
	"media-broadsheet-rose",
}

type mediaTheme struct {
	manifest ThemePackManifest
}

var (
	mediaOnce     sync.Once
	mediaRegistry map[string]*mediaTheme
	mediaOrder    []string
	mediaLoadErr  error
)

// loadMediaRegistry parses and validates every embedded media theme once.
// Invalid entries are skipped (fail-closed); the first error is remembered
// for diagnostics and tests.
func loadMediaRegistry() {
	mediaRegistry = map[string]*mediaTheme{}
	mediaOrder = nil
	entries, err := mediaThemesFS.ReadDir(mediaThemeDirName)
	if err != nil {
		mediaLoadErr = fmt.Errorf("read media themes: %w", err)
		return
	}
	var firstErr error
	remember := func(err error) {
		if firstErr == nil {
			firstErr = err
		}
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		id := e.Name()
		mt, err := loadMediaTheme(id)
		if err != nil {
			remember(fmt.Errorf("media theme %q: %w", id, err))
			continue
		}
		if _, dup := mediaRegistry[mt.manifest.ID]; dup {
			remember(fmt.Errorf("media theme id %q is duplicated", mt.manifest.ID))
			continue
		}
		mediaRegistry[mt.manifest.ID] = mt
		mediaOrder = append(mediaOrder, mt.manifest.ID)
	}
	// Prefer the explicit product order; append any unexpected extras last.
	ordered := make([]string, 0, len(mediaRegistry))
	seen := map[string]bool{}
	for _, id := range mediaThemeOrderFixed {
		if _, ok := mediaRegistry[id]; ok {
			ordered = append(ordered, id)
			seen[id] = true
		}
	}
	var extras []string
	for id := range mediaRegistry {
		if !seen[id] {
			extras = append(extras, id)
		}
	}
	sort.Strings(extras)
	mediaOrder = append(ordered, extras...)
	mediaLoadErr = firstErr
}

func loadMediaTheme(dirID string) (*mediaTheme, error) {
	if !themePackIDRe.MatchString(dirID) {
		return nil, fmt.Errorf("invalid directory name")
	}
	if !hasMediaThemeIDPrefix(dirID) {
		return nil, fmt.Errorf("media theme id %q must carry the %s prefix", dirID, mediaThemeIDPrefix)
	}
	raw, err := mediaThemesFS.ReadFile(mediaThemeDirName + "/" + dirID + "/" + themePackManifestName)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	m, err := parseThemePackManifest(raw)
	if err != nil {
		return nil, err
	}
	if m.ID != dirID {
		return nil, fmt.Errorf("manifest id %q does not match directory %q", m.ID, dirID)
	}
	if isBuiltinThemeID(m.ID) {
		return nil, fmt.Errorf("media theme id %q collides with a base style", m.ID)
	}
	if isOfficialThemeID(m.ID) {
		return nil, fmt.Errorf("media theme id %q collides with an official theme", m.ID)
	}
	if m.Background != nil || m.TaskBackground != nil {
		return nil, fmt.Errorf("media themes are token-only; background images are not allowed")
	}
	return &mediaTheme{manifest: *m}, nil
}

func hasMediaThemeIDPrefix(id string) bool {
	return len(id) > len(mediaThemeIDPrefix) && id[:len(mediaThemeIDPrefix)] == mediaThemeIDPrefix
}

func mediaThemes() []*mediaTheme {
	mediaOnce.Do(loadMediaRegistry)
	out := make([]*mediaTheme, 0, len(mediaOrder))
	for _, id := range mediaOrder {
		out = append(out, mediaRegistry[id])
	}
	return out
}

func findMediaTheme(id string) *mediaTheme {
	mediaOnce.Do(loadMediaRegistry)
	return mediaRegistry[id]
}

func isMediaThemeID(id string) bool {
	return findMediaTheme(id) != nil
}

// validateMediaThemes gates the build: every embedded entry must parse through
// the Theme Pack validator and the release set must be complete.
func validateMediaThemes() error {
	mediaOnce.Do(loadMediaRegistry)
	if mediaLoadErr != nil {
		return mediaLoadErr
	}
	if len(mediaOrder) != mediaExpectedCount {
		return fmt.Errorf("expected %d media themes, found %d", mediaExpectedCount, len(mediaOrder))
	}
	return nil
}

// resetMediaRegistryForTest clears the cached registry (tests only).
func resetMediaRegistryForTest() {
	mediaOnce = sync.Once{}
	mediaRegistry = nil
	mediaOrder = nil
	mediaLoadErr = nil
}
