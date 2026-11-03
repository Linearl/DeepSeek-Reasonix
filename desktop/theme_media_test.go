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
