import { useCallback, useEffect, useState } from "react";
import { AlertTriangle, HardDriveDownload, Loader2, RefreshCcw } from "lucide-react";
import { app } from "../lib/bridge";
import { useT } from "../lib/i18n";

// 任务 846 — 数据目录迁移向导（实验特性，experimental_home_pointer 之后可达）。
// 纯展示+编排层：校验/事务/回滚全部在 Go 侧（internal/homemigrate）；本组件
// 只负责预检调参、六条风险勾选（缺一禁发）、执行与结果呈现、恢复默认。
// 只读路径展示与 StorageSettingsPage 同款 mem-input 形态。

type HomePointerStatusView = Awaited<ReturnType<typeof app.HomePointerStatus>>;
type HomeMigrationPreviewView = Awaited<ReturnType<typeof app.HomeMigrationPreview>>;
type HomeMigrationResultView = Awaited<ReturnType<typeof app.HomeMigrationExecute>>;

export function HomePointerLabPane({ enabled }: { enabled: boolean }) {
  const t = useT();
  const [status, setStatus] = useState<HomePointerStatusView | null>(null);
  const [target, setTarget] = useState("");
  const [preview, setPreview] = useState<HomeMigrationPreviewView | null>(null);
  const [acknowledged, setAcknowledged] = useState<ReadonlySet<string>>(new Set());
  const [running, setRunning] = useState(false);
  const [result, setResult] = useState<HomeMigrationResultView | null>(null);
  const [actionError, setActionError] = useState("");

  const reloadStatus = useCallback(async () => {
    try {
      setStatus(await app.HomePointerStatus());
    } catch {
      setStatus(null);
    }
  }, []);

  useEffect(() => {
    void reloadStatus();
  }, [reloadStatus]);

  const risks = preview?.risks ?? [];
  const allAcknowledged = risks.length > 0 && risks.every((risk) => acknowledged.has(risk.id));
  const busy = running;

  const runPreview = async () => {
    setActionError("");
    setResult(null);
    setPreview(null);
    setAcknowledged(new Set());
    if (!target.trim()) return;
    try {
      setPreview(await app.HomeMigrationPreview(target.trim()));
    } catch (error) {
      setActionError(String(error));
    }
  };

  const runMigration = async () => {
    if (!preview?.valid || !allAcknowledged) return;
    setRunning(true);
    setActionError("");
    try {
      const outcome = await app.HomeMigrationExecute(preview.target, risks.map((risk) => risk.id));
      setResult(outcome);
      await reloadStatus();
    } catch (error) {
      setActionError(String(error));
    } finally {
      setRunning(false);
    }
  };

  const resetToDefault = async () => {
    setRunning(true);
    setActionError("");
    try {
      await app.HomeMigrationResetToDefault();
      setPreview(null);
      setResult(null);
      setAcknowledged(new Set());
      await reloadStatus();
    } catch (error) {
      setActionError(String(error));
    } finally {
      setRunning(false);
    }
  };

  return (
    <div className="home-pointer-pane" data-testid="home-pointer-pane">
      {!enabled && <div className="banner banner--warning">{t("settings.homePointer.switchOffHint")}</div>}
      {status?.migrationLockActive && (
        <div className="banner banner--error" role="alert">{t("settings.homePointer.lockActive")}</div>
      )}
      {status?.pointerError && (
        <div className="banner banner--warning">{t("settings.homePointer.pointerError")}: {status.pointerError}</div>
      )}

      <div className="settings-field">
        <div className="settings-field__label">{t("settings.homePointer.currentActive")}</div>
        <div className="settings-field__control settings-path-value">
          <input className="mem-input" value={status?.activeHome ?? ""} readOnly aria-label={t("settings.homePointer.currentActive")} />
        </div>
      </div>
      <div className="settings-field">
        <div className="settings-field__label">{t("settings.homePointer.defaultHome")}</div>
        <div className="settings-field__control settings-path-value">
          <input className="mem-input" value={status?.defaultHome ?? ""} readOnly aria-label={t("settings.homePointer.defaultHome")} />
        </div>
        {status?.pointerActive && <div className="settings-field__hint"><span className="settings-field__hint-line">{t("settings.homePointer.pointerActiveHint")}</span></div>}
      </div>

      {enabled && (
        <>
          <div className="settings-field">
            <div className="settings-field__label">{t("settings.homePointer.targetLabel")}</div>
            <div className="settings-field__control home-pointer-pane__row">
              <input
                className="mem-input"
                value={target}
                disabled={busy}
                placeholder={t("settings.homePointer.targetPlaceholder")}
                aria-label={t("settings.homePointer.targetLabel")}
                onChange={(event) => setTarget(event.target.value)}
              />
              <button className="btn btn--secondary btn--small" type="button" disabled={busy || !target.trim()} onClick={() => void runPreview()}>
                {t("settings.homePointer.preview")}
              </button>
            </div>
            <div className="settings-field__hint"><span className="settings-field__hint-line">{t("settings.homePointer.targetHint")}</span></div>
          </div>

          {preview && !preview.valid && (
            <div className="banner banner--error" role="alert">{preview.error || t("settings.homePointer.previewFailed")}</div>
          )}

          {preview?.valid && (
            <div className="home-pointer-pane__risks" data-testid="home-pointer-risks">
              <div className="settings-field__label">
                <AlertTriangle size={16} /> {t("settings.homePointer.riskTitle")}
              </div>
              <ul>
                {risks.map((risk) => (
                  <li key={risk.id}>
                    <label className={`home-pointer-pane__risk home-pointer-pane__risk--${risk.severity}`}>
                      <input
                        type="checkbox"
                        checked={acknowledged.has(risk.id)}
                        disabled={busy}
                        onChange={(event) => {
                          const next = new Set(acknowledged);
                          if (event.target.checked) next.add(risk.id); else next.delete(risk.id);
                          setAcknowledged(next);
                        }}
                      />
                      <span>{t(risk.detailKey as Parameters<typeof t>[0])}</span>
                    </label>
                  </li>
                ))}
              </ul>
              {(preview.warnings ?? []).includes("target-path-onedrive") && (
                <div className="banner banner--warning">{t("settings.homePointer.warning.onedrive")}</div>
              )}
              <div className="home-pointer-pane__row">
                <button className="btn btn--primary btn--small" type="button" disabled={busy || !allAcknowledged} onClick={() => void runMigration()}>
                  {busy ? <Loader2 size={14} className="spin" /> : <HardDriveDownload size={14} />}
                  {t("settings.homePointer.startMigration")}
                </button>
                {busy && <span>{t("settings.homePointer.migrating")}</span>}
              </div>
            </div>
          )}

          {result && (
            <div className={`banner ${result.success ? "banner--ok" : "banner--error"}`} role={result.success ? undefined : "alert"} data-testid="home-pointer-result">
              {result.success ? t("settings.homePointer.donePrefix") : t("settings.homePointer.abortPrefix")}: {result.detail}
            </div>
          )}
          {actionError && <div className="banner banner--error" role="alert">{actionError}</div>}

          <div className="home-pointer-pane__row">
            <button className="btn btn--secondary btn--small" type="button" disabled={busy || !status?.pointerActive} onClick={() => void resetToDefault()}>
              <RefreshCcw size={14} />
              {t("settings.homePointer.reset")}
            </button>
            <span className="settings-field__hint-line">{t("settings.homePointer.resetHint")}</span>
          </div>
        </>
      )}
    </div>
  );
}
