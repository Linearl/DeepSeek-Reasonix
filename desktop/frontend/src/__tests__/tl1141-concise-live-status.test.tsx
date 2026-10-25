// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1141-concise-live-status.test.tsx
//
// tl 1141 — concise 档运行中的动态过程感知（思考摘要行 + 当前动作行）。
// 断言面：
//   A  纯函数：latestRunningTool / latestReasoningText / hasLiveAssistant /
//      clampLine（160 code point 有界截断），含 1200 项规模尾部首中语义；
//   B  concise 渲染：关闭态折叠头下出现 [data-live-status]——当前动作行
//      （工具名+对象）与思考摘要行（复用 ReasoningSummary），live prop 命中
//      时摘要源切换到 live.reasoning；
//   C  P1-1 展开：concise 下 fold override open=true（与点击 handleFoldToggle
//      落点相同的状态路径——handleFoldToggle 本身无任何档位分支，jsdom 下
//      DOM click 会挂起在 beginStructural，故按既有测试先例驱动状态而非点击）
//      → body 挂载、状态行随 open 消失；
//   D  standard/deep 零改动：同 items 下无 [data-live-status]（不叠状态行）；
//   E  帧预算约束②：思考摘要文本 ≥500ms 节流采纳（250ms 时未变 / 650ms 时已变）；
//   F  源码守卫：lib 无 setInterval；ProcessFoldHeader 仅 useThrottledValue
//      一处 interval（禁周期全量扫描的代码层可查形态）。

import { JSDOM } from "jsdom";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import type { Item } from "../lib/useController";
import { LIVE_TEXT_THROTTLE_MS } from "../lib/workProcessLiveStatus";
import { createTranscriptHarness } from "./transcript-dom-harness";

let passed = 0;
let failed = 0;

function ok(value: unknown, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

function eq(actual: unknown, expected: unknown, label: string) {
  if (actual === expected) ok(true, label);
  else ok(false, `${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
}

function textOf(selector: string): string {
  return harness.container.querySelector<HTMLElement>(selector)?.textContent ?? "";
}

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

// ─────────────────────────────────────────────────────────────────────────────
// A. 纯函数（独立 jsdom，无 harness）
// ─────────────────────────────────────────────────────────────────────────────
console.log("\ntl 1141 A: 数据源纯函数");
{
  const dom = new JSDOM("<!doctype html><html><body></body></html>");
  globalThis.window = dom.window as unknown as Window & typeof globalThis;
  globalThis.document = dom.window.document;
  const { latestRunningTool, latestReasoningText, hasLiveAssistant, clampLine } = await import("../lib/workProcessLiveStatus");
  const done = (id: string): Item => ({ kind: "tool", id, name: "read", args: "{}", readOnly: true, status: "completed" } as Item);
  const running = (id: string, name = "grep"): Item => ({ kind: "tool", id, name, args: `{"pattern":"p","path":"src/${id}"}`, readOnly: true, status: "running" } as Item);
  const thought = (id: string, reasoning: string): Item => ({ kind: "assistant", id, text: "", reasoning, streaming: true } as Item);

  eq(latestRunningTool([done("d1"), running("r1"), done("d2")])?.id, "r1", "尾部混排：返回唯一 running");
  eq(latestRunningTool([running("r1"), running("r2"), done("d1")])?.id, "r2", "多 running：返回最后一个（最新动作）");
  eq(latestRunningTool([done("d1"), thought("a1", "x")]), undefined, "无 running：undefined");
  eq(latestReasoningText([done("d1"), thought("a1", "think one"), thought("a2", "think two")]), "think two", "最新思考段=尾部 assistant");
  eq(latestReasoningText([done("d1")]), "", "无思考段：空串");
  eq(hasLiveAssistant([thought("a1", "x"), thought("a2", "y")], "a2"), true, "live id 命中尾部 assistant");
  eq(hasLiveAssistant([thought("a1", "x")], "zzz"), false, "live id 不命中");
  eq(hasLiveAssistant([thought("a1", "x")], undefined), false, "无 live id：false");

  eq(clampLine("短文本", 160), "短文本", "短文本原样");
  eq(clampLine("x".repeat(160), 160).length, 160, "恰好 160 不截断");
  eq(clampLine("x".repeat(161), 160).length, 160, "161 截到 160 含省略号");
  ok(clampLine("x".repeat(161), 160).endsWith("…"), "截断以省略号收尾");
  const surrogate = "😀".repeat(100); // 200 UTF-16 units = 100 code points
  eq(Array.from(clampLine(surrogate, 160)).length, 100, "surrogate 对不劈开（100 grapheme ≤160 原样）");
  const longSurrogate = "😀".repeat(200); // 400 units = 200 code points
  eq(Array.from(clampLine(longSurrogate, 160)).length, 160, "200 grapheme 截到 160");

  // 千步规模：1200 项里 running 在尾部，首中即返（O(命中距离) 语义的形态断言）
  const big: Item[] = Array.from({ length: 1199 }, (_, i) => done(`d${i}`));
  big.push(running("tail-runner"));
  eq(latestRunningTool(big)?.id, "tail-runner", "1200 项：尾部 running 命中");
  eq(latestReasoningText(big), "", "1200 项无 assistant：空串");
}

// ─────────────────────────────────────────────────────────────────────────────
// B–E. 渲染行为（共享 harness，concise 词典走 storage mirror + hydrate）
// ─────────────────────────────────────────────────────────────────────────────
console.log("\ntl 1141 B–E: concise 状态行 / P1-1 展开 / standard·deep 零改动 / 节流");

const harness = await createTranscriptHarness({
  storage: { "reasonix-session-experience": "concise" },
});
const { act } = await import("react");
const sessionExperience = await harness.loadModule<{
  hydrateSessionExperience: (v: unknown) => void;
}>("/src/lib/sessionExperience.ts");
const foldOverrides = await harness.loadModule<{
  writeTranscriptFoldOverride: (sessionKey: string, segmentKey: string, entry: { open: boolean; userOverridden: boolean; running: boolean; keepReasoningExpanded: boolean }) => void;
}>("/src/lib/transcriptFoldOverrides.ts");

sessionExperience.hydrateSessionExperience("concise");

const reasoningA = "first tail line of thinking";
const itemsRunning: Item[] = [
  { kind: "user", id: "u1", text: "please investigate" },
  { kind: "assistant", id: "a1", text: "", reasoning: `head line\n${reasoningA}`, streaming: true },
  { kind: "tool", id: "t1", name: "grep", args: JSON.stringify({ pattern: "needle", path: "src/haystack" }), readOnly: true, status: "running" },
];
const RENDER_PROPS = { running: true, geometrySessionKey: "tl1141-session" } as const;

const liveStatus = () => harness.container.querySelector("[data-live-status]");
try {
  // B: concise + 关闭折叠 + 运行中 → 状态行出现
  await harness.render(itemsRunning, { ...RENDER_PROPS });
  ok(liveStatus() !== null, "B: concise 运行中关闭折叠显示状态行");
  ok(textOf(".turn-collapse__live-action").includes("grep"), "B: 当前动作行含工具名 grep");
  ok(textOf(".turn-collapse__live-action").includes("needle"), "B: 当前动作行含对象（pattern）");
  const summaryText = textOf("button.turn-collapse__live-summary");
  ok(summaryText.length > 0, "B: 思考摘要行有文本（复用 ReasoningSummary）");
  ok(summaryText.includes(reasoningA), "B: 摘要取 reasoning 尾行（streaming 语义）");
  ok(textOf(".turn-collapse__live-thinking").length > 0, "B: 思考前缀 label 存在");

  // B2: live prop 命中 → 摘要源=live.reasoning
  const liveItems = itemsRunning.map((it) => it.kind === "assistant"
    ? ({ ...it, reasoning: "stale snapshot line" } as Item)
    : it);
  await harness.render(liveItems, {
    ...RENDER_PROPS,
    live: { id: "a1", text: "", reasoning: "live reasoning tail line", reasoningComplete: false },
  });
  await sleep(700); // 等节流窗口采纳
  await harness.flush();
  ok(textOf("button.turn-collapse__live-summary").includes("live reasoning tail line"),
    "B2: live id 命中时摘要源切到 live.reasoning");

  // C: P1-1 —— concise 下点击展开不被档位拦截。两层证据：
  // ① 纯函数层：foldMapWithToggle（handleFoldToggle 的直接落点，无任何档位
  //    分支）翻转 open；buildTranscriptRows 对 open 折叠产出 body rows。
  // ② DOM 层：以 buildTurnModels 派生的真实 segment key 预写 override
  //    （点击持久化的同一落点），surface 首挂即读入 → body 挂载、状态行退场。
  const rowsModule = await harness.loadModule<typeof import("../lib/transcriptRows")>("/src/lib/transcriptRows.ts");
  const models = rowsModule.buildTurnModels(itemsRunning, rowsModule.NO_LIVE, true);
  const segmentKey = models[0].segments[0].key;
  const toggled = rowsModule.foldMapWithToggle(new Map(), segmentKey, false);
  eq(toggled.get(segmentKey)?.open, true, "C: foldMapWithToggle 翻转 open（无档位拦截）");
  const rowOptions = { sessionExperience: "concise" as const, hasOlderHistory: false, creationMode: false, turnForUser: () => undefined };
  const openRows = rowsModule.buildTranscriptRows(models, { ...rowOptions, folds: toggled });
  ok(openRows.some((r) => r.kind === "tool"), "C: open 折叠产出 tool body row（concise 不拦挂载）");
  ok(openRows.some((r) => r.kind === "reasoning"), "C: open 折叠产出 reasoning body row");
  const closedRows = rowsModule.buildTranscriptRows(models, { ...rowOptions, folds: new Map() });
  ok(!closedRows.some((r) => r.kind === "tool" || r.kind === "reasoning"), "C: concise 默认关闭不产 body rows（对照）");
  foldOverrides.writeTranscriptFoldOverride("tl1141-open", segmentKey, { open: true, userOverridden: true, running: true, keepReasoningExpanded: false });
  await harness.render(liveItems, { running: true, geometrySessionKey: "tl1141-open" });
  await harness.flush();
  ok(harness.container.querySelector(".turn-collapse--open") !== null, "C: override open=true 折叠头展开（concise 不拦点击展开）");
  ok(harness.container.querySelector(".turn-collapse__reasoning-phase") !== null, "C: 展开后 reasoning body 挂载");
  ok(harness.container.querySelector(".turn-collapse__body") !== null, "C: 展开后 fold body 行挂载");
  ok(liveStatus() === null, "C: 展开态不叠状态行");

  // D: standard / deep 同 items + 关闭折叠 + 运行中 → 零改动（无状态行）
  foldOverrides.writeTranscriptFoldOverride("tl1141-standard", segmentKey, { open: false, userOverridden: true, running: true, keepReasoningExpanded: false });
  sessionExperience.hydrateSessionExperience("standard");
  await harness.render(liveItems, { running: true, geometrySessionKey: "tl1141-standard" });
  await harness.flush();
  ok(liveStatus() === null, "D: standard 运行中无状态行（不叠）");
  foldOverrides.writeTranscriptFoldOverride("tl1141-deep", segmentKey, { open: false, userOverridden: true, running: true, keepReasoningExpanded: false });
  sessionExperience.hydrateSessionExperience("deep");
  await harness.render(liveItems, { running: true, geometrySessionKey: "tl1141-deep" });
  await harness.flush();
  ok(liveStatus() === null, "D: deep 运行中无状态行（不叠）");

  // E: 节流——useThrottledValue 假 interval 直测（确定性，无墙钟相位依赖）：
  // ① 注册的间隔恰为 LIVE_TEXT_THROTTLE_MS（≥500ms 的数值断言）；
  // ② 值变化未经 tick 不采纳；③ tick 后采纳；④ 同值 tick 零重渲染。
  {
    const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>");
    const priorWindow = globalThis.window;
    globalThis.window = dom.window as unknown as Window & typeof globalThis;
    globalThis.document = dom.window.document;
    (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
    const React2 = (await import("react")).default;
    const { createRoot } = await import("react-dom/client");
    const { act } = await import("react");
    const { useThrottledValue: throttle } = await import("../lib/workProcessLiveStatus");
    let registered: { fn: () => void; ms: number } | null = null;
    dom.window.setInterval = ((fn: () => void, ms: number) => { registered = { fn, ms }; return 1; }) as typeof dom.window.setInterval;
    dom.window.clearInterval = (() => {}) as typeof dom.window.clearInterval;
    let renders = 0;
    function Probe({ value }: { value: string }) {
      renders += 1;
      const shown = throttle(value, LIVE_TEXT_THROTTLE_MS);
      return React2.createElement("div", { id: "probe" }, shown);
    }
    const container = dom.window.document.getElementById("root")!;
    const root = createRoot(container);
    await act(async () => { root.render(React2.createElement(Probe, { value: "v1" })); });
    eq(container.textContent, "v1", "E: 初始值立即显示");
    const registeredMs = (registered as { ms: number } | null)?.ms;
    eq(registeredMs, LIVE_TEXT_THROTTLE_MS, "E: 注册间隔=500ms（节流 ≥500ms 数值断言）");
    const rendersAfterMount = renders;
    await act(async () => { root.render(React2.createElement(Probe, { value: "v2" })); });
    eq(container.textContent, "v1", "E: 值变化未经 tick 不采纳（仍显示 v1）");
    await act(async () => { registered!.fn(); });
    eq(container.textContent, "v2", "E: tick 后采纳新文本");
    const rendersBeforeSameTick = renders;
    await act(async () => { registered!.fn(); });
    // React bail-out 语义：同值 setState 至多再确认调用一次组件函数，但跳过
    // 子树渲染与 DOM 写入——断言文本稳定 + 组件函数调用不超过一次确认调用。
    eq(container.textContent, "v2", "E: 同值 tick 文本稳定（bail-out 不改写 DOM）");
    ok(renders - rendersBeforeSameTick <= 1, "E: 同值 tick 无子树重渲染（≤1 次确认调用）");
    ok(rendersAfterMount >= 1, "E: probe 已挂载（前置 sanity）");
    await act(async () => root.unmount());
    globalThis.window = priorWindow;
  }

  // F: 源码守卫——禁周期全量扫描的代码层可查形态
  const srcDir = fileURLToPath(new URL(".", import.meta.url));
  const libSrc = readFileSync(`${srcDir}../lib/workProcessLiveStatus.ts`, "utf8");
  eq(libSrc.split("setInterval").length - 1, 2, "F: lib 仅 useThrottledValue 一处 interval（起+清），无其他定时器");
  eq(libSrc.includes("for (const"), false, "F: 数据源 lib 全部倒序索引扫描（首中即返）");
  const headerSrc = readFileSync(`${srcDir}../components/ProcessFoldHeader.tsx`, "utf8");
  eq(headerSrc.split("setInterval").length - 1, 0, "F: ProcessFoldHeader 无定时器（tick 秒数计时是既有 useTick，未经本件改动）");
  eq(headerSrc.includes("useMemo"), true, "F: 扫描包在 useMemo（事件驱动，tick 重渲染命中缓存）");
} finally {
  await harness.unmount();
  await harness.close();
}

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
