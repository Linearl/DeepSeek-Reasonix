import { createPortal } from "react-dom";
import { useId } from "react";
import { useI18n } from "../lib/i18n";

// VersionSwitchDialog (task 210): the version picker behind the status-bar
// "快速切换版本" button. Lists the versions already published under
// versions/ (newest first), marks and disables the active one, and relaunches
// into the picked tree. The original task-81 entry — publishing the staging
// build as a NEW version — stays available in the dialog footer so the old
// interaction survives the new surface (fork rule 8: keep both paths).

export type VersionEntry = {
  version: string;
  active: boolean;
  modTimeUnix: number;
};

type VersionSwitchDialogProps = {
  open: boolean;
  versions: VersionEntry[];
  /** Version whose switch is in flight; the row shows a busy state. */
  switching: string | null;
  /** Error message from a failed switch, shown inside the dialog. */
  error: string | null;
  onSwitch: (version: string) => void;
  onClose: () => void;
  onPublishStaging: () => void;
  /** Formats a version tree's mtime for the row hint (locale-aware caller). */
  formatTime: (unixSeconds: number) => string;
};

export function VersionSwitchDialog({ open, versions, switching, error, onSwitch, onClose, onPublishStaging, formatTime }: VersionSwitchDialogProps) {
  const { t } = useI18n();
  const titleId = useId();
  if (!open) return null;
  const newest = versions.find((v) => !v.active)?.version;
  return createPortal(
    <div
      className="modal-backdrop reasonix-confirm-backdrop"
      role="presentation"
      onMouseDown={(event) => {
        if (event.target === event.currentTarget) onClose();
      }}
    >
      <div className="modal reasonix-confirm-dialog reasonix-confirm-dialog--wide" role="dialog" aria-modal="true" aria-labelledby={titleId}>
        <div className="modal__title reasonix-confirm-dialog__title" id={titleId}>{t("status.versionSwitchTitle")}</div>
        <div className="reasonix-confirm-dialog__message">{t("status.versionSwitchHint")}</div>
        <div style={{ maxHeight: "50vh", overflowY: "auto", margin: "8px 0" }}>
          {versions.length === 0 && <div style={{ opacity: 0.7 }}>{t("status.versionSwitchEmpty")}</div>}
          {versions.map((v) => {
            const busy = switching === v.version;
            return (
              <div
                key={v.version}
                role="button"
                tabIndex={v.active || switching ? -1 : 0}
                aria-disabled={v.active || Boolean(switching)}
                onClick={() => {
                  if (!v.active && !switching) onSwitch(v.version);
                }}
                onKeyDown={(event) => {
                  if ((event.key === "Enter" || event.key === " ") && !v.active && !switching) {
                    event.preventDefault();
                    onSwitch(v.version);
                  }
                }}
                style={{
                  display: "flex",
                  alignItems: "center",
                  justifyContent: "space-between",
                  gap: 12,
                  padding: "8px 10px",
                  borderRadius: 8,
                  cursor: v.active || switching ? "default" : "pointer",
                  opacity: v.active ? 0.55 : 1,
                  border: "1px solid var(--border, rgba(128,128,128,.35))",
                  marginBottom: 6,
                }}
              >
                <span style={{ fontFamily: "monospace" }}>{v.version}</span>
                <span style={{ display: "flex", alignItems: "center", gap: 8, fontSize: "0.85em" }}>
                  {v.modTimeUnix > 0 && <span style={{ opacity: 0.65 }}>{formatTime(v.modTimeUnix)}</span>}
                  {v.active && <strong>{t("status.versionSwitchCurrent")}</strong>}
                  {!v.active && v.version === newest && <span>{t("status.versionSwitchNewest")}</span>}
                  {busy && <span>{t("status.versionSwitchBusy")}</span>}
                </span>
              </div>
            );
          })}
        </div>
        {error && <div style={{ color: "var(--danger, #e5484d)", margin: "4px 0 8px" }}>{error}</div>}
        <div style={{ opacity: 0.7, fontSize: "0.85em", margin: "4px 0 10px" }}>{t("status.versionSwitchFirewallNote")}</div>
        <div className="modal__actions reasonix-confirm-dialog__actions">
          <button className="btn btn--small" type="button" onClick={onClose}>{t("common.cancel")}</button>
          <button className="btn btn--small" type="button" disabled={Boolean(switching)} onClick={onPublishStaging}>
            {t("status.versionSwitchPublishStaging")}
          </button>
        </div>
      </div>
    </div>,
    document.body,
  );
}
