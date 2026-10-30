// 任务 739：设置数据请求「点击即发」通道（lib/settingsPrefetch）的行为契约。
// 覆盖：点击同步帧发出请求、TTL 内去重、消费即取走、TTL 过期重发、
// 失败弃缓存、未消费的失败不产生未处理 rejection。
// tl 1284 新增：startedAt 跟请求本体走——消费方拿到的 startedAt 必须等于
// 该请求的发起时刻；消费/作废后不留任何可被下次 reload 继承的旧起点。
import assert from "node:assert/strict";
import { JSDOM } from "jsdom";

const dom = new JSDOM('<!doctype html><div id="root"></div>', { url: "http://localhost", pretendToBeVisual: true });
Object.assign(globalThis, { window: dom.window, document: dom.window.document, HTMLElement: dom.window.HTMLElement, Node: dom.window.Node });
// bridge.ts 的 app 是调用时才解析 window.go 的 Proxy，无需更多全局装配。

let settingsCalls = 0;
let nextResult: () => Promise<unknown> = async () => ({ ok: true });
(window as unknown as { go: unknown }).go = {
  main: { App: { Settings: () => { settingsCalls += 1; return nextResult(); } } },
};

const { prefetchSettingsView, takeSettingsViewPrefetch, SETTINGS_PREFETCH_TTL_MS } = await import("../lib/settingsPrefetch");
const tick = () => new Promise<void>((resolve) => setImmediate(resolve));

// 1) 预取在调用帧立即发出请求
prefetchSettingsView();
assert.equal(settingsCalls, 1, "prefetch fires app.Settings in the same synchronous frame");

// 2) TTL 内重复预取去重（连续两次点击只发一次请求）
prefetchSettingsView();
assert.equal(settingsCalls, 1, "repeat prefetch within TTL reuses the in-flight request");

// 3) 消费即取走：拿到同一 promise，缓存清空；再次消费为 null。
//    tl 1284：startedAt 等于该请求发起时刻（消费方计时起点与请求对齐）。
const taken = takeSettingsViewPrefetch();
assert.ok(taken, "take returns the prefetched request");
assert.equal(typeof taken!.startedAt, "number", "consumed prefetch carries its request start time");
assert.ok(Math.abs(taken!.startedAt - Date.now()) < 5_000, "startedAt is the fire time, not a stale timestamp");
await assert.doesNotReject(async () => { await taken!.promise; });
assert.equal(takeSettingsViewPrefetch(), null, "second take finds no cached prefetch");

// 4) 消费后再预取 = 新请求（面板保存后 reload 不吃旧快照），且新请求携带
//    新的 startedAt（tl 1284：面板重开不得继承上一次会话的计时起点）。
const firstStartedAt = taken!.startedAt;
prefetchSettingsView();
assert.equal(settingsCalls, 2, "prefetch after consumption issues a fresh request");
const taken2 = takeSettingsViewPrefetch();
assert.ok(taken2, "fresh prefetch is consumable");
assert.ok(taken2!.startedAt >= firstStartedAt, "fresh prefetch carries its own (non-inherited) startedAt");
takeSettingsViewPrefetch();

// 5) TTL 过期后预取重发
const realNow = Date.now;
Date.now = () => realNow() + SETTINGS_PREFETCH_TTL_MS + 1;
try {
  prefetchSettingsView();
  assert.equal(settingsCalls, 3, "expired prefetch issues a fresh request");
} finally {
  Date.now = realNow;
}
takeSettingsViewPrefetch();

// 6) 失败即弃：拒绝后缓存清空，下一次预取重发
nextResult = () => Promise.reject(new Error("backend busy"));
prefetchSettingsView();
assert.equal(settingsCalls, 4);
await tick();
await tick();
prefetchSettingsView();
assert.equal(settingsCalls, 5, "failed prefetch is discarded, next click re-fires");
await tick();
await tick();
takeSettingsViewPrefetch();

// 7) 未被消费的失败不产生未处理 rejection（预取后面板可能永远不来消费）
let unhandled: unknown[] = [];
const onUnhandled = (reason: unknown) => { unhandled.push(reason); };
process.on("unhandledRejection", onUnhandled);
nextResult = () => Promise.reject(new Error("nobody consumes this"));
prefetchSettingsView();
assert.equal(settingsCalls, 6);
await tick();
await tick();
await tick();
assert.deepEqual(unhandled, [], "unconsumed rejected prefetch must not surface as unhandledRejection");
process.off("unhandledRejection", onUnhandled);

console.log("PASS settings prefetch: sync-frame fire, TTL dedupe, take-once with request-bound startedAt, expiry, failure discard, no unhandled rejection");
