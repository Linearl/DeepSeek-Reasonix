// Task 151 (round 3) — tab-switch render baseline / residency comparison.
//
// Runs the real Transcript component in headless Chromium and reports, for one
// switch between two large resident tabs:
//
//   commit  — click -> layout effect (React render + DOM mutation done)
//   total   — click -> second rAF (what the user perceives as the pause)
//
// Two fixture modes are measured on the same page/data:
//   swap     — one Transcript instance, item set replaced on switch (= today)
//   resident — one pane per tab, switch toggles visibility (= candidate fix)
//
// Usage: node bench/tab-render.mjs [--turns=240] [--rounds=8]
import assert from "node:assert/strict";
import { existsSync } from "node:fs";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { build, loadConfigFromFile, preview } from "vite";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
// The repo-relative browser directory only exists on CI images; a workstation keeps
// the browsers in playwright's default location, so only point at it when it is there.
const localBrowsers = path.join(root, ".pw-browsers");
if ((!process.env.PLAYWRIGHT_BROWSERS_PATH || process.env.PLAYWRIGHT_BROWSERS_PATH === ".pw-browsers") && existsSync(localBrowsers)) {
  process.env.PLAYWRIGHT_BROWSERS_PATH = localBrowsers;
}
const { chromium } = await import("playwright");

const arg = (name, fallback) => {
  const hit = process.argv.find((value) => value.startsWith(`--${name}=`));
  return hit ? Number(hit.slice(name.length + 3)) : fallback;
};
const turns = arg("turns", 240);
const rounds = arg("rounds", 8);
const tabs = ["tab-a", "tab-b"];

const output = await mkdtemp(path.join(tmpdir(), "reasonix-tab-render-"));
const report = {};
let server;
let browser;
try {
  const loaded = await loadConfigFromFile({ command: "build", mode: "production" }, path.join(root, "vite.config.ts"));
  const config = loaded.config;
  await build({
    ...config, configFile: false, root, logLevel: "error",
    plugins: config.plugins.filter((plugin) => !["archive-hidden-sourcemaps", "keep-dist-placeholder"].includes(plugin?.name)),
    build: {
      ...config.build, outDir: output, minify: false, sourcemap: false,
      rolldownOptions: { ...config.build.rolldownOptions, input: path.join(root, "bench/tab-render.html") },
    },
  });
  server = await preview({ configFile: false, root, logLevel: "error", build: { outDir: output }, preview: { host: "127.0.0.1", port: 0 } });
  const address = server.httpServer.address();
  const url = `http://127.0.0.1:${address.port}/bench/tab-render.html`;
  browser = await chromium.launch({ headless: true, ...(process.env.PLAYWRIGHT_EXECUTABLE_PATH ? { executablePath: process.env.PLAYWRIGHT_EXECUTABLE_PATH } : {}) });
  const page = await browser.newPage({ viewport: { width: 1600, height: 1000 } });
  const errors = [];
  page.on("pageerror", (error) => { errors.push(error.message); console.error("pageerror:", error.message); });
  await page.goto(url);
  await page.waitForFunction(() => Boolean(window.tabRenderFixture));
  await page.evaluate(() => document.fonts.ready);

  const configure = async (options) => {
    const revision = await page.evaluate((next) => {
      const fixture = window.tabRenderFixture;
      const target = fixture.revision + 1;
      fixture.configure(next);
      return target;
    }, options);
    await page.waitForFunction((value) => window.tabRenderFixture.revision >= value, revision);
    await page.evaluate(() => new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve))));
  };

  const median = (values) => values.slice().sort((a, b) => a - b)[Math.floor(values.length / 2)];
  const stats = (samples) => ({
    commitMedian: median(samples.map((s) => Math.round(s.commit))),
    totalMedian: median(samples.map((s) => Math.round(s.total))),
    totalMax: Math.max(...samples.map((s) => Math.round(s.total))),
    domNodes: samples[samples.length - 1].domNodes,
    rows: samples[samples.length - 1].rows,
    heapMb: samples[samples.length - 1].heapMb,
  });

  const runMode = async (mode) => {
    await configure({ mode, turns, tabIds: tabs });
    const samples = [];
    // Two warm-up switches pay for the first mount of the incoming tab in either
    // mode; the steady state (both tabs already seen) is what the user reports.
    const switches = 2 + rounds;
    for (let index = 0; index < switches; index += 1) {
      const target = tabs[(index + 1) % tabs.length];
      const sample = await page.evaluate((tabId) => window.tabRenderFixture.switchTo(tabId), target);
      samples.push(sample);
      if (index === 0) report[`${mode}:firstMount`] = { commit: Math.round(sample.commit), total: Math.round(sample.total) };
    }
    const steady = samples.slice(2);
    report[mode] = stats(steady);
    report[`${mode}:samples`] = steady.map((s) => `${s.from}->${s.to} commit=${Math.round(s.commit)} total=${Math.round(s.total)}`);
    report[`${mode}:paneCount`] = await page.evaluate(() => window.tabRenderFixture.mountedTabIds());
  };

  await runMode("swap");
  await runMode("resident");

  assert.equal(errors.length, 0, `page errors: ${errors.join(" | ")}`);
  console.log(JSON.stringify({ turns, rounds, ...report }, null, 2));
} finally {
  if (browser) await browser.close();
  if (server) await server.close();
  await rm(output, { recursive: true, force: true });
}
