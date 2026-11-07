// tl1476: file list for the rewind preflight dialog. Renders the safe group
// (what the rewind will restore/remove) and the unsafe group with per-file
// reasons. When the unsafe group is non-empty the dialog's confirm button is
// disabled — the visible reasons are the explanation.

import { useT, type DictKey } from "../lib/i18n";
import type { RewindPreflightFile } from "../lib/rewindSafety";

const REASON_I18N: Record<string, DictKey> = {
  outside_workspace: "rewindPreflight.reason.outsideWorkspace",
  missing_payload: "rewindPreflight.reason.missingPayload",
  permission_denied: "rewindPreflight.reason.permissionDenied",
  changed: "rewindPreflight.reason.changed",
};

export function RewindPreflightList({ files }: { files: readonly RewindPreflightFile[] }) {
  const t = useT();
  const safe = files.filter((file) => file.safe);
  const unsafe = files.filter((file) => !file.safe);
  return (
    <div className="rewind-preflight">
      {safe.length > 0 && (
        <div className="rewind-preflight__group">
          <div className="rewind-preflight__heading">{t("rewindPreflight.safeHeading", { n: safe.length })}</div>
          <ul className="rewind-preflight__list">
            {safe.map((file) => (
              <li key={file.path} className="rewind-preflight__file rewind-preflight__file--safe">{file.path}</li>
            ))}
          </ul>
        </div>
      )}
      {unsafe.length > 0 && (
        <div className="rewind-preflight__group">
          <div className="rewind-preflight__heading rewind-preflight__heading--unsafe">{t("rewindPreflight.unsafeHeading", { n: unsafe.length })}</div>
          <ul className="rewind-preflight__list">
            {unsafe.map((file) => (
              <li key={file.path} className="rewind-preflight__file rewind-preflight__file--unsafe">
                <span className="rewind-preflight__path">{file.path}</span>
                <span className="rewind-preflight__reason">{t(REASON_I18N[file.reasonKey ?? "changed"])}</span>
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  );
}
