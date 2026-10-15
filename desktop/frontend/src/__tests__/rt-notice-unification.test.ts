// Run: tsx src/__tests__/rt-notice-unification.test.ts
// 任务 910：右下角弹窗统一的验收钉——任务书九条硬门里可证伪的部分：
//   1. 四档严重度语义映射固定（info→--info / warn→--warn / danger→--danger /
//      accent→--accent），CSS 规则 + paint 后的 data-level 双侧钉死防漂移；
//   2. 三兄弟重复 CSS 清零：族类名（hang-analysis__* / pending-crash__* /
//      performance-report__*）不再有任何 CSS 规则、三个 #id 骨架块消失——
//      「改一处三处生效」的一份组件族自证；
//   3. 硬编码橙清零：styles.css 里旧橙色值只允许出现在 --accent token 定义行
//      （组件取色一律 var()）；
//   4. 排除项回归对照：流内 notice-line 与全屏 crash overlay 未被统一化；
//   5. Toast 顶部位（top:16px 居中）与时长逻辑不动，三档改吃同一套 token；
//   6. 堆叠容器：先出现者在下、后出现者向上叠（demo 第三节交互等价的结构
//      + CSS 双证），关闭 × 可用且收口正确；
//   7. 三语：关闭 × 的 common.close 与三面文案键在 zh/zh-TW/en 齐；
//   8. 浅色主题自动适配前提：组件规则内零裸色值（token 定义处除外）。

import { JSDOM } from "jsdom";
import { readFileSync } from "node:fs";

const dom = new JSDOM("<!doctype html><html><body></body></html>", { url: "http://localhost/" });
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
globalThis.CustomEvent = dom.window.CustomEvent;
globalThis.Event = dom.window.Event;
globalThis.MutationObserver = dom.window.MutationObserver;
globalThis.sessionStorage = dom.window.sessionStorage;
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });

// 测试进程跑在 en locale 下（run-tests 的 LANG 约定），关闭 × 的 aria-label 断言用英文值。
const newestPayload = JSON.stringify({
  schemaVersion: 2,
  kind: "crash",
  source: "go",
  label: "scheduler.tick",
  message: "[go panic] scheduler.tick\n\npanic: runtime error",
  errorType: "runtime error",
  errorMessage: "Go panic captured at scheduler.tick.",
});

(window as unknown as { go?: unknown }).go = {
  main: {
    App: {
      CrashAnalysisAvailability: async () => ({ sourceReady: true, ghAuthenticated: true, workspaceReady: true, ready: true }),
      HangAnalysisAvailability: async () => ({ sessionPath: "…", verdict: "silent", detail: "ledger silent 8m", hung: true, ready: true }),
      StartHangAnalysis: async () => "hang analysis started",
      StartCrashAnalysis: async () => "crash analysis started",
      PendingCrashSnapshot: async () => ({ count: 1, reports: [newestPayload] }),
    },
  },
};

const { paintPerformancePromptForTest, buildCrashPayload, reportCrash } = await import("../lib/crash");
const { paintHangPromptForTest } = await import("../lib/hangPrompt");
const { installPendingCrashAnalysisEntry, resetPendingCrashEntryForTest } = await import("../lib/pendingCrashEntry");
const { rtNoticeHost, rtNoticeIcon } = await import("../lib/rtNotice");

let passed = 0;
let failed = 0;

function ok(cond: unknown, label: string) {
  if (cond) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

const tick = () => new Promise((resolve) => setTimeout(resolve, 0));
const styles = readFileSync(new URL("../styles.css", import.meta.url), "utf8");
const localeSource = (name: string) => readFileSync(new URL(`../locales/${name}`, import.meta.url), "utf8");

// 极简规则抽取：够本测试断言用（选择器 + 块体；先剥注释，@media 内层规则同样被抽出）。
const stylesNoComment = styles.replace(/\/\*[\s\S]*?\*\//g, " ");
function ruleBodies(selector: string): string[] {
  const bodies: string[] = [];
  const pattern = new RegExp(`${selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}\\s*\\{([^{}]*)\\}`, "g");
  for (const match of stylesNoComment.matchAll(pattern)) bodies.push(match[1]);
  return bodies;
}

console.log("\n四档严重度语义映射（CSS 侧，防换色漂移）");
const LEVEL_TOKEN: Array<[string, string]> = [
  ["info", "--info"],
  ["warn", "--warn"],
  ["danger", "--danger"],
  ["accent", "--accent"],
];
for (const [level, token] of LEVEL_TOKEN) {
  const bodies = ruleBodies(`.rt-notice[data-level="${level}"]`);
  ok(bodies.length === 1, `${level} 档有且仅有一条 data-level 规则`);
  const body = bodies[0] ?? "";
  ok(body.includes(`--n-color: var(${token})`), `${level} 档色源是 var(${token})`);
  ok(/color-mix\(in srgb, var\(--border\) 80%/.test(body), `${level} 档边框走 color-mix(theme token)`);
  ok(/color-mix\(in srgb, var\(--panel, var\(--bg-elev\)\) 96%/.test(body), `${level} 档底色走 color-mix(theme token)`);
}
ok(styles.includes("--info: #5b8def;"), "第四档 --info 是 :root token 定义（demo 提案新增档）");

console.log("\n组件族骨架与堆叠（一份 CSS，改一处三处生效）");
for (const cls of [
  "rt-notice-stack",
  "rt-notice",
  "rt-notice__bar",
  "rt-notice__inner",
  "rt-notice__head",
  "rt-notice__icon",
  "rt-notice__title",
  "rt-notice__close",
  "rt-notice__intro",
  "rt-notice__body",
  "rt-notice__actions",
  "rt-notice__note",
  "rt-notice__analysis",
  "rt-btn",
  "rt-btn--primary",
]) {
  ok(ruleBodies(`.${cls}`).length >= 1, `组件类 .${cls} 有 CSS 定义`);
}
const stackBody = ruleBodies(".rt-notice-stack")[0] ?? "";
ok(stackBody.includes("flex-direction: column-reverse"), "堆叠容器 column-reverse：先出现者在下，新弹窗向上叠");
ok(stackBody.includes("gap: 10px") && stackBody.includes("right: 18px") && stackBody.includes("bottom: 18px"), "堆叠沿用现状尺寸（右下 18px、间距 10px）");
ok(stackBody.includes("var(--z-performance-report-prompt)"), "堆叠沿用原 z 层 token");
ok((styles.match(/\.rt-notice-stack\s*\{[^}]*\}/g) ?? []).length >= 2, "窄屏媒体查询把右下 12px 收敛到容器一条规则");
const primaryBody = ruleBodies(".rt-btn--primary")[0] ?? "";
ok(primaryBody.includes("var(--n-color"), "主按钮描边吃严重度 token（demo：主按钮带严重度描边）");

console.log("\n三兄弟重复块清零（族类名降级为测试钩子）");
ok(!/^#performance-report-prompt\s*\{/m.test(styles), "#performance-report-prompt 固定骨架块已删");
ok(!/^#hang-analysis-prompt\s*\{/m.test(styles), "#hang-analysis-prompt 固定骨架块已删");
ok(!/^#pending-crash-entry\s*\{/m.test(styles), "#pending-crash-entry 固定骨架块已删");
const legacyFamily = [
  "hang-analysis__title",
  "hang-analysis__intro",
  "hang-analysis__body",
  "hang-analysis__actions",
  "hang-analysis__copy",
  "hang-analysis__dismiss",
  "hang-analysis__note",
  "hang-analysis__analyze",
  "pending-crash__title",
  "pending-crash__intro",
  "pending-crash__body",
  "pending-crash__actions",
  "pending-crash__dismiss",
  "pending-crash__analyze",
  "pending-crash__note",
  "performance-report__title",
  "performance-report__body",
  "performance-report__actions",
  "performance-report__copy",
  "performance-report__dismiss",
  "performance-report__note",
  "performance-report__analyze",
  "performance-report__analysis",
];
for (const cls of legacyFamily) {
  ok(ruleBodies(`.${cls}`).length === 0, `族钩子 .${cls} 已无 CSS 规则（皮肤只在 rt-notice 族）`);
}

console.log("\n硬编码橙清零（styles.css grep 自证）");
const hardOrange = styles
  .split("\n")
  .map((line, index) => ({ line, n: index + 1 }))
  .filter(({ line }) => /d97757/i.test(line));
ok(hardOrange.length > 0, "token 定义处仍持有色值（--accent 是唯一色源）");
for (const { line, n } of hardOrange) {
  ok(/^\s*--accent:\s*#d97757;\s*$/.test(line), `styles.css:${n} 只允许是 --accent token 定义行`);
}
// 组件区段（堆叠容器 → 折叠头距规则）除 --n-fg 作用域 token 外零裸色值——
// 浅色主题自动适配（demo 第四节）的前提；先剥注释再扫，避免注释里的示例值误伤。
const noticeRegion = styles
  .slice(styles.indexOf(".rt-notice-stack {"), styles.indexOf("#performance-report-prompt .rt-notice__head"))
  .replace(/\/\*[\s\S]*?\*\//g, " ")
  .replace(/--n-fg: #fff;/g, "");
ok(!/(^|[^-a-zA-Z0-9])#[0-9a-fA-F]{3,8}\b/m.test(noticeRegion), "组件区段除 --n-fg 作用域 token 外零裸色值（浅色主题自动适配前提）");

console.log("\n排除项回归对照（流内 notice-line / 全屏 crash overlay）");
const noticeLineWarn = ruleBodies(".notice-line--warn")[0] ?? "";
ok(noticeLineWarn.includes("color: var(--accent)"), "流内 notice-line--warn 仍用 --accent（第三种 accent 口径未动）");
ok(noticeLineWarn.includes("color-mix(in srgb, var(--accent) 8%, transparent)"), "流内 notice-line 底色未动");
const noticeLineRules = [...styles.matchAll(/([^{}]*\.notice-line[^{}]*)\{([^{}]*)\}/g)];
ok(noticeLineRules.length > 0 && noticeLineRules.every(([, , body]) => !body.includes("rt-notice")), "notice-line 规则族未混入 rt-notice 皮肤");
const overlayBody = ruleBodies("#crash-overlay")[0] ?? "";
ok(overlayBody.includes("position: fixed") && overlayBody.includes("inset: 0") && overlayBody.includes("var(--z-crash-overlay)"), "全屏 crash overlay 仍是独立全窗族（未并入 rt-notice）");

console.log("\nToast：顶部位与时长逻辑不动，三档同套 token");
const toastContainer = ruleBodies(".toast-container")[0] ?? "";
ok(toastContainer.includes("top: 16px") && toastContainer.includes("left: 50%") && toastContainer.includes("translateX(-50%)"), "Toast 仍顶部居中（darwin 抬升规则之外的位置零变化）");
const TOAST_LEVEL: Array<[string, string]> = [
  ["info", "--info"],
  ["warn", "--warn"],
  ["error", "--danger"],
];
for (const [mod, token] of TOAST_LEVEL) {
  const body = ruleBodies(`.toast--${mod}`)[0] ?? "";
  ok(body.includes(`var(${token})`), `toast--${mod} 吃 var(${token})（与 rt-notice 同一套严重度 token）`);
  ok(body.includes("70%") && body.includes("90%"), `toast--${mod} 描边/底色比例与 demo 轻通知节一致（70/30 + 90/10）`);
}
const toastSource = readFileSync(new URL("../lib/toast.tsx", import.meta.url), "utf8");
ok(toastSource.includes("8000") && toastSource.includes("2500"), "Toast 时长逻辑未动（带动作 8s / 轻通知 2.5s）");
ok(toastSource.includes("toast--${t.level}"), "Toast 分级类名接线未动（info/warn/error 三档）");

console.log("\nDOM 行为：四档挂载、堆叠顺序、关闭 ×");
const snapshot = { reason: "event-loop-lag", uptimeMs: 120_000, visibility: "visible", focused: true, online: true, hardwareConcurrency: 8 };
const payload = buildCrashPayload("performance", new TypeError("jank"), "");
const perfHost = paintPerformancePromptForTest(payload, snapshot as never);
ok(perfHost.classList.contains("rt-notice") && perfHost.dataset.level === "accent", "性能报告挂 accent 档（非故障强调，语义固定）");
ok(perfHost.querySelector(".rt-notice__bar") !== null && perfHost.querySelector(".rt-notice__inner") !== null, "性能报告骨架 = 色条 + 内容列");
paintHangPromptForTest({ sessionPath: "…", verdict: "silent", detail: "ledger silent 8m", hung: true, ready: true } as never);
const hangHost = document.getElementById("hang-analysis-prompt");
ok(hangHost?.classList.contains("rt-notice") === true && hangHost?.dataset.level === "warn", "卡顿分析挂 warn 档（警戒黄，语义固定）");
installPendingCrashAnalysisEntry();
await tick();
await tick();
const pendingHost = document.getElementById("pending-crash-entry");
ok(pendingHost?.classList.contains("rt-notice") === true && pendingHost?.dataset.level === "danger", "崩溃入口挂 danger 档（红色语义归位，语义固定）");

const stack = document.getElementById("rt-notice-stack");
ok(stack !== null && stack.className === "rt-notice-stack", "三面挂进同一个堆叠容器");
ok(
  stack !== null && [perfHost, hangHost, pendingHost].every((host) => host?.parentElement === stack),
  "三个宿主都是堆叠容器的直接子元素（flex column-reverse 即不重叠保证）",
);
ok(stack !== null && stack.children[0] === perfHost && stack.children[1] === hangHost && stack.children[2] === pendingHost, "堆叠顺序 = 出现顺序（先现者在下，新弹窗向上叠）");

for (const [host, icon] of [
  [perfHost, "↯"],
  [hangHost, "⚠"],
  [pendingHost, "!"],
] as const) {
  ok(host?.querySelector(".rt-notice__icon")?.textContent === icon, `图标徽章字形 ${icon}（demo 提案节）`);
}
ok(rtNoticeIcon("info") === "i", "info 档字形 i（demo 提案节，无常驻调用方时先钉字形）");
for (const [host, label] of [
  [perfHost, "performance-report__title"],
  [hangHost, "hang-analysis__title"],
  [pendingHost, "pending-crash__title"],
] as const) {
  ok(host?.querySelector(`.${label}`) !== null, `族测试钩子 .${label} 保留`);
}

// info 第四档虽暂无常驻弹窗调用方（更新提示等），挂载器对它同路可建可卸。
const infoHost = rtNoticeHost("rt-910-info-probe", "info");
ok(infoHost.dataset.level === "info" && infoHost.parentElement === stack, "info 第四档可经同一挂载器上屏（结构实证）");
infoHost.remove();

const perfClose = perfHost?.querySelector(".rt-notice__close") as HTMLButtonElement;
ok(perfClose?.getAttribute("aria-label") === "Close", "关闭 × 有 aria-label（common.close 三语键）");
ok(perfClose?.textContent === "✕", "关闭 × 字形 ✕");
perfClose.click();
ok(document.getElementById("performance-report-prompt") === null, "关闭 × 即关（demo 第三节交互）");
ok(
  document.getElementById("hang-analysis-prompt") !== null && document.getElementById("pending-crash-entry") !== null,
  "关一条不影响同栈其他弹窗",
);
resetPendingCrashEntryForTest();

console.log("\ncrash overlay 排除项（DOM 侧）");
reportCrash("react", new TypeError("renderer fault"));
const overlay = document.getElementById("crash-overlay");
ok(overlay !== null && !overlay.classList.contains("rt-notice") && overlay.dataset.level === undefined, "全屏 crash overlay 未挂 rt-notice（排除项钉死）");
overlay?.remove();

console.log("\n三语 locale（关闭 × aria 与三面文案键）");
for (const file of ["zh.ts", "zh-TW.ts", "en.ts"]) {
  const source = localeSource(file);
  for (const key of ["common.close", "performanceReport.dismiss", "hangPrompt.title", "hangPrompt.intro", "pendingCrash.title", "pendingCrash.intro", "pendingCrash.previewNote"]) {
    ok(source.includes(`"${key}"`), `${file} 含 ${key}`);
  }
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
