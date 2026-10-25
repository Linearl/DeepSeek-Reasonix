// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/task1131-trajectory-row-layout.test.ts
// 任务 1131 返修（wt-tl1131-2）: trajectory timeline 行布局契约。装机实测对照
// DSH 发现两问题——①标签列三排（输入/模型/工具）挤在一起未对齐、行高过小、
// 「模型」标签被截断出残字；②内容出现四排，模型行上方有色块溢出行容器。
// 溢出源定位（三条，均已修，非只对症状裁剪）：
//   a. laneOf 对 compaction/notice/phase 返回 null → 渲染层兜底 top:"42px"
//      画出三行之外的第四排（DSH timeline.ts laneFor 从不返回空：compacted→
//      模型行、其余→输入行）；
//   b. span 用 top: lane*14（带顶对齐）而标签用 line-height:14px 文本盒（字形
//      偏下）→ 色块与文字错位浮到文字上方；marker 的 translate(-50%,-50%)
//      再把 10px 高标记上提 5px 闯入上一行带；
//   c. .traj-timeline 无 overflow hidden，行边界约束不住内容。
// jsdom 无布局引擎 → 本套件做布局数值断言 + 溢出源源码守卫（CSS 块内钉死
// 统一行高/标签列固定宽/overflow hidden，断言 42px 兜底与 translate 上提已
// 移除），装机肉眼复验由出包后用户确认。
import assert from "node:assert";
import fs from "node:fs";
import path from "node:path";
import process from "node:process";
import { fileURLToPath } from "node:url";
import { buildTrajectoryLedger } from "../lib/trajectoryLedger";
import { buildTrajectorySequenceTimeline, laneOf } from "../lib/trajectoryTimeline";
import type { Item } from "../lib/useController";

let passed = 0;
let failed = 0;
function ok(cond: boolean, label: string) {
  if (cond) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; process.exitCode = 1; }
}

const here = path.dirname(fileURLToPath(import.meta.url));
const frontendRoot = path.resolve(here, "..", "..");
const viewSrc = fs.readFileSync(path.join(frontendRoot, "src/components/trajectoryViewBody.tsx"), "utf8");
const cssSrc = fs.readFileSync(path.join(frontendRoot, "src/components/TrajectoryView.css"), "utf8");

/** 提取某选择器规则块正文（块内无嵌套花括号，轨迹段均满足）。 */
function cssBlock(css: string, selector: string): string {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  return css.match(new RegExp(`(?:^|\\n)${escaped}\\s*\\{([^}]*)\\}`))?.[1] ?? "";
}

function user(id: string, text: string, extra: Partial<Extract<Item, { kind: "user" }>> = {}): Item {
  return { kind: "user", id, text, ...extra };
}
function assistant(id: string, extra: Partial<Extract<Item, { kind: "assistant" }>> = {}): Item {
  return { kind: "assistant", id, text: extra.text ?? `answer-${id}`, reasoning: "", streaming: false, ...extra };
}
function tool(id: string, extra: Partial<Extract<Item, { kind: "tool" }>> = {}): Item {
  return { kind: "tool", id, name: extra.name ?? "bash", args: "", readOnly: false, status: "done", ...extra };
}

console.log("\n任务 1131 返修 trajectory row layout: unified lane pitch, fixed label column, contained rows");

// ① 统一行高：行带几何全部收口到 --traj-lane-* 变量，pitch 落在 DSH 对照区间。
{
  const bar = cssBlock(cssSrc, ".traj-timeline");
  ok(/--traj-lane-pitch:\s*22px/.test(bar), "lane pitch is a single CSS variable (22px per lane)");
  const pitch = Number(bar.match(/--traj-lane-pitch:\s*(\d+(?:\.\d+)?)px/)?.[1] ?? NaN);
  ok(pitch >= 20 && pitch <= 24, `pitch ${pitch}px stays in the DSH reference range (20-24px/行)`);
  ok(bar.includes("height: calc(var(--traj-lane-pitch) * 3 + var(--traj-lane-inset) * 2)"),
    "bar height derives from lane geometry (no hardcoded height)");
  ok(!/height:\s*50px/.test(bar), "old 50px bar height is gone (was tied to the cramped 14px pitch)");
}

// ② 行边界约束：overflow hidden 硬约束（DSH .plot 同款），中心轴线残留清除。
{
  const bar = cssBlock(cssSrc, ".traj-timeline");
  ok(/overflow:\s*hidden/.test(bar), "timeline bar clips content at the row boundary (overflow: hidden)");
  ok(!/0 50% \/ 100% 1px/.test(cssSrc), "old 50% center-axis gradient is removed (would cut through the model lane)");
}

// ③ 标签列固定宽 + 带内居中：44px 固定宽（DSH 同款，容纳最宽标签不截断），
//    整行带高 flex 垂直居中与 span 同带对齐，行高 1 杜绝相邻行字形挤压。
{
  const label = cssBlock(cssSrc, ".traj-timeline__lane-label");
  ok(/width:\s*44px/.test(label), "lane label column keeps a fixed 44px width (DSH 同款)");
  ok(label.includes("height: var(--traj-lane-pitch)"), "label box spans the full lane band");
  ok(/align-items:\s*center/.test(label), "label is vertically centered in its lane band (aligned with its spans)");
  ok(/line-height:\s*1(?!\d)/.test(label), "label line-height is 1 (no glyph bleed across rows at font-scale)");
}

// ④ 溢出源守卫（渲染层）：42px 第四排兜底与 lane*14 旧行距必须消失，三处
//    top（标签/span/marker）统一走行带 calc 几何。
{
  ok(!viewSrc.includes("42px"), "no 42px fourth-row fallback in the render layer (overflow source a)");
  ok(!/\blane \* 14\b/.test(viewSrc) && !/\* 14px/.test(viewSrc), "no lane*14 cramped pitch arithmetic left (overflow source b)");
  const topCalcCount = (viewSrc.match(/top: `calc\(var\(--traj-lane-pitch\)/g) ?? []).length;
  ok(topCalcCount === 3, `all three positioned elements (label/span/marker) use lane-band calc tops (found ${topCalcCount})`);
}

// ⑤ 溢出源守卫（CSS）：marker 不再用 translate(-50%,-50%) 上提闯入上一行带。
{
  const marker = cssBlock(cssSrc, ".traj-timeline__marker");
  ok(marker !== "" && !/translate\(-50%,\s*-50%\)/.test(marker),
    "marker no longer translates -50% vertically into the lane above (overflow source b)");
  ok(/translateX\(-50%\)/.test(marker), "marker keeps horizontal centering on its anchor");
}

// ⑥ 溢出源守卫（模型层）：系统类记录折进三行（DSH laneFor 同款），投影中
//    不再产生 lane=null 的记录。
{
  ok(laneOf("compaction") === 1 && laneOf("notice") === 0 && laneOf("phase") === 0,
    "laneOf folds system kinds into the three lanes (compaction→model, notice/phase→input)");
  const items: Item[] = [
    user("u1", "hi", { createdAt: 1000 }),
    assistant("a1", { createdAt: 1500, workDurationMs: 400 }),
    tool("t1", { startedAt: 2000, durationMs: 100 }),
    { kind: "compaction", id: "c1", pending: false, trigger: "auto", messages: 40, summary: "s", archive: "a" },
    { kind: "notice", id: "n1", level: "info", text: "note" },
    { kind: "phase", id: "ph1", text: "plan" },
  ];
  const records = buildTrajectoryLedger(items).records;
  const sequence = buildTrajectorySequenceTimeline(records, "sequence");
  const duration = buildTrajectorySequenceTimeline(records, "duration");
  const projected = [...sequence.spans, ...sequence.markers, ...duration.spans, ...duration.markers];
  ok(projected.length > 0 && projected.every((entry) => entry.lane === 0 || entry.lane === 1 || entry.lane === 2),
    "every projected span/marker (both modes, incl. system records) carries a real lane");
}

// ⑦ 模式切换下行布局稳定：同一记录在 sequence/duration 两模式下 lane 一致
//    （top 几何只随 lane 走，与 left/width 的模式换算无关）；折叠开关不进
//    时间轴条 props，时间轴几何与轮次/调用折叠完全无关。
{
  const items: Item[] = [
    user("u1", "hi", { createdAt: 1000 }),
    assistant("a1", { createdAt: 1500, workDurationMs: 400 }),
    tool("t1", { startedAt: 2000, durationMs: 100 }),
    { kind: "compaction", id: "c1", pending: false, trigger: "auto", messages: 40, summary: "s", archive: "a" },
    { kind: "notice", id: "n1", level: "info", text: "note" },
  ];
  const records = buildTrajectoryLedger(items).records;
  const laneById = (mode: "sequence" | "duration"): Map<string, number> => {
    const model = buildTrajectorySequenceTimeline(records, mode);
    return new Map([...model.spans, ...model.markers].map((entry) => [entry.recordId, entry.lane]));
  };
  const sequenceLanes = laneById("sequence");
  const durationLanes = laneById("duration");
  ok(sequenceLanes.size > 0 && [...sequenceLanes].every(([id, lane]) => durationLanes.get(id) === lane),
    "each record keeps the same lane across sequence/duration modes (stable row layout)");
  ok(!/<TrajectoryTimelineBar[^>]*collapsed/.test(viewSrc),
    "turn/call collapse toggles never reach the timeline bar (row geometry is toggle-independent)");
}

console.log(`\n${passed} passed, ${failed} failed`);
if (!failed) process.stdout.write("task 1131 trajectory row layout contract: OK\n");
