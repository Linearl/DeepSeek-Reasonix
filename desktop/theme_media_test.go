package main

import (
	"os"
	"strings"
	"testing"
)

// Release gate: the embedded media presets must parse through the Theme Pack
// validator with unique ids, the media- prefix, and no base/official collision.
func TestMediaThemesAllValid(t *testing.T) {
	resetMediaRegistryForTest()
	if err := validateMediaThemes(); err != nil {
		t.Fatalf("media registry invalid: %v", err)
	}
	themes := mediaThemes()
	if len(themes) != mediaExpectedCount {
		t.Fatalf("expected %d media themes, got %d", mediaExpectedCount, len(themes))
	}
	ids := map[string]bool{}
	names := map[string]bool{}
	for _, mt := range themes {
		m := mt.manifest
		if ids[m.ID] {
			t.Fatalf("duplicate media id %q", m.ID)
		}
		ids[m.ID] = true
		if names[m.Name] {
			t.Fatalf("duplicate media name %q", m.Name)
		}
		names[m.Name] = true
		if !strings.HasPrefix(m.ID, mediaThemeIDPrefix) {
			t.Fatalf("media id %q must carry the %s prefix", m.ID, mediaThemeIDPrefix)
		}
		if isBuiltinThemeID(m.ID) {
			t.Fatalf("media id %q collides with a base style", m.ID)
		}
		if isOfficialThemeID(m.ID) {
			t.Fatalf("media id %q collides with an official theme", m.ID)
		}
		if m.Background != nil || m.TaskBackground != nil {
			t.Fatalf("media theme %q must be token-only", m.ID)
		}
	}
	// The tl1343 seed preset must be present by id.
	if findMediaTheme("media-broadsheet-rose") == nil {
		t.Fatalf("media-broadsheet-rose missing from the media registry")
	}
	// The fixed order must carry exactly the gated set.
	if len(mediaThemeOrderFixed) != mediaExpectedCount {
		t.Fatalf("mediaThemeOrderFixed has %d ids, gate expects %d", len(mediaThemeOrderFixed), mediaExpectedCount)
	}
}

// TestMediaThemeTokenDesign pins the tl1362 design ledger: per-theme light/dark
// anchor tokens (bg both modes, fg both modes, accent) and the corners recipe.
// Values must match desktop/themes/media/<id>/theme.json; the Go validator
// lowercases tokens, so expectations are lowercase. Dark variants are design
// work (tier-independent) — see the ledger comment in theme_media.go.
func TestMediaThemeTokenDesign(t *testing.T) {
	resetMediaRegistryForTest()
	expected := map[string]struct{ lightBg, darkBg, accent, corners string }{
		"media-broadsheet-rose": {"#fff1e5", "#1b1614", "#990f3d", "square"},
		"media-no44-cream":      {"#fff5f0", "#241b15", "#a96426", "soft"},
		"media-no48-economist":  {"#ffffff", "#211a18", "#e3120b", "square"},
		"media-no46-ikea":       {"#ffffff", "#16202b", "#0058a3", "square"},
		"media-no36-forest":     {"#f5f1e6", "#1c231b", "#3d6b3c", "soft"},
	}
	if len(expected) != mediaExpectedCount {
		t.Fatalf("token design table covers %d ids, gate expects %d", len(expected), mediaExpectedCount)
	}
	for _, id := range mediaThemeOrderFixed {
		want, ok := expected[id]
		if !ok {
			t.Fatalf("token design table missing %q", id)
		}
		mt := findMediaTheme(id)
		if mt == nil {
			t.Fatalf("%q missing from the media registry", id)
		}
		m := mt.manifest
		if got := m.Tokens.Light["bg"]; got != want.lightBg {
			t.Fatalf("%s light bg = %q, want %q", id, got, want.lightBg)
		}
		if got := m.Tokens.Dark["bg"]; got != want.darkBg {
			t.Fatalf("%s dark bg = %q, want %q", id, got, want.darkBg)
		}
		if m.Tokens.Light["fg"] == "" || m.Tokens.Dark["fg"] == "" {
			t.Fatalf("%s must carry body text tokens in both modes", id)
		}
		if got := m.Tokens.Light["accent"]; got != want.accent {
			t.Fatalf("%s light accent = %q, want %q", id, got, want.accent)
		}
		if want.darkBg == want.lightBg {
			t.Fatalf("%s dark bg must differ from light bg (dark variants are real designs)", id)
		}
		if m.Recipes.Corners != want.corners {
			t.Fatalf("%s corners = %q, want %q", id, m.Recipes.Corners, want.corners)
		}
	}
}

func TestMediaThemesListedAsMediaKind(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	resetMediaRegistryForTest()
	app := NewApp()
	packs, err := app.ListThemePacks()
	if err != nil {
		t.Fatal(err)
	}
	var media *ThemePackView
	for i := range packs {
		if packs[i].ID == "media-broadsheet-rose" {
			media = &packs[i]
			break
		}
	}
	if media == nil {
		t.Fatalf("media-broadsheet-rose not listed")
	}
	if media.Kind != themeKindMedia {
		t.Fatalf("kind = %q, want %q", media.Kind, themeKindMedia)
	}
	if !media.Builtin {
		t.Fatal("media preset must be builtin")
	}
	if media.NameKey != "settings.themes.media.media-broadsheet-rose.name" ||
		media.DescriptionKey != "settings.themes.media.media-broadsheet-rose.description" {
		t.Fatalf("name/description keys = %q/%q", media.NameKey, media.DescriptionKey)
	}
	if media.HasBackground {
		t.Fatal("token-only media preset must not advertise a background")
	}
	if media.Tokens.Light["bg"] != "#fff1e5" {
		t.Fatalf("light bg = %q, want #fff1e5", media.Tokens.Light["bg"])
	}
	// Every embedded media preset must ride the same list shape.
	listed := 0
	for i := range packs {
		if packs[i].Kind != themeKindMedia {
			continue
		}
		listed++
		if !packs[i].Builtin || packs[i].HasBackground ||
			packs[i].NameKey != "settings.themes.media."+packs[i].ID+".name" {
			t.Fatalf("media preset %q listed with wrong shape: %+v", packs[i].ID, packs[i])
		}
	}
	if listed != mediaExpectedCount {
		t.Fatalf("listed %d media presets, want %d", listed, mediaExpectedCount)
	}
}

func TestActivateMediaThemePack(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	resetMediaRegistryForTest()
	app := NewApp()
	if err := app.ActivateThemePack("media-broadsheet-rose"); err != nil {
		t.Fatal(err)
	}
	exp, err := app.GetThemeExperience()
	if err != nil {
		t.Fatal(err)
	}
	if exp.ActiveThemeID != "media-broadsheet-rose" {
		t.Fatalf("activeThemeId = %q", exp.ActiveThemeID)
	}
	if exp.ActivePack == nil || exp.ActivePack.Kind != themeKindMedia {
		t.Fatalf("active pack kind = %+v, want %q", exp.ActivePack, themeKindMedia)
	}
	if exp.EffectiveStyle != "graphite" {
		t.Fatalf("effectiveStyle = %q, want graphite (pack baseStyle)", exp.EffectiveStyle)
	}
	// tl1362: every media preset must activate and resolve to its own pack.
	for _, id := range mediaThemeOrderFixed {
		if err := app.ActivateThemePack(id); err != nil {
			t.Fatalf("activate %s: %v", id, err)
		}
		exp, err := app.GetThemeExperience()
		if err != nil {
			t.Fatal(err)
		}
		if exp.ActiveThemeID != id || exp.ActivePack == nil || exp.ActivePack.Kind != themeKindMedia {
			t.Fatalf("activate %s → activeThemeId=%q kind=%+v", id, exp.ActiveThemeID, exp.ActivePack)
		}
		if exp.EffectiveStyle != "graphite" {
			t.Fatalf("activate %s → effectiveStyle = %q, want graphite", id, exp.EffectiveStyle)
		}
	}
}

func TestMediaThemeReservedGuards(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	resetMediaRegistryForTest()
	app := NewApp()

	if err := app.ActivateThemePack("media-broadsheet-rose"); err != nil {
		t.Fatal(err)
	}
	if err := app.DeleteThemePack("media-broadsheet-rose"); err == nil {
		t.Fatal("deleting a media preset must fail")
	}
	_, err := app.SaveThemePack(ThemeSaveInput{
		ID: "media-broadsheet-rose", Name: "Hijack", BaseStyle: "graphite",
		Recipes: defaultThemePackRecipes(),
	})
	if err == nil {
		t.Fatal("saving over a media preset id must fail")
	}
	// Copying a media preset produces an ordinary editable user theme.
	copied, err := app.CopyThemePack("media-broadsheet-rose", "rose-private-copy", "Rose Private")
	if err != nil {
		t.Fatal(err)
	}
	if copied.Kind != themeKindUser {
		t.Fatalf("copied kind = %q, want user", copied.Kind)
	}
	if _, err := os.Stat(themeDir("rose-private-copy")); err != nil {
		t.Fatalf("copied user theme missing on disk: %v", err)
	}
	// A copy may not squat a reserved media id.
	if _, err := app.CopyThemePack("media-broadsheet-rose", "media-broadsheet-rose", "Dup"); err == nil {
		t.Fatal("copying onto a reserved media id must fail")
	}
}
