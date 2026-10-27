// Run: tsx src/__tests__/tl1187-tab-switch-tail-restore.test.tsx
//
// tl 1187 — 切 tab 第二次往返后阅读位置上漂（DOM 恢复/对齐层）。
//
// 用户现象（复现路径极准）：A 普通 → B、C 都在流式（都在底部）→ A→B 正常 →
// B→C 时 C 回弹「偏离底部，往上移动了很多」；C 停止流式后就正常。
//
// 协调线已排除 kernel 层（D6 26/26 全绿：anchor/offsetPx 维护正确）。真因在
// hook 层的 DOM 恢复断点：useTranscriptKernel 的 sessionKey layout effect 里
// replaceSurface 返回 anchor.kind==="tail" 时没有任何代码执行贴底写入（只有
// kind==="block" 调了 begin("restore")）。tail 的贴底完全寄希望于三条下游补救链：
//   ① geometryRevision effect —— 只在 revision 字符串变化时跑。真实场景里
//      A→B 时两个会话的 contentRevision 不同 → 补救跑（第一次正常）；
//      B→C 时两个同构流式会话的 revision 字符串完全相同 → 补救不跑；
//   ② 子组件 remount 注册的 afterCurrentGenerationPaint(onGeometryChange)
//      —— layout effects 子先父后，注册发生在 replaceSurface 之前，携带旧
//      generation，回调时被 generation 门作废；
//   ③ ResizeObserver —— 依赖容器/footer 尺寸变化，同构内容切换时无变化不触发。
// 三条全落空 → scrollTop 停在旧值，而后台流式期间内容一直在长 → 视觉「上漂」。
// C 停止流式后内容不再增长，旧 scrollTop 恰好仍是底部 → 看起来正常。
//
// 本 harness 挂真实 useTranscriptKernel + TranscriptViewportWriter + 同一个
// 复用的滚动 div（stub 几何），逐字复刻上述时序。

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

// ── scroll-write probe capture（验收 3 读数来源）──────────────
const writes: TranscriptScrollWriteRecord[] = [];
(window as unknown as { __REASONIX_TRANSCRIPT_SCROLL_WRITE__?: (write: TranscriptScrollWriteRecord) => void })
  .__REASONIX_TRANSCRIPT_SCROLL_WRITE__ = (write) => writes.push(write);

// ── 复用的滚动容器几何 stub ───────────────────────────────────
// 真实行为：tab 切换是单实例换内容，滚动 div 被复用——scrollTop 保留上一个
// 会话的值；后台流式只改 scrollHeight（无 tail 同步跟进）。
const geom = { height: 0, client: 500, top: 0 };
function attachGeometry(el: HTMLElement): void {
  Object.defineProperty(el, "clientHeight", { configurable: true, get: () => geom.client });
  Object.defineProperty(el, "scrollHeight", { configurable: true, get: () => geom.height });
  Object.defineProperty(el, "scrollTop", {
    configurable: true,
    get: () => geom.top,
    set: (value: number) => { geom.top = value; },
  });
}
const bottomOf = () => geom.height - geom.client;

// ── harness：真实 hook + 复用 div ────────────────────────────
type Handle = { kernel: TranscriptKernel };
let handle: Handle | null = null;
const geomBound = new WeakSet<Element>();

function Harness({ sessionKey, geometryRevision }: { sessionKey: string; geometryRevision: string }) {
  const hook = useTranscriptKernel({ sessionKey, geometryRevision });
  handle = { kernel: hook.kernel };
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

// 手造快照（不依赖 DOM 读数），驱动 kernel 的真实用户观察路径。visibleBlocks
// 覆盖全高一个块：真实布局里 reader 的 capture 总能落到具体块（否则 anchor
// 保留旧值，那是另一回事，不在本件范围）。
function snap(top: number, height: number): TranscriptViewportSnapshot {
  return { scrollTop: top, scrollHeight: height, clientHeight: geom.client, visibleBlocks: [{ key: "blk-0", top: 0, bottom: height }] };
}
/** 用户真实到达底部（拉回当时的几何底；位移观察 → atBottom=true + tail anchor）。 */
function parkAtBottom(kernel: TranscriptKernel): void {
  const top = bottomOf();
  kernel.beginUserGesture(snap(top, geom.height), "native");
  kernel.observeNativeScroll(snap(top, geom.height));
  kernel.endUserGesture();
}
/** 用户真实上翻到 top（位移观察 → reader + block anchor）。 */
function scrollUpTo(kernel: TranscriptKernel, top: number, height: number): void {
  kernel.beginUserGesture(snap(top, height), "native");
  kernel.observeNativeScroll(snap(top, height));
  kernel.endUserGesture();
}

process.stdout.write("\n[tl 1187] tab-switch tail restore (DOM recovery layer)\n");

// ── D1: 用户场景逐字复刻 —— A→B 正常，B→C（同 revision）回弹 ──
{
  writes.length = 0;
  geom.height = 1200; geom.top = 300; // A：普通会话，用户停在 300
  await render("tab:A", "rev-a");
  scrollUpTo(handle!.kernel, 300, 1200);
  eq(handle!.kernel.intent, "reader", "D1: A parks as reader");

  // A→B：B 的布局高 2000；div 复用，scrollTop 保留 300（A 的值）。
  // revision 不同（真实里 contentRevision 不同 → 下游补救跑 → 第一次正常）。
  geom.height = 2000;
  await render("tab:B", "rev-b1");
  await flushFrames();
  eq(handle!.kernel.intent, "tail", "D1: B first visit follows tail");
  eq(geom.top, bottomOf(), "D1: A→B lands at the bottom (first round trip OK, matches user report)");

  // B 前台在底部：真实用户路径确认 atBottom。
  parkAtBottom(handle!.kernel);

  // B→C：C 的布局高 2400；scrollTop 保留 B 的贴底值 1500 → 距底 400。
  // revision 字符串与 B 完全相同（两个同构流式普通会话）——这正是第二次
  // 往返的差异：geometryRevision effect 不跑，子组件 rAF 被 generation 门
  // 作废，ResizeObserver 无尺寸变化不触发 → 没有任何代码执行贴底。
  const writesBeforeSwitch = writes.length;
  geom.height = 2400; geom.top = 1500;
  await render("tab:C", "rev-b1");
  eq(geom.top, bottomOf(), "D1: B→C lands at the bottom immediately (THE BUG: was 1500, drifted 400px up)");
  const cWrite = writes.slice(writesBeforeSwitch).find((write) => write.outcome === "accepted" && write.kind === "pinTail");
  ok(cWrite != null, "D1: the tail landing went through the single writer");
  if (cWrite) {
    eq(cWrite.owner, "tail-follow", "D1: diagnostic owner is tail-follow");
    eq(cWrite.intent, "tail", "D1: diagnostic intent is tail");
    eq(cWrite.bottomDistance, 0, "D1: diagnostic bottomDistance is 0");
  }

  // C 前台在底部后，后台继续流式增长（后台不跑 tail 同步）。
  parkAtBottom(handle!.kernel);

  // C→A→C 往返 ×5（验收 1：连做 5 次不掉）。每次后台长 400。
  for (let round = 1; round <= 5; round += 1) {
    geom.height = 1200; // A 前台布局（scrollTop 保留 C 的贴底值，A 高度小会被 clamp——stub 不 clamp，直接设）
    geom.top = 300;
    await render("tab:A", "rev-a");
    await flushFrames();
    geom.height = 2400 + 400 * round; // C 后台又长了 400
    geom.top = 300; // 切回 C 时 scrollTop 是 div 上一个值（A 的 300）
    await render("tab:C", "rev-b1");
    eq(geom.top, bottomOf(), `D1: round-trip ${round} back to C lands at the new bottom (${bottomOf()})`);
    const drift = bottomOf() - geom.top;
    ok(drift === 0, `D1: round-trip ${round} zero drift (bottomDistance ${drift}px)`);
  }
}

// ── D2: 防手滑 —— reader 会话切回必须回到离开位置，绝不贴底 ──
{
  writes.length = 0;
  geom.height = 2000; geom.top = 400; // R：用户上翻到 400
  await render("tab:R", "rev-r");
  scrollUpTo(handle!.kernel, 400, 2000);
  eq(handle!.kernel.intent, "reader", "D2: R parked as reader");

  geom.height = 1200; geom.top = 300; // 切到 A
  await render("tab:A", "rev-a");
  await flushFrames();
  const writesBeforeReturn = writes.length;

  geom.height = 2000; geom.top = 400; // 切回 R：布局不变，scrollTop 保留 400
  await render("tab:R", "rev-r");
  await flushFrames();
  eq(geom.top, 400, "D2: returning reader keeps its position (must NOT be yanked to the bottom)");
  eq(handle!.kernel.intent, "reader", "D2: intent stays reader after the round trip");
  ok(!writes.slice(writesBeforeReturn).some((write) => write.kind === "pinTail" && write.outcome === "accepted"),
    "D2: no tail write was issued for the returning reader surface");
}

// ── D3: 贴底写入被拒（几何暂不可用）时，rAF 兜底重试仍要贴底 ──
{
  writes.length = 0;
  geom.height = Number.NaN; geom.top = 0; // 切回瞬间布局尚未就绪（hydration 中间态）
  await render("tab:C", "rev-b1");
  const rejected = writes.filter((write) => write.outcome === "invalid-geometry");
  ok(rejected.length > 0, "D3: the instant landing was refused while geometry was unavailable");
  geom.height = 2400; geom.top = 1500; // 布局就绪
  await flushFrames();
  eq(geom.top, bottomOf(), "D3: the rAF fallback retry lands at the bottom once geometry is ready");
}

await act(async () => { root.unmount(); });
dom.window.close();

process.stdout.write(`\n[tl 1187] passed=${passed} failed=${failed}\n`);
if (failed > 0) process.exit(1);
