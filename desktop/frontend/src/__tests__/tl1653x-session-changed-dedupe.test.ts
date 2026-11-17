// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1653x-session-changed-dedupe.test.ts
//
// tl1653x 形态①钉：resume 后同内容渲染两次（两轮 surface 批）。
//
// 根因（file:line）：resume 链的第一轮渲染来自启动恢复 hydrate
//（syncActiveTabFromBackend → loadSessionDataForTab reason=startup +
// replace-surface policy → history_replace）；第二轮来自 controller 重建链
//（resume 自动恢复/Diet 改写）发出的 session_changed+sessionReset
//（首轮 tl1653 F1 的信号）→ useController handleWireEvent 的
// loadSessionDataForTab(true, "session-changed") 无条件全量重灌——改写未真正
// 落到会话文件（同 revision+digest）时，同一内容被重灌一遍。
//
// 修法两层：
//   a. sessionChangedReloadNeeded 纯指纹门（hydrateHistoryApply.ts）：表面
//      指纹与 fresh meta 指纹同证 → 跳过二批重灌；未知/翻动 → 保守重载；
//   b. reducer 层幂等证明：两轮 history_page replace 到达，items 恒单份
//      （replace 真替换——叠加只可能来自 prepend/rebase 分支，由既有 726/316
//      钉守护）。

import assert from "node:assert/strict";

// useController 的模块副作用需要一个 window.go.main.App 形状的绑定面
//（turn-event-projection-reset.test.ts 判例）。
Object.defineProperty(globalThis, "window", {
  configurable: true,
  value: { go: { main: { App: {} } } } as Window,
});

const { sessionChangedReloadNeeded } = await import("../lib/hydrateHistoryApply");

const need = sessionChangedReloadNeeded;

// A1：同指纹（revision+digest 相等）→ 二批去重（不重灌）
assert.equal(
  need({ historyRevision: 41, historyDigest: "d-41" }, { sessionRevision: 41, sessionDigest: "d-41" }),
  false,
  "A1: an identical fingerprint skips the second pour",
);
// A1b：digest 任一侧缺失但 revision 同证 → 仍去重（revision 是权威序）
assert.equal(
  need({ historyRevision: 41, historyDigest: "" }, { sessionRevision: 41, sessionDigest: "" }),
  false,
  "A1b: equal revisions with no digests still dedupe",
);
// A2：revision 翻动（Diet/rewind 真改写）→ 必须重载
assert.equal(
  need({ historyRevision: 41, historyDigest: "d-41" }, { sessionRevision: 42, sessionDigest: "d-42" }),
  true,
  "A2: a moved revision reloads (rewrite semantics intact)",
);
// A3：同 revision 不同 digest（矛盾态）→ 保守重载
assert.equal(
  need({ historyRevision: 41, historyDigest: "d-41" }, { sessionRevision: 41, sessionDigest: "d-41b" }),
  true,
  "A3: same revision but a differing digest reloads conservatively",
);
// A4：fresh 指纹未知（meta 缺 revision）→ 不赌，重载
assert.equal(
  need({ historyRevision: 41, historyDigest: "d-41" }, { sessionRevision: undefined, sessionDigest: "d-41" }),
  true,
  "A4: an unknown fresh revision reloads",
);
// A4b：resident 无历史（表面积为空/未灌页）→ 重载
assert.equal(
  need({ historyRevision: undefined, historyDigest: "" }, { sessionRevision: 41, sessionDigest: "d-41" }),
  true,
  "A4b: a surface without a resident page reloads",
);
// A4c：两侧全空 → 重载（防御态）
assert.equal(need(undefined, undefined), true, "A4c: undefined fingerprints reload");
// A5：/new /clear 后新会话（revision 归零或未带）→ 重载
assert.equal(
  need({ historyRevision: 41, historyDigest: "d-41" }, { sessionRevision: 0, sessionDigest: "" }),
  true,
  "A5: a cleared/new session (zero revision) reloads",
);

// ── reducer 层：两轮 surface 批到达 → items 恒单份 ──────────────────────────
const { initialState, reducer } = await import("../lib/useController");

// 两轮 replace-surface 批（启动恢复 hydrate + session-changed 重载）同内容到达
// HistoryPage 载荷以 messages（HistoryMessage[]）为源，items 由 historyMessagesToItems
// 换算——id 断言走转换产物（r3/m4 形状）。
const mkMessages = () => [
  { role: "user", content: "question", turnId: "t3" },
  { role: "assistant", content: "answer", reasoning: "thought", turnId: "t3" },
  { role: "user", content: "follow-up", turnId: "t4" },
  { role: "assistant", content: "reply", reasoning: "more thought", turnId: "t4" },
];

let state = { ...initialState, meta: { sessionPath: "C:\\s\\session.jsonl" } };
state = reducer(state, { type: "hydrate_start", reason: "startup" });
state = reducer(state, { type: "reset" });
state = reducer(state, { type: "history_page", page: { messages: mkMessages(), startTurn: 3, endTurn: 4, totalTurns: 4, hasOlder: true, revision: 41, digest: "d-41" }, mode: "replace" });
const afterFirstBatch = state;
assert.equal(afterFirstBatch.items.length, 4, "first batch renders once");
assert.equal(afterFirstBatch.historyRevision, 41, "first batch stamps the fingerprint");

state = reducer(state, { type: "history_page", page: { messages: mkMessages(), startTurn: 3, endTurn: 4, totalTurns: 4, hasOlder: true, revision: 41, digest: "d-41" }, mode: "replace" });
assert.equal(state.items.length, 4, "second batch replaces, never stacks (replace is a true replacement)");
assert.equal(state.historyPrefixCount, 4, "prefix count stays single-batch");
assert.equal(state.historyMutation.kind, "replace", "mutation recorded as replace");

// session-changed 全量重灌等价的 history_replace 通道同样真替换
state = reducer(state, {
  type: "history_replace",
  items: afterFirstBatch.items.slice(),
  startTurn: 3,
  totalTurns: 4,
  hasOlder: true,
  revision: 41,
  digest: "d-41",
});
assert.equal(state.items.length, 4, "history_replace is equally idempotent over identical content");
assert.deepEqual(
  state.items.map((item) => item.id),
  afterFirstBatch.items.map((item) => item.id),
  "item identity and order survive the second batch",
);

// 真改写（revision 翻动）仍然换底
state = reducer(state, { type: "history_page", page: { messages: mkMessages(), startTurn: 3, endTurn: 4, totalTurns: 4, hasOlder: true, revision: 42, digest: "d-42" }, mode: "replace" });
assert.equal(state.historyRevision, 42, "a newer batch still replaces");

// 旧批不可回滚
const rolled = reducer(state, { type: "history_page", page: { messages: mkMessages(), startTurn: 3, endTurn: 4, totalTurns: 4, hasOlder: true, revision: 41, digest: "d-41" }, mode: "replace" });
assert.equal(rolled, state, "an older revision cannot roll the surface back");

console.log("tl1653x session-changed dedupe tests passed");
