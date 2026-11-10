// Run: npx tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1512-width-blank-block.test.tsx
//
// tl1512 — 转录区渲染偶发大空白块（宽度变化相关，选中文字后恢复）。
//
// 机制复现（诊断件）：右栏展开/收起改变转录区宽度 → 所有已挂载块重排（行高
// 随宽度变化）。TranscriptWindow 的测量扫描只重测 committed range 内的块并把
// 新尺寸 stage 进 ledger，但发布门（publishStaged canPublish）要求
// `firstMeasurements || kernel.intent === "reader" && …` —— tail 意图（用户停底，
// 右栏开合最常见的停驻位）下重排块的全新尺寸永远 stage 不 publish：
// virtualizer.itemSizeCache / prefix / placements / extent 保持旧宽度坐标系。
//
// 两个可见后果（同一根因）：
//   1. 窗口化模式：绝对定位块 placement.top 来自旧 prefix，真实 DOM 高度已是
//      新宽度值 → 收宽（变矮）方向出现块间空白缝 + 末冷块到 resident tail 的
//      边界空档（spacer extent 保持旧值 = 字面意义的空白占位块）；展宽（变高）
//      方向出现重叠。
//   2. full 兜底模式：extent 仍取自 stale prefix → spacer 保持旧总高，in-flow
//      内容被顶开一段旧坐标系的空白（「内容被顶开/占位异常」的直接形态）。
//
// 空白持续存在，直到某个事件把 intent 翻成 reader（选中文字拖选的边缘滚动
// 位移就是这条）→ 下一次测量扫描放行发布 → placements/extent 修正 → 空白
// 消失。这就是「选中文字后恢复」的机制身份。
//
// （scroller 宽度交付的结构性补偿——kernel 侧 readSnapshot 失效与 composer-resize
// 写笔——挂在 useTranscriptKernel hook 的 ResizeObserver 上，tl1243/tl1255 已有
// 端到端覆盖；本件直挂 TranscriptWindow，验证的是窗口测量层自身的发布门。）
//
// 验收矩阵：
//   A  复现钉：tail 意图停底 + 宽度突变（收宽→变矮→空白缝方向）→ settle 后
//      可见冷块间空白缝与 extent 偏差 > 0.5px 且多次 settle 不自愈（intent
//      保持 tail，无人翻面）。修复后：同路径零空白。
//   B  反方向（展宽→变高）：零重叠 + extent 与真实内容一致。
//   C  「选中文字才恢复」消失：宽度突变后不注入任何手势/滚动/意图翻转，
//      仅常规 settle 空白即闭合（intent 全程 tail）。
//   D  reader 意图回归：同宽度突变在 reader 意图下修复前后都自愈
//      （既有发布门语义不回退）。

import { JSDOM } from "jsdom";
import { act, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import type { ReactNode } from "react";
import type { TranscriptKernel, LogicalAnchor, TranscriptViewportSnapshot } from "../lib/transcriptKernel";
import { TranscriptViewportWriter } from "../lib/transcriptViewportWriter";
import type { TimelineProjection } from "../lib/transcriptTimeline";
import type { ProjectionViewProps } from "../components/TranscriptProjectionView";

let passed = 0;
let failed = 0;
function ok(condition: unknown, label: string) {
  if (condition) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; process.exitCode = 1; }
}
function closeTo(actual: number, expected: number, tolerance: number, label: string) {
  const good = Math.abs(actual - expected) <= tolerance;
  ok(good, good ? label : `${label}: expected ~${expected}±${tolerance}, got ${actual}`);
}

// ── DOM 全局（在动态 import 产物之前就位）────────────────────────────
const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", { url: "http://localhost/" });
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.Node = dom.window.Node;
globalThis.Element = dom.window.Element;
globalThis.Event = dom.window.Event;

type FrameCallback = (time: number) => void;
let frameCallbacks: FrameCallback[] = [];
globalThis.requestAnimationFrame = (callback: FrameCallback): number => {
  frameCallbacks.push(callback);
  return frameCallbacks.length;
};
globalThis.cancelAnimationFrame = (handle: number): void => {
  frameCallbacks = frameCallbacks.filter((_, index) => index !== handle - 1);
};
function bumpFrame(): void {
  const callbacks = frameCallbacks;
  frameCallbacks = [];
  callbacks.forEach((callback) => callback(0));
}

// ── 宽度可变的确定性布局 stub ────────────────────────────────────────
// 宽度语义=转录区列宽：800 基线；1200=右栏收起→更宽→更矮（空白缝方向）；
// 400=右栏展开→更窄→更高（重叠方向）。行高逐宽度离散模拟文本重排。
const state = {
  width: 800,
  clientHeight: 600,
  rowsPerBlock: 1,
  blockCount: 24,
};
const ROW_HEIGHT: Record<number, number> = { 400: 220, 800: 150, 1200: 100, 1300: 90 };
const rowHeight = (): number => ROW_HEIGHT[state.width] ?? 150;
const blockHeight = (): number => state.rowsPerBlock * rowHeight();

const heightOf = (element: HTMLElement): number => {
  if (element.classList.contains("transcript")) return state.clientHeight;
  if (element.classList.contains("transcript__window")) return Number.parseFloat(element.style.height) || 0;
  if (element.classList.contains("transcript__row")) return rowHeight();
  if (element.classList.contains("transcript__block")) return element.querySelectorAll(".transcript__row").length * rowHeight();
  return Array.from(element.children).reduce(
    (height, child) => height + ((child as HTMLElement).style.position === "absolute" ? 0 : heightOf(child as HTMLElement)),
    0,
  );
};
const topOf = (element: HTMLElement): number => {
  if (element.classList.contains("transcript") || !element.parentElement) return 0;
  const parent = element.parentElement;
  const parentTop = topOf(parent) - (parent.classList.contains("transcript") ? parent.scrollTop : 0);
  if (element.style.position === "absolute") return parentTop + (Number.parseFloat(element.style.top) || 0);
  let top = parentTop;
  for (const sibling of parent.children) {
    if (sibling === element) break;
    if ((sibling as HTMLElement).style.position !== "absolute") top += heightOf(sibling as HTMLElement);
  }
  return top;
};
const proto = dom.window.HTMLElement.prototype as unknown as Record<string, unknown>;
proto.getBoundingClientRect = function (this: HTMLElement) {
  const top = topOf(this), height = heightOf(this);
  return { top, bottom: top + height, left: 0, right: state.width, width: state.width, height, x: 0, y: top, toJSON: () => ({}) };
};
Object.defineProperty(proto, "offsetHeight", {
  configurable: true,
  get(this: HTMLElement) { return heightOf(this); },
});
Object.defineProperty(proto, "clientHeight", {
  configurable: true,
  get(this: HTMLElement) { return this.classList.contains("transcript") ? state.clientHeight : 0; },
});
Object.defineProperty(proto, "clientWidth", {
  configurable: true,
  get(this: HTMLElement) { return this.classList.contains("transcript") ? state.width : 0; },
});
Object.defineProperty(proto, "scrollHeight", {
  configurable: true,
  get(this: HTMLElement) {
    if (!this.classList.contains("transcript")) return 0;
    const spacer = this.querySelector<HTMLElement>(".transcript__window");
    const spacerHeight = Number.parseFloat(spacer?.style.height || "0");
    const residentRows = Array.from(this.querySelectorAll(".transcript__resident-tail .transcript__row"))
      .filter((row) => !row.closest(".transcript__window-item")).length;
    return Math.max(0, spacerHeight + residentRows * rowHeight());
  },
});
const nativeScrollTop = Object.getOwnPropertyDescriptor(dom.window.Element.prototype, "scrollTop")!;
Object.defineProperty(proto, "scrollTop", {
  configurable: true,
  get(this: HTMLElement) {
    const raw = nativeScrollTop.get!.call(this) as number;
    if (!this.classList.contains("transcript")) return raw;
    const maximum = Math.max(0, this.scrollHeight - this.clientHeight);
    return Math.max(0, Math.min(maximum, raw));
  },
  set(this: HTMLElement, value: number) {
    const maximum = Math.max(0, this.scrollHeight - this.clientHeight);
    const top = this.classList.contains("transcript") && Number.isFinite(maximum)
      ? Math.max(0, Math.min(maximum, value)) : value;
    nativeScrollTop.set!.call(this, top);
  },
});

// ── 手控 ResizeObserver（宽度/高度按当前 state 分类）─────────────────
type ROEntry = { target: Element; contentRect: { width: number; height: number } };
class FakeResizeObserver {
  static all: FakeResizeObserver[] = [];
  private callback: (entries: ROEntry[]) => void;
  private observed = new Set<Element>();
  constructor(callback: (entries: ROEntry[]) => void) {
    this.callback = callback;
    FakeResizeObserver.all.push(this);
  }
  observe(element: Element): void { this.observed.add(element); }
  unobserve(element: Element): void { this.observed.delete(element); }
  disconnect(): void { this.observed.clear(); }
  private rectOf(element: Element): { width: number; height: number } {
    const el = element as HTMLElement;
    const height = el.classList.contains("transcript")
      ? state.clientHeight
      : el.classList.contains("transcript__window")
        ? Number.parseFloat(el.style.height) || 0
        : heightOf(el);
    return { width: state.width, height };
  }
  fireAll(): void {
    for (const observer of FakeResizeObserver.all) {
      for (const element of observer.observed) {
        observer.callback([{ target: element, contentRect: this.rectOf(element) }]);
      }
    }
  }
  static reset(): void { FakeResizeObserver.all = []; }
}
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = FakeResizeObserver;
dom.window.ResizeObserver = FakeResizeObserver as unknown as typeof ResizeObserver;

const flushAll = async (): Promise<void> => {
  await act(async () => {
    for (let round = 0; round < 6; round += 1) {
      bumpFrame();
      await new Promise<void>((resolve) => setImmediate(resolve));
    }
  });
};

// ── 被测组件装载 ─────────────────────────────────────────────────────
process.stdout.write("\ntl1512 宽度突变空白块 — 复现与修复验证\n");
const { default: TranscriptWindow } = await import("../components/TranscriptWindow");
const { TranscriptKernel } = await import("../lib/transcriptKernel");

const kernel = new TranscriptKernel({}) as TranscriptKernel;
const writer = new TranscriptViewportWriter();
kernel.replaceSurface("tl1512");
kernel.connectWriter(writer.write);

const projection: TimelineProjection = {
  hasOlderHistory: false,
  completedBlocks: Array.from({ length: state.blockCount }, (_, index) => ({
    key: `blk-${index}`,
    phase: "completed" as const,
    rows: [],
    contentRevision: 1,
    measurementRevision: "1",
  })),
};

const host = document.createElement("div");
document.body.append(host);
const root: Root = createRoot(host);
let scroller = null as unknown as HTMLDivElement;
let lastLayout: { mode: string; extent: number; placementCount: number; blockCount: number } | null = null;

function Fixture(): ReactNode {
  const [element, setElement] = useState<HTMLDivElement | null>(null);
  scroller = element!;
  return <div className="transcript" ref={setElement}>
    <TranscriptWindow
      projection={projection}
      scrollElement={element}
      kernel={kernel}
      protectedBlockKeys={new Set<string>()}
      forceFull={false}
      pinnedJumpBlockKey={undefined}
      onPinnedJumpVisible={() => {}}
      estimateBlock={() => blockHeight()}
      onGeometryWillChange={(anchor?: LogicalAnchor) => {
        if (!kernel.userGestureActive && !kernel.activeTransaction) kernel.begin("restore", anchor);
      }}
      onGeometryChange={(covered?: boolean, beforePaint?: boolean) => {
        if (beforePaint || !covered) return;
        kernel.advanceGeometry();
        const transaction = kernel.activeTransaction;
        if (transaction && scroller) {
          const snapshot = readSnapshotNow();
          kernel.correctAnchor(transaction, (key) => snapshot.visibleBlocks.find((block) => block.key === key)?.top, {
            scrollTop: snapshot.scrollTop,
            clientHeight: snapshot.clientHeight,
            visibleBlocks: snapshot.visibleBlocks,
          });
        }
      }}
      renderProjection={(layout: ProjectionViewProps): ReactNode => {
        lastLayout = {
          mode: layout.mode,
          extent: layout.extent,
          placementCount: layout.placements?.size ?? 0,
          blockCount: layout.blocks.length,
        };
        return <div ref={layout.tailRef} className="transcript__resident-tail">
        <div ref={layout.spacerRef} className="transcript__window" style={{ height: layout.extent }}
          data-transcript-render-mode={layout.mode} data-transcript-safe-fallback={layout.safety || undefined} />
        {layout.blocks.map((block) => {
          const place = layout.placements?.get(block.key);
          return <div key={block.key}
            className={`transcript__block${place ? " transcript__window-item" : ""}`}
            data-index={place?.index}
            data-transcript-block-key={block.key}
            style={place ? { position: "absolute", top: place.top, left: 0, width: "100%" } : undefined}>
            {Array.from({ length: state.rowsPerBlock }, (_, row) => <div key={row} className="transcript__row" />)}
          </div>;
        })}
      </div>;
      }}
    />
  </div>;
}

function readSnapshotNow(): TranscriptViewportSnapshot {
  return {
    scrollTop: scroller.scrollTop,
    scrollHeight: scroller.scrollHeight,
    clientHeight: scroller.clientHeight,
    visibleBlocks: Array.from(scroller.querySelectorAll<HTMLElement>("[data-transcript-block-key]")).map((element) => {
      const top = topOf(element) + scroller.scrollTop;
      return { key: element.dataset.transcriptBlockKey!, top, bottom: top + heightOf(element) };
    }),
  };
}

// 空白缝读数：全部挂载窗口块按 placement.top 排序，相邻 next.top −
// (cur.top + realHeight)；再含末冷块真实底到 resident tail 顶（= spacer extent）
// 的边界空档；extent 与真实冷区总高的偏差单独返回（占位异常读数）。
function blankInfo(): { maxGap: number; maxOverlap: number; extentError: number; gaps: string[]; mode: string; extent: number; placementCount: number } {
  const items = Array.from(scroller.querySelectorAll<HTMLElement>(".transcript__window-item"))
    .map((element) => ({
      key: element.dataset.transcriptBlockKey!,
      top: Number.parseFloat(element.style.top) || 0,
      real: heightOf(element),
    }))
    .sort((left, right) => left.top - right.top);
  const gaps: string[] = [];
  let maxGap = 0;
  let maxOverlap = 0;
  for (let index = 1; index < items.length; index += 1) {
    const delta = items[index].top - (items[index - 1].top + items[index - 1].real);
    if (delta > maxGap) maxGap = delta;
    if (-delta > maxOverlap) maxOverlap = -delta;
    if (delta > 0.5) gaps.push(`${items[index - 1].key}->${items[index].key}:+${delta.toFixed(1)}px`);
  }
  const spacer = scroller.querySelector<HTMLElement>(".transcript__window");
  const extent = Number.parseFloat(spacer?.style.height || "0");
  const lastCold = items[items.length - 1];
  if (lastCold) {
    const tailGap = extent - (lastCold.top + lastCold.real);
    if (tailGap > maxGap) {
      maxGap = tailGap;
      gaps.push(`${lastCold.key}->resident-tail:+${tailGap.toFixed(1)}px`);
    }
    if (-tailGap > maxOverlap) maxOverlap = -tailGap;
  }
  const realExtent = items.length * blockHeight();
  return {
    maxGap, maxOverlap, extentError: extent - realExtent, gaps,
    mode: lastLayout?.mode ?? "unknown", extent,
    placementCount: lastLayout?.placementCount ?? 0,
  };
}

async function gestureToBottom(): Promise<void> {
  await act(async () => {
    kernel.beginUserGesture(readSnapshotNow());
    scroller.scrollTop = scroller.scrollHeight;
    kernel.observeNativeScroll(readSnapshotNow());
    scroller.dispatchEvent(new Event("scroll"));
    kernel.endUserGesture();
  });
  await flushAll();
}

async function changeWidth(width: number): Promise<void> {
  state.width = width;
  await act(async () => { FakeResizeObserver.all.forEach((observer) => observer.fireAll()); });
  await flushAll();
}

try {
  await act(async () => root.render(<Fixture />));
  writer.attach(scroller, kernel.generation);
  await flushAll();

  // 初始首测发布（firstMeasurements 绕意图门）：宽度 800 坐标系自洽。
  let blank = blankInfo();
  closeTo(blank.maxGap, 0, 0.5, "A0 初始渲染宽度800零空白缝");
  ok(kernel.intent === "tail", `A0 新表面默认 tail 意图（实际=${kernel.intent}）`);

  await gestureToBottom();
  blank = blankInfo();
  closeTo(blank.maxGap, 0, 0.5, "A1 停底后零空白缝（底部首测已发布）");
  ok(kernel.intent === "tail", `A1 停底意图=tail（实际=${kernel.intent}）`);

  // ── A 复现钉：右栏收起（800→1200，块变矮）→ 空白缝方向。不注入任何
  //    手势/滚动/意图翻转，仅 RO 交付 + 常规 settle。
  await changeWidth(1200);
  blank = blankInfo();
  ok(kernel.intent === "tail", `A2 全程零手势零位移，意图仍 tail（实际=${kernel.intent}）`);
  ok(blank.mode === "windowed", `A2 保持窗口化模式（实际=${blank.mode}）`);
  ok(blank.placementCount > 0, "A2 窗口块 placements 非空（committed range 未失守）");
  closeTo(blank.maxGap, 0, 0.5, `A2/A①/② 宽度突变零空白块（${blank.gaps.slice(0, 3).join(" ") || "no gaps"}）`);
  closeTo(blank.extentError, 0, 1, `A2 extent=真实冷区总高（偏差=${blank.extentError.toFixed(1)}px）`);

  // ── B 反方向：右栏展开（1200→400，块变高）→ 重叠/extent 低估方向。
  await changeWidth(400);
  blank = blankInfo();
  ok(kernel.intent === "tail", `B 意图仍 tail（实际=${kernel.intent}）`);
  closeTo(blank.maxOverlap, 0, 0.5, `B 展宽方向零重叠（maxOverlap=${blank.maxOverlap.toFixed(1)}px）`);
  closeTo(blank.maxGap, 0, 0.5, `B 展宽方向零空白缝（maxGap=${blank.maxGap.toFixed(1)}px）`);
  closeTo(blank.extentError, 0, 1, `B extent=真实冷区总高（偏差=${blank.extentError.toFixed(1)}px）`);

  // ── D reader 意图回归：上滚一位移翻 intent=reader，同宽度突变应保持自愈。
  await act(async () => {
    kernel.beginUserGesture(readSnapshotNow());
    scroller.scrollTop = scroller.scrollTop - 300;
    kernel.observeNativeScroll(readSnapshotNow());
    scroller.dispatchEvent(new Event("scroll"));
    kernel.endUserGesture();
  });
  await flushAll();
  ok(kernel.intent === "reader", `D 位移后意图=reader（实际=${kernel.intent}）`);
  await changeWidth(800);
  blank = blankInfo();
  closeTo(blank.maxGap, 0, 0.5, "D reader 意图宽度突变零空白（既有语义不回退）");
  closeTo(blank.extentError, 0, 1, "D reader 意图 extent 自洽");

  // ── C 终态确认：回到 tail 停底再来一轮宽度突变——「选中文字才恢复」行为
  //    已消失：不注入任何恢复动作，settle 即闭缝。
  await gestureToBottom();
  ok(kernel.intent === "tail", `C 回停意图=tail（实际=${kernel.intent}）`);
  await changeWidth(1300);
  blank = blankInfo();
  ok(kernel.intent === "tail", `C 终轮意图仍 tail（实际=${kernel.intent}）`);
  closeTo(blank.maxGap, 0, 0.5, `C/C③ 无选中文字无滚动空白自闭（maxGap=${blank.maxGap.toFixed(1)}px）`);
  closeTo(blank.extentError, 0, 1, `C extent=真实冷区总高（偏差=${blank.extentError.toFixed(1)}px）`);
} finally {
  await act(async () => root.unmount());
  host.remove();
  FakeResizeObserver.reset();
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
