// Run: tsx src/__tests__/tool-grouping-preference.test.ts
//
// 任务 668 — 消息流工具分组开关的 store 契约：惰性水合、持久化、变更通知顺序
// （beforeChange 在值翻转前、订阅者在翻转后）、重复写入短路、坏存储兜底。

import { JSDOM } from "jsdom";
import {
  applyToolGroupingEnabled,
  getToolGroupingEnabled,
  onToolGroupingWillChange,
} from "../lib/toolGroupingPreference";

const dom = new JSDOM("<!doctype html><html><body></body></html>", {
  url: "http://localhost/",
});
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.localStorage = dom.window.localStorage;
globalThis.CustomEvent = dom.window.CustomEvent;

let passed = 0;
let failed = 0;
function ok(cond: unknown, label: string) {
  if (cond) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

console.log("\ntool grouping preference");

// 首读前预置的持久值必须生效（首帧即用户上次的选择，无闪烁）。
// 「off」是最有风险的持久方向（必须跨重启存活），在此覆盖；空存储/垃圾值都走
// `!== "off"` 归开分支，空存储默认开由 session-experience-settings 测试覆盖
// （store 模块单次水合，同进程只能观察第一条水合路径）。
localStorage.clear();
localStorage.setItem("reasonix-tool-grouping", "off");
ok(getToolGroupingEnabled() === false, "pre-seeded off storage hydrates on first read");

// 模块已水合，回到默认开再验证变更链路。
applyToolGroupingEnabled(true);

let beforeSeen: Array<[boolean, boolean]> = [];
let notified = 0;
let eventDetail: unknown = null;
const unsubscribeBefore = onToolGroupingWillChange((previous, next) => {
  beforeSeen.push([previous, next]);
  // beforeChange 必须发生在值翻转之前（Transcript 借此先开结构性事务）。
  ok(getToolGroupingEnabled() === previous, "beforeChange fires while the previous value is still current");
});
const windowListener = (event: Event) => {
  notified += 1;
  eventDetail = (event as CustomEvent).detail;
};
window.addEventListener("reasonix:tool-grouping", windowListener);
const unsubscribe = () => window.removeEventListener("reasonix:tool-grouping", windowListener);

applyToolGroupingEnabled(false);
ok(getToolGroupingEnabled() === false, "apply(false) flips the value");
ok(localStorage.getItem("reasonix-tool-grouping") === "off", "apply(false) persists off");
ok(notified === 1, "change emits exactly one signal");
ok(eventDetail === false, "change event carries the new value");

const notifiedBefore = notified;
applyToolGroupingEnabled(false);
ok(notified === notifiedBefore, "re-applying the same value short-circuits (no emit)");

beforeSeen = [];
applyToolGroupingEnabled(true);
ok(beforeSeen.length === 1 && beforeSeen[0][0] === false && beforeSeen[0][1] === true, "beforeChange receives (previous=false, next=true)");
ok(getToolGroupingEnabled() === true, "apply(true) flips back");
ok(localStorage.getItem("reasonix-tool-grouping") === "on", "apply(true) persists on");

unsubscribe();
unsubscribeBefore();

// 坏存储兜底：setItem 抛错时内存值仍然生效、仍发出变更（本运行内可用）。
const throwingStorage = {
  getItem: (key: string) => (key === "reasonix-tool-grouping" ? "on" : null),
  setItem: () => { throw new Error("quota exceeded"); },
  removeItem: () => {},
  clear: () => {},
  key: () => null,
  length: 0,
};
globalThis.localStorage = throwingStorage as unknown as Storage;
applyToolGroupingEnabled(false);
ok(getToolGroupingEnabled() === false, "storage write failure still applies the in-memory value");

if (failed > 0) {
  throw new Error(`${failed} tool grouping preference checks failed`);
}
console.log(`  ${passed} checks passed`);
