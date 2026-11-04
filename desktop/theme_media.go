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
	mediaExpectedCount = 5 // release gate: all embedded media presets (tl1385 trimmed 8→5)
	mediaThemeIDPrefix = "media-"
)

// Design ledger for the tl1362 set (皮皮QAQ 公众号排版笔记, exact parameters
// from the 2026-11-03 user-verified collection: the user logged into Xiaohongshu,
// read each note's parameter card and transcribed them; the sheet lives in the
// workspace under docs/report/主题风格参数-皮皮QAQ-20261101.md). All four carry
// tier-A sourced parameters now; where the source names a style without a hex
// (noted below), the token fills in with the brand-standard / faithful
// approximation and the dark variants remain our own design work.
//
// tl1385 trimmed the set from eight to five presets: no47 muji, no52 natgeo
// and no42 apple leaned on structural typography (letter-spacing, pills,
// layout hierarchy) that the Theme Pack schema cannot express, so shipping
// them as token-only skins misadvertised the style. Their ledger entries
// retired with the directories; the surviving four keep their typography
// notes for future schema work:
//
//	no44 cream   16px/1.8 justified · H1 25px centered bold · H2 18px bold on a
//	             soft-apricot pill · H3 17px orange-brown + warm dot · captions
//	             13px · quote = white card + apricot dashed rounded border ·
//	             emphasis = orange-brown bold (source gave no accent hex; sheet
//	             kills the earlier #A5A233 guess → filled #A96426)
//	no48 econ    16px/1.75 · fg #2B2B29 (sourced) · H2 18px extra-bold, red
//	             01/02 numerals, pale hairline below · pull-quote 22px bold +
//	             red left rule, no fill · red = rules only, never a fill
//	             (Swiss style): accent bar above headings, red index, quote rule
//	no46 ikea    IKEA blue #0058A3 (sourced) writes, IKEA yellow #FFDA1A
//	             (sourced) only paints blocks/rules, never words · H1 24px
//	             extra-bold blue over a 4px yellow rule · H2 numbered chips,
//	             yellow fill + blue 01/02 · emphasis = bold + blue, never red ·
//	             the yellow block surfaces as sidebar #FCEDA8 (paper-tone step
//	             of #FFDA1A; full-strength yellow stays a rule color)
//	no36 forest  warm beige sheet, deep coffee-brown body (lower contrast for
//	             long reads) · 16px/1.8 + 20px paragraph spacing · H2 20px
//	             white on pea-green pill with big side radii (bookmark feel) ·
//	             wood-tone left bar on subheads · pull-quote on pale green
//	             rounded fill + thin green dashed ring · emphasis = bold, deep
//	             forest green
//
// Dark variants are our own design work for every theme (the source covers are
// light-only): brand hue kept on a darkened inversion of the light sheet,
// ~#E8E6E3-tier body text, accents brightened one step when too dark for a
// dark ground (economist #E3120B → #FF5566) or muted when too vivid (IKEA blue
// #0058A3 → #5BA3D9, yellow via warn #D9A400).

// Fixed gallery / list order (not alphabetical). Broadsheet Rose (tl1343
// seed) first, then the tl1362 sheet order.
var mediaThemeOrderFixed = []string{
	"media-broadsheet-rose",
	"media-no44-cream",
	"media-no48-economist",
	"media-no46-ikea",
	"media-no36-forest",
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
