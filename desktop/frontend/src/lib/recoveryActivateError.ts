// tl1415c B3: localized rendering of the two version-activation refusals.
//
// The backend marks them with stable machine codes over the Wails boundary
// (same "reasonix_error:" channel as topic_archive.go / inbox_app.go — see
// recovery_lineage.go recoveryAlreadyMainCode/recoveryActiveWorkCode):
//   reasonix_error:recovery_already_main: …
//   reasonix_error:recovery_active_work: …
// Dual-track contract (tl1446 standard, extended to the Go side): the
// localized human copy leads and names the next step; the backend's technical
// text stays verbatim after it (hidden or fabricated copy is forbidden).
// Unmarked errors pass through unchanged (older-backend compatibility, same
// policy as sessionBusyError.ts).

import { t } from "./i18n";

export const RECOVERY_ALREADY_MAIN_PREFIX = "reasonix_error:recovery_already_main: ";
export const RECOVERY_ACTIVE_WORK_PREFIX = "reasonix_error:recovery_active_work: ";

function errorText(error: unknown): string {
  if (error instanceof Error) return error.message;
  if (typeof error === "string") return error;
  return String(error ?? "");
}

export function formatRecoveryActivateError(error: unknown): string {
  const raw = errorText(error);
  const isAlreadyMain = raw.startsWith(RECOVERY_ALREADY_MAIN_PREFIX);
  const isActiveWork = !isAlreadyMain && raw.startsWith(RECOVERY_ACTIVE_WORK_PREFIX);
  if (!isAlreadyMain && !isActiveWork) return raw;
  const cause = raw.slice((isAlreadyMain ? RECOVERY_ALREADY_MAIN_PREFIX : RECOVERY_ACTIVE_WORK_PREFIX).length);
  const human = isAlreadyMain
    ? t("settings.sessionVersions.activateAlreadyMain")
    : t("settings.sessionVersions.activateActiveWork");
  // The English human copy and the backend cause can be near-duplicates; keep
  // one of them instead of printing the same sentence twice. Any other locale
  // appends the technical cause verbatim (双轨并存).
  if (!cause || human.includes(cause)) return human;
  return `${human}（${cause}）`;
}
