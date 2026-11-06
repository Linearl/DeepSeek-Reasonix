#!/usr/bin/env node
// tl1468 real-Chromium layout bench for the side-files dock tabs (产物/参考).
// jsdom cannot lay out, so the scrollbar fix is verified here: the fixture
// mounts SideFilesDockPanel inside a fixed-size .workbench-dock__body replica
// (bench/side-files-dock-layout.tsx) and this runner drives it through the
// three states and asserts on scrollHeight vs clientHeight:
//   few rows        -> no overflow, no inner list scroll, no "show more" foot;
//   exactly full    -> content fills the body without overflowing;
//   overflowing     -> the dock panel is the single scroll container and
//                      actually scrolls; the inner list never scrolls on its
//                      own (no resurrected cap).
// Screenshots land in $REASONIX_DOCK_LAYOUT_ARTIFACTS (one per state).
//
// Usage:
//   PLAYWRIGHT_EXECUTABLE_PATH=<chromium.exe> node bench/side-files-dock-layout.mjs
//   (falls back to PLAYWRIGHT_BROWSERS_PATH=.pw-browsers / default resolution)

import { mkdirSync, writeFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { build, loadConfigFromFile, preview } from "vite";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
process.env.PLAYWRIGHT_BROWSERS_PATH = !process.env.PLAYWRIGHT_BROWSERS_PATH || process.env.PLAYWRIGHT_BROWSERS_PATH === ".pw-browsers"
  ? path.join(root, ".pw-browsers") : process.env.PLAYWRIGHT_BROWSERS_PATH;
const { chromium } = await import("playwright");

const artifactsDir = process.env.REASONIX_DOCK_LAYOUT_ARTIFACTS ?? "";
if (artifactsDir) mkdirSync(artifactsDir, { recursive: true });

const samples = [];
let server;
let browser;
try {
  const loaded = await loadConfigFromFile({ command: "build", mode: "production" }, path.join(root, "vite.config.ts"));
  const config = loaded.config;
  const output = path.join(root, ".bench-side-files-dock-out");
  await build({ ...config, configFile: false, root, logLevel: "error",
    plugins: config.plugins.filter((plugin) => !["archive-hidden-sourcemaps", "keep-dist-placeholder"].includes(plugin?.name)),
    build: { ...config.build, outDir: output, minify: false, sourcemap: false,
      rolldownOptions: { ...config.build.rolldownOptions, input: path.join(root, "bench/side-files-dock-layout.html") } },
  });
  server = await preview({ configFile: false, root, logLevel: "error", build: { outDir: output },
    preview: { host: "127.0.0.1", port: 0 } });
  const address = server.httpServer.address();
  const url = `http://127.0.0.1:${address.port}/bench/side-files-dock-layout.html`;
  browser = await chromium.launch({ headless: true, ...(process.env.PLAYWRIGHT_EXECUTABLE_PATH ? { executablePath: process.env.PLAYWRIGHT_EXECUTABLE_PATH } : {}) });
  const page = await browser.newPage({ locale: "en-US", viewport: { width: 900, height: 700 } });
  const errors = [];
  page.on("pageerror", (error) => errors.push(error.message));
  await page.goto(url);
  await page.waitForSelector(".side-files--dock");
  await page.evaluate(() => document.fonts.ready);

  const settle = async () => {
    await page.evaluate(() => new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve))));
  };
  const configure = async (options) => {
    const before = await page.evaluate(() => window.sideFilesDockFixture.revision);
    await page.evaluate((next) => window.sideFilesDockFixture.configure(next), options);
    await page.waitForFunction((prev) => window.sideFilesDockFixture.revision > prev, before);
    await settle();
  };
  const read = async (label) => {
    const state = await page.evaluate(() => window.sideFilesDockFixture.state());
    samples.push({ label, ...state });
    process.stdout.write(`  ${label}: dock(scroll=${state.dock.scrollHeight}, client=${state.dock.clientHeight}, scrollTop=${state.dock.scrollTop}) list(scroll=${state.list.scrollHeight}, client=${state.list.clientHeight}) rows=${state.rows} foot=${state.foot}\n`);
    return state;
  };
  const shot = async (name) => {
    if (!artifactsDir) return;
    await page.screenshot({ path: path.join(artifactsDir, `${name}.png`) });
  };

  // The BODY is what the dock panel fills: .workbench-dock__body is flex:1 in
  // the fixture's column, so its height = bodyHeight of the fixture frame.
  const bodyBox = await page.evaluate(() => {
    const body = document.querySelector(".workbench-dock__body").getBoundingClientRect();
    return { height: body.height, width: body.width };
  });
  process.stdout.write(`  fixture dock body: ${JSON.stringify(bodyBox)}\n`);

  // State 1: few rows — nothing may overflow, the inner list must not scroll,
  // and the "show more" foot must be gone (tl1428/tl1468 fold removal).
  await configure({ artifacts: 3 });
  const few = await read("few(3 rows)");
  if (!(few.dock.scrollHeight <= few.dock.clientHeight)) throw new Error("few: dock content overflows (scrollHeight > clientHeight)");
  if (!(few.list.scrollHeight <= few.list.clientHeight)) throw new Error("few: inner list scrolls on its own (cap resurrected?)");
  if (few.foot) throw new Error("few: show-more foot rendered in the dock (fold resurrected?)");
  if (few.rows !== 3) throw new Error("few: expected all 3 rows rendered");
  await shot("few-3-rows");

  // State 2: exactly full — grow the row count until one more row would
  // overflow; the largest non-overflowing count must pass without a scrollbar.
  // Derived from measured row pitch + chrome (everything above the list plus
  // the dock padding below it), then verified by reading.
  const { rowPitch, chrome } = await page.evaluate(() => {
    const dock = document.querySelector(".side-files--dock");
    const rows = document.querySelectorAll(".side-files__row");
    const dockStyle = getComputedStyle(dock);
    const dockTop = dock.getBoundingClientRect().top;
    const listTop = document.querySelector(".side-files__list").getBoundingClientRect().top;
    return {
      rowPitch: rows[1].getBoundingClientRect().top - rows[0].getBoundingClientRect().top,
      chrome: (listTop - dockTop) + (parseFloat(dockStyle.paddingBottom) || 0),
    };
  });
  const fitRows = Math.max(1, Math.floor((bodyBox.height - chrome) / rowPitch));
  await configure({ artifacts: fitRows });
  const full = await read(`exactly-full(${fitRows} rows)`);
  if (!(full.dock.scrollHeight <= full.dock.clientHeight + 1)) throw new Error(`exactly-full: unexpected overflow (scroll=${full.dock.scrollHeight} client=${full.dock.clientHeight})`);
  if (!(full.list.scrollHeight <= full.list.clientHeight)) throw new Error("exactly-full: inner list scrolls on its own");
  if (full.foot) throw new Error("exactly-full: show-more foot rendered while everything fits");
  if (full.rows !== fitRows) throw new Error(`exactly-full: expected ${fitRows} rows, got ${full.rows}`);
  await shot("exactly-full");

  // State 3: overflowing — NOW scrolling is correct: the dock panel scrolls
  // (scrollTop applies), the inner list still never scrolls on its own, and
  // no fold hides rows.
  const overRows = fitRows + 10;
  await configure({ artifacts: overRows });
  const over = await read(`overflow(${overRows} rows)`);
  if (!(over.dock.scrollHeight > over.dock.clientHeight)) throw new Error("overflow: dock does not overflow despite excess rows");
  if (!(over.list.scrollHeight <= over.list.clientHeight)) throw new Error("overflow: inner list scrolls on its own (cap resurrected?)");
  if (over.foot) throw new Error("overflow: show-more foot rendered in the dock (fold resurrected?)");
  if (over.rows !== overRows) throw new Error(`overflow: expected all ${overRows} rows rendered`);
  await page.evaluate(() => { document.querySelector(".side-files--dock").scrollTop = 24; });
  await settle();
  const scrolled = await page.evaluate(() => document.querySelector(".side-files--dock").scrollTop);
  if (scrolled <= 0) throw new Error("overflow: dock panel does not actually scroll (scrollTop stuck)");
  await shot("overflow-scrolled");

  // State 4 (guard): the COMBINED task-114 panel keeps its 240px cap — the
  // scoped CSS rule still applies where it should. Swap the fixture variant is
  // not enough (it renders the dock class), so assert the computed style of a
  // synthetic non-dock list instead.
  const combinedCap = await page.evaluate(() => {
    const host = document.createElement("div");
    host.className = "side-files";
    host.innerHTML = '<ul class="side-files__list"><li class="side-files__item">x</li></ul>';
    document.body.appendChild(host);
    const style = getComputedStyle(host.querySelector(".side-files__list"));
    const value = style.maxHeight;
    host.remove();
    return value;
  });
  process.stdout.write(`  combined-panel list computed max-height: ${combinedCap}\n`);
  if (combinedCap === "none") throw new Error("combined panel lost its 240px cap (scoping inverted)");

  const dockCap = await page.evaluate(() => {
    const dockList = document.querySelector(".side-files--dock .side-files__list");
    return getComputedStyle(dockList).maxHeight;
  });
  if (dockCap !== "none") throw new Error(`dock list still capped (computed max-height=${dockCap})`);
  process.stdout.write("  dock list computed max-height: none\n");

  if (errors.length > 0) throw new Error(`page errors: ${errors.join(" | ")}`);
  process.stdout.write(`PASS side-files dock layout (${samples.length} states measured in real Chromium)\n`);
  if (artifactsDir) writeFileSync(path.join(artifactsDir, "side-files-dock-layout.json"), JSON.stringify(samples, null, 2));
} finally {
  await browser?.close();
  await server?.httpServer.close();
  await import("node:fs/promises").then((fs) => fs.rm(path.join(root, ".bench-side-files-dock-out"), { recursive: true, force: true }));
}
