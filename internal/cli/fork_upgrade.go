package cli

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"reasonix/internal/forkupdate"
	"reasonix/internal/i18n"
)

// forkFetchLatest is the channel seam; tests pin it instead of hitting the
// network, mirroring loadCLIUpgradeStyle seams in upgrade.go.
var forkFetchLatest = func(ctx context.Context, c *http.Client) (*forkupdate.Release, error) {
	return forkupdate.FetchLatestRelease(ctx, c)
}

// fork_upgrade.go is the fork-build face of `reasonix upgrade` (tl1680,
// issue #96). The official face (upgrade.go) stays byte-for-byte what it was:
// its channel is the upstream release gateway + GitHub v* releases, which is
// exactly what a fork build must NOT consume — the gateway is shut down
// (NXDOMAIN) and the official channel serves upstream versions whose install
// would silently drop every fork feature.
//
// The fork face has the same three-state contract as the desktop check:
//
//  1. channel unreachable (dead endpoint, GitHub 403 rate limit, EOF) —
//     degrade SILENTLY: one neutral line, exit 0. A failed check is not a
//     failed update, and a shell script gating on the exit code must not see
//     the issue #96 noise;
//  2. running build is current — "Already on the latest version.", exit 0;
//  3. a newer fork release exists — print it plus the fork releases page.
//
// Fork desktop releases carry no CLI archive asset, so there is nothing this
// face can self-replace; the new CLI ships inside the desktop's one-click
// fork update (staging publish) or a manual release download. That is why the
// newer-release face is informational instead of a download-and-swap, and why
// --force has no reinstall semantics here.
func forkUpgradeCommand(syntax cliUpgradeSyntax, version string, c *http.Client) int {
	_ = syntax // --force/--check share the informational face; see the file comment.
	fmt.Println(i18n.M.UpgradeChecking)
	rel, err := forkFetchLatest(context.Background(), c)
	if err != nil {
		// tl1680: 检查失败≠更新失败。渠道不可达静默降级——不再出现
		// "release gateway … EOF" 这类把用户吓一跳的噪声。
		fmt.Fprintln(os.Stderr, i18n.M.UpgradeCheckUnavailable)
		return 0
	}
	running, ok := forkupdate.ParseForkVersion(version)
	if ok && rel.CompareVersion(running) <= 0 {
		fmt.Println(i18n.M.UpgradeAlreadyLatest)
		return 0
	}
	fmt.Printf(i18n.M.UpgradeForkAvailableFmt+"\n", rel.Version)
	fmt.Printf(i18n.M.UpgradeForkDownloadHintFmt+"\n", forkupdate.ReleasesPage)
	return 0
}
