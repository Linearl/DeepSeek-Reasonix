// Task 663 gap ④: the startup face for a REAL crash. A Go panic kills the
// process, so the crash overlay and the rotate-tab analysis pipeline can never
// run for it — the report only exists as a pending queue file captured by the
// next launch (App.PendingCrashSnapshot, snapshotted before flushPendingCrash
// ships or drops it). This entry paints that snapshot as an explicit
// "previous run crashed" banner with the same one-click analysis flow, instead
// of the old silent ship-or-drop.
//
// Task 694: the banner is imperative DOM painted once, and it races the locale
// boot — the install runs before LocaleProvider's first render flips the
// currentLocale mirror (and before the lazy zh/zh-TW dictionary lands), so on
// zh installs the title/intro/Dismiss froze in English (2026-10-09 user
// screenshot). The provider mirrors every locale application into
// <html lang> (lib/i18n.tsx), so observing that attribute and re-rendering the
// banner repaints it with the settled locale — covering both the boot race and
// a later config-driven language switch.

import { tFor, type Locale } from "./i18n";
import { analyzeEntryButton } from "./crash";
import { rtNoticeHead, rtNoticeHost, rtNoticeMount } from "./rtNotice";
import type { PendingCrashSnapshotReport } from "./types";

const PENDING_CRASH_DISMISS_KEY = "reasonix:pending-crash-dismissed";

// The provider mirrors the applied locale into <html lang> ("zh-CN"/"zh-TW"/"en",
// empty until its first effect run). Translating against the DOM's declared
// locale — not the module mirror — is what lets the flip-triggered repaint below
// render the locale that triggered it.
function localeFromHtmlLang(): Locale {
  const lang = typeof document !== "undefined" ? document.documentElement.lang.toLowerCase() : "";
  if (lang === "zh-cn") return "zh";
  if (lang === "zh-tw" || lang === "zh-hk" || lang === "zh-mo" || lang === "zh-hant") return "zh-TW";
  return "en";
}

// The report behind the currently visible banner, kept so a locale flip can
// re-render it; cleared on dismiss so the observer never resurrects the banner.
let visibleReport: PendingCrashSnapshotReport | null = null;
let localeWatchInstalled = false;

/** Test seam: the banner is module state via sessionStorage + the DOM. */
export function resetPendingCrashEntryForTest(): void {
  try {
    sessionStorage.removeItem(PENDING_CRASH_DISMISS_KEY);
  } catch {
    // sessionStorage can throw in exotic embeds; the install is a no-op then.
  }
  visibleReport = null;
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

// Split from paint so the locale-flip watch can re-render the same report in
// place. The spend-confirmation/progress note resets on a repaint — the flip
// happens at boot (or on a settings language switch), where losing a half-armed
// confirmation is the honest outcome; the analysis poller self-cleans when its
// note node disconnects.
function renderPendingCrashBanner(host: HTMLElement, report: PendingCrashSnapshotReport): void {
  const tt = tFor(localeFromHtmlLang());
  const title = document.createElement("div");
  title.className = "pending-crash__title rt-notice__title";
  title.textContent = `${tt("pendingCrash.title")}（${report.count}）`;
  const intro = document.createElement("div");
  intro.className = "pending-crash__intro rt-notice__intro";
  intro.textContent = tt("pendingCrash.intro");
  const slots: HTMLElement[] = [intro];
  const preview = pendingCrashPreview(report);
  if (preview) {
    // Task 695: the panic line itself is backend-log verbatim (translating it
    // would corrupt the technical evidence), so it ships with a localized
    // caption saying exactly that — the zh face of the banner then has no bare
    // English block. Rides the intro class: same family look as the hang
    // prompt, zero CSS delta (ratchet stays untouched).
    const previewNote = document.createElement("div");
    previewNote.className = "pending-crash__intro rt-notice__intro";
    previewNote.textContent = tt("pendingCrash.previewNote");
    slots.push(previewNote);
    const body = document.createElement("pre");
    body.className = "pending-crash__body rt-notice__body";
    body.textContent = preview;
    slots.push(body);
  }
  const actions = document.createElement("div");
  actions.className = "pending-crash__actions rt-notice__actions";
  const analysisNote = document.createElement("div");
  analysisNote.className = "pending-crash__note rt-notice__analysis";
  const analyze =
    report.reports?.length && window.go?.main?.App?.StartCrashAnalysis
      ? analyzeEntryButton("pending-crash__analyze rt-btn rt-btn--primary", analysisNote, () => {
          const start = window.go?.main?.App?.StartCrashAnalysis;
          if (!start) return Promise.reject(new Error("analysis binding vanished"));
          // The newest payload is schema-2 JSON captured by the backend — it
          // travels to StartCrashAnalysis verbatim.
          return start("crash", report.reports![0]);
        })
      : null;
  const dismiss = document.createElement("button");
  dismiss.className = "pending-crash__dismiss rt-btn";
  dismiss.textContent = tt("performanceReport.dismiss");
  // 头行关闭 × 与动作行「关闭」走同一收口（demo 提案节两处并存）。
  const closeEntry = () => {
    try {
      sessionStorage.setItem(PENDING_CRASH_DISMISS_KEY, "1");
    } catch {
      // The banner still closes for this paint; it may return next run.
    }
    visibleReport = null;
    host.remove();
  };
  // Task 1051: 「关闭」只写 sessionStorage 隐藏本次，报告仍留队列 —— 14 天
  // 保留期内每次启动都重弹。「清空」（关闭右侧）调后端把队列真删掉，才是一次性
  // 告别。收尾与「关闭」同款：置 dismissed + 摘横幅；失败时按钮保持可用并把
  // 后端原因写进 note（不静默吞掉，用户才知道为什么没清掉）。
  const clearAll = document.createElement("button");
  clearAll.className = "pending-crash__clear rt-btn";
  clearAll.textContent = tt("pendingCrash.clearAll");
  clearAll.onclick = async () => {
    const clear = window.go?.main?.App?.ClearPendingCrashReports;
    if (!clear) {
      analysisNote.textContent = tt("pendingCrash.clearUnavailable");
      return;
    }
    clearAll.disabled = true;
    try {
      await clear();
      closeEntry();
    } catch (err) {
      clearAll.disabled = false;
      analysisNote.textContent = err instanceof Error && err.message ? err.message : String(err);
    }
  };
  dismiss.onclick = closeEntry;
  if (analyze) actions.append(analyze);
  actions.append(clearAll, dismiss);
  slots.push(actions, analysisNote);
  // 任务 910：崩溃=danger 档（红，语义归位——过去是硬编码橙边框 + --accent
  // 橙标题两种来源，现统一走 danger token，见 rtNotice.ts）。
  const head = rtNoticeHead(title, "danger", tt("common.close"), closeEntry);
  rtNoticeMount(host, head, ...slots);
}

// One observer for the module lifetime: LocaleProvider writes the applied
// locale to <html lang> on mount and on every switch, so an attribute flip is
// the canonical "locale settled/changed" signal for this out-of-React surface.
function installLocaleFlipWatch(): void {
  if (localeWatchInstalled || typeof document === "undefined" || typeof MutationObserver === "undefined") return;
  localeWatchInstalled = true;
  new MutationObserver(() => {
    const host = document.getElementById("pending-crash-entry");
    if (!host || !visibleReport) return;
    renderPendingCrashBanner(host, visibleReport);
  }).observe(document.documentElement, { attributes: true, attributeFilter: ["lang"] });
}

function paintPendingCrashPrompt(report: PendingCrashSnapshotReport): void {
  if (typeof document === "undefined") return;
  visibleReport = report;
  // 任务 910：崩溃=danger 档，挂进统一右下角堆叠（见 rtNotice.ts）。
  const host = rtNoticeHost("pending-crash-entry", "danger");
  renderPendingCrashBanner(host, report);
}

export function installPendingCrashAnalysisEntry(): void {
  if (typeof window === "undefined" || typeof document === "undefined") return;
  installLocaleFlipWatch();
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
