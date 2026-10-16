// Task 663 gap ①/⑥: the hang face of the one-click analysis family. A stuck
// session ("没崩但卡死") never produces a crash report, so the crash overlay
// never fires; this watch polls the backend's doctor-responsiveness hang gate
// (task 370 verdicts served by App.HangAnalysisAvailability) and surfaces the
// same analyze entry the crash/performance prompts use. Upstream comparison:
// the stream idle watchdog self-heals in-stream stalls at 5m — what surfaces
// here is the residue it cannot classify, which is exactly what the backend
// gate calls hung.

import { t } from "./i18n";
import { analyzeEntryButton, copyButton, type CrashPayload } from "./crash";
import { buildCrashIssueSkeleton } from "./crashIssue";
import { snapshotBreadcrumbs } from "./breadcrumbs";
import { rtNoticeHead, rtNoticeHost, rtNoticeMount } from "./rtNotice";
import type { HangAnalysisAvailabilityReport } from "./types";

declare const __BUILD_COMMIT__: string;
declare const __BUILD_CHANNEL__: string;

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

// Task 769: the hang face shares the crash.analyzeFailed failure note, which
// tells the user to fall back to「复制」— so the prompt must actually carry the
// copy button (task 674's zero-dependency feedback path; the upstream upload
// endpoint is down and the analysis start can fail on its own). The payload
// mirrors the backend's hangAnalysisPayload field naming
// (desktop/crash_analysis_hang.go: kind "performance" — a hang is a
// responsiveness failure, not a crash; label "session.hang"; errorType
// "SessionUnresponsive"; topFrame "doctor.responsiveness:<verdict>").
function buildHangIssuePayload(report: HangAnalysisAvailabilityReport): CrashPayload {
  const verdict = (report.verdict || "").trim() || "unknown";
  const detail = typeof report.detail === "string" ? report.detail.trim() : "";
  return {
    schemaVersion: 2,
    source: "frontend.hang",
    kind: "performance",
    label: "session.hang",
    message: [`[session.hang]`, `verdict: ${verdict}`, detail].filter(Boolean).join("\n"),
    errorType: "SessionUnresponsive",
    errorMessage: `Session turn is unresponsive (doctor verdict: ${verdict}).${detail ? ` ${detail}` : ""}`,
    topFrame: `doctor.responsiveness:${verdict}`,
    buildCommit: typeof __BUILD_COMMIT__ === "string" ? __BUILD_COMMIT__ : "dev",
    channel: typeof __BUILD_CHANNEL__ === "string" ? __BUILD_CHANNEL__ : "",
    language: typeof navigator !== "undefined" ? navigator.language || "" : "",
    view: "",
    breadcrumbs: snapshotBreadcrumbs(),
    occurredAt: new Date().toISOString(),
  };
}

function paintHangPrompt(report: HangAnalysisAvailabilityReport): void {
  if (typeof document === "undefined") return;
  // 任务 910：卡顿=warn 档（黄，警戒非故障——语义固定，见 rtNotice.ts）。
  const host = rtNoticeHost("hang-analysis-prompt", "warn");
  const title = document.createElement("div");
  title.className = "hang-analysis__title rt-notice__title";
  title.textContent = t("hangPrompt.title");
  const intro = document.createElement("div");
  intro.className = "hang-analysis__intro rt-notice__intro";
  intro.textContent = t("hangPrompt.intro");
  const body = document.createElement("pre");
  body.className = "hang-analysis__body rt-notice__body";
  const detail = typeof report.detail === "string" ? report.detail.trim() : "";
  body.textContent = detail ? `verdict: ${report.verdict}\n${detail}` : `verdict: ${report.verdict}`;
  const actions = document.createElement("div");
  actions.className = "hang-analysis__actions rt-notice__actions";
  const analysisNote = document.createElement("div");
  analysisNote.className = "hang-analysis__note rt-notice__analysis";
  const start = window.go?.main?.App?.StartHangAnalysis;
  const analyze = start
    ? analyzeEntryButton("hang-analysis__analyze rt-btn rt-btn--primary", analysisNote, () => start())
    : null;
  const dismiss = document.createElement("button");
  dismiss.className = "hang-analysis__dismiss rt-btn";
  dismiss.textContent = t("performanceReport.dismiss");
  // 头行关闭 × 与动作行「关闭」走同一收口（demo 提案节两处并存）。
  const closePrompt = () => {
    lastDismissedAt = Date.now();
    host.remove();
  };
  dismiss.onclick = closePrompt;
  // Task 769 button layout: [一键分析][复制][关闭] — the same three-slot family
  // as the performance prompt (crash.ts paintPerformancePrompt). Copy is
  // unconditional: the analyzeFailed note names it, so it must always exist.
  const copy = copyButton(buildCrashIssueSkeleton(buildHangIssuePayload(report)), "hang-analysis__copy rt-btn");
  if (analyze) actions.append(analyze);
  actions.append(copy);
  actions.append(dismiss);
  const head = rtNoticeHead(title, "warn", t("common.close"), closePrompt);
  rtNoticeMount(host, head, intro, body, actions, analysisNote);
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
