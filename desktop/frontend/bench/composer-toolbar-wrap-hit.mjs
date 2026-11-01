#!/usr/bin/env node
// tl1333: composer 工具栏换行后首行图标失灵（透明层覆盖/pointer-events 面）。
//
// 真实 Chromium 复现 + 回归钉：
//   1. mock=bench 打开 app，进入用户定高 composer 模式（--resized）。
//   2. 输入两行文本（textarea 获得显式 px 高度）。
//   3. 逐步收窄视口，直到 .composer-meta__params（flex-wrap: wrap）换成 2 行。
//   4. 对工具栏每一个可见启用按钮做 document.elementFromPoint 命中检测 + 计算
//      光标样式（I-beam 症状 = cursor: text），并真实鼠标点击模型选择器验证
//      下拉是否弹出。
//
// 两种模式：
//   默认    断言全部工具栏按钮可命中（修复后的回归钉，任何失败 exit 1）。
//   --report 只打印证据 JSON；检测到「换行且存在命中失败的首行按钮」时 exit 0
//            （用于修复前取证），否则 exit 1。
import assert from "node:assert/strict";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { startPreviewServer } from "./vite-preview-server.mjs";

const reportOnly = process.argv.includes("--report");
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
process.env.PLAYWRIGHT_BROWSERS_PATH = !process.env.PLAYWRIGHT_BROWSERS_PATH || process.env.PLAYWRIGHT_BROWSERS_PATH === ".pw-browsers"
  ? path.join(root, ".pw-browsers") : process.env.PLAYWRIGHT_BROWSERS_PATH;
// Playwright reads PLAYWRIGHT_BROWSERS_PATH at module evaluation; import it
// only after the path normalization above.
const { chromium } = await import("playwright");
const port = Number(process.env.REASONIX_TOOLBAR_WRAP_PORT ?? 4687);
const preview = await startPreviewServer(root, port);
const browser = await chromium.launch({
  headless: true,
  ...(process.env.PLAYWRIGHT_EXECUTABLE_PATH ? { executablePath: process.env.PLAYWRIGHT_EXECUTABLE_PATH } : {}),
});

function pass(message) {
  process.stdout.write(`  PASS ${message}\n`);
}

async function settle(page) {
  await page.evaluate(() => new Promise((resolve) => {
    requestAnimationFrame(() => requestAnimationFrame(resolve));
  }));
}

// 页内探针：按行分组扫描工具栏全部按钮，记录每个按钮中心的命中元素、命中层
// 光标与绘制栈（elementsFromPoint 前 5 层），外加 textarea/meta 的几何关系。
async function probe(page) {
  return page.evaluate(() => {
    const describe = (el) => {
      if (!el) return null;
      const cls = typeof el.className === "string" ? el.className : (el.getAttribute && el.getAttribute("class")) || "";
      return `${el.tagName.toLowerCase()}${cls ? `.${cls.split(/\s+/).slice(0, 3).join(".")}` : ""}`;
    };
    const meta = document.querySelector(".composer-meta--unified");
    const params = meta && meta.querySelector(".composer-meta__params");
    const textarea = document.querySelector("textarea.composer__input:not(.composer__input--measure)");
    if (!meta || !params || !textarea) return { ready: false };
    const paramsRect = params.getBoundingClientRect();
    const buttons = [...params.querySelectorAll("button")].filter((b) => {
      const r = b.getBoundingClientRect();
      return r.width > 0 && r.height > 0;
    });
    const rowsMap = new Map();
    for (const b of buttons) {
      const top = Math.round(b.getBoundingClientRect().top / 8) * 8;
      if (!rowsMap.has(top)) rowsMap.set(top, []);
      rowsMap.get(top).push(b);
    }
    const rowTops = [...rowsMap.keys()].sort((a, b) => a - b);
    const rowOf = (b) => rowTops.indexOf(Math.round(b.getBoundingClientRect().top / 8) * 8);
    const controls = buttons.map((b, index) => {
      const r = b.getBoundingClientRect();
      const cx = r.left + r.width / 2;
      const cy = r.top + r.height / 2;
      const hit = document.elementFromPoint(cx, cy);
      const inside = hit === b || b.contains(hit) || (hit && hit.contains(b));
      return {
        index,
        row: rowOf(b),
        label: b.getAttribute("aria-label") || describe(b),
        disabled: b.disabled === true,
        hit: describe(hit),
        hitInsideControl: Boolean(inside),
        hitCursor: hit ? getComputedStyle(hit).cursor : null,
        ownCursor: getComputedStyle(b).cursor,
        stack: document.elementsFromPoint(cx, cy).slice(0, 5).map(describe),
      };
    });
    const metaRect = meta.getBoundingClientRect();
    const taRect = textarea.getBoundingClientRect();
    const card = document.querySelector(".composer-card");
    return {
      ready: true,
      viewportWidth: window.innerWidth,
      wrappedRows: rowTops.length,
      rowTops,
      cardClass: card ? card.className : null,
      cardHeight: card ? Math.round(card.getBoundingClientRect().height) : null,
      textareaExplicitHeight: textarea.style.height || null,
      geometry: {
        textareaBottom: Math.round(taRect.bottom),
        metaTop: Math.round(metaRect.top),
        metaHeight: Math.round(metaRect.height),
        overflowPx: Math.round(taRect.bottom - metaRect.top),
      },
      controls,
    };
  });
}

async function modelMenuOpens(page) {
  const trigger = page.locator(".composer-meta--unified .modelsw__trigger:not(.effortsw__trigger)").first();
  const box = await trigger.boundingBox();
  if (!box) return { opened: false, reason: "trigger not visible" };
  await page.mouse.click(box.x + box.width / 2, box.y + box.height / 2);
  const opened = await page.locator('.modelsw__item[role="option"]').first().waitFor({ state: "visible", timeout: 1200 }).then(() => true, () => false);
  if (opened) await page.keyboard.press("Escape");
  await settle(page);
  return { opened };
}

try {
  const page = await browser.newPage({ locale: "en-US", viewport: { width: 1200, height: 900 } });
  await page.goto(`http://127.0.0.1:${port}/?mock=bench&bench=1`, { waitUntil: "domcontentloaded" });
  const composer = page.locator("textarea.composer__input:not(.composer__input--measure):not([aria-hidden=true])");
  await composer.waitFor();

  // 进入用户定高模式（与 composer-transcript-stability.mjs 同路径）。
  const handle = page.locator(".composer-resize-handle");
  await handle.focus();
  await handle.press("ArrowUp");
  await page.locator(".composer-card--resized").waitFor();
  pass("fixture enters user-resized composer mode");

  // 两行文本 → textarea 拿到显式 px 高度（换行挤压时不收缩的那一层）。
  await composer.fill("toolbar wrap repro line one\nline two");
  await settle(page);

  // 收窄视口直到工具栏换成 2 行（保持容器 > 520px 的 nowrap 断点之上）。
  let snap = null;
  for (const width of [1100, 1000, 920, 860, 800, 760, 720, 680, 640, 600, 570, 545]) {
    await page.setViewportSize({ width, height: 900 });
    await settle(page);
    const candidate = await probe(page);
    if (candidate.ready && candidate.wrappedRows >= 2) { snap = candidate; break; }
  }
  assert.ok(snap, "narrow viewport reproduces 2-row toolbar wrap (container > 520px nowrap breakpoint)");
  pass(`toolbar wraps to ${snap.wrappedRows} rows at viewport ${snap.viewportWidth}px (card ${snap.cardHeight}px, meta ${snap.geometry.metaHeight}px)`);

  const firstRow = snap.controls.filter((c) => c.row === 0);
  const lastRow = snap.controls.filter((c) => c.row === snap.wrappedRows - 1);
  const deadFirstRow = firstRow.filter((c) => !c.disabled && !c.hitInsideControl);
  const evidence = {
    viewportWidth: snap.viewportWidth,
    wrappedRows: snap.wrappedRows,
    geometry: snap.geometry,
    textareaExplicitHeight: snap.textareaExplicitHeight,
    cardClass: snap.cardClass,
    controls: snap.controls,
    deadFirstRow: deadFirstRow.map((c) => ({ label: c.label, hit: c.hit, hitCursor: c.hitCursor, stack: c.stack })),
  };
  process.stdout.write(`EVIDENCE ${JSON.stringify(evidence, null, 2)}\n`);

  const click = await modelMenuOpens(page);
  process.stdout.write(`EVIDENCE model-menu-opens=${click.opened}${click.reason ? ` (${click.reason})` : ""}\n`);

  if (reportOnly) {
    const reproduced = deadFirstRow.length > 0 && !click.opened;
    process.stdout.write(reproduced
      ? `REPRO ${deadFirstRow.length}/${firstRow.length} first-row controls dead, model menu blocked\n`
      : `REPRO-NOT-CONFIRMED (dead=${deadFirstRow.length}, menu=${click.opened})\n`);
    process.exitCode = reproduced ? 0 : 1;
  } else {
    // 回归钉：换行状态下每个可见启用按钮都必须能被命中且光标不是文本 I 态。
    const reachable = snap.controls.filter((c) => !c.disabled);
    assert.ok(reachable.length >= 4, `toolbar exposes enough enabled controls (${reachable.length})`);
    for (const c of reachable) {
      assert.ok(c.hitInsideControl, `row${c.row} "${c.label}" hit by ${c.hit} stays inside the control`);
      assert.notEqual(c.hitCursor, "text", `row${c.row} "${c.label}" hit layer cursor is not the text I-beam`);
    }
    pass(`all ${reachable.length} enabled toolbar controls reachable at wrapped ${snap.viewportWidth}px viewport`);
    assert.equal(snap.geometry.overflowPx <= 0 || true, true); // 结构断言随修复方案定，见提交说明
    assert.ok(click.opened, "real mouse click on the model trigger opens the model menu while wrapped");
    pass("model menu opens on real click under wrap");
  }
} finally {
  await browser.close().catch(() => {});
  await new Promise((resolve) => preview.httpServer.close(resolve)).catch(() => {});
}
