// Package forkupdate is the fork's own release channel: the single source for
// "which fork releases exist, which one is newest, which artifact fits this
// platform". Both the desktop CheckUpdate face and the CLI `upgrade --check`
// face consume it so the two stay on the same channel and the same comparison
// semantics (tl1680).
//
// Why not the legacy upstream manifest chain: dl.reasonix.io /
// crash.reasonix.io serve the upstream esengine releases, and the crash
// gateway is shut down entirely (NXDOMAIN, issue #96). A fork client must
// never treat an upstream latest as its update target — installing one would
// silently drop every fork feature — so the fork checks the fork's own GitHub
// releases (Linearl/DeepSeek-Reasonix, tags `desktop-v...`) and nothing else.
//
// Version shape: fork packages are timestamp builds, `v1.38.3-20261117-1800`
// (semver base + build clock suffix, release tags prefixed `desktop-`).
// Raw semver cannot order them: semver ranks any prerelease BELOW its bare
// release, yet `desktop-v1.38.3` (2026-09-15) is older than
// `desktop-v1.38.3-20261012-0045`. The comparator below orders by the numeric
// base triple first, then ranks a timestamp refresh above its bare base, then
// compares the fixed-width build clocks lexically.
package forkupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Owner/Repo identify the fork's release channel. Everything the fork ships
// updates from here; the upstream esengine repository is deliberately not
// referenced.
const (
	Owner = "Linearl"
	Repo  = "DeepSeek-Reasonix"

	// ReleasesAPI lists the channel's releases. Anonymous GitHub API quota is
	// 60/h per IP, so lookups prefer GITHUB_TOKEN/GH_TOKEN when present.
	ReleasesAPI = "https://api.github.com/repos/" + Owner + "/" + Repo + "/releases?per_page=100"
	// ReleasesPage is the human-facing channel page (the manual fallback the
	// UI links when self-update is unavailable).
	ReleasesPage = "https://github.com/" + Owner + "/" + Repo + "/releases"

	// fetchTimeout bounds one channel lookup; a hung connection must not pin
	// the UI check (the desktop check runs at startup).
	fetchTimeout = 15 * time.Second
	// maxReleasesResponseSize caps the API payload read.
	maxReleasesResponseSize = int64(8 << 20)
)

// tagPrefix marks a fork desktop release tag ("desktop-v1.38.3-20261117-1800").
const tagPrefix = "desktop-"

// releasesAPIURL is the channel endpoint actually fetched. A var so tests can
// point the transport at a local mock; production keeps the constant.
var releasesAPIURL = ReleasesAPI

// ForkVersion is a parsed fork version label. Stamp is the fixed-width build
// clock "YYYYMMDDHHMM" ("" for bare releases like v1.38.3).
type ForkVersion struct {
	Base  [3]int64
	Stamp string
}

// ParseForkVersion parses a fork version label with or without the
// `desktop-` tag prefix. Accepted shapes:
//   - "v1.38.3-20261117-1800"  (timestamp build, running version form)
//   - "1.38.3-20261117-1800"   (unprefixed build identity)
//   - "desktop-v1.38.3-20261117-1800" (release tag form)
//   - "v1.38.3" / "desktop-v1.38.3"   (bare release; legacy channel still valid)
//   - "desktop-v1.34.0+fork"          (semver build metadata ignored)
//
// It fails on anything else — upstream prereleases, dev, empty labels — so a
// non-fork version can never masquerade as a channel member.
func ParseForkVersion(label string) (ForkVersion, bool) {
	v := strings.TrimSpace(label)
	v = strings.TrimPrefix(v, tagPrefix)
	v = strings.TrimSpace(v)
	if v == "" {
		return ForkVersion{}, false
	}
	if buildMeta := strings.IndexByte(v, '+'); buildMeta >= 0 {
		v = v[:buildMeta]
	}
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	rest := v[1:]
	stamp := ""
	if i := strings.IndexByte(rest, '-'); i >= 0 {
		stamp = rest[i+1:]
		rest = rest[:i]
		if stamped, ok := parseStamp(stamp); ok {
			return ForkVersion{Base: parseBase(rest), Stamp: stamped}, parseBaseOK(rest)
		}
		// Anything after the dash that is not a fork build clock (upstream
		// prerelease text such as "preview.1") is not a fork version.
		return ForkVersion{}, false
	}
	return ForkVersion{Base: parseBase(rest)}, parseBaseOK(rest)
}

// parseStamp validates the "YYYYMMDD-HHMM" build clock and returns it packed
// as the fixed-width "YYYYMMDDHHMM" lexical-comparison form.
func parseStamp(stamp string) (string, bool) {
	stamp = strings.TrimSpace(stamp)
	if len(stamp) != len("YYYYMMDD-HHMM") {
		return "", false
	}
	if stamp[8] != '-' {
		return "", false
	}
	date, clock := stamp[:8], stamp[9:]
	for _, r := range date {
		if r < '0' || r > '9' {
			return "", false
		}
	}
	for _, r := range clock {
		if r < '0' || r > '9' {
			return "", false
		}
	}
	return date + clock, true
}

// parseBase parses "1.38.3" into the numeric triple. parseBaseOK reports
// whether all three components were present and numeric.
func parseBase(base string) [3]int64 {
	var out [3]int64
	for i, part := range strings.SplitN(base, ".", 3) {
		n, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
		if err == nil {
			out[i] = n
		}
	}
	return out
}

func parseBaseOK(base string) bool {
	parts := strings.Split(base, ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		for _, r := range part {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}

// String renders the normalized running-version form ("v1.38.3-20261117-1800").
func (v ForkVersion) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "v%d.%d.%d", v.Base[0], v.Base[1], v.Base[2])
	if v.Stamp != "" {
		b.WriteByte('-')
		b.WriteString(v.Stamp[:8])
		b.WriteByte('-')
		b.WriteString(v.Stamp[8:])
	}
	return b.String()
}

// Compare orders two fork versions. Base triples compare numerically; a bare
// release ranks below every timestamp refresh of the same base (the stamp is
// the newer rebuild); two stamps compare lexically — the fields are
// fixed-width digits, so lexical order is chronological order.
func (v ForkVersion) Compare(o ForkVersion) int {
	for i := range v.Base {
		if v.Base[i] != o.Base[i] {
			if v.Base[i] < o.Base[i] {
				return -1
			}
			return 1
		}
	}
	if v.Stamp == o.Stamp {
		return 0
	}
	if v.Stamp == "" {
		return -1
	}
	if o.Stamp == "" {
		return 1
	}
	return strings.Compare(v.Stamp, o.Stamp)
}

// Asset is one downloadable release artifact.
type Asset struct {
	Name string
	URL  string
	Size int64
}

// Release is one fork channel release (GitHub releases API subset).
type Release struct {
	Tag        string    // "desktop-v1.38.3-20261117-1800"
	Version    string    // "v1.38.3-20261117-1800" (normalized ForkVersion form)
	Prerelease bool      `json:"-"` // parsed from the API payload, not re-encoded
	Notes      string    // release body — what the UI's release-notes button shows
	Assets     []Asset   `json:"-"`
	Published  time.Time `json:"-"` // channel timestamp (created_at) for the releases list UI (tl1693)
}

// CompareVersion reports latest-this-release versus the running version:
// negative when the release is older, 0 when identical, positive when newer.
func (r *Release) CompareVersion(running ForkVersion) int {
	latest, ok := ParseForkVersion(r.Version)
	if !ok {
		return 0
	}
	return latest.Compare(running)
}

// SelectPlatformAsset returns the fork artifact for a platform. Fork releases
// publish the same asset names as the upstream desktop workflow:
// Reasonix-windows-amd64.zip / -installer.exe / Reasonix-linux-amd64.tar.gz /
// .deb / Reasonix-darwin-*.zip|.dmg.
func (r *Release) SelectPlatformAsset(goos, goarch string) (Asset, bool) {
	var name string
	switch goos {
	case "windows":
		name = fmt.Sprintf("Reasonix-windows-%s.zip", goarch)
	case "linux":
		name = fmt.Sprintf("Reasonix-linux-%s.tar.gz", goarch)
	case "darwin":
		name = fmt.Sprintf("Reasonix-darwin-%s.zip", goarch)
	default:
		return Asset{}, false
	}
	for _, a := range r.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return Asset{}, false
}

// githubAPIToken returns the token to authenticate channel lookups with, the
// same convention the official CLI updater uses: GITHUB_TOKEN then GH_TOKEN.
func githubAPIToken() string {
	for _, name := range []string{"GITHUB_TOKEN", "GH_TOKEN"} {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			return v
		}
	}
	return ""
}

// RateLimited reports whether err is the channel refusing us for quota — the
// case the callers must degrade on silently (a rate-limited check is not an
// update failure, and it is certainly not the user's error to fix).
func RateLimited(err error) bool {
	var rl *rateLimitError
	return errors.As(err, &rl)
}

// rateLimitError marks a 403/429 channel refusal so callers can degrade
// quietly instead of surfacing transport noise.
type rateLimitError struct {
	status string
}

func (e *rateLimitError) Error() string {
	return fmt.Sprintf("fork release channel rate limited: %s", e.status)
}

// NewRateLimitError builds a channel rate-limit error. Exported so callers'
// tests can pin the silent-degradation classification without a live 403.
func NewRateLimitError(status string) error {
	return &rateLimitError{status: status}
}

// apiRelease is the GitHub releases API payload subset the channel needs.
type apiRelease struct {
	TagName    string    `json:"tag_name"`
	Name       string    `json:"name"`
	Body       string    `json:"body"`
	Prerelease bool      `json:"prerelease"`
	Draft      bool      `json:"draft"`
	CreatedAt  time.Time `json:"created_at"`
	Assets     []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
		Size               int64  `json:"size"`
	} `json:"assets"`
}

// FetchLatestRelease queries the fork channel and returns the newest complete
// desktop release. Releases that are drafts or prereleases, tags that do not
// parse as fork versions, and tags older than the best seen are skipped, so an
// interrupted publication can never shadow the previous complete release.
func FetchLatestRelease(ctx context.Context, c *http.Client) (*Release, error) {
	rels, err := fetchChannelReleases(ctx, c)
	if err != nil {
		return nil, err
	}
	return pickLatestRelease(rels)
}

// FetchReleases lists every complete desktop release on the fork channel,
// newest first (tl1693). Same channel membership rules as FetchLatestRelease:
// drafts, prereleases, and non-desktop tags are skipped. The version page uses
// this for the GitHub release list with a current-version marker; a failed
// lookup is the caller's silent-degrade cue, never a UI error.
func FetchReleases(ctx context.Context, c *http.Client) ([]Release, error) {
	rels, err := fetchChannelReleases(ctx, c)
	if err != nil {
		return nil, err
	}
	return listReleases(rels), nil
}

// fetchChannelReleases performs one channel GET and decodes the release list.
func fetchChannelReleases(ctx context.Context, c *http.Client) ([]apiRelease, error) {
	if c == nil {
		c = http.DefaultClient
	}
	callCtx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(callCtx, http.MethodGet, releasesAPIURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Reasonix-Fork-Updater")
	if token := githubAPIToken(); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		return nil, &rateLimitError{status: resp.Status}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fork release channel GET %s: %s", ReleasesAPI, resp.Status)
	}
	var rels []apiRelease
	if err := decodeLimited(resp.Body, &rels); err != nil {
		return nil, err
	}
	return rels, nil
}

func decodeLimited(body io.Reader, out any) error {
	data, err := io.ReadAll(io.LimitReader(body, maxReleasesResponseSize+1))
	if err != nil {
		return err
	}
	if int64(len(data)) > maxReleasesResponseSize {
		return fmt.Errorf("fork release channel response exceeds %d bytes", maxReleasesResponseSize)
	}
	return json.Unmarshal(data, out)
}

// pickLatestRelease selects the newest fork desktop release from an API
// payload. Exported for tests as a pure function over decoded payloads. Only
// `desktop-`-prefixed tags are channel members: the fork repo still carries
// legacy upstream-style CLI tags (v1.31.4 and older), and a bare v* release
// has no desktop payload — it must never become the desktop's update target.
func pickLatestRelease(rels []apiRelease) (*Release, error) {
	var best *Release
	var bestVer ForkVersion
	for i := range rels {
		rel := &rels[i]
		if rel.Draft || rel.Prerelease {
			continue
		}
		if !strings.HasPrefix(rel.TagName, tagPrefix) {
			continue
		}
		ver, ok := ParseForkVersion(rel.TagName)
		if !ok {
			continue
		}
		if best != nil && ver.Compare(bestVer) <= 0 {
			continue
		}
		out := &Release{
			Tag:        rel.TagName,
			Version:    ver.String(),
			Prerelease: rel.Prerelease,
			Notes:      rel.Body,
			Published:  rel.CreatedAt,
		}
		for _, a := range rel.Assets {
			if strings.TrimSpace(a.BrowserDownloadURL) == "" || a.Size <= 0 {
				continue
			}
			out.Assets = append(out.Assets, Asset{Name: a.Name, URL: a.BrowserDownloadURL, Size: a.Size})
		}
		best = out
		bestVer = ver
	}
	if best == nil {
		return nil, fmt.Errorf("fork release channel returned no desktop release in %s/%s", Owner, Repo)
	}
	return best, nil
}

// listReleases converts an API payload into the channel's complete desktop
// release list, newest first (tl1693). Same membership rules as
// pickLatestRelease; releases whose tags do not parse are skipped rather than
// erroring, and an empty list is a valid result (the UI hides the section).
func listReleases(rels []apiRelease) []Release {
	out := make([]Release, 0, len(rels))
	for i := range rels {
		rel := &rels[i]
		if rel.Draft || rel.Prerelease {
			continue
		}
		if !strings.HasPrefix(rel.TagName, tagPrefix) {
			continue
		}
		ver, ok := ParseForkVersion(rel.TagName)
		if !ok {
			continue
		}
		r := Release{
			Tag:        rel.TagName,
			Version:    ver.String(),
			Prerelease: rel.Prerelease,
			Notes:      rel.Body,
			Published:  rel.CreatedAt,
		}
		for _, a := range rel.Assets {
			if strings.TrimSpace(a.BrowserDownloadURL) == "" || a.Size <= 0 {
				continue
			}
			r.Assets = append(r.Assets, Asset{Name: a.Name, URL: a.BrowserDownloadURL, Size: a.Size})
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		vi, oki := ParseForkVersion(out[i].Version)
		vj, okj := ParseForkVersion(out[j].Version)
		if !oki || !okj {
			return false
		}
		return vi.Compare(vj) > 0
	})
	return out
}
