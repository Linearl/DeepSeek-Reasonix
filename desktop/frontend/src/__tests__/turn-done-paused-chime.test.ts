// Run: tsx src/__tests__/turn-done-paused-chime.test.ts
//
// 任务 907（上游 8859 / PR11085 重实施）：turn_done 的四类「停在等用户」
// 结局（final_readiness / recovery_paused / incomplete_read /
// completion_uncertain）必须响 attention chime——旧链 `if (!e.err)` 把它们
// 整体静音。本测试钉死 sound.ts 的判定矩阵 + 播放分派 + 活树/分解树的接线
// （上游同款测试 runtime-turn-done-chime.test.ts 针对 runtimeNotifications；
// fork 无该文件，故测 sound.ts 判定 + App.tsx / useRuntimeEventHandlers 源码合同）。

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import {
  PAUSED_TURN_OUTCOMES,
  playAttentionChime,
  playSuccessChime,
  resetSharedAudioContextForTests,
  setAttentionPreference,
  setSuccessPreference,
  turnDoneChimeChoice,
} from "../lib/sound";

let passed = 0;
let failed = 0;

function eq(actual: unknown, expected: unknown, label: string) {
  if (actual === expected) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}\n`);
    failed += 1;
  }
}

const stored = new Map<string, string>();
Object.defineProperty(globalThis, "localStorage", {
  configurable: true,
  value: {
    getItem(key: string) { return stored.get(key) ?? null; },
    setItem(key: string, value: string) { stored.set(key, value); },
    removeItem(key: string) { stored.delete(key); },
    clear() { stored.clear(); },
  },
});

// Count constructed AudioContexts: fork's chimes share one singleton, so the
// test resets it before each probe (test seam exported by sound.ts).
let contexts = 0;
class FakeAudioContext {
  destination = {};
  state = "running";
  constructor() { contexts += 1; }
  createOscillator() {
    return { type: "sine", frequency: { setValueAtTime() {} }, connect() {}, start() {}, stop() {} };
  }
  createGain() {
    return { gain: { setValueAtTime() {}, linearRampToValueAtTime() {}, exponentialRampToValueAtTime() {} }, connect() {} };
  }
  resume() { return Promise.resolve(); }
  suspend() { return Promise.resolve(); }
  close() {}
}
Object.defineProperty(globalThis, "AudioContext", { configurable: true, value: FakeAudioContext });

// ── 判定矩阵（上游 diff 语义逐条对照） ────────────────────────────────────────
eq(turnDoneChimeChoice("delivery checks are incomplete", "final_readiness"), "attention", "final readiness stop alerts the user");
eq(turnDoneChimeChoice("paused", "recovery_paused"), "attention", "recovery pause waits on the user");
eq(turnDoneChimeChoice("paused", "incomplete_read"), "attention", "incomplete read waits on the user");
eq(turnDoneChimeChoice("paused", "completion_uncertain"), "attention", "completion uncertain waits on the user");
eq(turnDoneChimeChoice(undefined, "completed"), "success", "clean finish keeps the success chime");
eq(turnDoneChimeChoice(undefined, undefined), "success", "bare turn_done keeps the success chime");
eq(turnDoneChimeChoice("provider failed"), "none", "an ordinary failure stays silent");
eq(turnDoneChimeChoice("provider failed", "completed"), "none", "a failed turn with a done outcome is still a failure");
eq(PAUSED_TURN_OUTCOMES.size, 4, "the paused outcome set covers exactly the four waiting outcomes");

// ── 播放分派：attention 音实际发声，且不误播 success ──────────────────────────
{
  setAttentionPreference("synth");
  setSuccessPreference("off");
  resetSharedAudioContextForTests();
  const before = contexts;
  if (turnDoneChimeChoice("delivery checks are incomplete", "final_readiness") === "attention") playAttentionChime();
  eq(contexts - before, 1, "a delivery-check stop rings the attention chime");

  resetSharedAudioContextForTests();
  const beforeClean = contexts;
  if (turnDoneChimeChoice(undefined, "completed") === "attention") playAttentionChime();
  eq(contexts - beforeClean, 0, "a clean finish does not ring the attention chime");

  setSuccessPreference("synth");
  setAttentionPreference("off");
  resetSharedAudioContextForTests();
  const beforeSuccess = contexts;
  if (turnDoneChimeChoice(undefined, "completed") === "success") playSuccessChime();
  eq(contexts - beforeSuccess, 1, "a clean finish keeps the success chime");

  resetSharedAudioContextForTests();
  const beforeSilent = contexts;
  if (turnDoneChimeChoice("delivery checks are incomplete", "final_readiness") === "attention") playAttentionChime();
  eq(contexts - beforeSilent, 0, "paused chime respects the attention preference (off = silent)");
}

// ── 接线合同：活树（App.tsx）与分解树（useRuntimeEventHandlers）同判定 ────────
{
  const here = dirname(fileURLToPath(import.meta.url));
  const appSource = readFileSync(resolve(here, "../App.tsx"), "utf8");
  const handlersSource = readFileSync(resolve(here, "../app-runtime/useRuntimeEventHandlers.ts"), "utf8");
  const soundSource = readFileSync(resolve(here, "../lib/sound.ts"), "utf8");
  eq(/if \(e\.kind === "turn_done"\) \{\s*\n\s*\/\/ 任务 907[\s\S]*?turnDoneChimeChoice\(e\.err, e\.outcome\)/.test(appSource), true,
    "App.tsx live tree routes turn_done through turnDoneChimeChoice");
  eq(!appSource.includes("if (!e.err) playSuccessChime()"), true,
    "App.tsx no longer has the silent-on-err success branch");
  eq(handlersSource.includes("turnDoneChimeChoice(event.err, event.outcome)"), true,
    "useRuntimeEventHandlers routes turn_done through turnDoneChimeChoice");
  eq(!handlersSource.includes("if (!event.err) playSuccessChime()"), true,
    "useRuntimeEventHandlers no longer has the silent-on-err success branch");
  eq(soundSource.includes('PAUSED_TURN_OUTCOMES = new Set(["final_readiness", "recovery_paused", "incomplete_read", "completion_uncertain"])'), true,
    "sound.ts declares the four paused outcomes");
}

if (failed > 0) {
  console.error(`\nturn-done paused chime: ${failed} failed, ${passed} passed.`);
  process.exit(1);
}
console.log(`\nturn-done paused chime: ${passed} passed.`);
