import { Suspense, lazy, useCallback, useEffect, useState } from "react";

import { app } from "../lib/bridge";
import { useT } from "../lib/i18n";

const QRCodeSVG = lazy(() => import("qrcode.react").then((module) => ({ default: module.QRCodeSVG })));

type ServePoolStatus = {
  enabled: boolean;
  running: boolean;
  bind: string;
  addr: string;
  port: number;
  token: string;
  listen: string;
  lan_hosts: string[];
  /** tl1539: QR connect base override (config key serve_connect_base_override);
   *  empty = default lan mode. */
  base_override: string;
};

/** serveConnectTokenQuery is the single token-encoding point (tl1536/tl1539):
 *  every serve connect URL - lan host:port or custom tunnel base - rides auth
 *  in the same `?token=` query form, encoded exactly once here. The field is
 *  the one internal/servepool gateway.go checkToken accepts (alongside the
 *  Bearer header) and the one GrandCouncil's HttpClientFactory already
 *  injects, so a scan-then-fill into the GC connection form's Base URL is a
 *  complete pairing: no second secret field to type. */
function serveConnectTokenQuery(token: string): string {
  return `?token=${encodeURIComponent(token)}`;
}

/** Build the scannable lan serve connect URL for the GC mobile client
 *  (tl1536): http://<host>:<port>?token=<gateway-token>. */
export function buildServeConnectURL(host: string, port: number, token: string): string {
  return `http://${host}:${port}${serveConnectTokenQuery(token)}`;
}

/** normalizeServeConnectBase tidies a user-supplied tunnel base (tl1539):
 *  trim, strip trailing slashes, and default the scheme to https:// (a bare
 *  "xxx.trycloudflare.com" must still produce a scannable URL). Internal on
 *  purpose: the Go side stores the raw trimmed string, this is the single
 *  place URL-shape normalization happens. */
function normalizeServeConnectBase(base: string): string {
  const b = base.trim().replace(/\/+$/, "");
  if (b === "" || /^[a-z][a-z0-9+.-]*:\/\//i.test(b) || b.startsWith("//")) return b;
  return `https://${b}`;
}

/** buildServeConnectURLFromBase is the custom-base constructor (tl1539): the
 *  QR encodes a tunnel/public base (e.g. https://xxx.trycloudflare.com)
 *  instead of the lan host:port pair. Token encoding is shared with
 *  buildServeConnectURL - one encodeURIComponent, two base shapes. An empty
 *  base yields "" so callers fall back to hiding the QR block. */
export function buildServeConnectURLFromBase(base: string, token: string): string {
  const b = normalizeServeConnectBase(base);
  return b === "" ? "" : `${b}${serveConnectTokenQuery(token)}`;
}

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
  const [qrCopied, setQrCopied] = useState(false);
  const [showToken, setShowToken] = useState(false);
  const [portDraft, setPortDraft] = useState("");
  const [hostIdx, setHostIdx] = useState(0);
  const [customDraft, setCustomDraft] = useState("");

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
      setPortDraft(String(s.port || ""));
      setCustomDraft(String(s.base_override ?? ""));
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

  const copyConnectLink = async () => {
    if (!qrURL) return;
    try {
      await navigator.clipboard.writeText(qrURL);
      setQrCopied(true);
      setTimeout(() => setQrCopied(false), 1500);
    } catch {
      setKo(t("localserver.copyFailed"));
    }
  };

  // Task 130: persist the port and restart the gateway when it is enabled.
  const savePort = async () => {
    if (!status || busy) return;
    const next = Number.parseInt(String(portDraft).trim(), 10);
    if (!Number.isFinite(next) || next < 1 || next > 65535) {
      setKo(t("localserver.portInvalid"));
      return;
    }
    if (next === status.port) return;
    setBusy(true);
    try {
      await app.SetServePoolPort(next);
      await refresh();
    } catch (e) {
      failWith(e);
    } finally {
      setBusy(false);
    }
  };

  // tl1539: persist the custom connect base. The draft is trimmed here; the
  // Go side re-trims in storage as defense. Empty = back to default lan mode.
  const saveConnectBase = async () => {
    if (busy) return;
    const next = customDraft.trim();
    if (next === (status?.base_override ?? "").trim()) return;
    setBusy(true);
    try {
      await app.SetServePoolConnectBase(next);
      await refresh();
    } catch (e) {
      failWith(e);
    } finally {
      setBusy(false);
    }
  };

  const on = !!status?.enabled;
  const running = !!status?.running;
  // tl1536: candidate hosts come from ServePoolStatus.lan_hosts (best-first,
  // see lanHostsForQR). hostIdx may go stale after a refresh narrows the list,
  // so clamp before reading.
  const lanHosts: string[] = Array.isArray(status?.lan_hosts) ? status.lan_hosts : [];
  const qrHost = lanHosts.length > 0 ? lanHosts[Math.min(hostIdx, lanHosts.length - 1)] : "";
  // tl1539: base_override switches the QR's Base source. Non-empty = custom
  // tunnel mode (lan hosts unused, picker hidden); empty = verbatim tl1536
  // lan mode, byte-for-byte the URL the panel has always encoded.
  const baseOverride = (status?.base_override ?? "").trim();
  const customMode = baseOverride !== "";
  const qrURL = running && status?.token
    ? customMode
      ? buildServeConnectURLFromBase(baseOverride, status.token)
      : qrHost
        ? buildServeConnectURL(qrHost, status.port, status.token)
        : ""
    : "";

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
          font-size: var(--text-sm); letter-spacing: 0.06em; word-break: break-all;
          color: var(--text, #222); text-align: left;
        }
        .token-display__eye { font-size: var(--text-base); opacity: .7; margin-left: 8px; flex: none; }
        .settings-field__link { color: var(--link, #4a9eff); font-size: var(--text-md); text-decoration: none; }
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
          <div className="settings-field__label">{t("localserver.port")}</div>
          <div className="settings-field__value" style={{ display: "flex", gap: 8, alignItems: "center" }}>
            <input
              className="mem-input"
              type="number"
              min={1}
              max={65535}
              value={portDraft}
              disabled={busy}
              onChange={(e) => setPortDraft(e.target.value)}
              onKeyDown={(e) => { if (e.key === "Enter") void savePort(); }}
              style={{ width: 120 }}
              aria-label={t("localserver.port")}
            />
            <button className="btn btn--primary btn--small" type="button" disabled={busy || !portDraft} onClick={() => void savePort()}>
              {t("localserver.savePort")}
            </button>
          </div>
          <small>{t("localserver.portHint")}</small>
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
            <button className="btn btn--secondary btn--small" onClick={copyToken} disabled={!status?.token}>
              {copied ? t("localserver.copied") : t("localserver.copyToken")}
            </button>
            <button className="btn btn--secondary btn--small" onClick={refresh} disabled={busy}>
              {t("localserver.refresh")}
            </button>
          </div>
        </div>

        {qrURL ? (
          <div className="settings-field" data-testid="serve-qr">
            <div className="settings-field__label">{t("localserver.qrTitle")}</div>
            <div style={{ display: "flex", gap: 16, alignItems: "flex-start", flexWrap: "wrap" }}>
              <div
                style={{
                  padding: 8, borderRadius: 8, background: "#fff",
                  border: "1px solid color-mix(in srgb, var(--border, #888) 40%, transparent)",
                }}
              >
                <Suspense fallback={<div style={{ width: 180, height: 180 }} />}>
                  <QRCodeSVG value={qrURL} size={180} marginSize={1} />
                </Suspense>
              </div>
              <div style={{ flex: 1, minWidth: 220, display: "flex", flexDirection: "column", gap: 8 }}>
                {!customMode && lanHosts.length > 1 ? (
                  <select
                    className="mem-input"
                    value={Math.min(hostIdx, lanHosts.length - 1)}
                    onChange={(e) => setHostIdx(Number(e.target.value))}
                    aria-label={t("localserver.qrHostPick")}
                  >
                    {lanHosts.map((h, i) => (
                      <option key={h} value={i}>{h}</option>
                    ))}
                  </select>
                ) : null}
                <code style={{ wordBreak: "break-all", fontSize: "var(--text-sm)" }}>{qrURL}</code>
                <div className="settings-actions">
                  <button className="btn btn--secondary btn--small" onClick={copyConnectLink} type="button">
                    {qrCopied ? t("localserver.copied") : t("localserver.qrCopyLink")}
                  </button>
                </div>
                <small>{t("localserver.qrHint")}</small>
                <details>
                  <summary style={{ cursor: "pointer", fontSize: "var(--text-sm)" }}>{t("localserver.qrCustomToggle")}</summary>
                  <div style={{ display: "flex", gap: 8, alignItems: "center", flexWrap: "wrap", marginTop: 8 }}>
                    <input
                      className="mem-input"
                      type="text"
                      value={customDraft}
                      placeholder="https://xxx.trycloudflare.com"
                      onChange={(e) => setCustomDraft(e.target.value)}
                      onKeyDown={(e) => { if (e.key === "Enter") void saveConnectBase(); }}
                      aria-label={t("localserver.qrCustomLabel")}
                      style={{ flex: 1, minWidth: 200 }}
                    />
                    <button
                      className="btn btn--secondary btn--small"
                      type="button"
                      disabled={busy || customDraft.trim() === baseOverride}
                      onClick={() => void saveConnectBase()}
                    >
                      {t("localserver.qrCustomSave")}
                    </button>
                  </div>
                  <small>{t("localserver.qrCustomHint")}</small>
                </details>
              </div>
            </div>
          </div>
        ) : null}

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
