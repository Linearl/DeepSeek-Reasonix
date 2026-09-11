import { useCallback, useEffect, useState } from "react";

import { app } from "../lib/bridge";
import { useT } from "../lib/i18n";

type ServePoolStatus = {
  enabled: boolean;
  running: boolean;
  bind: string;
  addr: string;
  port: number;
  token: string;
  listen: string;
};

/** ServeFailureKind splits a failed gateway operation into the four causes a user
 *  can act on, instead of echoing the raw error. The panel used to render whatever
 *  the backend said, which in practice was one timeout string for every cause -
 *  a wrong token, an unreachable host, an older remote, and a protocol mismatch
 *  all read the same. 清单 任务 7 第 3 项. */
type ServeFailureKind = "unreachable" | "auth" | "version" | "protocol" | "unknown";

function classifyServeFailure(raw: string): ServeFailureKind {
  const s = raw.toLowerCase();
  if (s.includes("401") || s.includes("403") || s.includes("unauthor") || s.includes("forbidden") || s.includes("token")) {
    return "auth";
  }
  if (s.includes("404") || s.includes("not found") || s.includes("unknown route")) {
    return "version";
  }
  if (s.includes("protocol") || s.includes("malformed") || s.includes("unexpected") || s.includes("invalid")) {
    return "protocol";
  }
  if (s.includes("refused") || s.includes("timeout") || s.includes("deadline") || s.includes("unreachable") || s.includes("connect")) {
    return "unreachable";
  }
  return "unknown";
}

/** LocalServerPage is the Settings → 集成与连接 → 本地服务器服务 panel.
 *  It exposes the serve pool remote gateway toggle, its listen address, and a
 *  "copy gateway-token to clipboard" action for GrandCouncil / Tailscale setup. */
export function LocalServerPage() {
  const t = useT();
  const [status, setStatus] = useState<ServePoolStatus | null>(null);
  const [busy, setBusy] = useState(false);
  const [ko, setKo] = useState("");
  const [koRaw, setKoRaw] = useState("");
  const [copied, setCopied] = useState(false);
  const [showToken, setShowToken] = useState(false);

  // failWith renders the classified cause and keeps the raw text beside it: the
  // category is what the user acts on, the detail is what they paste in a report.
  const failWith = useCallback(
    (e: unknown) => {
      const raw = String(e);
      setKo(t(`localserver.failure.${classifyServeFailure(raw)}`));
      setKoRaw(raw);
    },
    [t],
  );

  const refresh = useCallback(async () => {
    try {
      const s = await app.ServePoolStatus();
      setStatus(s);
      setKo("");
      setKoRaw("");
    } catch (e) {
      failWith(e);
    }
  }, [failWith]);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const toggle = async () => {
    if (!status || busy) return;
    setBusy(true);
    try {
      await app.SetServePoolEnabled(!status.enabled);
      await refresh();
    } catch (e) {
      failWith(e);
    } finally {
      setBusy(false);
    }
  };

  const copyToken = async () => {
    if (!status?.token) return;
    try {
      await navigator.clipboard.writeText(status.token);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      setKo(t("localserver.copyFailed"));
    }
  };

  const on = !!status?.enabled;
  const running = !!status?.running;

  return (
    <div className="settings-page settings-page--localserver">
      <style>{`
        .token-display {
          display: flex; align-items: center; justify-content: space-between;
          width: 100%; padding: 8px 12px; border-radius: 8px;
          border: 1px solid color-mix(in srgb, var(--border, #888) 40%, transparent);
          background: color-mix(in srgb, var(--muted, #888) 8%, transparent);
          cursor: pointer; transition: border-color .15s ease;
        }
        .token-display:hover { border-color: color-mix(in srgb, var(--border, #888) 80%, transparent); }
        .token-display__text {
          font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
          font-size: 12px; letter-spacing: 0.06em; word-break: break-all;
          color: var(--text, #222); text-align: left;
        }
        .token-display__eye { font-size: 14px; opacity: .7; margin-left: 8px; flex: none; }
        .settings-field__link { color: var(--link, #4a9eff); font-size: 13px; text-decoration: none; }
        .settings-field__link:hover { text-decoration: underline; }
      `}</style>
      <div className="settings-section">
        <div className="settings-section__header">
          <h3>{t("localserver.title")}</h3>
          <p>{t("localserver.desc")}</p>
        </div>

        {ko && (
          <div className="settings-error">
            {ko}
            {koRaw && <div className="settings-error__detail">{koRaw}</div>}
          </div>
        )}

        <div className="settings-field">
          <label className="settings-toggle">
            <input type="checkbox" checked={on} onChange={toggle} disabled={busy} />
            <span>{t("localserver.enable")}</span>
          </label>
          <small>{t("localserver.enableHint")}</small>
        </div>

        <div className="settings-field">
          <div className="settings-field__label">{t("localserver.status")}</div>
          <div className="settings-field__value">{running ? t("localserver.running") : t("localserver.off")}</div>
        </div>

        <div className="settings-field">
          <div className="settings-field__label">{t("localserver.listen")}</div>
          <div className="settings-field__value">{status ? status.listen : "—"}</div>
          <small>{t("localserver.listenHint")}</small>
        </div>

        <div className="settings-field">
          <div className="settings-field__label">{t("localserver.token")}</div>
          <button className="token-display" onClick={() => setShowToken((v) => !v)} disabled={!status?.token} type="button">
            <span className="token-display__text">
              {showToken && status?.token ? status.token : "••••••••••••••••"}
            </span>
            <span className="token-display__eye">{showToken ? "🙈" : "👁"}</span>
          </button>
          <div className="settings-actions">
            <button className="btn btn--small" onClick={copyToken} disabled={!status?.token}>
              {copied ? t("localserver.copied") : t("localserver.copyToken")}
            </button>
            <button className="btn btn--small" onClick={refresh} disabled={busy}>
              {t("localserver.refresh")}
            </button>
          </div>
        </div>

        <div className="settings-field">
          <div className="settings-field__label">{t("localserver.grandCouncilTitle")}</div>
          <div className="settings-field__value">{t("localserver.grandCouncilDesc")}</div>
          <a className="settings-field__link" href="https://github.com/Linearl/GrandCouncil/releases" target="_blank" rel="noreferrer">
            {t("localserver.grandCouncilDownload")}
          </a>
        </div>
      </div>
    </div>
  );
}
