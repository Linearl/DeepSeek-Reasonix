// Run: tsx --import ./scripts/css-stub-register.mjs src/__tests__/tl1204-tab-switch-hydrate-landing.test.tsx
//
// tl 1204 — 1196 三形态收敛的 hook 层验收：切 tab 恢复的两条路径（快路径
// replaceSurface / 全量重载 hydrate 完成帧）都必须「该贴底首帧即底」。
//
// 快路径：replaceSurface effect 在 layout effect 批次里同步问权威
// restoreLanding —— render（act 提交）返回、rAF 尚未 flush 时写已发生。
// 全量重载路径：hydrate 期间几何不可用（同步写被拒 → 唯一 rAF 兜底布防）；
// hydrate 完成帧（真内容挂载、paint 前）Transcript 调 hook 暴露的
// restoreLanding —— 本 harness 在同一时序点手动调用它，断言同步落底。
// reader 会话走同一权威：恢复 block 锚位，绝不贴底。

import { JSDOM } from "jsdom";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { useTranscriptKernel } from "../lib/useTranscriptKernel";
import type { TranscriptKernel, TranscriptViewportSnapshot } from "../lib/transcriptKernel";
import type { TranscriptScrollWriteRecord } from "../lib/transcriptScrollProbe";

let passed = 0;
let failed = 0;
function ok(condition: unknown, label: string) {
  if (condition) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}
function eq(actual: unknown, expected: unknown, label: string) {
  const good = actual === expected;
  ok(good, good ? label : `${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
}

const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", { pretendToBeVisual: true, url: "http://localhost/" });
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);

const writes: TranscriptScrollWriteRecord[] = [];
(window as unknown as { __REASONIX_TRANSCRIPT_SCROLL_WRITE__?: (write: TranscriptScrollWriteRecord) => void })
  .__REASONIX_TRANSCRIPT_SCROLL_WRITE__ = (write) => writes.push(write);

// 复用滚动容器：scrollTop 保留上一个会话的值（真实 tab 切换语义）。
// heightReady=false 模拟 hydrate 中间态（scrollHeight 非有限 → 写被拒）。
const geom = { height: 0, client: 500, top: 0, heightReady: true };
function attachGeometry(el: HTMLElement): void {
  Object.defineProperty(el, "clientHeight", { configurable: true, get: () => geom.client });
  Object.defineProperty(el, "scrollHeight", { configurable: true, get: () => (geom.heightReady ? geom.height : Number.NaN) });
  Object.defineProperty(el, "scrollTop", {
    configurable: true,
    get: () => geom.top,
    set: (value: number) => { geom.top = value; },
  });
}
const bottomOf = () => geom.height - geom.client;

type Handle = { kernel: TranscriptKernel; restoreLanding: () => void };
let handle: Handle | null = null;
const geomBound = new WeakSet<Element>();

function Harness({ sessionKey, geometryRevision }: { sessionKey: string; geometryRevision: string }) {
  const hook = useTranscriptKernel({ sessionKey, geometryRevision });
  handle = { kernel: hook.kernel, restoreLanding: hook.restoreLanding };
  return <div
    ref={(node) => {
      hook.scrollRef.current = node;
      if (node && !geomBound.has(node)) { geomBound.add(node); attachGeometry(node); }
    }}
  />;
}

const rootElement = document.getElementById("root");
if (!rootElement) throw new Error("missing root");
const root: Root = createRoot(rootElement);

async function render(sessionKey: string, revision: string): Promise<void> {
  await act(async () => { root.render(<Harness sessionKey={sessionKey} geometryRevision={revision} />); });
}
async function flushFrames(count = 2): Promise<void> {
  for (let index = 0; index < count; index += 1) {
    await act(async () => { await new Promise<void>((resolve) => dom.window.requestAnimationFrame(() => resolve())); });
  }
}

function snap(top: number, height: number): TranscriptViewportSnapshot {
  return { scrollTop: top, scrollHeight: height, clientHeight: geom.client, visibleBlocks: [{ key: "blk-0", top: 0, bottom: height }] };
}
function parkAtBottom(kernel: TranscriptKernel): void {
  const top = bottomOf();
  kernel.beginUserGesture(snap(top, geom.height), "native");
  kernel.observeNativeScroll(snap(top, geom.height), );
  kernel.endUserGesture();
}
function scrollUpTo(kernel: TranscriptKernel, top: number, height: number): void {
  kernel.beginUserGesture(snap(top, height), "native");
  kernel.observeNativeScroll(snap(top, height));
  kernel.endUserGesture();
}

process.stdout.write("\n[tl 1204] tab-switch hydrate landing (hook layer, both paths)\n");

// --- P1 (1196 form 1): fast path — the switch-back lands on the commit tick,
// BEFORE any animation frame, so the first painted frame is already at the
// bottom. ---
{
  writes.length = 0;
  geom.heightReady = true;
  geom.height = 1200; geom.top = 300;
  await render("tab:A", "rev-a");
  scrollUpTo(handle!.kernel, 300, 1200); // A parks as reader at 300

  geom.height = 2000; geom.top = 300;
  await render("tab:B", "rev-b");
  parkAtBottom(handle!.kernel); // B follows the tail and owns the bottom

  const writesBefore = writes.length;
  geom.height = 2400; geom.top = 1500; // C's layout; reused scroller keeps 1500
  await render("tab:C", "rev-b"); // same revision string as B (1187's quiet chain)
  eq(geom.top, bottomOf(), "P1: B→C first frame is already at the bottom (no visible scroll)");
  eq(writes.slice(writesBefore).some((write) => write.kind === "pinTail" && write.outcome === "accepted"),
    true, "P1: the landing rode the single writer before any rAF flush");
}

// --- P2 (1196 form 2): full-reload path — hydrate holds geometry hostage,
// the hydrate-complete frame lands synchronously BEFORE the first paint. ---
{
  writes.length = 0;
  geom.height = 800; geom.top = 300;
  await render("tab:H", "rev-h1");
  parkAtBottom(handle!.kernel);

  // Switch away, then switch back while the reload is in flight: the surface
  // shows placeholder geometry (scrollHeight not finite) — the sync landing
  // is refused and the single rAF fallback arms.
  geom.height = 1200; geom.top = 100;
  await render("tab:A", "rev-a");
  await flushFrames();

  geom.heightReady = false; geom.height = 2400; geom.top = 100;
  await render("tab:H", "rev-h2");
  ok(geom.top !== bottomOf(), "P2: during the reload the view has NOT landed (geometry held hostage)");
  eq(handle!.kernel.intent, "tail", "P2: the authority still owns the tail verdict");

  // Hydrate completes: real blocks measured, paint pending. The hydrate frame
  // asks the authority — synchronously, before the browser paints.
  const writesBeforeLanding = writes.length;
  geom.heightReady = true;
  handle!.restoreLanding();
  eq(geom.top, bottomOf(), "P2: hydrate-complete frame lands at the bottom in-tick (form 2 fixed)");
  eq(writes.slice(writesBeforeLanding).some((write) => write.kind === "pinTail" && write.outcome === "accepted"),
    true, "P2: the reload landing rode the single writer");
}

// --- P3 (1196 form 3): the same authority on a reader-parked session must
// restore its block position — never drag it to the bottom. ---
{
  writes.length = 0;
  geom.heightReady = true;
  geom.height = 2000; geom.top = 400;
  await render("tab:R", "rev-r");
  scrollUpTo(handle!.kernel, 400, 2000);
  eq(handle!.kernel.intent, "reader", "P3: R parked as reader");

  geom.height = 1200; geom.top = 100;
  await render("tab:A", "rev-a");
  await flushFrames();

  const writesBeforeReturn = writes.length;
  geom.height = 2000; geom.top = 400;
  await render("tab:R", "rev-r");
  handle!.restoreLanding(); // the hydrate-complete frame asks the authority
  eq(geom.top, 400, "P3: the reader stays exactly where it left (NOT the bottom)");
  ok(!writes.slice(writesBeforeReturn).some((write) => write.kind === "pinTail" && write.outcome === "accepted"),
    "P3: no tail write exists on the reader's restore path");
}

await act(async () => { root.unmount(); });
dom.window.close();

process.stdout.write(`\n[tl 1204 hook] passed=${passed} failed=${failed}\n`);
if (failed > 0) process.exit(1);
