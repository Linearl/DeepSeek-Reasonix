// 任务675: turn 头部丢段（一键分析会话正文空白只剩工具条）的回归钉。
//
// 形态：后端直接对同 tab 换会话并立刻提交 turn（StartCrashAnalysis →
// NewSessionForTab + SubmitToTab），前端投影器在该 tab 上拿到新鲜游标的时刻
// 恰逢 turn 已在跑。旧逻辑把游标播到 `latest`（active 误判或 turnReplayAfterSeq
// 被 omitempty 省略），头部事件（turn_started、前几个采样段的正文结算）被
// 永久跳过，且 latest == projected 让回填条件自封——渲染停在与实际状态分裂
// 的形状：running=false（折叠收起、恢复面板拉取、文案「已工作」）+ 正文空白，
// 只有工具条。修复 = 播种信任 durable floor（replayAfter），floor 未知时才
// 退回 latest；surface reset 同步 release 游标。
//
// 本套件在 projector + reducer 真实管线上钉三件事：
//   1. floor 已知 → 从 floor 回放头部（active 标志不再能压过 floor）；
//   2. floor 缺席（旧后端/无 turn）→ 维持 latest 播种（有意的成本边界）；
//   3. 头部回放进入 reducer 后，事故形状恢复完整（running=true、正文恰一次、
//      工具条恰一次），且 live 尾部照常续接。
import assert from "node:assert/strict";
import type { AppBindings } from "../lib/bridge";
import type { TurnEventEnvelope, TurnEventReplayView, WireEvent } from "../lib/types";
import type { Item } from "../lib/useController";

type Ledger = { seq: number; event: Omit<WireEvent, "seq"> };

// 合同事件序：镜像 20261009 一键分析 turn 的前 45 秒（截断到 run_skill 之前
// 的两个采样段 + 三个工具），seq 从 1 起。
function buildHeadLedger(): Ledger[] {
  const out: Ledger[] = [];
  let seq = 0;
  const ev = (event: Omit<WireEvent, "seq">) => { seq += 1; out.push({ seq, event }); };
  const delta = (kind: "text" | "reasoning", text: string) => ev({ kind, [kind]: text } as Omit<WireEvent, "seq">);
  ev({ kind: "turn_started", turnId: "turn-ana", status: "in_progress" });
  delta("reasoning", "先加载技能。");
  delta("text", "我先调用 gh-issue-submit 技能获取提交规范，同时定位本地 fork 源码。");
  ev({ kind: "message", turnId: "turn-ana", text: "我先调用 gh-issue-submit 技能获取提交规范，同时定位本地 fork 源码。", reasoning: "先加载技能。" });
  ev({ kind: "tool_dispatch", tool: { id: "t1", name: "skill_search", args: "{\"query\":\"gh-issue-submit\"}", readOnly: true } });
  ev({ kind: "tool_result", tool: { id: "t1", name: "skill_search", output: "{\"results\":[]}", readOnly: true, durationMs: 500 } });
  ev({ kind: "tool_dispatch", tool: { id: "t2", name: "ls", args: "fork", readOnly: true } });
  ev({ kind: "tool_result", tool: { id: "t2", name: "ls", output: "CHANGELOG.md", readOnly: true, durationMs: 100 } });
  delta("reasoning", "技能已找到。调用 skill:gh-issue-submit。");
  ev({ kind: "message", turnId: "turn-ana", text: "", reasoning: "技能已找到。调用 skill:gh-issue-submit。" });
  ev({ kind: "tool_dispatch", tool: { id: "t3", name: "run_skill", args: "{\"name\":\"gh-issue-submit\"}", readOnly: false } });
  ev({ kind: "tool_result", tool: { id: "t3", name: "run_skill", output: "<skill-pin>", readOnly: false, durationMs: 500 } });
  delta("text", "技能已加载。现在并行做两件事：源码调研定位根因 + GitHub 模板发现/查重。");
  ev({ kind: "message", turnId: "turn-ana", text: "技能已加载。现在并行做两件事：源码调研定位根因 + GitHub 模板发现/查重。", reasoning: "" });
  return out;
}

const envelopeOf = (line: Ledger): TurnEventEnvelope => ({
  turnId: "turn-ana",
  seq: line.seq,
  status: "in_progress",
  runtimeEpoch: "epoch-ana",
  event: { ...line.event, seq: line.seq } as WireEvent,
});

const flush = async (rounds = 40) => { for (let i = 0; i < rounds; i += 1) await Promise.resolve(); };

const binding: Partial<AppBindings> = {};
Object.defineProperty(globalThis, "window", {
  configurable: true,
  value: { go: { main: { App: binding as AppBindings } } } as Window,
});

const [{ TurnEventProjector }, { initialState, reducer }, { buildTurnModels }] = await Promise.all([
  import("../lib/turnEventProjection"),
  import("../lib/useController"),
  import("../lib/transcriptRows"),
]);

// 把投影器输出灌进真实 reducer（text/reasoning 走 stream_batch，镜像生产 rAF 批）。
function wireProjectorToReducer(projector: InstanceType<typeof TurnEventProjector>) {
  const state = { current: reducer(initialState, { type: "history", messages: [] }) };
  projector.bind((e: WireEvent) => {
    if (e.kind === "text" || e.kind === "reasoning") {
      state.current = reducer(state.current, { type: "stream_batch", segments: [{ kind: e.kind, delta: (e.text ?? e.reasoning ?? ""), seq: e.seq }] });
    } else {
      state.current = reducer(state.current, { type: "event", e });
    }
  });
  return state;
}

// ---- 1. floor 已知：active=false 也不能压过 floor，头部从 floor 回放 ----
{
  const head = buildHeadLedger();
  const fetches: number[] = [];
  binding.TurnEventsForTab = async (_tabId: string, afterSeq: number) => {
    fetches.push(afterSeq);
    const events = head.filter((l) => l.seq > afterSeq).map(envelopeOf);
    const view: TurnEventReplayView = {
      events, floorSeq: 0, latestSeq: head.length, nextAfterSeq: head.length,
      hasMore: false, resetRequired: false, runtimeEpoch: "epoch-ana",
    };
    return view;
  };
  const projector = new TurnEventProjector();
  const state = wireProjectorToReducer(projector);
  // 一键分析形状：turn 已在跑（latest>0），active 误判 false，replayAfter=0
  // （首 turn 的 turnStartSeq-1=0 被 Go omitempty 省略，调用点已还原为 0）。
  projector.observeRuntime("tab-ana", "epoch-ana", head.length, 0, false);
  await flush();
  assert.deepEqual(fetches, [0], "播种必须落在 durable floor（0）并触发头部回放，而不是跳到 latest");
  const s = state.current;
  assert.equal(s.running, true, "回放把 turn_started 灌回 reducer——running 不再卡 false");
  assert.equal(s.turnActive, true, "turnActive 同步恢复");
  const tools = s.items.filter((it: Item) => it.kind === "tool");
  assert.equal(tools.length, 3, "三个工具条恰一次");
  const answers = s.items.filter((it: Item) => it.kind === "assistant" && it.text.trim() !== "");
  assert.equal(answers.length, 2, "两个正文段恰一次（无幽灵段、无丢段）");
  const models = buildTurnModels(s.items, { hasAnswerText: false, hasReasoning: false });
  const answerText = models.map((m) => m.turnItems).flat().filter((it) => it.kind === "assistant").map((it) => (it as Extract<Item, { kind: "assistant" }>).text).join("");
  assert.ok(answerText.includes("我先调用 gh-issue-submit"), "头部正文在事故形状里本来会丢，修复后必须渲染");
  // live 尾部照常续接
  assert.equal(projector.acceptLive("tab-ana", { kind: "turn_status", seq: head.length + 1, runtimeEpoch: "epoch-ana" } as WireEvent, "epoch-ana"), true, "回放结束后 live 尾部照常放行");
}

// ---- 2. floor 缺席：无 turn 元数据时维持 latest 播种（成本边界，不回放） ----
{
  const fetches: number[] = [];
  binding.TurnEventsForTab = async (_tabId: string, afterSeq: number) => { fetches.push(afterSeq); return { events: [], floorSeq: 0, latestSeq: 0, nextAfterSeq: 0, hasMore: false, resetRequired: false }; };
  const projector = new TurnEventProjector();
  projector.observeRuntime("tab-idle", "epoch-idle", 6455, undefined, false);
  await flush();
  assert.deepEqual(fetches, [], "floor 未知的旧元数据维持 latest 播种（不触发回放）");
  assert.equal(
    projector.acceptLive("tab-idle", { kind: "turn_started", seq: 1, runtimeEpoch: "epoch-idle" } as WireEvent, "epoch-idle"),
    false,
    "latest 播种下游标语义不变：旧 seq 照旧拒绝",
  );
}

// ---- 2b. 已终结 turn（floor == latest）零回放成本 ----
{
  const fetches: number[] = [];
  binding.TurnEventsForTab = async (_tabId: string, afterSeq: number) => { fetches.push(afterSeq); return { events: [], floorSeq: 0, latestSeq: 0, nextAfterSeq: 0, hasMore: false, resetRequired: false }; };
  const projector = new TurnEventProjector();
  projector.observeRuntime("tab-done", "epoch-done", 6455, 6455, false);
  await flush();
  assert.deepEqual(fetches, [], "已压实/已终结 turn 的 floor==latest，不产生回放流量");
}

// ---- 3. surface reset 后游标 release：下一次 observeRuntime 从 floor 重建 ----
{
  const head = buildHeadLedger();
  const fetches: number[] = [];
  binding.TurnEventsForTab = async (_tabId: string, afterSeq: number) => {
    fetches.push(afterSeq);
    return { events: head.filter((l) => l.seq > afterSeq).map(envelopeOf), floorSeq: 0, latestSeq: head.length, nextAfterSeq: head.length, hasMore: false, resetRequired: false, runtimeEpoch: "epoch-r" };
  };
  const projector = new TurnEventProjector();
  projector.observeRuntime("tab-r", "epoch-r", head.length, head.length, false);
  await flush();
  assert.deepEqual(fetches, [], "floor==latest 时不回放");
  // surface reset（loadSessionDataForTab 的 reset 分支现在会 release 游标）
  projector.release("tab-r");
  // 重绑后 turn 仍在跑：latest 推进、floor=0（活跃首 turn）
  projector.observeRuntime("tab-r", "epoch-r", head.length, 0, false);
  await flush();
  assert.deepEqual(fetches, [0], "release 后的播种必须回到 floor 并全量回放 live turn");
}

// ---- 4. epoch 变更（runtime 装配完成换 epoch）同样回到 floor ----
{
  const head = buildHeadLedger();
  const fetches: number[] = [];
  binding.TurnEventsForTab = async (_tabId: string, afterSeq: number) => {
    fetches.push(afterSeq);
    return { events: head.filter((l) => l.seq > afterSeq).map(envelopeOf), floorSeq: 0, latestSeq: head.length, nextAfterSeq: head.length, hasMore: false, resetRequired: false, runtimeEpoch: "epoch-new" };
  };
  const projector = new TurnEventProjector();
  projector.observeRuntime("tab-e", "epoch-old", 3, 0, true);
  await flush();
  const firstFetches = fetches.length;
  projector.observeRuntime("tab-e", "epoch-new", head.length, 0, false);
  await flush();
  assert.ok(fetches.length > firstFetches, "epoch 变更清游标后按 floor 重播");
  assert.equal(fetches[fetches.length - 1] ?? -1, 0, "重播从 floor 0 起");
}

console.log("turn head replay after rebind tests passed");
