// Run: tsx --tsconfig tsconfig.tsx-runner.json --import ./scripts/css-stub-register.mjs src/__tests__/tl1464-notify-unification.test.tsx
// tl1464 通知整合验收钉：四族收敛三类（A 操作反馈 / B 状态横幅 / C 诊断告警）。
//   【A 段】toast info 档迁右下角统一堆叠（data-kind="action"）；warn/error 档
//     原样留顶部居中（位置/样式/时长零变化，用户修正1）；A/C 同住
//     #rt-notice-stack 以 data-kind 区分，column-reverse 按到达次序交错。
//   【B 段】banner 7 组件零改动（git diff 自证，见交付报告）；类型开关的
//     data-notify-banners 隐藏规则只打在 6 个族根选择器上（RemoteReclaimBanner
//     无独立类名钩子，零改动验收下不覆盖，报告已列）。
//   【C 段】rt-notice 宿主 data-kind="diagnostic"；warn 档（卡顿提示）30s 自动
//     消失并归并手动关闭同一收口（记 10min 冷却防 90s 轮询重弹）；error/danger
//     档手动关闭不设时限。
//   【开关段】三类开关默认全开（零变化）；关闭只抑制展示，留档照写（留档≠展示）；
//     错误提示不设开关（用户修正1的延伸：错误类不可被静默）。
import { JSDOM } from "jsdom";
import { readFileSync } from "node:fs";

const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", {
  url: "http://localhost/",
  pretendToBeVisual: true,
});
Object.assign(globalThis, {
  window: dom.window,
  document: dom.window.document,
  HTMLElement: dom.window.HTMLElement,
  Element: dom.window.Element,
  Node: dom.window.Node,
  localStorage: dom.window.localStorage,
  sessionStorage: dom.window.sessionStorage,
  CustomEvent: dom.window.CustomEvent,
  Event: dom.window.Event,
  IS_REACT_ACT_ENVIRONMENT: true,
});
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });

// 测试进程跑在 en locale 下（run-tests 的 LANG 约定）。
(window as unknown as { go?: unknown }).go = {
  main: {
    App: {
      HangAnalysisAvailability: async () => ({ sessionPath: "…", verdict: "silent", detail: "ledger silent 8m", hung: true, ready: true }),
      StartHangAnalysis: async () => "hang analysis started",
    },
  },
};

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
const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));
const sourceOf = (name: string) => readFileSync(new URL(name, import.meta.url), "utf8");

// ────────────────────────────────────────────────────────────────────────
// 【C 段】rt-notice：data-kind="diagnostic" + warn 档 30s 自动消失
// ────────────────────────────────────────────────────────────────────────
console.log("\n【C】rt-notice data-kind 与 warn 档自动消失");
{
  const { rtNoticeHost, ensureRtNoticeStack, RT_NOTICE_WARN_AUTO_DISMISS_MS, armRtNoticeAutoDismiss, clearRtNoticeAutoDismiss } =
    await import("../lib/rtNotice");

  ok(RT_NOTICE_WARN_AUTO_DISMISS_MS === 30_000, "warn 档自动消失窗口 = 30s");
  ok(rtNoticeHost("tl1464-kind-fresh", "warn").dataset.kind === "diagnostic", "rt-notice 宿主挂 data-kind=diagnostic（新建路径）");
  // 归位路径：宿主建于堆叠容器出现之前 → reparent 后同样带 data-kind。
  const orphan = dom.window.document.createElement("div");
  orphan.id = "tl1464-kind-orphan";
  dom.window.document.body.appendChild(orphan);
  ok(rtNoticeHost("tl1464-kind-orphan", "danger").dataset.kind === "diagnostic", "rt-notice 宿主挂 data-kind=diagnostic（归位路径）");
  ok(document.getElementById("tl1464-kind-orphan")?.parentElement === ensureRtNoticeStack(), "归位宿主收进统一堆叠容器");
  ok(document.getElementById("tl1464-kind-fresh")?.parentElement === ensureRtNoticeStack(), "新建宿主收进统一堆叠容器");

  // 自动消失机制（小窗口行为实证；真实 30s 由常量钉 + hangPrompt 源码接线钉）。
  const probe = rtNoticeHost("tl1464-arm-probe", "warn");
  let expired = 0;
  armRtNoticeAutoDismiss(probe, 20, () => { expired += 1; });
  await sleep(60);
  ok(!probe.isConnected && expired === 1, "arm 到期移除宿主且到期回调恰执行一次");
  const probe2 = rtNoticeHost("tl1464-arm-probe2", "warn");
  armRtNoticeAutoDismiss(probe2, 20, () => { expired += 1; });
  clearRtNoticeAutoDismiss(probe2);
  await sleep(60);
  ok(probe2.isConnected && expired === 1, "clear 撤销定时器：宿主存活、回调不触发");
  // 手动已关后到期：回调不执行（isConnected 闸）。
  const probe3 = rtNoticeHost("tl1464-arm-probe3", "warn");
  armRtNoticeAutoDismiss(probe3, 20, () => { expired += 1; });
  probe3.remove();
  await sleep(60);
  ok(expired === 1, "宿主先被手动移除时到期回调不触发（不误记冷却）");
  // 重复 arm = 重置（第二次 arm 后第一次定时器不再触发）。
  const probe4 = rtNoticeHost("tl1464-arm-probe4", "warn");
  armRtNoticeAutoDismiss(probe4, 20);
  armRtNoticeAutoDismiss(probe4, 10_000);
  await sleep(60);
  ok(probe4.isConnected, "重复 arm 重置窗口（第一次短定时器失效）");
  clearRtNoticeAutoDismiss(probe4);
  probe4.remove();
}

{
  // 真实 paint 路径：卡顿提示挂 warn + diagnostic，关闭 × 走同一收口；
  // 30s 接线由源码钉（不能等 30s），机制行为上面已证。
  const hangSource = sourceOf("../lib/hangPrompt.ts");
  ok(
    hangSource.includes("armRtNoticeAutoDismiss(host, RT_NOTICE_WARN_AUTO_DISMISS_MS, closePrompt)"),
    "hangPrompt paint 后 arm 30s 自动消失，到期归并 closePrompt 同一收口（记冷却防重弹）",
  );
  ok(
    /const closePrompt = \(\) => \{\s*clearRtNoticeAutoDismiss\(host\);/.test(hangSource),
    "手动关闭先撤自动消失定时器（不双触发）",
  );
  const crashSource = sourceOf("../lib/crash.ts");
  const pendingSource = sourceOf("../lib/pendingCrashEntry.ts");
  ok(!crashSource.includes("armRtNoticeAutoDismiss"), "性能面（accent）不自动消失（保持手动）");
  ok(!pendingSource.includes("armRtNoticeAutoDismiss"), "崩溃入口（danger）不自动消失（保持手动）");

  const { paintHangPromptForTest, resetHangWatchForTest } = await import("../lib/hangPrompt");
  paintHangPromptForTest({ sessionPath: "…", verdict: "silent", detail: "ledger silent 8m", hung: true, ready: true } as never);
  const hang = document.getElementById("hang-analysis-prompt");
  ok(hang?.dataset.level === "warn" && hang?.dataset.kind === "diagnostic", "卡顿提示 = warn + diagnostic（真实 paint 路径）");
  const closeX = hang?.querySelector(".rt-notice__close") as HTMLButtonElement | null;
  closeX?.click();
  ok(document.getElementById("hang-analysis-prompt") === null, "关闭 × 手动收走卡顿提示");
  resetHangWatchForTest();
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
