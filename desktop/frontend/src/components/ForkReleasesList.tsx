import { useEffect, useState } from "react";
import { ArrowDownToLine, CircleDot, ExternalLink } from "lucide-react";
import { app, openExternal } from "../lib/bridge";
import { useT } from "../lib/i18n";
import type { ForkReleaseAsset, ForkReleaseList } from "../lib/types";
import { FORK_RELEASES_URL } from "../lib/forkLinks";

// tl1693: the version page's release list — the fork channel's published
// desktop releases, newest first, with the running version's row marked and
// per-release installer links (the channel page's artifacts, linked directly).
//
// Availability-first rendering: the list mounts hidden and stays hidden when
// the lookup fails ({failed: true}) or comes back empty — a channel hiccup is
// the same neutral silence the check face degrades to, never an error banner.
// One fetch per mount; Settings re-mounts the section per visit, so the list
// is fresh whenever the user opens the updates tab (no background polling).
function formatPublished(value: string): string {
  if (!value) return "";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  return date.toLocaleDateString();
}

// pickInstallerAsset prefers this machine's platform artifact, then any
// portable zip (the complete release unit the fork's seamless apply uses),
// then the first asset — the same artifact the channel page lists first.
function pickInstallerAsset(assets: ForkReleaseAsset[]): ForkReleaseAsset | null {
  if (assets.length === 0) return null;
  const platform = typeof navigator !== "undefined" && /win/i.test(navigator.platform || navigator.userAgent) ? "windows" : undefined;
  if (platform) {
    const match = assets.find((a) => a.name.toLowerCase().includes(platform));
    if (match) return match;
  }
  const zip = assets.find((a) => a.name.toLowerCase().endsWith(".zip"));
  return zip ?? assets[0];
}

export function ForkReleasesList() {
  const t = useT();
  const [list, setList] = useState<ForkReleaseList | null>(null);

  useEffect(() => {
    let cancelled = false;
    app.ForkListReleases()
      .then((result) => {
        if (!cancelled) setList(result);
      })
      .catch(() => {
        // Silent: a transport failure renders exactly like a channel failure —
        // the list stays hidden, nothing in the UI errors or flickers.
        if (!cancelled) setList({ releases: [], failed: true });
      });
    return () => {
      cancelled = true;
    };
  }, []);

  if (!list || list.failed || list.releases.length === 0) return null;

  return (
    <div className="fork-releases">
      <div className="fork-releases__head">
        <span className="fork-releases__title">{t("updater.releasesTitle")}</span>
        <button
          className="btn btn--small"
          type="button"
          onClick={() => void openExternal(FORK_RELEASES_URL)}
          style={{ height: "auto", minHeight: 0, padding: 0, borderColor: "transparent", background: "transparent", color: "var(--fg-dim)", textDecoration: "underline", textUnderlineOffset: 2 }}
        >
          <ExternalLink size={13} aria-hidden="true" />
        </button>
      </div>
      <div className="fork-releases__hint">{t("updater.releasesHint")}</div>
      <ul className="fork-releases__rows">
        {list.releases.slice(0, 10).map((release) => {
          const installer = pickInstallerAsset(release.assets);
          return (
            <li key={release.tag} className="fork-releases__row">
              <span className="fork-releases__tag" title={release.tag}>{release.tag}</span>
              {release.published && <span className="fork-releases__date">{formatPublished(release.published)}</span>}
              {release.current && (
                <span className="fork-releases__current">
                  <CircleDot size={12} aria-hidden="true" />
                  {t("updater.releasesCurrent")}
                </span>
              )}
              {installer && (
                <button
                  className="btn btn--small fork-releases__download"
                  type="button"
                  title={installer.name}
                  onClick={() => void openExternal(installer.url)}
                >
                  {t("updater.releasesInstallers")}
                  <ArrowDownToLine size={13} aria-hidden="true" />
                </button>
              )}
            </li>
          );
        })}
      </ul>
    </div>
  );
}
