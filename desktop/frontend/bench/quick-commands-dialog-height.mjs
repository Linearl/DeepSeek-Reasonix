#!/usr/bin/env node
// tl1345 real-Chromium layout bench for the quick-command manager dialog.
// jsdom cannot lay out, so the vertical chain is verified here: the fixture
// (bench/quick-commands-dialog-height.tsx) mounts QuickCommandsManagerDialog
// with the production CSS and this runner drives it across entry counts and
// viewport sizes, asserting the dialog never exceeds the viewport and the
// list area scrolls once entries overflow it:
//   few rows (3)      -> dialog small, panel does not scroll;
//   many rows (24)    -> panel is the scroll container (overflow-y:auto,
//                        scrollHeight > clientHeight, scrollTop applies) and
//                        the dialog bottom stays inside the viewport;
//   viewport delta    -> the panel's computed max-height tracks
//                        calc(100dvh - 160px) at both viewport heights (the
//                        cap is viewport-proportional, not a fixed pixel).
// Screenshots land in $REASONIX_QC_DIALOG_ARTIFACTS (one per state).
//
// Usage:
//   PLAYWRIGHT_EXECUTABLE_PATH=<chromium.exe> node bench/quick-commands-dialog-height.mjs
//   (falls back to PLAYWRIGHT_BROWSERS_PATH=.pw-browsers / default resolution)

import { mkdirSync, writeFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { build, loadConfigFromFile, preview } from "vite";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
process.env.PLAYWRIGHT_BROWSERS_PATH = !process.env.PLAYWRIGHT_BROWSERS_PATH || process.env.PLAYWRIGHT_BROWSERS_PATH === ".pw-browsers"
  ? path.join(root, ".pw-browsers") : process.env.PLAYWRIGHT_BROWSERS_PATH;
const { chromium } = await import("playwright");

const artifactsDir = process.env.REASONIX_QC_DIALOG_ARTIFACTS ?? "";
if (artifactsDir) mkdirSync(artifactsDir, { recursive: true });

const samples = [];
let server;
let browser;
try {
  const loaded = await loadConfigFromFile({ command: "build", mode: "production" }, path.join(root, "vite.config.ts"));
  const config = loaded.config;
  const output = path.join(root, ".bench-quick-commands-dialog-out");
  await build({ ...config, configFile: false, root, logLevel: "error",
    plugins: config.plugins.filter((plugin) => !["archive-hidden-sourcemaps", "keep-dist-placeholder"].includes(plugin?.name)),
    build: { ...config.build, outDir: output, minify: false, sourcemap: false,
      rolldownOptions: { ...config.build.rolldownOptions, input: path.join(root, "bench/quick-commands-dialog-height.html") } },
  });
  server = await preview({ configFile: false, root, logLevel: "error", build: { outDir: output },
    preview: { host: "127.0.0.1", port: 0 } });
  const address = server.httpServer.address();
  const url = `http://127.0.0.1:${address.port}/bench/quick-commands-dialog-height.html`;
  browser = await chromium.launch({ headless: true, ...(process.env.PLAYWRIGHT_EXECUTABLE_PATH ? { executablePath: process.env.PLAYWRIGHT_EXECUTABLE_PATH } : {}) });
  const page = await browser.newPage({ locale: "en-US", viewport: { width: 1280, height: 800 } });
  const errors = [];
  page.on("pageerror", (error) => errors.push(error.message));
  await page.goto(url);
  await page.waitForSelector(".settings-quick-commands--panel");
  await page.evaluate(() => document.fonts.ready);

  const settle = async () => {
    await page.evaluate(() => new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve))));
  };
  const configure = async (options) => {
    const before = await page.evaluate(() => window.quickCommandsDialogFixture.revision);
    await page.evaluate((next) => window.quickCommandsDialogFixture.configure(next), options);
    await page.waitForFunction((prev) => window.quickCommandsDialogFixture.revision > prev, before);
    await settle();
  };
  const read = async (label) => {
    const state = await page.evaluate(() => window.quickCommandsDialogFixture.state());
    samples.push({ label, ...state });
    process.stdout.write(`  ${label}: dialog(h=${Math.round(state.dialog.height)}, bottom=${Math.round(state.dialog.bottom)}, vh=${state.viewportHeight})` +
      ` panel(scroll=${state.panel.scrollHeight}, client=${state.panel.clientHeight}, max=${state.panel.maxHeight}, overflowY=${state.panel.overflowY})` +
      ` rows=${state.rows}\n`);
    return state;
  };
  const shot = async (name) => {
    if (!artifactsDir) return;
    await page.screenshot({ path: path.join(artifactsDir, `${name}.png`) });
  };
  const assertWithinViewport = (state, label) => {
    if (!(state.dialog.bottom <= state.viewportHeight + 1)) {
      throw new Error(`${label}: dialog bottom ${Math.round(state.dialog.bottom)} exceeds viewport ${state.viewportHeight}`);
    }
    if (!(state.dialog.height <= state.viewportHeight - 48 + 1)) {
      throw new Error(`${label}: dialog height ${Math.round(state.dialog.height)} exceeds the shell cap 100dvh-48px`);
    }
  };

  // State 1: few rows — the dialog stays small and the panel does not scroll.
  await configure({ entries: 3 });
  const few = await read("few(3 rows) @1280x800");
  if (few.rows !== 3) throw new Error(`few: expected 3 rows, got ${few.rows}`);
  if (!(few.panel.scrollHeight <= few.panel.clientHeight)) throw new Error("few: panel scrolls with only 3 rows (cap too tight?)");
  assertWithinViewport(few, "few");
  await shot("few-3-rows");

  // State 2: many rows — the user's report shape (20+). The panel must own
  // the scroll (its own scrollbar) while the whole dialog stays on screen.
  await configure({ entries: 24 });
  const many = await read("many(24 rows) @1280x800");
  if (many.rows !== 24) throw new Error(`many: expected 24 rows, got ${many.rows}`);
  if (many.panel.overflowY !== "auto") throw new Error(`many: panel overflow-y is ${many.panel.overflowY}, expected auto`);
  if (!(many.panel.scrollHeight > many.panel.clientHeight)) throw new Error("many: panel does not overflow at 24 rows (cap not engaging?)");
  await page.evaluate(() => { document.querySelector(".settings-quick-commands--panel").scrollTop = 40; });
  await settle();
  const scrolled = await page.evaluate(() => document.querySelector(".settings-quick-commands--panel").scrollTop);
  if (scrolled <= 0) throw new Error("many: panel does not actually scroll (scrollTop stuck)");
  await read("many(scrolled) @1280x800");
  assertWithinViewport(many, "many");
  await shot("many-24-rows-scrolled");

  // State 3: viewport-proportional cap — shrink the viewport and the panel's
  // computed max-height must follow calc(100dvh - 160px), still containing
  // the 24-row list inside the viewport.
  await page.setViewportSize({ width: 1024, height: 640 });
  await settle();
  const small = await read("many(24 rows) @1024x640");
  assertWithinViewport(small, "small-viewport");
  await shot("small-viewport-24-rows");

  for (const state of samples) {
    const expected = `${state.viewportHeight - 160}px`;
    if (state.panel.maxHeight !== expected) {
      throw new Error(`panel computed max-height ${state.panel.maxHeight} != calc(100dvh - 160px) resolved ${expected}`);
    }
  }
  process.stdout.write("  panel max-height tracks calc(100dvh - 160px) at both viewports\n");

  if (errors.length > 0) throw new Error(`page errors: ${errors.join(" | ")}`);
  process.stdout.write(`PASS quick-commands dialog height (${samples.length} states measured in real Chromium)\n`);
  if (artifactsDir) writeFileSync(path.join(artifactsDir, "quick-commands-dialog-height.json"), JSON.stringify(samples, null, 2));
} finally {
  await browser?.close();
  await server?.httpServer.close();
  await import("node:fs/promises").then((fs) => fs.rm(path.join(root, ".bench-quick-commands-dialog-out"), { recursive: true, force: true }));
}
