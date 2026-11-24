#!/usr/bin/env node
// Run: node bench/tl1791-lab-deeplink.mjs
//
// tl1791 真浏览器验收（任务书验收②③的 live 面——CDP 驱动等价场景，
// Playwright on Chromium 即 CDP 协议直连）：
//   ① 单跳：page.goto 带 #settings/lab/runtimeObservability 的 URL，零点击
//      落到目标卡，[data-testid=lab-toggle-prepairAutosplice] 直接可点可读；
//   ② 开关全链路：锚点读默认态 → 点 ON → --on 翻转 + 桥 fixture 落盘回读；
//   ③ 往返防脏态：Esc 关设置页 → hash 被清空（replaceState）→ 再 goto 同一
//      深链 → 再次落位（开→关→再开无脏态）；
//   ④ 组点击路径（无深链的自动化兜底）：点 [data-testid=lab-group-observability]
//      → 点 [data-testid=lab-entry-runtimeObservability] → pane 锚点出现；
//   ⑤ 全程零 pageerror。
//
// 桥：不注入 window.go——用应用内置浏览器 mock（makeMockApp + ?mock=bench
// fixtures，app-browser.mjs 同款完整 boot；注入 null-fallback 桩会击穿 App
// 装配层对桥返回形状的假设，dev 探针实测 App.tsx 渲染即崩）。代价：mock 的
// SetExperimental* 是 noop，开关落盘回读在真浏览器面不可断言——该链路由
// jsdom 验收套②覆盖（fixture 桩真落盘）；本 bench 主断言=导航现实（单跳/
// 往返/组点击/零 pageerror），点击面断言到「点击被接受 + 行仍可读」为止。

import assert from "node:assert/strict";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const port = Number(process.env.REASONIX_TL1791_PORT ?? 4691);

// vite dev server（免整包 build——bench 只吃源码面转换；首次加载含依赖
// 预优化，下方用长超时吸收）。
const { createServer } = await import("vite");
const server = await createServer({
  root,
  logLevel: "silent",
  server: { host: "127.0.0.1", port, strictPort: true },
});
await server.listen();
process.stdout.write(`vite dev server on http://127.0.0.1:${port}/\n`);

const { chromium } = await import("playwright");
// app-browser.mjs 同款：本机默认缓存里的浏览器版本可能落后于本 playwright
// 构建的期望——显式 executablePath 让运行不依赖重新下载（环境变量可覆盖）。
const executablePath = process.env.PLAYWRIGHT_EXECUTABLE_PATH
  ?? path.join(process.env.LOCALAPPDATA ?? "", "ms-playwright", "chromium-1228", "chrome-win64", "chrome.exe");
const browser = await chromium.launch({ headless: true, executablePath });
const page = await browser.newPage({ locale: "en-US", viewport: { width: 1600, height: 1000 } });
const pageErrors = [];
page.on("pageerror", (error) => pageErrors.push(error.message));

const settle = (frames = 8) => page.evaluate((count) => new Promise((resolve) => {
  const tick = () => (--count <= 0 ? resolve() : requestAnimationFrame(tick));
  requestAnimationFrame(tick);
}), frames);

let cases = 0;
function pass(label) { process.stdout.write(`  PASS ${label}\n`); cases += 1; }

try {
  // ① 单跳深链落位（零点击）。
  await page.goto(`http://127.0.0.1:${port}/?mock=bench&bench=1#settings/lab/runtimeObservability`, { waitUntil: "domcontentloaded", timeout: 120000 });
  const row = page.locator('[data-testid="lab-toggle-prepairAutosplice"]');
  await row.waitFor({ state: "visible", timeout: 90000 });
  pass("single hop: goto #settings/lab/runtimeObservability lands on the prepairAutosplice row with zero clicks");

  // 目标分组锚点同时在场（rail 组头/卡行/成员行三层锚点齐）。
  await page.locator('[data-testid="lab-group-observability"]').waitFor({ timeout: 15000 });
  await page.locator('[data-testid="lab-entry-runtimeObservability"]').waitFor({ timeout: 15000 });
  pass("anchor ladder: lab-group-observability + lab-entry-runtimeObservability + lab-toggle-prepairAutosplice all present");

  // ② 开关读状态 + 锚点点中 + 落盘回读。
  const stateOf = (el) => el.evaluate((row) => {
    const buttons = [...row.querySelectorAll("button.set-seg__btn")];
    return { off: buttons[0]?.classList.contains("set-seg__btn--on") ?? false, on: buttons[1]?.classList.contains("set-seg__btn--on") ?? false };
  });
  const before = await stateOf(row);
  assert.equal(before.on, false, "default state readable: prepairAutosplice off");
  assert.equal(before.off, true, "default state readable: off side carries --on");
  pass("state read via stable anchor: default off (--on on the off segment)");
  const buttons = row.locator("button.set-seg__btn");
  await buttons.nth(1).click();
  await settle(10);
  const after = await stateOf(page.locator('[data-testid="lab-toggle-prepairAutosplice"]'));
  assert.equal(pageErrors.length, 0, "click accepted without page errors");
  pass(`toggle click via stable anchor accepted (post-click state readable: on=${after.on}; persistence is jsdom-harness covered — dev mock setters are noops)`);

  // ③ 往返防脏态：双 Esc 关设置页（Task 133 双击退出契约，600ms 内）→
  // hash 清空 → 再 goto 同深链 → 再落位。
  await page.keyboard.press("Escape");
  await page.keyboard.press("Escape");
  await page.waitForFunction(() => document.querySelector("#management-back") === null, { timeout: 30000 });
  await settle(6);
  const hashAfterClose = await page.evaluate(() => window.location.hash);
  assert.equal(hashAfterClose, "", `hash cleared on settings close (got "${hashAfterClose}")`);
  pass("round trip: Escape closes the settings page and the settings hash is cleared (no dirty state)");
  await page.goto(`http://127.0.0.1:${port}/?mock=bench&bench=1#settings/lab/runtimeObservability`, { waitUntil: "domcontentloaded" });
  await row.waitFor({ state: "visible", timeout: 60000 });
  pass("round trip: re-goto the same deep link lands again (open → close → reopen clean)");

  // ④ 组点击路径（仍在③落位的实验室页上）：组锚点展开 → 卡锚点选中 →
  // pane 成员锚点出现。裸工作区没有 rail（齿轮按钮盲选属性正是本任务要
  // 消灭的导航成本，不引入 bench）——设置页从无到有由①③的深链往返覆盖。
  await page.locator('[data-testid="lab-group-efficiency"]').click();
  await page.locator('[data-testid="lab-entry-safetyCostControl"]').waitFor({ timeout: 15000 });
  await page.locator('[data-testid="lab-entry-safetyCostControl"]').click();
  await page.locator('[data-testid="lab-toggle-memoryIndexSlim"]').waitFor({ timeout: 30000 });
  pass("group-click path: lab-group anchor expands, lab-entry (card) anchor selects, pane member anchor renders");

  // ⑤ 全程零 pageerror。
  assert.equal(pageErrors.length, 0, `zero page errors (got: ${JSON.stringify(pageErrors)})`);
  pass("zero pageerror across the whole run");

  process.stdout.write(`\n${cases} passed\n`);
} finally {
  await browser.close().catch(() => {});
  await server.close().catch(() => {});
}
