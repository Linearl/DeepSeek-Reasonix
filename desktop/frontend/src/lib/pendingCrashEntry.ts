// Task 663 gap ④: the startup face for a REAL crash. A Go panic kills the
// process, so the crash overlay and the rotate-tab analysis pipeline can never
// run for it — the report only exists as a pending queue file captured by the
// next launch (App.PendingCrashSnapshot, snapshotted before flushPendingCrash
// ships or drops it). This entry paints that snapshot as an explicit
// "previous run crashed" banner with the same one-click analysis flow, instead
// of the old silent ship-or-drop.

import { t } from "./i18n";
import { analyzeEntryButton } from "./crash";
import type { PendingCrashSnapshotReport } from "./types";

const PENDING_CRASH_DISMISS_KEY = "reasonix:pending-crash-dismissed";

/** Test seam: the banner is module state via sessionStorage + the DOM. */
export function resetPendingCrashEntryForTest(): void {
  try {
    sessionStorage.removeItem(PENDING_CRASH_DISMISS_KEY);
  } catch {
    // sessionStorage can throw in exotic embeds; the install is a no-op then.
  }
  document.getElementById("pending-crash-entry")?.remove();
}

export function pendingCrashDismissedThisRun(): boolean {
  try {
    return sessionStorage.getItem(PENDING_CRASH_DISMISS_KEY) !== null;
  } catch {
    return true;
  }
}

export function shouldSurfacePendingCrash(
  report: PendingCrashSnapshotReport | null | undefined,
  opts: { dismissed: boolean },
): boolean {
  return Boolean(report && report.count > 0 && !opts.dismissed);
}

// The banner previews the newest payload: its error line, clipped — the full
// diagnostic travels to the analysis (and Copy-able text) on click, not here.
export function pendingCrashPreview(report: PendingCrashSnapshotReport): string {
  const newest = report.reports?.[0];
  if (!newest) return "";
  try {
    const payload = JSON.parse(newest) as { errorMessage?: string; message?: string; label?: string };
    const line = payload.errorMessage?.trim() || payload.message?.trim() || payload.label || "";
    return line.length > 240 ? `${line.slice(0, 240)}…` : line;
  } catch {
    return "";
  }
}

function paintPendingCrashPrompt(report: PendingCrashSnapshotReport): void {
  if (typeof document === "undefined") return;
  let host = document.getElementById("pending-crash-entry");
  if (!host) {
    host = document.createElement("div");
    host.id = "pending-crash-entry";
    document.body.appendChild(host);
  }
  const title = document.createElement("div");
  title.className = "pending-crash__title";
  title.textContent = `${t("pendingCrash.title")}（${report.count}）`;
  const intro = document.createElement("div");
  intro.className = "pending-crash__intro";
  intro.textContent = t("pendingCrash.intro");
  const children: HTMLElement[] = [title, intro];
  const preview = pendingCrashPreview(report);
  if (preview) {
    const body = document.createElement("pre");
    body.className = "pending-crash__body";
    body.textContent = preview;
    children.push(body);
  }
  const actions = document.createElement("div");
  actions.className = "pending-crash__actions";
  const analysisNote = document.createElement("div");
  analysisNote.className = "pending-crash__note";
  const analyze =
    report.reports?.length && window.go?.main?.App?.StartCrashAnalysis
      ? analyzeEntryButton("pending-crash__analyze", analysisNote, () => {
          const start = window.go?.main?.App?.StartCrashAnalysis;
          if (!start) return Promise.reject(new Error("analysis binding vanished"));
          // The newest payload is schema-2 JSON captured by the backend — it
          // travels to StartCrashAnalysis verbatim.
          return start("crash", report.reports![0]);
        })
      : null;
  const dismiss = document.createElement("button");
  dismiss.className = "pending-crash__dismiss";
  dismiss.textContent = t("performanceReport.dismiss");
  dismiss.onclick = () => {
    try {
      sessionStorage.setItem(PENDING_CRASH_DISMISS_KEY, "1");
    } catch {
      // The banner still closes for this paint; it may return next run.
    }
    host?.remove();
  };
  if (analyze) actions.append(analyze);
  actions.append(dismiss);
  host.replaceChildren(...children, actions, analysisNote);
}

export function installPendingCrashAnalysisEntry(): void {
  if (typeof window === "undefined" || typeof document === "undefined") return;
  if (pendingCrashDismissedThisRun()) return;
  const probe = window.go?.main?.App?.PendingCrashSnapshot;
  if (!probe) return;
  void probe()
    .then((report) => {
      if (shouldSurfacePendingCrash(report, { dismissed: pendingCrashDismissedThisRun() })) {
        paintPendingCrashPrompt(report);
      }
    })
    .catch(() => {
      // Startup probes must never throw into the boot path.
    });
}
