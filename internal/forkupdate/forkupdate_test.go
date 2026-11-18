package forkupdate

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestParseForkVersion pins the fork version label shapes the channel and the
// two updater faces rely on. tl1680: the running version form (v1.38.3-…),
// the release tag form (desktop-v1.38.3-…), the bare legacy form, and the
// build-metadata form must all parse; upstream prereleases must not.
func TestParseForkVersion(t *testing.T) {
	tests := []struct {
		label   string
		want    ForkVersion
		wantOK  bool
		wantStr string
	}{
		{label: "v1.38.3-20261117-1800", want: ForkVersion{Base: [3]int64{1, 38, 3}, Stamp: "202611171800"}, wantOK: true, wantStr: "v1.38.3-20261117-1800"},
		{label: "1.38.3-20261115-0841", want: ForkVersion{Base: [3]int64{1, 38, 3}, Stamp: "202611150841"}, wantOK: true, wantStr: "v1.38.3-20261115-0841"},
		{label: "desktop-v1.38.3-20261117-1800", want: ForkVersion{Base: [3]int64{1, 38, 3}, Stamp: "202611171800"}, wantOK: true, wantStr: "v1.38.3-20261117-1800"},
		{label: "desktop-v1.38.3", want: ForkVersion{Base: [3]int64{1, 38, 3}}, wantOK: true, wantStr: "v1.38.3"},
		{label: "desktop-v1.34.0+fork", want: ForkVersion{Base: [3]int64{1, 34, 0}}, wantOK: true, wantStr: "v1.34.0"},
		{label: "v2.0.0", want: ForkVersion{Base: [3]int64{2, 0, 0}}, wantOK: true, wantStr: "v2.0.0"},
		{label: " desktop-v1.38.3 ", want: ForkVersion{Base: [3]int64{1, 38, 3}}, wantOK: true, wantStr: "v1.38.3"},
		// Rejections: empty, dev, upstream prerelease text, malformed stamps.
		{label: "", wantOK: false},
		{label: "dev", wantOK: false},
		{label: "v1.38.3-preview.1", wantOK: false},
		{label: "v1.38.3-2026111X-1800", wantOK: false},
		{label: "v1.38.3-20261117-180", wantOK: false},
		{label: "v1.38.3-20261117", wantOK: false},
		{label: "v1.38.x", wantOK: false},
		{label: "desktop-", wantOK: false},
	}
	for _, tt := range tests {
		got, ok := ParseForkVersion(tt.label)
		if ok != tt.wantOK {
			t.Fatalf("ParseForkVersion(%q) ok=%v, want %v (got %+v)", tt.label, ok, tt.wantOK, got)
		}
		if !tt.wantOK {
			continue
		}
		if got != tt.want {
			t.Fatalf("ParseForkVersion(%q) = %+v, want %+v", tt.label, got, tt.want)
		}
		if tt.wantStr != "" && got.String() != tt.wantStr {
			t.Fatalf("ParseForkVersion(%q).String() = %q, want %q", tt.label, got.String(), tt.wantStr)
		}
	}
}

// TestForkVersionCompare pins the timestamp-package ordering that raw semver
// gets wrong: a bare release ranks BELOW every timestamp refresh of the same
// base, and the fixed-width build clocks order chronologically.
func TestForkVersionCompare(t *testing.T) {
	mustParse := func(label string) ForkVersion {
		v, ok := ParseForkVersion(label)
		if !ok {
			t.Fatalf("ParseForkVersion(%q) unexpectedly failed", label)
		}
		return v
	}
	tests := []struct {
		a, b string
		want int
	}{
		{a: "v1.38.3-20261117-1800", b: "v1.38.3-20261115-0841", want: 1},  // newer stamp wins
		{a: "v1.38.3-20261115-0841", b: "v1.38.3-20261117-1800", want: -1}, // and the mirror
		{a: "v1.38.3-20261117-1800", b: "v1.38.3-20261117-1800", want: 0},  // same build
		{a: "v1.38.3-20261012-0045", b: "v1.38.3", want: 1},                // stamp refresh beats its bare base (semver cannot say this)
		{a: "v1.38.3", b: "v1.38.3-20261012-0045", want: -1},
		{a: "v1.39.0", b: "v1.38.3-20261117-1800", want: 1},               // base bump wins over any stamp
		{a: "v1.38.10", b: "v1.38.9-20261117-1800", want: 1},              // numeric, not lexical, base compare
		{a: "v1.34.0+fork", b: "v1.34.0", want: 0},                        // build metadata never affects order
		{a: "v1.38.3-20261118-0045", b: "v1.38.3-20261117-2359", want: 1}, // day rollover with zero-padded clock
	}
	for _, tt := range tests {
		if got := mustParse(tt.a).Compare(mustParse(tt.b)); got != tt.want {
			t.Fatalf("Compare(%s, %s) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func releaseJSON(tags ...string) string {
	var b strings.Builder
	b.WriteString("[")
	for i, tag := range tags {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"tag_name":%q,"name":"Reasonix fork %s","body":"notes for %s","prerelease":false,"draft":false,"assets":[{"name":"Reasonix-windows-amd64.zip","browser_download_url":"https://github.com/Linearl/DeepSeek-Reasonix/releases/download/%s/Reasonix-windows-amd64.zip","size":12345},{"name":"Reasonix-darwin-universal.dmg","browser_download_url":"https://github.com/Linearl/DeepSeek-Reasonix/releases/download/%s/Reasonix-darwin-universal.dmg","size":999}]}`, tag, tag, tag, tag, tag)
	}
	b.WriteString("]")
	return b.String()
}

// decodeReleasesForTest feeds a raw payload through the same decode path
// FetchLatestRelease uses, exposing pickLatestRelease to the pure tests.
func decodeReleasesForTest(payload string) ([]apiRelease, error) {
	var rels []apiRelease
	if err := decodeLimited(strings.NewReader(payload), &rels); err != nil {
		return nil, err
	}
	return rels, nil
}

// TestPickLatestRelease pins channel selection: non-fork tags, prereleases and
// drafts are skipped; the newest fork release wins regardless of list order.
func TestPickLatestRelease(t *testing.T) {
	payload := releaseJSON(
		"v1.31.4", // legacy upstream tag shape — not a fork desktop release
		"desktop-v1.38.3",
		"desktop-v1.38.3-20261115-0841",
		"desktop-v1.38.3-20261117-1800",
		"desktop-v1.39.0-rc.1", // prerelease text — unparsable as a fork version
	)
	rels, err := decodeReleasesForTest(payload)
	if err != nil {
		t.Fatalf("decode test payload: %v", err)
	}
	got, err := pickLatestRelease(rels)
	if err != nil {
		t.Fatalf("pickLatestRelease: %v", err)
	}
	if got.Tag != "desktop-v1.38.3-20261117-1800" {
		t.Fatalf("pickLatestRelease tag = %q, want desktop-v1.38.3-20261117-1800", got.Tag)
	}
	if got.Version != "v1.38.3-20261117-1800" {
		t.Fatalf("pickLatestRelease version = %q, want v1.38.3-20261117-1800", got.Version)
	}
	if len(got.Assets) != 2 {
		t.Fatalf("pickLatestRelease assets = %d, want 2", len(got.Assets))
	}
	if got.Notes != "notes for desktop-v1.38.3-20261117-1800" {
		t.Fatalf("pickLatestRelease notes = %q", got.Notes)
	}

	// Order independence: shuffled input picks the same release.
	rels, _ = decodeReleasesForTest(releaseJSON(
		"desktop-v1.38.3-20261117-1800",
		"desktop-v1.38.3-20261115-0841",
		"desktop-v1.38.3",
	))
	got, err = pickLatestRelease(rels)
	if err != nil {
		t.Fatalf("pickLatestRelease(shuffled): %v", err)
	}
	if got.Tag != "desktop-v1.38.3-20261117-1800" {
		t.Fatalf("shuffled pick = %q, want desktop-v1.38.3-20261117-1800", got.Tag)
	}
}

// TestPickLatestReleasePrereleaseAndDraftSkipped: tl1610-adjacent trust rule —
// draft and prerelease channel entries can never become an update target.
func TestPickLatestReleasePrereleaseAndDraftSkipped(t *testing.T) {
	payload := `[
		{"tag_name":"desktop-v1.39.0-9999","prerelease":false,"draft":true,"assets":[]},
		{"tag_name":"desktop-v1.39.0-9999","prerelease":true,"draft":false,"assets":[]},
		{"tag_name":"desktop-v1.38.3-20261117-1800","prerelease":false,"draft":false,"assets":[]}
	]`
	rels, err := decodeReleasesForTest(payload)
	if err != nil {
		t.Fatalf("decode test payload: %v", err)
	}
	got, err := pickLatestRelease(rels)
	if err != nil {
		t.Fatalf("pickLatestRelease: %v", err)
	}
	if got.Tag != "desktop-v1.38.3-20261117-1800" {
		t.Fatalf("draft/prerelease leaked into the channel pick: %q", got.Tag)
	}
}

func TestPickLatestReleaseEmptyChannel(t *testing.T) {
	rels, err := decodeReleasesForTest(releaseJSON("v1.31.4", "v1.31.3"))
	if err != nil {
		t.Fatalf("decode test payload: %v", err)
	}
	if _, err := pickLatestRelease(rels); err == nil {
		t.Fatal("a channel with no fork desktop release must error, not return nil")
	}
}

func TestReleaseCompareVersion(t *testing.T) {
	rels, err := decodeReleasesForTest(releaseJSON("desktop-v1.38.3-20261117-1800"))
	if err != nil {
		t.Fatalf("decode test payload: %v", err)
	}
	rel, err := pickLatestRelease(rels)
	if err != nil {
		t.Fatalf("pickLatestRelease: %v", err)
	}
	running, _ := ParseForkVersion("v1.38.3-20261115-0841")
	if rel.CompareVersion(running) <= 0 {
		t.Fatal("20261117-1800 must compare newer than running 20261115-0841")
	}
	same, _ := ParseForkVersion("v1.38.3-20261117-1800")
	if rel.CompareVersion(same) != 0 {
		t.Fatal("identical builds must compare equal")
	}
}

// TestSelectPlatformAsset pins the fork artifact naming per platform so the
// desktop apply path and the check's size probe agree with what the fork
// workflow publishes.
func TestSelectPlatformAsset(t *testing.T) {
	rels, err := decodeReleasesForTest(releaseJSON("desktop-v1.38.3-20261117-1800"))
	if err != nil {
		t.Fatalf("decode test payload: %v", err)
	}
	rel, err := pickLatestRelease(rels)
	if err != nil {
		t.Fatalf("pickLatestRelease: %v", err)
	}
	if a, ok := rel.SelectPlatformAsset("windows", "amd64"); !ok || a.Name != "Reasonix-windows-amd64.zip" {
		t.Fatalf("windows/amd64 asset = %+v ok=%v", a, ok)
	}
	if _, ok := rel.SelectPlatformAsset("windows", "arm64"); ok {
		t.Fatal("release fixture has no windows-arm64 asset; selection must report absent")
	}
	if _, ok := rel.SelectPlatformAsset("plan9", "amd64"); ok {
		t.Fatal("unsupported platform must report no asset")
	}
}

// TestFetchLatestRelease is the mock-server pin of the channel transport:
// token header honored, 403 classified as rate limited (the silent-degrade
// signal), transport errors surface as plain errors.
func TestFetchLatestRelease(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if want := "/repos/" + Owner + "/" + Repo + "/releases"; !strings.HasPrefix(r.URL.Path, want) {
			t.Errorf("request path %q does not target the fork channel %q", r.URL.Path, want)
		}
		if got := r.Header.Get("User-Agent"); got != "Reasonix-Fork-Updater" {
			t.Errorf("User-Agent = %q, want Reasonix-Fork-Updater", got)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, releaseJSON("desktop-v1.38.3-20261117-1800", "desktop-v1.38.3-20261115-0841"))
	}))
	defer srv.Close()

	t.Setenv("FORKUPDATE_TEST_API", srv.URL)
	// FetchLatestRelease targets the constant ReleasesAPI; reroute via the
	// package seam the same way the callers see it.
	client := srv.Client()
	orig := releasesAPIURL
	releasesAPIURL = srv.URL + "/repos/" + Owner + "/" + Repo + "/releases?per_page=100"
	defer func() { releasesAPIURL = orig }()

	rel, err := FetchLatestRelease(context.Background(), client)
	if err != nil {
		t.Fatalf("FetchLatestRelease: %v", err)
	}
	if rel.Tag != "desktop-v1.38.3-20261117-1800" {
		t.Fatalf("fetch picked %q, want the newest tag", rel.Tag)
	}
	if RateLimited(err) {
		t.Fatal("a successful fetch must not classify as rate limited")
	}
}

func TestFetchLatestReleaseRateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"message":"API rate limit exceeded"}`)
	}))
	defer srv.Close()

	orig := releasesAPIURL
	releasesAPIURL = srv.URL + "/releases"
	defer func() { releasesAPIURL = orig }()

	_, err := FetchLatestRelease(context.Background(), srv.Client())
	if err == nil {
		t.Fatal("403 must produce an error")
	}
	if !RateLimited(err) {
		t.Fatalf("403 must classify as rate limited, got %v", err)
	}
	// errors.Is-style reachability through wrapping: the callers degrade on
	// wrapped chains too.
	wrapped := fmt.Errorf("desktop check: %w", err)
	if !RateLimited(wrapped) {
		t.Fatal("wrapped rate-limit error must still classify")
	}
	if RateLimited(errors.New("Get \"...\": EOF")) {
		t.Fatal("transport EOF is not a rate limit")
	}
}

func TestFetchLatestReleaseTransportFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate a dead channel: close without a response body (EOF).
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Fatalf("hijack: %v", err)
		}
		_ = conn.Close()
	}))
	defer srv.Close()

	orig := releasesAPIURL
	releasesAPIURL = srv.URL + "/releases"
	defer func() { releasesAPIURL = orig }()

	_, err := FetchLatestRelease(context.Background(), srv.Client())
	if err == nil {
		t.Fatal("dead channel must error")
	}
	if RateLimited(err) {
		t.Fatal("transport failure must not classify as rate limited")
	}
}

func TestFetchLatestReleaseTokenSent(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "tok-123")
	t.Setenv("GH_TOKEN", "")
	seen := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("Authorization")
		fmt.Fprint(w, releaseJSON("desktop-v1.38.3"))
	}))
	defer srv.Close()

	orig := releasesAPIURL
	releasesAPIURL = srv.URL + "/releases"
	defer func() { releasesAPIURL = orig }()

	if _, err := FetchLatestRelease(context.Background(), srv.Client()); err != nil {
		t.Fatalf("FetchLatestRelease: %v", err)
	}
	if seen != "Bearer tok-123" {
		t.Fatalf("Authorization = %q, want Bearer tok-123", seen)
	}
}
