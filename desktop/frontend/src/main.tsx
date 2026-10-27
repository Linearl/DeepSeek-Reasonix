import "./lib/compat";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import App from "./App";
import { ErrorBoundary } from "./components/ErrorBoundary";
import { installPerformancePressureMonitor } from "./lib/crash";
import { installGlobalCrashHandlers } from "./lib/globalCrashHandlers";
import { installWailsNonFileDragErrorSuppression } from "./lib/bridge";
import { installBreadcrumbConsoleHook } from "./lib/breadcrumbs";
import { installMessageSelectionCopy } from "./lib/messageSelectionCopy";
import { installPerfDebugHook } from "./lib/perfDebug";
import { installHangAnalysisWatch } from "./lib/hangPrompt";
import { installPendingCrashAnalysisEntry } from "./lib/pendingCrashEntry";
import { LocaleProvider, preloadDetectedLocale } from "./lib/i18n";
import { ToastProvider } from "./lib/toast";
import { initFontFamily } from "./lib/fontFamily";
import { initTextSize } from "./lib/textSize";
import { initTypographyPreferences } from "./lib/typographyPreferences";
import { initTheme } from "./lib/theme";
import { initConversationWidth } from "./lib/conversationWidth";
import { startupModeAssetPrefetch } from "./lib/desktopLayoutStyle";
import appShellStylesheetURL from "./styles.css?url";
import { reportFrontendLog } from "./lib/frontendLog";

// tl1181 探针（纯 performance.mark，零行为）：dom_ready→frontend_ready 段的
// 分段计时——①模块图求值（compat-top 起，上面首个 import 已打点）②挂载前
// 并行集（CSS/locale/mode 预取）③React 首帧 ④心跳 chunk。读数经既有
// boot-timeline 通道落 desktop.log（不带 kind= 前缀，Go 收集器按 kind 解析、
// 无 kind 静默跳过，不污染 boot:summary 既有字段）。
performance.mark("tl1181:main-body-top");

// Install first so startup/runtime failures paint a useful error instead of a
// featureless webview background, with the recent console trail attached.
installWailsNonFileDragErrorSuppression();
installGlobalCrashHandlers();
installBreadcrumbConsoleHook();
installPerformancePressureMonitor();
installPerfDebugHook();
// Task 663: the hang watch (doctor-responsiveness trigger) and the startup
// entry for the previous run's real crash report join the same boot block —
// both are optional-binding no-ops in the browser mock.
installHangAnalysisWatch();
installPendingCrashAnalysisEntry();

// Apply the saved appearance (auto/light/dark) before the first paint.
function initTypographyPlatform() {
  if (typeof document === "undefined" || typeof navigator === "undefined") return;
  const params = new URLSearchParams(window.location.search);
  const override = params.get("platform");
  const marker = `${navigator.platform} ${navigator.userAgent}`;
  const platform =
    override === "darwin" || override === "windows" || override === "linux"
      ? override
      : /Win/i.test(marker)
        ? "windows"
        : /Mac/i.test(marker)
          ? "darwin"
          : "linux";
  document.documentElement.setAttribute("data-platform", platform);
}

initTypographyPlatform();
initTheme();
initConversationWidth();
initTextSize();
initFontFamily();
initTypographyPreferences();

// Pre-warm font fallback stacks so the first frame doesn't flicker between the
// browser default font and the app's configured typeface. Inserting a hidden span
// with CJK + emoji + math glyphs forces the OS font subsystem to resolve and
// cache the fallback chains before React mounts.
function prewarmFontFallbacks() {
  const span = document.createElement("span");
  span.style.cssText = "position:absolute;visibility:hidden;font-size:1px;pointer-events:none";
  span.textContent = "中文日本語한국어 математика 😀🎉✓⚠∑∏∫";
  document.body.appendChild(span);
  // Force layout so the browser resolves font fallback chains.
  void span.offsetHeight;
  requestAnimationFrame(() => {
    requestAnimationFrame(() => {
      span.remove();
    });
  });
}
prewarmFontFallbacks();

installMessageSelectionCopy(document);

// Inside the Wails shell, suppress the webview's default right-click menu — its
// Reload / Back / Inspect entries are easy to hit by accident and can reset or
// navigate away from the app. Text inputs keep their native Cut/Copy/Paste menu;
// the terminal area is exempt so its own context menu can offer copy/paste.
// Left alone in a plain browser (pnpm dev) so devtools stay reachable.
if (typeof window !== "undefined" && window.runtime) {
  window.addEventListener("contextmenu", (e) => {
    const target = e.target as HTMLElement | null;
    if (!target?.closest("input, textarea") && !target?.closest(".terminal-view")) e.preventDefault();
  });
}

const root = document.getElementById("root");
if (!root) throw new Error("missing #root");
const rootElement = root;

// tl1181：读回探针 marks，一行经既有 boot-timeline 通道上报（fire-and-forget，
// 纯诊断；网关不可达时 console 与环形缓冲仍留证据）。全部取值来自
// performance.mark 时间线，不引入任何新计时器、不改任何加载顺序。
function reportMountSegments(): void {
  const at = (name: string): number => {
    const entry = performance.getEntriesByName(`tl1181:${name}`)[0];
    return entry ? Math.round(entry.startTime * 10) / 10 : -1;
  };
  const compatTop = at("compat-top");
  const firstFrame = at("first-frame");
  const seg = (name: string, from: number, to: number): string =>
    `${name}=${(to - from).toFixed(1)}`;
  const detail = [
    "seg=mount",
    seg("module_eval_ms", compatTop, at("main-body-top")),
    seg("body_sync_ms", at("main-body-top"), at("parallel-start")),
    seg("parallel_ms", at("parallel-start"), at("parallel-done")),
    seg("css_ms", at("parallel-start"), at("css-loaded")),
    seg("locale_ms", at("parallel-start"), at("locale-done")),
    seg("render_first_frame_ms", at("parallel-done"), firstFrame),
    seg("hb_chunk_ms", at("render-scheduled"), at("heartbeat-installed")),
    seg("mount_total_ms", compatTop, firstFrame),
    `atMs=${Math.round(firstFrame)}`,
    `epochMs=${Date.now()}`,
  ].join(" ");
  reportFrontendLog("boot-timeline", "tl1181 mount segments", detail, "info");
}

async function mountApp() {
  // The HTML boot shell paints immediately with critical inline styles. Load
  // the full stylesheet and detected locale in parallel, then replace that
  // shell in one React commit so users never see an unstyled application.
  // tl1191 joins the same parallel set with the active layout mode's own
  // stylesheet (and classic's lazy chrome chunk), so the first React commit
  // renders with mode styles already in — no unstyled-mode first frame.
  const preloadLocaleForMount = async () => {
    await preloadDetectedLocale();
    performance.mark("tl1181:locale-done");
  };
  performance.mark("tl1181:parallel-start");
  const stylesResult = await Promise.allSettled([
    new Promise<void>((resolve, reject) => {
      const link = document.createElement("link");
      link.rel = "stylesheet";
      link.href = appShellStylesheetURL;
      link.onload = () => {
        performance.mark("tl1181:css-loaded");
        resolve();
      };
      link.onerror = () => reject(new Error(`failed to load desktop stylesheet: ${appShellStylesheetURL}`));
      document.head.appendChild(link);
    }),
    preloadLocaleForMount(),
    ...startupModeAssetPrefetch(),
  ]);
  const [styleResult, localeResult] = stylesResult;
  if (styleResult.status === "rejected") {
    console.error("failed to load desktop stylesheet", styleResult.reason);
    return;
  }
  for (const modeResult of stylesResult.slice(2)) {
    if (modeResult.status === "rejected") console.error("failed to preload layout-mode assets", modeResult.reason);
  }
  if (localeResult.status === "rejected") console.error("failed to preload desktop locale", localeResult.reason);
  performance.mark("tl1181:parallel-done");
  createRoot(rootElement).render(
    <StrictMode>
      <ErrorBoundary>
        <LocaleProvider>
          <ToastProvider>
            <App />
          </ToastProvider>
        </LocaleProvider>
      </ErrorBoundary>
    </StrictMode>,
  );
  performance.mark("tl1181:render-scheduled");

  void import("./lib/desktopWebViewHeartbeat").then(({ installDesktopWebViewHeartbeat }) => {
    installDesktopWebViewHeartbeat();
    performance.mark("tl1181:heartbeat-installed");
  });

  // tl1181：首帧后读回全部分段（rAF 与心跳自身的首次上报同帧排队，读数即
  // 「心跳首跳可用」时刻）。
  requestAnimationFrame(() => {
    performance.mark("tl1181:first-frame");
    reportMountSegments();
  });
}

void mountApp();
