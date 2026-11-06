// tl1446-3: formatActionError closes the bare-error toast gap the tl1436
// survey pinned (§3.8: ~50 sites toasted `err.message` with zero human
// context — the technical track was 100% there, the human track 0%).
//
// Dual-track contract (tl1436 product stance: never hide the technical
// detail, always add the human half): the returned string is
// "{action} failed: {raw}" — the action name is the missing human half,
// the raw backend text stays verbatim for troubleshooting. Errors that
// already speak for themselves (the sessionBusy holder family, which
// embeds pid/path/host inside its localized copy) render through
// formatSessionBusyError unchanged instead of gaining a redundant prefix.

import { t, type DictKey } from "./i18n";
import { formatSessionBusyError, isSessionBusyError } from "./sessionBusyError";

function errorText(err: unknown): string {
  if (err instanceof Error) return err.message;
  if (typeof err === "string") return err;
  return String(err ?? "");
}

export function formatActionError(actionKey: DictKey, err: unknown): string {
  return formatActionErrorWith(t(actionKey), err);
}

// Variant for call sites that already hold an action label — either a
// different existing dict key or a dynamic string (e.g. a pack name).
export function formatActionErrorWith(action: string, err: unknown): string {
  const raw = errorText(err).trim();
  if (!raw) return t("errors.actionFailedNoDetail", { action });
  if (isSessionBusyError(raw)) return formatSessionBusyError(raw);
  return t("errors.actionFailed", { action, error: raw });
}
