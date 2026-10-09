// Task 663 gap ①/⑥: the hang face of the one-click analysis family. A stuck
// session ("没崩但卡死") never produces a crash report, so the crash overlay
// never fires; this watch polls the backend's doctor-responsiveness hang gate
// (task 370 verdicts served by App.HangAnalysisAvailability) and surfaces the
// same analyze entry the crash/performance prompts use. Upstream comparison:
// the stream idle watchdog self-heals in-stream stalls at 5m — what surfaces
// here is the residue it cannot classify, which is exactly what the backend
// gate calls hung.

import { t } from "./i18n";
import { analyzeEntryButton } from "./crash";
import type { HangAnalysisAvailabilityReport } from "./types";

const HANG_WATCH_STARTUP_GRACE_MS = 60_000;
const HANG_WATCH_POLL_MS = 90_000;
const HANG_DISMISS_COOLDOWN_MS = 10 * 60_000;

let installed = false;
let lastDismissedAt = 0;

/** Test seam: the watch is module state. */
export function resetHangWatchForTest(): void {
  installed = false;
  lastDismissedAt = 0;
}

/** Test seam: pretend the user just dismissed the prompt. */
export function markHangPromptDismissedForTest(now: number): void {
  lastDismissedAt = now;
}

export function hangPromptDismissedRecently(now: number): boolean {
  return now - lastDismissedAt < HANG_DISMISS_COOLDOWN_MS;
}

// Pure surface decision: only a backend-hung report counts, and only on a
// visible, focused window that is not already showing (or cooling down from a
// dismissal) — the same gating family as the performance pressure prompt.
export function shouldSurfaceHangPrompt(
  report: HangAnalysisAvailabilityReport | null | undefined,
  opts: { hidden: boolean; unfocused: boolean; alreadyShowing: boolean; dismissedRecently: boolean },
): boolean {
  if (!report?.ready || !report.hung) return false;
  if (opts.hidden || opts.unfocused) return false;
  if (opts.alreadyShowing || opts.dismissedRecently) return false;
  return true;
}

function paintHangPrompt(report: HangAnalysisAvailabilityReport): void {
  if (typeof document === "undefined") return;
  let host = document.getElementById("hang-analysis-prompt");
  if (!host) {
    host = document.createElement("div");
    host.id = "hang-analysis-prompt";
    document.body.appendChild(host);
  }
  const title = document.createElement("div");
  title.className = "hang-analysis__title";
  title.textContent = t("hangPrompt.title");
  const intro = document.createElement("div");
  intro.className = "hang-analysis__intro";
  intro.textContent = t("hangPrompt.intro");
  const body = document.createElement("pre");
  body.className = "hang-analysis__body";
  const detail = typeof report.detail === "string" ? report.detail.trim() : "";
  body.textContent = detail ? `verdict: ${report.verdict}\n${detail}` : `verdict: ${report.verdict}`;
  const actions = document.createElement("div");
  actions.className = "hang-analysis__actions";
  const analysisNote = document.createElement("div");
  analysisNote.className = "hang-analysis__note";
  const start = window.go?.main?.App?.StartHangAnalysis;
  const analyze = start
    ? analyzeEntryButton("hang-analysis__analyze", analysisNote, () => start())
    : null;
  const dismiss = document.createElement("button");
  dismiss.className = "hang-analysis__dismiss";
  dismiss.textContent = t("performanceReport.dismiss");
  dismiss.onclick = () => {
    lastDismissedAt = Date.now();
    host?.remove();
  };
  if (analyze) actions.append(analyze);
  actions.append(dismiss);
  host.replaceChildren(title, intro, body, actions, analysisNote);
}

/** Test seam: paint the prompt directly — the watch path is timer-gated
 * (startup grace + poll cadence), which no suite should wait on. */
export function paintHangPromptForTest(report: HangAnalysisAvailabilityReport): void {
  paintHangPrompt(report);
}

export function installHangAnalysisWatch(): void {
  if (installed || typeof window === "undefined" || typeof document === "undefined") return;
  const probe = window.go?.main?.App?.HangAnalysisAvailability;
  if (!probe || !window.runtime) return;
  installed = true;
  window.setTimeout(() => {
    window.setInterval(async () => {
      const hidden = document.visibilityState === "hidden";
      const unfocused = document.hasFocus?.() === false;
      try {
        const report = await probe();
        const due = shouldSurfaceHangPrompt(report, {
          hidden,
          unfocused,
          alreadyShowing: document.getElementById("hang-analysis-prompt") !== null,
          dismissedRecently: hangPromptDismissedRecently(Date.now()),
        });
        if (due && report) paintHangPrompt(report);
      } catch {
        // A failed probe must never throw into the timer path.
      }
    }, HANG_WATCH_POLL_MS);
  }, HANG_WATCH_STARTUP_GRACE_MS);
}
