// Run: tsx --tsconfig tsconfig.tsx-runner.json --import ./scripts/css-stub-register.mjs src/__tests__/crash-activity-lane.test.ts
//
// tl1417 (issue #82 gap 2): the hot-path activity lane behind the performance
// report. Covers: run-length coalescing (stream burst, alternating poll), the
// ring cap, name sanitization at snapshot, the dual privacy outlets (upload
// message text / local jank record), the bridge proxy and stream taps, the
// interaction taps, the trigger-source line, and the write-cost reading (the
// hot path must not gain a perceptible cost).

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import {
  buildJankRecord,
  buildPerformancePayload,
  formatPerformanceContext,
  type PerformanceSnapshot,
} from "../lib/crash";
import {
  activityCapacity,
  installBreadcrumbConsoleHook,
  noteActivity,
  resetActivityForTest,
  snapshotActivity,
  type Breadcrumb,
} from "../lib/breadcrumbs";
import { app, resetStreamActivityTapForTest, installStreamActivityTap } from "../lib/bridge";

// The proxy is exercised with ad-hoc binding names that AppBindings does not declare.
const anyApp = app as unknown as Record<string, (...args: unknown[]) => unknown>;

let passed = 0;
let failed = 0;

function eq(a: unknown, b: unknown, label: string) {
  if (JSON.stringify(a) === JSON.stringify(b)) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}: expected ${JSON.stringify(b)}, got ${JSON.stringify(a)}\n`);
    failed += 1;
  }
}
function ok(value: boolean, label: string) {
  eq(value, true, label);
}

function snapshotOf(reason: string): PerformanceSnapshot {
  return {
    reason,
    uptimeMs: 3734_400,
    visibility: "visible",
    focused: true,
    online: true,
    hardwareConcurrency: 16,
    longTasks: { count: 7, totalMs: 20_145, maxMs: 16_587, recent: [{ startMs: 3713_500, durationMs: 16_587 }] },
  };
}

console.log("\ncrash activity lane (tl1417 gap 2)");

// ── coalescing ──────────────────────────────────────────────────────────────
{
  resetActivityForTest();
  for (let i = 0; i < 400; i += 1) noteActivity("stream", "assistant_delta");
  let lane = snapshotActivity();
  eq(lane.length, 1, "a 400-delta stream burst is one entry");
  eq(lane[0].n, 400, "the burst is counted, not dropped");
  eq(lane[0].kind, "stream", "carries the kind");

  resetActivityForTest();
  for (let i = 0; i < 12; i += 1) {
    noteActivity("bridge", "GetHistoryPage");
    noteActivity("bridge.done", "GetHistoryPage");
  }
  lane = snapshotActivity();
  eq(lane.map((e) => e.kind), ["bridge", "bridge.done"], "alternating call/done poll stays two entries (depth-2 coalescing)");
  eq(lane.map((e) => e.n), [12, 12], "both counted");

  resetActivityForTest();
  noteActivity("bridge", "A");
  noteActivity("bridge", "B");
  noteActivity("bridge", "C");
  noteActivity("bridge", "A");
  lane = snapshotActivity();
  eq(lane.map((e) => e.name), ["A", "B", "C", "A"], "depth-2 only: A,B,C,A is not over-coalesced");

  resetActivityForTest();
  noteActivity("stream", "tool_delta");
  noteActivity("ui", "pointerdown");
  noteActivity("stream", "tool_delta");
  noteActivity("ui", "tool_delta");
  lane = snapshotActivity();
  eq(lane.map((e) => [e.kind, e.name, e.n].join(":")), ["stream:tool_delta:2", "ui:pointerdown:1", "ui:tool_delta:1"], "same name under a different kind is a new entry; same kind+name at depth 2 coalesces");
}

// ── ring cap ────────────────────────────────────────────────────────────────
{
  resetActivityForTest();
  for (let i = 0; i < 100; i += 1) noteActivity("bridge", `Method${i}`);
  const lane = snapshotActivity();
  eq(lane.length, activityCapacity(), "ring holds its cap (48)");
  eq(lane[0].name, "Method52", "the oldest 52 were dropped, not the newest");
  eq(lane[activityCapacity() - 1].name, "Method99", "the newest survives");
  const raw = lane.map((e) => ({ ...e }));
  for (let i = 0; i < 5; i += 1) noteActivity("stream", "assistant_delta");
  eq(raw[0].name, "Method52", "snapshot is a copy, later writes do not leak into it");
}

// ── sanitization at snapshot (privacy by construction) ──────────────────────
{
  resetActivityForTest();
  noteActivity("bridge", "ReadFile");
  noteActivity("bridge", "C:\\Users\\alice\\notes.txt");
  noteActivity("stream", "assistant delta kind with text");
  noteActivity("ui", "x".repeat(80));
  noteActivity("bridge", "ok-name_with.1-2:3");
  const lane = snapshotActivity();
  eq(lane.map((e) => e.name), ["ReadFile", "other", "other", "other", "ok-name_with.1-2:3"], "non-token names collapse to 'other' (paths, sentences, overlong)");
  ok(!JSON.stringify(lane).includes("alice"), "no path fragments in the snapshot");
}

// ── dual privacy outlets ────────────────────────────────────────────────────
{
  resetActivityForTest();
  noteActivity("bridge", "SetModel");
  noteActivity("stream", "history_slice");
  noteActivity("ui", "pointerdown");
  const snapshot: PerformanceSnapshot = {
    ...snapshotOf("long task 7 in 60s, max 16587ms, total 20145ms"),
    longTaskMaxFrames: { startMs: 3713_500, durationMs: 16_587, samples: 10, sampledFromMs: 0, sampledToMs: 310, frames: [] },
    recentActivity: snapshotActivity(),
  };
  // Outlet 1 — upload: the section must ride the payload MESSAGE (the Go upload
  // path scrubs Message; unknown structured fields are dropped by the Go struct,
  // so text is the only route), and it must carry no argument-shaped content.
  const payload = buildPerformancePayload(snapshot);
  ok(payload.message.includes("recent activity (names only, coalesced; @ uptime):"), "upload outlet: activity section present in the message");
  ok(/@ \d+\.\ds bridge SetModel\n/.test(payload.message), "upload outlet: entries render with uptime stamps");
  ok(/last activity before max long task @ 3713\.5s: @ \d+\.\ds ui pointerdown \(\d+\.\ds earlier\)/.test(payload.message), "upload outlet: trigger-source line present");
  ok(!payload.message.includes("args") && !payload.message.includes("payload"), "upload outlet: no argument/payload keys in the text");

  // Outlet 2 — local jank record: structured recentActivity rides the snapshot
  // and is serialized losslessly for scrubJankStrings to walk on the Go side.
  const crumbs: Breadcrumb[] = [{ t: 1, cat: "app", msg: "start" }];
  const record = buildJankRecord("performance.longtask", snapshot, crumbs, 0, "2026-11-06T00:00:00.000Z");
  const json = JSON.stringify(record);
  const parsed = JSON.parse(json) as { snapshot: PerformanceSnapshot };
  eq(parsed.snapshot.recentActivity?.length, 3, "local outlet: recentActivity serialized with the record");
  ok(!json.includes("args") && !json.includes("params") && !json.includes("payload"), "local outlet: no argument/payload keys anywhere in the record");
}

// ── trigger-source line ─────────────────────────────────────────────────────
{
  // The #82 shape: click + hydrate at 3700.8s, a quiet gap, then the event whose
  // handler IS the stall lands at the stall start (3713.5s).
  const snapshot: PerformanceSnapshot = {
    ...snapshotOf("long task 1 in 60s, max 16587ms"),
    recentActivity: [
      { t: 3700_800, last: 3700_800, kind: "ui", name: "pointerdown", n: 1 },
      { t: 3700_900, last: 3700_900, kind: "bridge.done", name: "GetHistoryPage", n: 1 },
      { t: 3713_490, last: 3713_490, kind: "stream", name: "session_changed", n: 1 },
    ],
  };
  const text = formatPerformanceContext(snapshot);
  ok(text.includes("recent activity (names only, coalesced; @ uptime):"), "activity section header");
  ok(text.includes("@ 3700.8s ui pointerdown"), "uptime stamps line up with the long-task timeline");
  ok(text.includes("@ 3700.9s bridge.done GetHistoryPage"), "bridge.done entries render");
  ok(text.includes("last activity before max long task @ 3713.5s: @ 3713.5s stream session_changed (0.0s earlier)"), "trigger-source line names the event 0.0s before the stall (its handler IS the stall)");
  const burst = formatPerformanceContext({ ...snapshot, recentActivity: [{ t: 3700_000, last: 3712_000, kind: "stream", name: "assistant_delta", n: 412 }] });
  ok(burst.includes("@ 3700.0s..3712.0s stream assistant_delta x412"), "coalesced runs render as a span with a count");
  const quiet = formatPerformanceContext({ ...snapshot, recentActivity: [{ t: 3650_000, last: 3650_000, kind: "bridge", name: "GetStatus", n: 1 }] });
  ok(quiet.includes("last activity before max long task @ 3713.5s: @ 3650.0s bridge GetStatus (63.5s earlier)"), "a quiet gap is stated with its length instead of being invisible");
  const none = formatPerformanceContext({ ...snapshot, recentActivity: [{ t: 3800_000, last: 3800_000, kind: "ui", name: "pointerdown", n: 1 }] });
  ok(none.includes("no recorded activity before max long task @ 3713.5s"), "activity only after the stall is stated as none-before");
  const plain = formatPerformanceContext(snapshotOf("long task 1 in 60s, max 16587ms"));
  ok(!plain.includes("recent activity"), "no lane → no section (old reports render unchanged)");
}

// ── bridge proxy tap ────────────────────────────────────────────────────────
{
  resetActivityForTest();
  const g = globalThis as any;
  const prevGo = g.window?.go;
  g.window = { ...(g.window ?? {}), go: { main: { App: { GetStatus: () => Promise.resolve({ ok: true }), FailingCall: () => Promise.reject(new Error("boom")), SyncCall: () => 42 } } } };
  await anyApp.GetStatus();
  await (anyApp.GetStatus() as Promise<unknown>).catch(() => {});
  let lane = snapshotActivity();
  eq(lane.map((e) => [e.kind, e.name, e.n].join(":")), ["bridge:GetStatus:2", "bridge.done:GetStatus:2"], "proxy notes call and done (name only); a repeated call coalesces at depth 2");
  resetActivityForTest();
  await (anyApp.FailingCall() as Promise<unknown>).catch(() => {});
  lane = snapshotActivity();
  eq(lane.map((e) => e.kind), ["bridge", "bridge.error"], "rejections note bridge.error");
  resetActivityForTest();
  anyApp.SyncCall();
  lane = snapshotActivity();
  eq(lane.map((e) => e.kind), ["bridge", "bridge.done"], "sync results note done too");
  ok(!JSON.stringify(lane).includes("boom"), "the error message never enters the lane");
  g.window.go = prevGo;
}

// ── stream tap ──────────────────────────────────────────────────────────────
{
  resetActivityForTest();
  resetStreamActivityTapForTest();
  const g = globalThis as any;
  const prevWindow = g.window;
  const subscriptions: { name: string; cb: (...args: unknown[]) => void }[] = [];
  g.window = {
    runtime: {
      EventsOn: (name: string, cb: (...args: unknown[]) => void) => {
        subscriptions.push({ name, cb });
        return () => {};
      },
    },
  };
  installStreamActivityTap();
  installStreamActivityTap();
  eq(subscriptions.length, 21, "tap installs once: agent:event + 20 channel names");
  eq(subscriptions.filter((s) => s.name === "agent:event").length, 1, "exactly one agent:event subscription");
  subscriptions.find((s) => s.name === "agent:event")!.cb({ kind: "assistant_delta", text: "SECRET BODY", detail: "C:\\Users\\alice" });
  subscriptions.find((s) => s.name === "tab:meta")!.cb({ label: "SECRET LABEL" });
  let lane = snapshotActivity();
  eq(lane.map((e) => [e.kind, e.name].join(":")), ["stream:assistant_delta", "stream:tab:meta"], "stream entries carry kind/channel name only");
  ok(!JSON.stringify(lane).includes("SECRET") && !JSON.stringify(lane).includes("alice"), "payload contents never enter the lane");
  // Payload without a kind falls back to a constant.
  subscriptions.find((s) => s.name === "agent:event")!.cb(undefined);
  lane = snapshotActivity();
  eq(lane[2].name, "event", "missing kind falls back to 'event'");
  g.window = prevWindow;
  resetActivityForTest();
  installStreamActivityTap();
  eq(snapshotActivity().length, 0, "no runtime → no tap, lane stays empty");
  resetStreamActivityTapForTest();
}

// ── interaction taps ────────────────────────────────────────────────────────
{
  resetActivityForTest();
  const g = globalThis as any;
  const prevWindow = g.window;
  const prevDocument = g.document;
  const listeners: { target: string; type: string; cb: () => void; options?: unknown }[] = [];
  const addEventListener = (type: string, cb: () => void, options?: unknown) => listeners.push({ target: "document", type, cb, options });
  g.window = { addEventListener: () => {}, };
  g.document = { addEventListener, visibilityState: "visible" };
  installBreadcrumbConsoleHook();
  const taps = listeners.filter((l) => ["pointerdown", "keydown", "wheel"].includes(l.type));
  eq(taps.length, 3, "three interaction taps installed");
  ok(taps.every((t) => (t.options as { capture?: boolean; passive?: boolean })?.capture === true && (t.options as { passive?: boolean })?.passive === true), "taps are capture+passive");
  taps.find((t) => t.type === "pointerdown")!.cb();
  taps.find((t) => t.type === "pointerdown")!.cb();
  taps.find((t) => t.type === "keydown")!.cb();
  eq(snapshotActivity().map((e) => [e.kind, e.name, e.n].join(":")), ["ui:pointerdown:2", "ui:keydown:1"], "interactions coalesce by type");
  g.window = prevWindow;
  g.document = prevDocument;
}

// ── write-cost reading (E1: measure first) ──────────────────────────────────
{
  // Hot path: coalesced stream bursts (the common case) and distinct pushes
  // (worst case, ring full → shift). One warm-up round, then 2M measured calls.
  resetActivityForTest();
  const N = 2_000_000;
  for (let i = 0; i < 10_000; i += 1) noteActivity("stream", "assistant_delta");
  resetActivityForTest();
  const t0 = performance.now();
  for (let i = 0; i < N; i += 1) noteActivity("stream", "assistant_delta");
  const coalescedNsPerOp = ((performance.now() - t0) / N) * 1e6;
  resetActivityForTest();
  const t1 = performance.now();
  for (let i = 0; i < N; i += 1) noteActivity("bridge", `M${i}`);
  const worstNsPerOp = ((performance.now() - t1) / N) * 1e6;
  process.stdout.write(`  READ  noteActivity coalesced ${coalescedNsPerOp.toFixed(0)}ns/op, worst-case push ${worstNsPerOp.toFixed(0)}ns/op\n`);
  // Generous sanity bounds only — these catch pathological regressions (regex,
  // JSON, allocation per call), not noise. For scale: one 60fps frame is 16.7ms.
  ok(coalescedNsPerOp < 2_000, `coalesced write stays trivially cheap (${coalescedNsPerOp.toFixed(0)}ns/op < 2us)`);
  ok(worstNsPerOp < 2_000, `worst-case write stays trivially cheap (${worstNsPerOp.toFixed(0)}ns/op < 2us)`);
  // Streaming at the observed worst (~50 events/s) costs:
  const perSecondUs = (coalescedNsPerOp * 50) / 1000;
  process.stdout.write(`  READ  stream at 50 ev/s = ${perSecondUs.toFixed(2)}us/s of main-thread time\n`);
}

// ── source pins ─────────────────────────────────────────────────────────────
{
  const testDir = dirname(fileURLToPath(import.meta.url));
  const crashSrc = readFileSync(resolve(testDir, "../lib/crash.ts"), "utf8");
  const bridgeSrc = readFileSync(resolve(testDir, "../lib/bridge.ts"), "utf8");
  const crumbSrc = readFileSync(resolve(testDir, "../lib/breadcrumbs.ts"), "utf8");
  ok(crashSrc.includes("recentActivity: recentActivity.length > 0 ? recentActivity : undefined,"), "snapshot carries the lane (omitted when empty)");
  ok(crashSrc.includes("installStreamActivityTap();"), "the monitor installs the stream tap");
  ok(bridgeSrc.includes('noteActivity("bridge", method);'), "bridge proxy notes every call");
  ok(bridgeSrc.includes('noteActivity("bridge.done", method);'), "bridge proxy notes resolutions");
  ok(bridgeSrc.includes('noteActivity("bridge.error", method);'), "bridge proxy notes rejections");
  ok(bridgeSrc.includes("const ACTIVITY_TAP_CHANNELS"), "stream taps are a fixed channel list");
  ok(crumbSrc.includes('noteActivity("ui", type)'), "interaction taps note the event type only");
  ok(crumbSrc.includes("const ACTIVITY_NAME_RE = /^[A-Za-z0-9._:-]{1,64}$/;"), "names are allowlisted tokens at snapshot");
  ok(crumbSrc.includes("activity.length > ACTIVITY_MAX) activity.shift();"), "ring is bounded");
  ok(!crumbSrc.includes("JSON.stringify") || crumbSrc.split("JSON.stringify").length - 1 <= 1, "lane write path does no serialization");
}

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exitCode = 1;
