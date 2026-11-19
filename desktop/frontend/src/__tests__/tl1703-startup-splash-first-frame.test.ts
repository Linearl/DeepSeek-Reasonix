// Run: npx tsx src/__tests__/tl1703-startup-splash-first-frame.test.ts
//
// tl1703 acceptance harness（启动遮罩首帧漏出修复）:
// 用户 OBS 抽帧实证（5s 完整主界面 → 7s 遮罩才盖上）：开关已开时 boot
// snapshot（applyLabFlags）异步推入前首帧读默认值（off）→ 主界面裸奔整个
// snapshot 往返。修复契约：
//  ① 首帧同步路径：labFlags 模块初始化时从 localStorage 镜像种子
//     startupSplash —— 镜像=1（上次会话开关已开）⇒ 首帧 flag 即 true，
//     不等 applyLabFlags；
//  ② 种子只覆盖 startupSplash——其余 flag 保持 default-until-snapshot；
//  ③ 推入纠偏：applyLabFlags 落地后仍是唯一权威，覆盖种子并把解析值回写
//     镜像（"1"/"0"）；
//  ④ 无镜像/存储被拒 ⇒ 默认关——flag-off 字节级回归承诺不受影响；
//  ⑤ 场景①-③ 与场景④需要两次独立模块求值：①③用首次动态导入（求值点
//     在镜像写入之后，等价真实应用「bundle 求值 → React 首帧」时序），
//     ④用 cache-busting query 强制重新求值。禁止静态 import labFlags
//     （静态导入会在镜像写入前求值，测不到种子时序）。

import { JSDOM } from "jsdom";

let passed = 0;
let failed = 0;

function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

const MIRROR_KEY = "reasonix.labflag.startupSplash";

const dom = new JSDOM("<!doctype html><html><body></body></html>", { url: "http://localhost/" });
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.localStorage = dom.window.localStorage;

// ── 场景 ①②③：镜像=1 的种子 + 推入纠偏 ──────────────────────
{
  window.localStorage.setItem(MIRROR_KEY, "1");
  // 首次动态导入 = labFlags 模块求值点；种子读取发生在此刻（applyLabFlags 之前）。
  const lf = await import("../lib/labFlags");
  ok(lf.labFlagEnabled("startupSplash") === true,
    "mirror=1 seeds startupSplash true at module init, before any applyLabFlags (first-frame sync path)");
  ok(lf.labFlagEnabled("workflowPanel") === false,
    "the seed covers only startupSplash (other flags keep default-until-snapshot)");

  lf.applyLabFlags({ startupSplash: false });
  ok(lf.labFlagEnabled("startupSplash") === false,
    "applyLabFlags stays the authority after the snapshot lands (correction over seed)");
  ok(window.localStorage.getItem(MIRROR_KEY) === "0",
    "applyLabFlags mirrors the resolved value back to localStorage");

  lf.applyLabFlags({ startupSplash: true });
  ok(window.localStorage.getItem(MIRROR_KEY) === "1",
    "applyLabFlags(true) writes the mirror the next boot's first frame seeds from");
}

// ── 场景 ④：无镜像 ⇒ 默认关（flag-off 字节级不变）──────────────
{
  window.localStorage.removeItem(MIRROR_KEY);
  const fresh = await import("../lib/labFlags?tl1703-fresh-no-mirror");
  ok(fresh.labFlagEnabled("startupSplash") === false,
    "no mirror → the first frame resolves to the default (off), flag-off path byte-identical");
}

process.stdout.write(`\n${passed} checks passed${failed ? `, ${failed} failed` : ""}\n`);
if (failed) process.exitCode = 1;
else process.stdout.write("tl 1703 startup splash first-frame: OK\n");
