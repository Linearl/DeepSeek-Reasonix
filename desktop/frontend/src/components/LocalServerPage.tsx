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

/** LocalServerPage is the Settings → 集成与连接 → 本地服务器服务 panel.
 *  It exposes the serve pool remote gateway toggle, its listen address, and a
 *  "copy gateway-token to clipboard" action for GrandCouncil / Tailscale setup. */
export function LocalServerPage() {
  const t = useT();
  const [status, setStatus] = useState<ServePoolStatus | null>(null);
  const [busy, setBusy] = useState(false);
  const [ko, setKo] = useState("");
  const [copied, setCopied] = useState(false);

  const refresh = useCallback(async () => {
    try {
      const s = await app.ServePoolStatus();
      setStatus(s);
      setKo("");
    } catch (e) {
      setKo(String(e));
    }
  }, []);

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
      setKo(String(e));
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
      <div className="settings-section">
        <div className="settings-section__header">
          <h3>{t("localserver.title")}</h3>
          <p>{t("localserver.desc")}</p>
        </div>

        {ko && <div className="settings-error">{ko}</div>}

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
          <div className="settings-field__value">
            <code>{status?.token || "…"}</code>
          </div>
          <div className="settings-actions">
            <button className="button button--secondary" onClick={copyToken} disabled={!status?.token}>
              {copied ? t("localserver.copied") : t("localserver.copyToken")}
            </button>
            <button className="button button--secondary" onClick={refresh} disabled={busy}>
              {t("localserver.refresh")}
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
