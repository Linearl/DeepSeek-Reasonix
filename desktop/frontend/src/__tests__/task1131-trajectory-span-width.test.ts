// Run: tsx src/__tests__/task1131-trajectory-span-width.test.ts
// 任务 1131: trajectory timeline span-width contract (装机反馈「每个事件都很
// 窄」的对齐修复). DSH 对照结论（github-repo/deepseek-harness，只读）：
//   - 模型域 = 原始单位（timeline.ts: sequence 序位 / timed ms），渲染层统一
//     除以域长折算百分比（TrajectoryTimeline.tsx:685-687）；
//   - 默认模式 = sequence 等宽序位（TrajectoryView.tsx:347 两布尔默认 false
//     → 'sequence'），每个事件 1/N 等宽满铺；
//   - 最小可见宽 = 纯 CSS（.span min-width: 2px + width: max(2px, ...)），
//     不依赖 JS 域计算。
// fork 1112 的窄因：默认走真实时间墙钟投影（会话空闲把 span 稀释成细线）；
// durationOn 态把原始序位/ms 单位直乘 100 当百分比（span 溢出/占满）。
// 本套件钉死：模型原始域、trajectorySpanRect 唯一换算口、等宽满铺、
// duration 真实占比、focus 区间交叠、以及渲染层源码契约。
import assert from "node:assert";
import fs from "node:fs";
import path from "node:path";
import process from "node:process";
import { fileURLToPath } from "node:url";
import { buildTrajectoryLedger } from "../lib/trajectoryLedger";
import { buildTrajectorySequenceTimeline, buildTrajectoryTimeline, recordIdsInFocus, trajectorySpanRect } from "../lib/trajectoryTimeline";
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

function user(id: string, text: string, extra: Partial<Extract<Item, { kind: "user" }>> = {}): Item {
  return { kind: "user", id, text, ...extra };
}
function assistant(id: string, extra: Partial<Extract<Item, { kind: "assistant" }>> = {}): Item {
  return { kind: "assistant", id, text: extra.text ?? `answer-${id}`, reasoning: "", streaming: false, ...extra };
}
function tool(id: string, extra: Partial<Extract<Item, { kind: "tool" }>> = {}): Item {
  return { kind: "tool", id, name: extra.name ?? "bash", args: "", readOnly: false, status: "done", ...extra };
}

console.log("\n任务 1131 trajectory span width: equal-width default, ms-proportional toggle, unified normalization");

// ① Sequence (default): every event renders 1/N wide — full-width usage with
//    no idle dilution, on anchored history and anchor-less history alike.
{
  const items: Item[] = [
    user("u1", "hi", { createdAt: 1000 }),
    assistant("a1", { createdAt: 1500, workDurationMs: 400 }),
    tool("t1", { startedAt: 2000, durationMs: 100 }),
    user("u2", "again", { createdAt: 300000 }), // 5min idle gap before it
    assistant("a2", { createdAt: 300500, workDurationMs: 200 }),
  ];
  const seq = buildTrajectorySequenceTimeline(buildTrajectoryLedger(items).records, "sequence");
  ok(seq.spans.length === 5 && seq.markers.length === 0, "sequence projects every record as a span");
  const rects = seq.spans.map((span) => trajectorySpanRect(seq, span));
  ok(rects.every((rect) => Math.abs(rect.widthPct - 20) < 1e-9), "each event is exactly 1/N = 20% wide (equal-width full usage)");
  ok(rects.every((rect, i) => Math.abs(rect.leftPct - i * 20) < 1e-9), "events tile left→right with no gaps");
  ok(Math.abs(rects.reduce((sum, rect) => sum + rect.widthPct, 0) - 100) < 1e-9, "spans cover the full domain (no dead space)");
}
{
  // Anchor-less history (1111 finding): sequence still renders completely.
  const items: Item[] = [user("u1", "hi"), assistant("a1"), tool("t1")];
  const seq = buildTrajectorySequenceTimeline(buildTrajectoryLedger(items).records, "sequence");
  const rects = seq.spans.map((span) => trajectorySpanRect(seq, span));
  ok(rects.length === 3 && rects.every((rect) => Math.abs(rect.widthPct - 100 / 3) < 1e-9),
    "anchor-less history: three equal-width events (33.3% each)");
}

// ② Duration toggle: spans take their real-ms share of the active-time domain.
{
  const items: Item[] = [
    user("u1", "hi", { createdAt: 1000 }),
    assistant("a1", { createdAt: 1500, workDurationMs: 1000 }),
    tool("t1", { startedAt: 9000, durationMs: 500 }), // 7500ms idle before it
  ];
  const dur = buildTrajectorySequenceTimeline(buildTrajectoryLedger(items).records, "duration");
  const byId = new Map(dur.spans.map((span) => [span.recordId, span]));
  const a1 = trajectorySpanRect(dur, byId.get("a1")!);
  const t1 = trajectorySpanRect(dur, byId.get("t1")!);
  ok(Math.abs(a1.widthPct - (1000 / 1500) * 100) < 1e-9, "duration span width = real ms share (66.7%, idle compressed out)");
  ok(Math.abs(t1.leftPct - a1.widthPct) < 1e-9 && Math.abs(t1.widthPct - (500 / 1500) * 100) < 1e-9,
    "next span starts where the previous ends (no fake filler units)");
}

// ③ Real-time projection keeps the raw-ms model domain (DSH timeline.ts 同款);
//    the render-layer normalization turns it into container percents.
{
  const items: Item[] = [
    user("u1", "hi", { createdAt: 1000 }),
    assistant("a1", { createdAt: 2000, workDurationMs: 1000 }),
    tool("t1", { startedAt: 6000, durationMs: 500 }),
  ];
  const time = buildTrajectoryTimeline(buildTrajectoryLedger(items).records)!;
  const a1 = time.spans.find((span) => span.recordId === "a1")!;
  ok(a1.start === 2000 && a1.width === 1000, "time spans carry raw epoch-ms start + ms width (same unit as t0/t1)");
  const rect = trajectorySpanRect(time, a1);
  // Domain 1000..6500 (t1 ends at 6000+500); a1 occupies 2000..3000.
  ok(Math.abs(rect.leftPct - (1000 / 5500) * 100) < 1e-9 && Math.abs(rect.widthPct - (1000 / 5500) * 100) < 1e-9,
    "trajectorySpanRect normalizes raw-ms spans against the wall-clock domain");
}

// ④ Focus membership follows projected spans in the model domain (DSH
//    trajectoryTimelineFocusIndexes 同款) — sequence ordinals work too.
{
  const items: Item[] = [
    user("u1", "hi"),
    assistant("a1"),
    tool("t1"),
    user("u2", "again"),
  ];
  const seq = buildTrajectorySequenceTimeline(buildTrajectoryLedger(items).records, "sequence");
  const inside = recordIdsInFocus(seq, { start: 1.5, end: 2.5 }); // ordinals 1.5..2.5
  ok(inside.has("a1") && inside.has("t1") && !inside.has("u1") && !inside.has("u2"),
    "sequence-domain drag focus covers spans overlapping the ordinal range only");
}

// ⑤ Render-layer source contract: normalization is centralized, minimum
//    visible width is pure CSS, and the mode wiring matches DSH semantics.
{
  ok(viewSrc.includes("trajectorySpanRect(model, span)"), "spans normalize through trajectorySpanRect");
  ok(!viewSrc.includes("span.start * 100") && !viewSrc.includes("span.width * 100"),
    "no raw model units are multiplied into percents at the call site");
  ok(viewSrc.includes('mode={durationOn ? "duration" : "sequence"}'),
    "timeline mode is sequence by default, duration under the toggle (DSH 同款)");
  ok(/\.traj-timeline__span\s*\{[^}]*min-width:\s*2px/.test(cssSrc),
    "span minimum visible width is enforced by CSS (2px floor)");
  ok(viewSrc.includes("max(2px, calc("), "span width keeps a CSS max() floor at the inline style");
  ok(/\.traj-timeline__focus\s*\{[^}]*min-width:\s*2px/.test(cssSrc),
    "focus range keeps a CSS minimum visible width");
}

console.log(`\n${passed} passed, ${failed} failed`);
if (!failed) process.stdout.write("task 1131 trajectory span width contract: OK\n");
