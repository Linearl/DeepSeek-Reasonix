// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1341-tab-switch-tail-landing.test.tsx
//
// tl 1341 — 切 tab 贴底两段式（先落旧位、再被拉到底）的 hook 层验收。
//
// 病灶机制（probe 复现 + 静态链核对）：tab 切换是单实例换内容，切面 commit
// 那一刻复用的滚动容器仍报上一会话的 scrollHeight/scrollTop——replaceSurface
// effect 里的同步 restoreLanding 用这份陈旧几何贴底，首帧落在「上一会话的底」
// （对大会话=视觉中间）；新表面的块窗口在下一个 commit 才挂载（scrollHeight
// 变成真实高度），随后的几何补救链（settleGeometry / scheduleTailSync 兜底）
// 才把它拉到底——两段式由此构成，且第一段与第二段都发生在首帧之后。
//
// 修法：切面时刻把 TAIL 落位挂起（记下切面几何三元组），restoreLanding 与
// settleGeometry 只在「观测几何已翻新（≠切面时刻）」时经同一权威落位；陈旧
// 观测期间连流式跟进也扣住（否则它会再写一次旧底、事后拖拽）。block 恢复
// 走原同步路径不动（1196 读者驻留保护）。
//
// 验收读数（本文件）：
//   L1 切回 commit 时刻不写旧底（零 tail 写入）；
//   L2 新表面几何挂载 commit 之后、任何 rAF 兜底 flush 之前，贴底一次到位
//      （写经 tail-follow 单写者，acceptedOffset = 新底）；
//   L3 中途无第二段：落位前 scrollTop 未被写到过中间值；
//   L4 读者会话红线：恢复 block 锚位，绝不贴底（tl1196 形态 2/3 不破）；
//   L5 同一会话同几何的切回（切面几何即真实几何）仍当帧贴底——快路径不回退。

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

type Handle = { kernel: TranscriptKernel; restoreLanding: () => boolean; commitViewportGeometry: (covered?: boolean, beforePaint?: boolean) => void };
let handle: Handle | null = null;
const geomBound = new WeakSet<Element>();

function Harness({ sessionKey, geometryRevision }: { sessionKey: string; geometryRevision: string }) {
  const hook = useTranscriptKernel({ sessionKey, geometryRevision });
  handle = {
    kernel: hook.kernel,
    restoreLanding: hook.restoreLanding,
    commitViewportGeometry: hook.commitViewportGeometry,
  };
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
async function flushFrames(count = 3): Promise<void> {
  for (let index = 0; index < count; index += 1) {
    await act(async () => { await new Promise<void>((resolve) => dom.window.requestAnimationFrame(() => resolve())); });
  }
}
/** 真实两提交时序：切面 commit（scroller 仍是旧几何）→ 新表面块挂载 commit。 */
async function switchSurface(key: string, revision: string, mountedHeight: number, mountedTop?: number): Promise<void> {
  await render(key, revision);
  geom.height = mountedHeight;
  if (mountedTop !== undefined) geom.top = mountedTop;
  await render(key, revision);
}

function snap(top: number, height: number): TranscriptViewportSnapshot {
  return { scrollTop: top, scrollHeight: height, clientHeight: geom.client, visibleBlocks: [{ key: "blk-0", top: 0, bottom: height }] };
}
function parkAtBottom(kernel: TranscriptKernel, height: number): void {
  const top = height - geom.client;
  kernel.beginUserGesture(snap(top, height), "native");
  kernel.observeNativeScroll(snap(top, height));
  kernel.endUserGesture();
}
function scrollUpTo(kernel: TranscriptKernel, top: number, height: number): void {
  kernel.beginUserGesture(snap(top, height), "native");
  kernel.observeNativeScroll(snap(top, height));
  kernel.endUserGesture();
}

process.stdout.write("\n[tl 1341] tab-switch tail landing deferral (hook layer)\n");

// --- L1/L2/L3: 贴底会话切回 —— 切面 commit 零写入，新几何挂载 commit 之后、
// rAF 兜底之前一次贴底到位，中途无中间值写入。 ---
{
  writes.length = 0;
  geom.heightReady = true;
  // A = 大会话，用户停底（tail）。
  geom.height = 10000; geom.top = 9500;
  await render("tab:A", "rev-a");
  parkAtBottom(handle!.kernel, 10000);
  eq(handle!.kernel.intent, "tail", "L1: A parked at the bottom owns the tail");

  // 切到小会话 B，B 也停底。
  geom.height = 2000; geom.top = 1500;
  await render("tab:B", "rev-b");
  parkAtBottom(handle!.kernel, 2000);

  // 切回 A：commit 1（切面）——scroller 仍是 B 的几何。
  writes.length = 0;
  geom.height = 2000; geom.top = 1500;
  await render("tab:A", "rev-b2");
  const commit1TailWrites = writes.filter((write) => write.kind === "pinTail" && write.outcome !== "invalid-geometry" && write.outcome !== "no-op");
  eq(commit1TailWrites.length, 0, "L1: the switch commit issues no tail write against the outgoing surface's geometry");

  // commit 2（A 的块窗口挂载）：scrollHeight 变 A 的真实高度，scrollTop 保留。
  // 真实流水线里 TranscriptWindow 的布局 effect 会以 beforePaint 提交几何
  // （commitViewportGeometry(covered, true)）；jsdom harness 没有窗口适配器，
  // 在这里手动复刻同一条 pre-paint 提交。
  geom.height = 10000;
  const writesBeforeMount = writes.length;
  await render("tab:A", "rev-b2");
  await act(async () => { handle!.commitViewportGeometry(undefined, true); });
  const mountWrites = writes.slice(writesBeforeMount);
  const landing = mountWrites.find((write) => write.kind === "pinTail" && write.outcome === "accepted");
  ok(landing != null, "L2: the mount commit lands through the single writer before any rAF fallback");
  eq(landing?.owner, "tail-follow", "L2: the landing owner is tail-follow");
  eq(geom.top, bottomOf(), "L2: the view is at the incoming surface's bottom in the same pre-paint commit");
  const physicalWrites = mountWrites.filter((write) => write.outcome === "accepted" && typeof write.acceptedOffset === "number");
  eq(physicalWrites.length, 1, "L3: exactly one physical write — no intermediate mid-scroll write exists");
  eq(physicalWrites[0]?.acceptedOffset, bottomOf(), "L3: that write targets the final bottom, not a stale floor");

  // rAF 兜底 flush 不再产生第二段。
  const writesBeforeFlush = writes.length;
  await flushFrames(3);
  const flushWrites = writes.slice(writesBeforeFlush).filter((write) => write.outcome === "accepted");
  eq(flushWrites.length, 0, "L3: flushing fallback frames adds no second-stage write");
  eq(geom.top, bottomOf(), "L3: still parked at the bottom after the flush (stable)");
}

// --- L4（红线）: 读者会话切回 —— 恢复 block 锚位，绝不贴底（1196 形态 2/3）。 ---
{
  writes.length = 0;
  geom.heightReady = true;
  geom.height = 2000; geom.top = 400;
  await render("tab:R", "rev-r");
  scrollUpTo(handle!.kernel, 400, 2000);
  eq(handle!.kernel.intent, "reader", "L4: R parked as reader");

  geom.height = 1200; geom.top = 300;
  await render("tab:B", "rev-b");
  await flushFrames(2);

  const writesBeforeReturn = writes.length;
  geom.height = 2000; geom.top = 400;
  await render("tab:R", "rev-r");
  await flushFrames(3);
  eq(geom.top, 400, "L4: the returning reader keeps its exact position (not yanked to the bottom)");
  eq(handle!.kernel.intent, "reader", "L4: intent stays reader after the round trip");
  ok(!writes.slice(writesBeforeReturn).some((write) => write.kind === "pinTail" && write.outcome === "accepted"),
    "L4: no tail write exists on the reader's restore path");
}

// --- L5: 切面几何即真实几何（同会话同布局的快路径）—— 当帧贴底不回退。 ---
{
  writes.length = 0;
  geom.heightReady = true;
  geom.height = 800; geom.top = 300;
  await render("tab:Q", "rev-q");
  parkAtBottom(handle!.kernel, 800);

  geom.height = 2400; geom.top = 1500;
  await render("tab:S", "rev-s");
  parkAtBottom(handle!.kernel, 2400);

  // 切回 Q：切面 commit 时刻 scroller 已是 Q 的真实几何（同布局复用）。
  // 切面 tick 的权威同步落位 = tl1204 快路径（写可为 no-op——视口已在底）。
  const writesBeforeFast = writes.length;
  geom.height = 800; geom.top = 300;
  await render("tab:Q", "rev-q");
  const fastWrites = writes.slice(writesBeforeFast).filter((write) => write.kind === "pinTail" && (write.outcome === "accepted" || write.outcome === "no-op"));
  eq(geom.top, bottomOf(), "L5: a fast-path switch lands in the commit tick itself (no deferred frame)");
  ok(fastWrites.length >= 1, "L5: the fast-path landing rode the single writer synchronously");
}

await act(async () => { root.unmount(); });
dom.window.close();

process.stdout.write(`\n[tl 1341] passed=${passed} failed=${failed}\n`);
if (failed > 0) process.exit(1);
