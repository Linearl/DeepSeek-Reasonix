// Run: tsx src/__tests__/tl1165-smooth-scroll-follow.test.tsx
// tl 1165 acceptance harness — smooth streaming scroll follow (DSH ScrollFollow
// parity, lab switch `experimental_smooth_scroll_follow`):
//   ① off state: the reasoning pane's scrollTop trajectory over the same delta
//      sequence is byte-identical to the pre-1165 instant writes, and no extra
//      listeners are armed (acceptance ①);
//   ② on state: follow requests issue native smooth scrollTo at the measured
//      floor, an outstanding target finishes before another is issued, and the
//      scrollTop sequence shows monotone per-write motion with no large jumps
//      (acceptance ② and ④);
//   ③ a reader gesture cancels the animation and pins the position (acceptance ③);
//   ④ prefers-reduced-motion: reduce degrades to instant positioning (acceptance ⑤);
//   ⑤ the transcript writer keeps every non-tail write instant even with the
//     switch on, and tail writes report honest acceptance to the kernel;
//   ⑥ the lab surface registers the entry in the 界面 group at the optional
//     tier (acceptance ⑥, badge counts pinned in task562).

import { JSDOM } from "jsdom";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { SmoothTailFollower } from "../lib/smoothTailFollow";
import { TranscriptViewportWriter } from "../lib/transcriptViewportWriter";
import { applyLabFlags, labFlagEnabled } from "../lib/labFlags";
import { EXPERIMENT_FEATURE_TIERS } from "../lib/experimentTiers";
import { LAB_LAYOUT_DEFAULT_DATA } from "../lab/labLayoutDefault";
import { useReasoningScrollFollow } from "../lib/useReasoningScrollFollow";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}
function eq(actual: unknown, expected: unknown, label: string) {
  const good = JSON.stringify(actual) === JSON.stringify(expected);
  if (good) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}\n`); failed += 1; }
}

console.log("\ntl 1165 smooth streaming scroll follow");

// ── shared DOM stubs ─────────────────────────────────────────
const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", { pretendToBeVisual: true });
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.Event = dom.window.Event;

type ScrollCall = { top: number; behavior: string };
let scrollToCalls: ScrollCall[] = [];
let listenerArms: string[] = [];
const proto = dom.window.HTMLElement.prototype as unknown as Record<string, unknown>;
const nativeAdd = proto.addEventListener as typeof HTMLElement.prototype.addEventListener;
proto.scrollTo = function scrollStub(this: HTMLElement, arg: ScrollCall) {
  scrollToCalls.push({ top: arg.top, behavior: String(arg.behavior) });
  if (arg.behavior === "instant") this.scrollTop = arg.top;
};
proto.addEventListener = function addSpy(this: HTMLElement, type: string, listener: EventListenerOrEventListenerObject, options?: AddEventListenerOptions | boolean) {
  listenerArms.push(type);
  return nativeAdd.call(this, type, listener, options);
} as typeof HTMLElement.prototype.addEventListener;

function makeScroller(scrollHeight: number, clientHeight: number): HTMLElement {
  const el = document.createElement("div");
  let top = 0;
  let height = scrollHeight;
  Object.defineProperty(el, "clientHeight", { configurable: true, value: clientHeight });
  Object.defineProperty(el, "scrollHeight", {
    configurable: true,
    get: () => height,
    set: (value: number) => { height = value; },
  });
  Object.defineProperty(el, "scrollTop", {
    configurable: true,
    get: () => top,
    set: (value: number) => { top = value; },
  });
  document.body.appendChild(el);
  return el;
}

/** Simulate the browser animating toward `to`: three monotone frames plus a
 * scroll delivery per frame — the shape a real smooth follow produces. */
function animateFrames(el: HTMLElement, from: number, to: number): number[] {
  const frames: number[] = [];
  for (let step = 1; step <= 3; step += 1) {
    const next = from + ((to - from) * step) / 3;
    el.scrollTop = next;
    frames.push(el.scrollTop);
    el.dispatchEvent(new dom.window.Event("scroll"));
  }
  return frames;
}

// ── ⑥ lab surface registration ───────────────────────────────
{
  eq(EXPERIMENT_FEATURE_TIERS.smoothScrollFollow, "optional", "lab register carries smoothScrollFollow at the optional tier (界面组三档徽章「可选」)");
  const viewLayout = LAB_LAYOUT_DEFAULT_DATA.groups
    .find((group) => group.key === "ui")?.entries
    .find((entry) => entry.id === "viewLayout");
  const member = viewLayout?.members?.find((m) => m.id === "smoothScrollFollow");
  eq(member?.tier, "optional", "视图布局卡 renders a smoothScrollFollow member row");
  ok(viewLayout?.onKeys?.includes("experimentalSmoothScrollFollow") === true, "viewLayout card light reads experimentalSmoothScrollFollow");
  eq(labFlagEnabled("smoothScrollFollow"), false, "the switch ships off (默认关)");
}

// ── SmoothTailFollower: the four DSH guards ──────────────────
console.log("\nSmoothTailFollower core (DSH 四条)");
{
  const el = makeScroller(2_000, 200); // floor 1800, top 0
  const f = new SmoothTailFollower();
  scrollToCalls = [];
  f.follow(el);
  eq(scrollToCalls, [{ top: 1800, behavior: "smooth" }], "guard 1: follow issues a native smooth scrollTo at the measured floor");
  eq(f.animating, true, "an outstanding target is tracked");

  scrollToCalls = [];
  f.follow(el);
  eq(scrollToCalls, [], "guard 2: no new target while the previous animation is outstanding");

  // Simulate the animation delivering part-way (not yet at the target).
  el.scrollTop = 900;
  f.follow(el);
  eq(scrollToCalls, [], "guard 2: mid-flight growth still waits for the outstanding target");

  // Arrival settles the target; the next request issues a fresh one.
  el.scrollTop = 1797;
  f.noteScroll(el);
  eq(f.animating, false, "a delivery at the target (±4px) settles the animation");
  scrollToCalls = [];
  el.scrollTop = 1700; // content grew again
  el.scrollHeight = 2_100; // floor 1900
  f.follow(el);
  eq(scrollToCalls, [{ top: 1900, behavior: "smooth" }], "guard 2: a settled animation frees the next follow request");

  // Guard 3: reader gesture cancels and pins instantly.
  scrollToCalls = [];
  f.interrupt(el);
  eq(scrollToCalls, [{ top: 1700, behavior: "instant" }], "guard 3: interrupt pins the current position instantly");
  eq(f.animating, false, "guard 3: interrupt clears the outstanding target");
  f.interrupt(el);
  eq(scrollToCalls.length, 1, "guard 3: interrupt is a no-op while idle");
  scrollToCalls = [];
  el.scrollTop = 0;
  f.follow(el);
  eq(scrollToCalls, [{ top: 1900, behavior: "smooth" }], "guard 3: after a cancel the next request issues a fresh target");

  f.reset();
  el.remove();
}
{
  // Guard 4: reduced motion degrades to the instant write.
  const mediaCalls: string[] = [];
  (globalThis as unknown as { matchMedia: (q: string) => { matches: boolean } }).matchMedia = (query: string) => {
    mediaCalls.push(query);
    return { matches: query.includes("reduce") };
  };
  const el = makeScroller(2_000, 200);
  const f = new SmoothTailFollower();
  scrollToCalls = [];
  f.follow(el);
  eq(el.scrollTop, 1800, "guard 4: reduced motion positions instantly (scrollTop write)");
  eq(scrollToCalls, [], "guard 4: reduced motion issues no smooth request");
  eq(mediaCalls, ["(prefers-reduced-motion: reduce)"], "guard 4: the reduce media query is consulted");

  // Near-bottom arrivals position immediately even without reduced motion.
  (globalThis as unknown as { matchMedia: (q: string) => { matches: boolean } }).matchMedia = () => ({ matches: false });
  const near = makeScroller(2_000, 200);
  near.scrollTop = 1798; // 2px above the floor
  scrollToCalls = [];
  f.follow(near);
  eq(near.scrollTop, 1800, "near-bottom arrivals land instantly (DSH tolerance)");
  eq(scrollToCalls, [], "near-bottom arrivals issue no smooth request");
  el.remove();
  near.remove();
  delete (globalThis as unknown as { matchMedia?: unknown }).matchMedia;
}

// ── TranscriptViewportWriter integration ─────────────────────
console.log("\nTranscriptViewportWriter (transcript 正文)");
{
  // Off state: tail writes keep the exact instant behaviour.
  const el = makeScroller(2_000, 200);
  const writer = new TranscriptViewportWriter({ smoothFollow: () => false });
  writer.attach(el, 7);
  scrollToCalls = [];
  const offResult = writer.write({ session: "s", generation: 7, transactionId: 1, geometryRevision: 0, owner: "tail-follow", intent: "tail", offset: Number.POSITIVE_INFINITY });
  eq(el.scrollTop, 1800, "off: tail write keeps the instant floor positioning");
  eq(scrollToCalls, [], "off: tail write issues no scrollTo (逐字节一致)");
  eq([offResult.accepted, offResult.changed], [true, true], "off: kernel acceptance report unchanged");

  // On state: tail writes go smooth and report honest acceptance.
  const onWriter = new TranscriptViewportWriter({ smoothFollow: () => true });
  onWriter.attach(el, 7);
  el.scrollTop = 900;
  scrollToCalls = [];
  const first = onWriter.write({ session: "s", generation: 7, transactionId: 2, geometryRevision: 0, owner: "tail-follow", intent: "tail", offset: Number.POSITIVE_INFINITY });
  eq(scrollToCalls, [{ top: 1800, behavior: "smooth" }], "on: tail write issues one smooth scrollTo at the floor");
  eq([first.accepted, first.changed, first.offset], [true, false, 900], "on: the in-flight write reports accepted/unchanged at the start position");

  // High-frequency deltas: no new targets while animating, no jitter.
  scrollToCalls = [];
  for (let i = 0; i < 5; i += 1) {
    el.scrollHeight = 2_000 + (i + 1) * 120;
    const r = onWriter.write({ session: "s", generation: 7, transactionId: 3 + i, geometryRevision: 0, owner: "tail-follow", intent: "tail", offset: Number.POSITIVE_INFINITY });
    eq([r.accepted, r.changed], [true, false], `on: burst write ${i + 1} stays accepted while the target is outstanding`);
  }
  eq(scrollToCalls, [], "on: five burst writes issue no additional scroll target (guard 2, 无抽搐)");

  // Arrival: lazy settle on the next write frees a fresh smooth target.
  el.scrollTop = 2_320; // delivered at (past) the outstanding target
  el.scrollHeight = 2_600; // floor 2400
  scrollToCalls = [];
  onWriter.write({ session: "s", generation: 7, transactionId: 99, geometryRevision: 0, owner: "tail-follow", intent: "tail", offset: Number.POSITIVE_INFINITY });
  eq(scrollToCalls, [{ top: 2400, behavior: "smooth" }], "on: arrival + growth lets the next write issue a fresh target");

  // Non-tail writes stay instant even with the switch on.
  scrollToCalls = [];
  el.scrollTop = 500;
  const restore = onWriter.write({ session: "s", generation: 7, transactionId: 100, geometryRevision: 0, owner: "restore", intent: "reader", offset: 800 });
  eq(el.scrollTop, 800, "on: a restore write keeps the exact instant assignment");
  eq(scrollToCalls, [], "on: a restore write issues no smooth request");
  eq(restore.accepted, true, "on: restore acceptance unchanged");

  // Gesture cancel through the writer gateway.
  scrollToCalls = [];
  onWriter.interruptViewport();
  eq(scrollToCalls, [{ top: 800, behavior: "instant" }], "on: writer interrupt pins the position (手势取消)");
  el.remove();
}

// ── reasoning pane (React harness) ───────────────────────────
console.log("\nreasoning pane (思考面板)");
const rootElement = document.getElementById("root")!;
const root = createRoot(rootElement);

function Harness({ content }: { content: string }) {
  const [elementRef, onScroll] = useReasoningScrollFollow(content, true);
  return <div ref={elementRef} onScroll={onScroll}>{content}</div>;
}

async function renderHarness(content: string, key?: string) {
  await act(async () => currentRoot.render(
    key ? <Harness key={key} content={content} /> : <Harness content={content} />,
  ));
}

// One mounted pane at a time; the geometry closure survives across renders.
let paneTop = 0;
let contentVersion = 0;
function pane(): HTMLElement {
  const el = rootElement.firstElementChild;
  if (!el) throw new Error("missing pane");
  Object.defineProperty(el, "clientHeight", { configurable: true, value: 200 });
  Object.defineProperty(el, "scrollHeight", { configurable: true, get: () => 1_000 + contentVersion * 120 });
  Object.defineProperty(el, "scrollTop", {
    configurable: true,
    get: () => paneTop,
    set: (value: number) => { paneTop = value; },
  });
  return el;
}
let currentRoot = root;

// Off state — the acceptance-① byte-for-byte trajectory. The first render only
// mounts the pane (the stubbed geometry lands right after it), so an armed
// re-render with a distinct content string records the first real write.
await act(async () => applyLabFlags({ smoothScrollFollow: false }));
contentVersion = 0;
paneTop = 0;
listenerArms = [];
scrollToCalls = [];
await renderHarness("start");
pane();
contentVersion = 0;
await renderHarness("start-armed"); // arms the geometry; scrollTop jumps to 1000
const offPane = pane();
const offTrajectory: number[] = [offPane.scrollTop];
for (let delta = 1; delta <= 4; delta += 1) {
  contentVersion = delta;
  await renderHarness(`start delta ${delta}`);
  offTrajectory.push(offPane.scrollTop);
}
eq(offTrajectory, [1000, 1120, 1240, 1360, 1480], "off: scrollTop trajectory is exactly one instant write per delta (逐字节一致)");
eq(scrollToCalls, [], "off: no scrollTo calls at all");
eq(listenerArms.filter((t) => t === "wheel" || t === "touchstart" || t === "pointerdown"), [], "off: no gesture listeners armed (不挂任何额外事件)");

// On state — the same mounted pane flips the switch (the real settings-save
// path): the armed listeners appear as exactly the gesture-cancel trio.
listenerArms = [];
await act(async () => applyLabFlags({ smoothScrollFollow: true }));
eq(listenerArms.sort(), ["pointerdown", "touchstart", "wheel"], "on: flipping the switch arms exactly the three gesture listeners");
const onPane = pane();

// The flip's follow already re-armed the tail (top>=floor lands instantly at
// 1280); record the streaming trajectory from here.
scrollToCalls = [];
const onTrajectory: (number | string)[] = [onPane.scrollTop];
// delta 1 → one smooth request toward the new floor.
contentVersion = 6;
await renderHarness("fresh token");
const last6 = scrollToCalls.at(-1);
onTrajectory.push(last6 ? `smooth→${last6.top}@${last6.behavior}` : "none");
// The browser animates in three monotone frames; scroll delivery settles it.
onTrajectory.push(...animateFrames(onPane, 1280, 1520));
// delta 2 after settlement → a fresh smooth target for the new floor.
scrollToCalls = [];
contentVersion = 8;
await renderHarness("fresh token more");
const last8 = scrollToCalls.at(-1);
onTrajectory.push(last8 ? `smooth→${last8.top}@${last8.behavior}` : "none");
eq(onTrajectory, [1280, "smooth→1520@smooth", 1360, 1440, 1520, "smooth→1760@smooth"],
  "on: monotone per-delta motion, one smooth target each, no jumps (scrollTop 序列)");
// High-frequency deltas mid-flight produce no second target (无抽搐).
scrollToCalls = [];
for (const delta of [10, 12, 14]) {
  contentVersion = delta;
  await renderHarness(`fresh token ${delta}`);
}
eq(scrollToCalls, [], "on: three mid-flight deltas issue no new target (连续高频 delta 无抽搐)");

// Reader gesture: wheel cancels and pins; upward reading releases follow.
onPane.scrollTop = 2_000;
onPane.dispatchEvent(new dom.window.Event("wheel"));
eq(scrollToCalls, [{ top: 2000, behavior: "instant" }], "on: wheel cancels the animation and pins the reader position");
onPane.scrollTop = 600;
onPane.dispatchEvent(new dom.window.Event("scroll"));
contentVersion = 16;
scrollToCalls = [];
await renderHarness("fresh token final");
eq(scrollToCalls, [], "on: after an upward gesture the pane stops following (不被拽回)");

// Reduced motion: instant writes even with the switch on. The reader first
// returns near the bottom, which re-arms the follow intent (the released
// intent from the upward gesture above must stay released until then).
(globalThis as unknown as { matchMedia: (q: string) => { matches: boolean } }).matchMedia = () => ({ matches: true });
contentVersion = 16;
onPane.scrollTop = 2_718; // within 8px of the floor (2920-200)
onPane.dispatchEvent(new dom.window.Event("scroll"));
contentVersion = 20;
await renderHarness("reduce token");
eq(onPane.scrollTop, 3200, "reduced-motion: growth positions instantly under the switch-on state (验收⑤)");
scrollToCalls = [];
contentVersion = 22;
await renderHarness("reduce token more");
eq(onPane.scrollTop, 3440, "reduced-motion: further growth positions instantly");
eq(scrollToCalls, [], "reduced-motion: no smooth request");
delete (globalThis as unknown as { matchMedia?: unknown }).matchMedia;

await act(async () => currentRoot.unmount());
dom.window.close();

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
