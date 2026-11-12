// Run: tsx src/__tests__/task1559-compaction-readout-contacts.test.tsx
//
// tl1559 验收（tl556/973 复发续作：压缩实时读数 UI 触点补全）：
//  ① 底部折叠头条 readout 空窗（summarizer TTFT，首 token 估计未到）降级为
//     「估算中…」占位腿，不再裸秒数半截读数（刀1）；
//  ② 首 chunk 延迟机制核实：后端 gate 不卡首帧（compact_live.go 首个 emission
//     绕过 1s 节流间隔），空窗=TTFT，前端 startedAt+估算占位兜底（Part A/B 钉）；
//  ③ 既有 556/973 钉零回退：降级是 opt-in（estimatingWhenEmpty），压缩卡与
//     无 opts 调用面的输出逐字节不变（Part A 钉死兼容形态）。
//
// r2（打回重修）：用户裁决摘除右栏容量卡的读数胶囊——与左侧转录区压缩行完全
// 重复。第一轮刀2 的 compactionWindowOverride 读数源字段与 ContextPanel 容量卡
// 渲染（原 Part C/D）整段回收，实时读数触点收敛为折叠头条（本文件 Part B）与
// 973 压缩卡两处。
//
// 刀1（本文件初始版）：helper 降级参数化 + 折叠头条接线钉。
// 刀2（已回收）：compactionWindowOverride 读数源字段 + ContextPanel 容量卡渲染钉。

import { JSDOM } from "jsdom";
import React from "react";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { LocaleProvider } from "../lib/i18n";
import { ProcessFoldHeader } from "../components/ProcessFoldHeader";
import { compactionLiveReadout } from "../lib/compactionLive";
import type { Item } from "../lib/useController";
import { en } from "../locales/en";
import type { Translator } from "../lib/i18n";

let passed = 0;
let failed = 0;

function ok(value: boolean, label: string) {
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

// en 字典直灌的最小 Translator（973 同款拼装契约）。
const t: Translator = ((key: keyof typeof en, vars?: Record<string, string | number>) => {
  let out: string = en[key] ?? String(key);
  if (vars) {
    for (const [k, v] of Object.entries(vars)) out = out.replaceAll(`{${k}}`, String(v));
  }
  return out;
}) as Translator;

// ── Part A: compactionLiveReadout 降级参数化（opt-in，无 opts 逐字节兼容）────

{
  process.stdout.write("helper: estimatingWhenEmpty opt-in degradation\n");
  const now = 1_000_000;

  // tl1559 ②: 有戳无 tokens（TTFT 空窗）→ 裸秒数升格为「秒数 · 估算中…」。
  eq(
    compactionLiveReadout({ pending: true, startedAt: now - 7_000 }, now, t, { estimatingWhenEmpty: true }),
    " · 7s · estimating…",
    "TTFT window: bare elapsed upgrades to elapsed + estimating leg",
  );

  // tokens 已到 → 实数腿照常，绝不出现估算占位。
  eq(
    compactionLiveReadout({ pending: true, startedAt: now - 40_000, tokens: 12300, tokensPerSec: 45 }, now, t, { estimatingWhenEmpty: true }),
    " · 40s · 12.3K tokens · 45 tokens/s",
    "tokens present: full readout, no estimating leg",
  );
  eq(
    compactionLiveReadout({ pending: true, startedAt: now - 5_000, tokens: 800, tokensPerSec: 0 }, now, t, { estimatingWhenEmpty: true }),
    " · 5s · 800 tokens",
    "tokens below tps noise floor: tokens leg without tps, no estimating leg",
  );

  // 水合 pending 卡（无内存戳）→ 空串零残值，降级不复活静态卡。
  eq(compactionLiveReadout({ pending: true }, now, t, { estimatingWhenEmpty: true }), "", "hydrated pending card: no stamp → still empty");

  // 非 pending → 空串（降级不越界到 resolved/failed 卡）。
  eq(
    compactionLiveReadout({ pending: false, startedAt: now - 40_000 }, now, t, { estimatingWhenEmpty: true }),
    "",
    "resolved card: empty even with the opt-in",
  );

  // ④ 兼容钉：无 opts 的调用面（973 压缩卡）保持既有精确串——TTFT 空窗仍是裸秒数。
  eq(compactionLiveReadout({ pending: true, startedAt: now - 1_000 }, now, t), " · 1s", "legacy (no opts): bare elapsed byte-for-byte");
  eq(
    compactionLiveReadout({ pending: true, startedAt: now - 90_000 }, now, t),
    " · 1m 30s",
    "legacy (no opts): minute-formatted elapsed byte-for-byte",
  );
}

// ── Part B: 折叠头条（底部条）接线渲染 ───────────────────────────────────────

function installDom() {
  const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", {
    pretendToBeVisual: true,
    url: "http://localhost/",
  });
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  globalThis.window = dom.window as unknown as Window & typeof globalThis;
  globalThis.document = dom.window.document;
  Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
  globalThis.Node = dom.window.Node;
  globalThis.Element = dom.window.Element;
  globalThis.HTMLElement = dom.window.HTMLElement;
  globalThis.Event = dom.window.Event;
  globalThis.MouseEvent = dom.window.MouseEvent;
  globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
  globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
  return dom;
}

const dom = installDom();
const container = dom.window.document.getElementById("root") as HTMLElement;
let root: Root | null = null;

function renderHeader(compactionItem: Item) {
  const segment = {
    key: "seg-1559",
    processItems: [compactionItem],
    outsideItems: [],
    displayItems: [compactionItem],
    hasOutsideContent: false,
    foldActive: false,
    hasRunningWork: compactionItem.kind === "compaction" ? compactionItem.pending : false,
    durationMs: 0,
    labelStyle: "full",
    turnItems: [],
  } as unknown as Parameters<typeof ProcessFoldHeader>[0]["segment"];
  const header = React.createElement(ProcessFoldHeader, {
    segment,
    open: false,
    onToggle: () => {},
  });
  root!.render(React.createElement(LocaleProvider, null, header));
}

function headerLabel(): string {
  const el = container.querySelector(".turn-collapse__label, .readonly-batch__label");
  return el ? el.textContent ?? "" : "";
}

// LocaleProvider 在 jsdom 里可能落到任一语言——占位腿两形态都收。
const estimatingShapes = ["estimating…", "估算中…", "估算中…"];

try {
  process.stdout.write("component: ProcessFoldHeader TTFT-window degradation\n");
  root = createRoot(container);

  // 有戳无 tokens（TTFT 空窗）→ 头条带秒数 + 估算占位，不再是半截读数。
  const ttftWindow: Item = { kind: "compaction", id: "c1559a", pending: true, trigger: "auto", messages: 0, summary: "", archive: "", startedAt: Date.now() - 7_000 };
  act(() => renderHeader(ttftWindow));
  let label = headerLabel();
  ok(elapsedShapePresent(label, 7), `TTFT window: strip keeps the elapsed leg: ${JSON.stringify(label)}`);
  ok(estimatingShapes.some((shape) => label.includes(shape)), `TTFT window: strip shows the estimating placeholder: ${JSON.stringify(label)}`);

  // tokens 已到 → 实数腿照常（占位消失）。
  const withTokens: Item = { kind: "compaction", id: "c1559b", pending: true, trigger: "auto", messages: 0, summary: "", archive: "", startedAt: Date.now() - 40_000, tokens: 12300, tokensPerSec: 45 };
  act(() => renderHeader(withTokens));
  label = headerLabel();
  ok(label.includes("12.3K tokens") && label.includes("45 tokens/s"), `tokens landed: strip shows the real readout: ${JSON.stringify(label)}`);
  ok(!estimatingShapes.some((shape) => label.includes(shape)), `tokens landed: estimating placeholder gone: ${JSON.stringify(label)}`);

  // 水合态（无戳无 tokens）→ 无读数（与 556 基线一致，降级不越界）。
  const hydrated: Item = { kind: "compaction", id: "c1559c", pending: true, trigger: "auto", messages: 0, summary: "", archive: "" };
  act(() => renderHeader(hydrated));
  label = headerLabel();
  ok(!estimatingShapes.some((shape) => label.includes(shape)) && !/\d+s\b/.test(label) && !label.includes("tokens"), `hydrated: no readout, static label only: ${JSON.stringify(label)}`);

  await act(async () => {
    root?.unmount();
    root = null;
  });
} catch (err) {
  ok(false, `component test threw: ${err}`);
}

// 秒数腿两语言形态（"Ns" / "N秒"）。
function elapsedShapePresent(label: string, seconds: number): boolean {
  return new RegExp(`\\b${seconds}s\\b`).test(label) || label.includes(`${seconds}秒`) || label.includes(`${seconds} 秒`);
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
