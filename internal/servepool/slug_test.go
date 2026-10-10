package servepool

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// twoSameBasenameRoots builds the portable equivalent of task 661's
// D:\work\pp vs E:\play\pp: two sibling temp roots each ending in a folder
// named "pp" (different parents, same basename).
func twoSameBasenameRoots(t *testing.T) (first, second string) {
	t.Helper()
	first = filepath.Join(t.TempDir(), "first", "pp")
	second = filepath.Join(t.TempDir(), "second", "pp")
	for _, dir := range []string{first, second} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return first, second
}

// TestWorkspaceSlugCollisionBothRegistered is acceptance ①: two registered
// projects sharing a basename both land in the pool with distinct, routable
// ids — the pre-661 pool silently dropped the second root instead.
func TestWorkspaceSlugCollisionBothRegistered(t *testing.T) {
	first, second := twoSameBasenameRoots(t)
	m := newTestManager(t, first, second)

	projects := m.Projects()
	if len(projects) != 2 {
		t.Fatalf("pool has %d projects, want 2 (second root used to be dropped silently)", len(projects))
	}
	ids := map[string]string{} // id -> root
	for _, p := range projects {
		if prev, dup := ids[p.ID]; dup {
			t.Fatalf("pool id %q collision between %q and %q", p.ID, prev, p.Root)
		}
		ids[p.ID] = p.Root
	}
	// First-come keeps the plain basename; the later root carries the short
	// full-path hash (FNV-1a, 6 hex chars).
	if got := ids["pp"]; got != filepath.Clean(first) {
		t.Fatalf("first-registered root id = %q (root %q), want plain basename id for %q", "pp", got, first)
	}
	wantSecond := "pp-" + slugHash(filepath.Clean(second), 6)
	if got := ids[wantSecond]; got != filepath.Clean(second) {
		t.Fatalf("second root id: got entry %q for id %q, want root %q", got, wantSecond, second)
	}
	// Distinct ids must route to their own roots (what /p/<id>/* resolves
	// through): Root(id) round-trips, and Open on the disambiguated id fails
	// with a spawn error (fake test binary), NOT "unknown project".
	if got := m.Root("pp"); !sameRoot(got, filepath.Clean(first)) {
		t.Fatalf("Root(pp) = %q, want %q", got, filepath.Clean(first))
	}
	err := m.Open(wantSecond)
	if err == nil {
		t.Fatal("Open(disambiguated id) succeeded with a fake binary; want spawn error")
	}
	if strings.Contains(err.Error(), "unknown project") {
		t.Fatalf("disambiguated id not routable: %v", err)
	}
}

// TestGatewayCollisionManifest checks acceptance ① at the gateway URL space:
// the manifest lists both colliding roots under distinct ids.
func TestGatewayCollisionManifest(t *testing.T) {
	first, second := twoSameBasenameRoots(t)
	m := newTestManager(t, first, second)
	g := NewGateway(m, "s")
	ts := httptest.NewServer(g)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/manifest", nil)
	req.Header.Set("Authorization", "Bearer s")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out []ProjectState
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("manifest has %d entries, want 2", len(out))
	}
	seen := map[string]string{}
	for _, p := range out {
		if prev, dup := seen[p.ID]; dup {
			t.Fatalf("manifest id %q collision between %q and %q", p.ID, prev, p.Root)
		}
		seen[p.ID] = p.Root
	}
	if _, ok := seen["pp"]; !ok {
		t.Fatalf("manifest lost the plain basename id: %+v", out)
	}
}

// TestWorkspaceSlugNoCollisionIDUnchanged is acceptance ②: projects without a
// basename clash keep the exact pre-661 ids — zero migration.
func TestWorkspaceSlugNoCollisionIDUnchanged(t *testing.T) {
	app := filepath.Join(t.TempDir(), "app")
	m := newTestManager(t, app)
	projects := m.Projects()
	if len(projects) != 1 || projects[0].ID != "app" {
		t.Fatalf("single project = %+v, want id exactly \"app\"", projects)
	}

	a := filepath.Join(t.TempDir(), "alpha")
	b := filepath.Join(t.TempDir(), "beta")
	m2 := newTestManager(t, a, b)
	got := map[string]bool{}
	for _, p := range m2.Projects() {
		got[p.ID] = true
	}
	if !got["alpha"] || !got["beta"] || len(got) != 2 {
		t.Fatalf("non-colliding multi-root ids changed: %v", got)
	}
}

// TestIDForRootRoutesOwnershipRequest is acceptance ③'s mechanism: the
// root->id lookup desktop's RequestOwnershipFromRemote makes resolves each
// colliding root to its own project instead of collapsing onto the first.
func TestIDForRootRoutesOwnershipRequest(t *testing.T) {
	first, second := twoSameBasenameRoots(t)
	m := newTestManager(t, first, second)

	firstID := m.IDForRoot(first)
	secondID := m.IDForRoot(second)
	if firstID != "pp" {
		t.Fatalf("IDForRoot(first) = %q, want \"pp\"", firstID)
	}
	if secondID == "" || secondID == firstID {
		t.Fatalf("IDForRoot(second) = %q, want a distinct disambiguated id", secondID)
	}
	if got := m.Root(secondID); !sameRoot(got, filepath.Clean(second)) {
		t.Fatalf("Root(%q) = %q, want %q", secondID, got, filepath.Clean(second))
	}
	// Same root spelled with different case must hit the same id on Windows
	// (the desktop registry folds case, tabs.go sameProjectRoot).
	if runtime.GOOS == "windows" {
		if got := m.IDForRoot(strings.ToUpper(first)); got != firstID {
			t.Fatalf("IDForRoot(case-variant) = %q, want %q", got, firstID)
		}
	}
	if got := m.IDForRoot(filepath.Join(t.TempDir(), "absent")); got != "" {
		t.Fatalf("IDForRoot(unknown) = %q, want empty", got)
	}
}

// TestSlugCollisionRefreshKeepsIDs: a refresh with the same roots must not
// rename anything (ids are client-visible URL keys), and dropping a root
// still removes its stopped project.
func TestSlugCollisionRefreshKeepsIDs(t *testing.T) {
	first, second := twoSameBasenameRoots(t)
	m := newTestManager(t, first, second)
	before := map[string]string{}
	for _, p := range m.Projects() {
		before[p.Root] = p.ID
	}
	m.RefreshProjects([]string{first, second})
	for _, p := range m.Projects() {
		if before[p.Root] != p.ID {
			t.Fatalf("root %q renamed on refresh: %q -> %q", p.Root, before[p.Root], p.ID)
		}
	}

	m.RefreshProjects([]string{first})
	left := m.Projects()
	if len(left) != 1 || left[0].ID != "pp" || !sameRoot(left[0].Root, filepath.Clean(first)) {
		t.Fatalf("after removal = %+v, want only the first project as \"pp\"", left)
	}
}

// TestDisambiguatedSlug pins the disambiguation algorithm: deterministic
// FNV-1a full-path hash, widening on prefix collisions, counter terminator.
func TestDisambiguatedSlug(t *testing.T) {
	root := filepath.Join("D:", "work", "pp")
	free := func(string) bool { return false }

	want := "pp-" + slugHash(root, 6)
	if got := disambiguatedSlug("pp", root, free); got != want {
		t.Fatalf("slug = %q, want %q", got, want)
	}
	if again := disambiguatedSlug("pp", root, free); again != want {
		t.Fatalf("not deterministic: %q vs %q", again, want)
	}
	// Windows case spellings of one path must hash alike (config.WorkspaceSlug
	// folds case for the same reason).
	if runtime.GOOS == "windows" && slugHash(strings.ToUpper(root), 6) != slugHash(strings.ToLower(root), 6) {
		t.Fatal("hash input is not case-folded on Windows")
	}
	// Distinct roots hash apart in the everyday case.
	other := filepath.Join("E:", "play", "pp")
	if slugHash(root, 6) == slugHash(other, 6) {
		t.Fatal("distinct roots produced identical 6-hex suffixes")
	}
	// 6-hex candidate taken -> widen to 8, not silently reuse.
	if got := disambiguatedSlug("pp", root, func(c string) bool { return c == want }); got != "pp-"+slugHash(root, 8) {
		t.Fatalf("widened slug = %q, want %q", got, "pp-"+slugHash(root, 8))
	}
	// Counter terminator: pool membership is a finite set, so the counter
	// always finds a free id. Model that with a finite taken set covering
	// every hash width plus counter candidates 2..9; the next counter value
	// must be minted.
	h6 := slugHash(root, 6)
	taken := map[string]bool{"pp-" + h6: true}
	for n := 8; n <= 16; n += 2 {
		taken["pp-"+slugHash(root, n)] = true
	}
	for i := 2; i <= 9; i++ {
		taken[fmt.Sprintf("pp-%s-%d", h6, i)] = true
	}
	end := disambiguatedSlug("pp", root, func(c string) bool { return taken[c] })
	if wantEnd := fmt.Sprintf("pp-%s-10", h6); end != wantEnd {
		t.Fatalf("counter slug = %q, want %q", end, wantEnd)
	}
}
